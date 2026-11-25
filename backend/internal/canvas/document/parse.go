package document

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"yingce/backend/internal/canvas/capability"
)

func Parse(descriptor capability.Descriptor, text string) (Patch, []Repair, error) {
	return ParseWithOptions(descriptor, text, ParseOptions{})
}

type ParseOptions struct {
	Node map[string]any
}

func ParseWithOptions(descriptor capability.Descriptor, text string, options ParseOptions) (Patch, []Repair, error) {
	normalized, repairs := normalize(text)
	if strings.TrimSpace(descriptor.Type) == "" {
		return Patch{}, repairs, &Error{Code: CodeNodeMismatch, Detail: "目标描述符没有类型，无法校验文件头的 type"}
	}
	if line := invalidUTF8Line(normalized); line > 0 {
		return Patch{}, repairs, &Error{Code: CodeSyntax, Line: line, Detail: "文件不是合法 UTF-8"}
	}
	escapedText, breaks := relaxStringNewlines(normalized)
	lines := newLineIndex(escapedText, breaks)
	root, err := decodeDocument(escapedText, lines)
	if err != nil {
		return Patch{}, repairs, err
	}
	patch, err := parseDocument(descriptor, root, lines, options, &repairs)
	if err != nil {
		return Patch{}, repairs, err
	}
	return patch, repairs, nil
}

// relaxStringNewlines pre-escapes raw newlines inside JSON strings so the
// standard decoder can read the model-friendly document. Returned offsets map
// decoder positions back to original lines.
func relaxStringNewlines(text string) (string, []int) {
	var out strings.Builder
	out.Grow(len(text) + 16)
	var breaks []int
	inString, escaped := false, false
	for index := 0; index < len(text); index++ {
		c := text[index]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			case c == '\n':
				breaks = append(breaks, out.Len())
				out.WriteString(`\n`)
				continue
			case c == '\r':
				out.WriteString(`\r`)
				continue
			}
		} else if c == '"' {
			inString = true
		}
		out.WriteByte(c)
	}
	return out.String(), breaks
}

func originalLine(breaks []int, starts []int, escapedLine int) int {
	if escapedLine <= 1 || len(breaks) == 0 {
		return escapedLine
	}
	if escapedLine > len(starts) {
		escapedLine = len(starts)
	}
	return escapedLine + sort.SearchInts(breaks, starts[escapedLine-1])
}

type lineIndex struct {
	starts []int
	breaks []int
}

func newLineIndex(text string, breaks []int) *lineIndex {
	starts := []int{0}
	for index := 0; index < len(text); index++ {
		if text[index] == '\n' {
			starts = append(starts, index+1)
		}
	}
	return &lineIndex{starts: starts, breaks: breaks}
}

func (l *lineIndex) lineAt(offset int) int {
	return originalLine(l.breaks, l.starts, l.escapedLineAt(offset))
}

func (l *lineIndex) escapedLineAt(offset int) int {
	if offset <= 0 {
		return 1
	}
	low, high := 0, len(l.starts)-1
	for low < high {
		mid := (low + high + 1) / 2
		if l.starts[mid] <= offset {
			low = mid
			continue
		}
		high = mid - 1
	}
	return low + 1
}

type rawValue struct {
	scalar any
	object []rawMember
	array  []rawValue
	kind   byte
	offset int
}

type rawMember struct {
	key    string
	value  rawValue
	offset int
}

func decodeDocument(text string, lines *lineIndex) (rawValue, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	root, err := decodeValue(decoder, lines)
	if err != nil {
		return rawValue{}, err
	}
	if _, err := decoder.Token(); err == nil {
		return rawValue{}, &Error{Code: CodeSyntax, Line: lines.lineAt(int(decoder.InputOffset())),
			Detail: "文件在第一个 JSON 值之后还有内容：整份文档必须恰好是一个对象"}
	}
	return root, nil
}

func decodeValue(decoder *json.Decoder, lines *lineIndex) (rawValue, error) {
	token, err := decoder.Token()
	if err != nil {
		return rawValue{}, syntaxError(err, decoder, lines)
	}
	return decodeFromToken(decoder, lines, token)
}

func decodeFromToken(decoder *json.Decoder, lines *lineIndex, token json.Token) (rawValue, error) {
	offset := int(decoder.InputOffset())
	switch typed := token.(type) {
	case json.Delim:
		switch typed {
		case '{':
			return decodeObject(decoder, lines, offset)
		case '[':
			return decodeArray(decoder, lines, offset)
		}
		return rawValue{}, &Error{Code: CodeSyntax, Line: lines.lineAt(offset),
			Detail: fmt.Sprintf("意外的 JSON 分隔符 %q", typed.String())}
	default:
		return rawValue{scalar: token, kind: 's', offset: offset}, nil
	}
}

func decodeObject(decoder *json.Decoder, lines *lineIndex, offset int) (rawValue, error) {
	value := rawValue{kind: 'o', offset: offset}
	seen := map[string]int{}
	for {
		token, err := decoder.Token()
		if err != nil {
			return rawValue{}, syntaxError(err, decoder, lines)
		}
		if delim, ok := token.(json.Delim); ok && delim == '}' {
			return value, nil
		}
		key, ok := token.(string)
		if !ok {
			return rawValue{}, &Error{Code: CodeSyntax, Line: lines.lineAt(int(decoder.InputOffset())),
				Detail: "对象的键必须是字符串"}
		}
		keyOffset := int(decoder.InputOffset())
		line := lines.lineAt(keyOffset)
		if previous, duplicate := seen[key]; duplicate {
			return rawValue{}, &Error{Code: CodeDuplicate, Line: line, Detail: fmt.Sprintf(
				"键 %s 重复声明：第 %d 行与第 %d 行（同一对象里一个键只能出现一次，请删掉一处）",
				key, previous, line)}
		}
		seen[key] = line
		member, err := decodeValue(decoder, lines)
		if err != nil {
			return rawValue{}, err
		}
		value.object = append(value.object, rawMember{key: key, value: member, offset: keyOffset})
	}
}

func decodeArray(decoder *json.Decoder, lines *lineIndex, offset int) (rawValue, error) {
	value := rawValue{kind: 'a', offset: offset}
	for {
		token, err := decoder.Token()
		if err != nil {
			return rawValue{}, syntaxError(err, decoder, lines)
		}
		if delim, ok := token.(json.Delim); ok && delim == ']' {
			return value, nil
		}
		item, err := decodeFromToken(decoder, lines, token)
		if err != nil {
			return rawValue{}, err
		}
		value.array = append(value.array, item)
	}
}

func syntaxError(err error, decoder *json.Decoder, lines *lineIndex) error {
	line := 1
	if offset := int(decoder.InputOffset()); offset > 0 {
		line = lines.lineAt(offset)
	}
	return &Error{Code: CodeSyntax, Line: line, Detail: "文件不是合法 JSON：" + err.Error()}
}

type parseContext struct {
	lines   *lineIndex
	node    map[string]any
	nodeID  string
	repairs *[]Repair
}

func (c parseContext) line(raw rawValue) int {
	return c.lines.lineAt(raw.offset)
}

func (c parseContext) recordRepair(repair Repair) {
	if c.repairs == nil {
		return
	}
	*c.repairs = append(*c.repairs, repair)
}

func parseDocument(descriptor capability.Descriptor, root rawValue, lines *lineIndex, options ParseOptions, repairs *[]Repair) (Patch, error) {
	if root.kind != 'o' {
		return Patch{}, &Error{Code: CodeSyntax, Line: 1,
			Detail: "整份文档必须是一个 JSON 对象（以 { 开头），形如 {\"type\": \"text\", \"content\": \"…\"}"}
	}
	context := parseContext{lines: lines, node: options.Node, repairs: repairs}
	patch := Patch{Values: map[string]any{}}
	nodeMembers := make([]rawMember, 0, len(root.object))
	declaredType := ""
	for _, member := range root.object {
		switch member.key {
		case "type":
			text, ok := member.value.scalar.(string)
			if !ok || member.value.kind != 's' {
				return Patch{}, &Error{Code: CodeType, Line: context.line(member.value),
					Detail: fmt.Sprintf("type 必须是字符串（能力类型 id，例如 %q）", descriptor.Type)}
			}
			if text != descriptor.Type {
				return Patch{}, &Error{Code: CodeNodeMismatch, Line: context.line(member.value), Detail: fmt.Sprintf(
					"文件声明的 type 是 %q，目标节点类型是 %q；这是另一类节点的文件", text, descriptor.Type)}
			}
			declaredType = text
			continue
		case "id":
			text, ok := member.value.scalar.(string)
			if !ok || member.value.kind != 's' {
				return Patch{}, &Error{Code: CodeType, Line: context.line(member.value),
					Detail: "id 必须是字符串（节点 ID 原文，例如 \"text-1\"）"}
			}
			patch.ID = text
			continue
		}
		nodeMembers = append(nodeMembers, member)
	}
	if declaredType == "" {
		return Patch{}, &Error{Code: CodeNodeMismatch, Line: 1, Detail: fmt.Sprintf(
			"文件没有 \"type\" 键；第一项应是 \"type\": %q", descriptor.Type)}
	}
	values, err := parseContainer(nodeMembers, descriptor, context)
	if err != nil {
		return Patch{}, err
	}
	patch.Type = declaredType
	patch.Values = values
	return patch, nil
}

func parseContainer(members []rawMember, descriptor capability.Descriptor, context parseContext) (map[string]any, error) {
	fields := documentFields(descriptor.Fields)
	excluded := excludedDocumentFields(descriptor.Fields)
	available := append([]string{"type", "id"}, fields.Ordered()...)
	values := make(map[string]any, len(members))
	for _, member := range members {
		key := member.key
		if field, inDocument := fields[key]; inDocument {
			value, err := coerceFieldValue(field, member.value, context)
			if err != nil {
				return nil, err
			}
			values[key] = value
			continue
		}
		if field, declared := excluded[key]; declared {
			return nil, &Error{Code: CodeNotInDocument, Line: context.line(member.value), Detail: notInDocumentDetail(key, field)}
		}
		return nil, &Error{Code: CodeUnknownField, Line: context.line(member.value), Detail: fmt.Sprintf(
			"未知字段 %s；节点可用键：%s", key, strings.Join(available, "、"))}
	}
	return values, nil
}

func coerceFieldValue(field capability.Field, raw rawValue, context parseContext) (any, error) {
	input, err := rawToGo(field, raw)
	if err != nil {
		if detail, ok := err.(*Error); ok && detail.Line == 0 {
			detail.Line = context.line(raw)
		}
		return nil, err
	}
	if field.Wrap && field.Kind == capability.KindString {
		if text, ok := input.(string); ok {
			if _, count := capability.WrapSentences(text); count > 0 {
				context.recordRepair(Repair{
					Line: context.line(raw), Kind: RepairSentenceWrap,
					Detail: capability.SentenceWrapDetail(field.Path, count),
				})
			}
		}
	}
	value, err := field.Coerce(input, field.Path)
	if err != nil {
		return nil, &Error{Code: CodeValue, Line: context.line(raw), Detail: err.Error()}
	}
	return value, nil
}

func rawToGo(field capability.Field, raw rawValue) (any, error) {
	switch field.Kind {
	case capability.KindString:
		return rawString(field, raw)
	case capability.KindNumber:
		return rawNumber(field, raw)
	case capability.KindBoolean:
		return rawBoolean(field, raw)
	case capability.KindStrings:
		return rawStrings(field, raw)
	case capability.KindMap:
		return rawStringMap(field, raw)
	case capability.KindCollection:
		return rawCollection(field, raw)
	}
	return nil, &Error{Code: CodeNotInDocument, Detail: notInDocumentDetail(field.Path, field)}
}

func rawString(field capability.Field, raw rawValue) (any, error) {
	text, ok := raw.scalar.(string)
	if !ok || raw.kind != 's' {
		return nil, typeError(field, "字符串", raw)
	}
	return text, nil
}

func rawNumber(field capability.Field, raw rawValue) (any, error) {
	number, ok := raw.scalar.(json.Number)
	if !ok || raw.kind != 's' {
		return nil, typeError(field, "数字", raw)
	}
	parsed, err := strconv.ParseFloat(number.String(), 64)
	if err != nil {
		return nil, &Error{Code: CodeType, Detail: fmt.Sprintf("字段 %s 的值 %q 不是数字", field.Path, number.String())}
	}
	return parsed, nil
}

func rawBoolean(field capability.Field, raw rawValue) (any, error) {
	flag, ok := raw.scalar.(bool)
	if !ok || raw.kind != 's' {
		return nil, typeError(field, "true 或 false", raw)
	}
	return flag, nil
}

func rawStrings(field capability.Field, raw rawValue) (any, error) {
	if raw.kind != 'a' {
		return nil, typeError(field, "字符串数组", raw)
	}
	items := make([]any, 0, len(raw.array))
	for index, item := range raw.array {
		text, ok := item.scalar.(string)
		if !ok || item.kind != 's' {
			return nil, &Error{Code: CodeType, Detail: fmt.Sprintf(
				"字段 %s 的第 %d 项必须是字符串，当前是%s", field.Path, index+1, rawKindName(item))}
		}
		items = append(items, text)
	}
	return items, nil
}

func rawStringMap(field capability.Field, raw rawValue) (any, error) {
	if raw.kind != 'o' {
		return nil, typeError(field, "对象（键与值都是字符串）", raw)
	}
	values := make(map[string]any, len(raw.object))
	for _, member := range raw.object {
		text, ok := member.value.scalar.(string)
		if !ok || member.value.kind != 's' {
			return nil, &Error{Code: CodeType, Detail: fmt.Sprintf(
				"字段 %s 的成员 %s 必须是字符串，当前是%s", field.Path, member.key, rawKindName(member.value))}
		}
		values[member.key] = text
	}
	return values, nil
}

func rawCollection(field capability.Field, raw rawValue) (any, error) {
	if raw.kind != 'a' {
		return nil, typeError(field, "对象数组", raw)
	}
	items := make([]any, 0, len(raw.array))
	for index, item := range raw.array {
		entry, err := coerceElement(field, item, index)
		if err != nil {
			return nil, err
		}
		items = append(items, entry)
	}
	return items, nil
}

func coerceElement(field capability.Field, raw rawValue, index int) (map[string]any, error) {
	if raw.kind != 'o' {
		return nil, &Error{Code: CodeType, Detail: fmt.Sprintf(
			"字段 %s 的第 %d 项必须是对象，当前是%s", field.Path, index+1, rawKindName(raw))}
	}
	entry := make(map[string]any, len(raw.object))
	for _, member := range raw.object {
		element, declared := field.Element[member.key]
		if !declared {
			return nil, &Error{Code: CodeUnknownField, Detail: fmt.Sprintf(
				"字段 %s 的第 %d 项不接受键 %s；可用键：%s",
				field.Path, index+1, member.key, strings.Join(field.Element.Ordered(), "、"))}
		}
		value, err := rawToGo(element, member.value)
		if err != nil {
			return nil, err
		}
		coerced, err := element.Coerce(value, fmt.Sprintf("%s[%d].%s", field.Path, index, member.key))
		if err != nil {
			return nil, &Error{Code: CodeValue, Detail: err.Error()}
		}
		entry[member.key] = coerced
	}
	return entry, nil
}

func typeError(field capability.Field, want string, raw rawValue) error {
	return &Error{Code: CodeType, Detail: fmt.Sprintf(
		"字段 %s 必须是%s，当前是%s", field.Path, want, rawKindName(raw))}
}

func rawKindName(raw rawValue) string {
	switch raw.kind {
	case 'o':
		return "对象"
	case 'a':
		return "数组"
	}
	switch value := raw.scalar.(type) {
	case string:
		return "字符串 " + strconv.Quote(value)
	case json.Number:
		return "数字 " + value.String()
	case bool:
		return "布尔值 " + strconv.FormatBool(value)
	case nil:
		return "null"
	}
	return "未知值"
}

func invalidUTF8Line(text string) int {
	line := 1
	for index := 0; index < len(text); {
		r, size := utf8.DecodeRuneInString(text[index:])
		if r == utf8.RuneError && size == 1 {
			return line
		}
		if r == '\n' {
			line++
		}
		index += size
	}
	return 0
}
