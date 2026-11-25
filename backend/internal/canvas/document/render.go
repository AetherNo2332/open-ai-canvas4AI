package document

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"yingce/backend/internal/canvas/capability"
)

const jsonInlineLimit = 100

func Render(descriptor capability.Descriptor, node map[string]any) (string, error) {
	text, _, err := renderDocument(descriptor, node)
	return text, err
}

func renderDocument(descriptor capability.Descriptor, node map[string]any) (string, []FieldBlock, error) {
	if strings.TrimSpace(descriptor.Type) == "" {
		return "", nil, &Error{Code: CodeNodeMismatch, Detail: "目标描述符没有类型，无法确定文件头的 type"}
	}
	members, err := documentMembers(descriptor, node)
	if err != nil {
		return "", nil, err
	}
	emitter := &jsonEmitter{line: 1}
	if err := emitter.writeObject(members, 0, false); err != nil {
		return "", nil, err
	}
	emitter.raw("\n")
	return emitter.buf.String(), emitter.blocks, nil
}

type jsonMember struct {
	key      string
	writable bool
	value    jsonValue
}

type jsonValue struct {
	scalar   any
	object   []jsonMember
	array    []jsonValue
	isArray  bool
	isObject bool
}

func jsonScalar(value any) jsonValue { return jsonValue{scalar: value} }

func jsonArrayValue(items []jsonValue) jsonValue {
	return jsonValue{array: items, isArray: true}
}

func jsonObjectValue(members []jsonMember) jsonValue {
	return jsonValue{object: members, isObject: true}
}

func documentMembers(descriptor capability.Descriptor, node map[string]any) ([]jsonMember, error) {
	id, err := renderNodeID(node)
	if err != nil {
		return nil, err
	}
	members := make([]jsonMember, 0, len(descriptor.Fields)+3)
	members = append(members, jsonMember{key: "type", value: jsonScalar(descriptor.Type)})
	if id != "" {
		members = append(members, jsonMember{key: "id", value: jsonScalar(id)})
	}
	for _, key := range documentFields(descriptor.Fields).Ordered() {
		field := descriptor.Fields[key]
		value, exists := field.Value(node)
		if !exists || value == nil {
			continue
		}
		rendered, err := renderFieldValue(key, field, value)
		if err != nil {
			return nil, err
		}
		members = append(members, jsonMember{key: key, writable: true, value: rendered})
	}
	return members, nil
}

func renderNodeID(node map[string]any) (string, error) {
	raw, exists := node["id"]
	if !exists || raw == nil {
		return "", nil
	}
	id, ok := raw.(string)
	if !ok {
		return "", &Error{Code: CodeType, Detail: "节点的 id 不是字符串，无法写入文件头"}
	}
	id = strings.TrimSpace(id)
	if strings.ContainsAny(id, "\r\n") {
		return "", &Error{Code: CodeType, Detail: "节点的 id 含换行，无法写入文件头"}
	}
	return id, nil
}

func renderFieldValue(key string, field capability.Field, raw any) (jsonValue, error) {
	switch field.Kind {
	case capability.KindString:
		value, ok := raw.(string)
		if !ok {
			return jsonValue{}, &Error{Code: CodeType, Detail: fmt.Sprintf("字段 %s（%s）在节点里的值不是字符串，无法渲染", key, field.Path)}
		}
		return jsonScalar(stripCarriageReturns(value)), nil
	case capability.KindNumber:
		number, ok := numberOf(raw)
		if !ok {
			return jsonValue{}, &Error{Code: CodeType, Detail: fmt.Sprintf("字段 %s（%s）在节点里的值不是数字，无法渲染", key, field.Path)}
		}
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return jsonValue{}, &Error{Code: CodeValue, Detail: fmt.Sprintf("字段 %s 在节点里的值不是有效数字（NaN/Inf），无法渲染", key)}
		}
		return jsonScalar(number), nil
	case capability.KindBoolean:
		flag, ok := raw.(bool)
		if !ok {
			return jsonValue{}, &Error{Code: CodeType, Detail: fmt.Sprintf("字段 %s（%s）在节点里的值不是布尔值，无法渲染", key, field.Path)}
		}
		return jsonScalar(flag), nil
	case capability.KindStrings:
		items, ok := stringSlice(raw)
		if !ok {
			return jsonValue{}, &Error{Code: CodeType, Detail: fmt.Sprintf("字段 %s（%s）在节点里的值不是字符串数组，无法渲染", key, field.Path)}
		}
		out := make([]jsonValue, 0, len(items))
		for _, item := range items {
			out = append(out, jsonScalar(stripCarriageReturns(item)))
		}
		return jsonArrayValue(out), nil
	case capability.KindMap:
		values, ok := raw.(map[string]any)
		if !ok {
			return jsonValue{}, &Error{Code: CodeType, Detail: fmt.Sprintf("字段 %s（%s）在节点里的值不是对象，无法渲染", key, field.Path)}
		}
		members := make([]jsonMember, 0, len(values))
		for _, name := range sortedMapKeys(values) {
			text, isString := values[name].(string)
			if !isString {
				return jsonValue{}, &Error{Code: CodeType, Detail: fmt.Sprintf("字段 %s 的成员 %s 不是字符串，无法渲染", key, name)}
			}
			members = append(members, jsonMember{key: name, value: jsonScalar(stripCarriageReturns(text))})
		}
		return jsonObjectValue(members), nil
	case capability.KindCollection:
		return renderCollection(key, field, raw)
	}
	return jsonValue{}, &Error{Code: CodeNotInDocument, Detail: notInDocumentDetail(key, field)}
}

func renderCollection(key string, field capability.Field, raw any) (jsonValue, error) {
	items, ok := mapSlice(raw)
	if !ok {
		return jsonValue{}, &Error{Code: CodeType, Detail: fmt.Sprintf("字段 %s（%s）在节点里的值不是对象数组，无法渲染", key, field.Path)}
	}
	elementKeys := field.Element.Ordered()
	out := make([]jsonValue, 0, len(items))
	for index, item := range items {
		members := make([]jsonMember, 0, len(elementKeys))
		for _, name := range elementKeys {
			element := field.Element[name]
			value, exists := element.Value(item)
			if !exists || value == nil {
				continue
			}
			rendered, err := renderFieldValue(key+"."+name, element, value)
			if err != nil {
				return jsonValue{}, err
			}
			members = append(members, jsonMember{key: name, value: rendered})
		}
		if len(members) == 0 {
			return jsonValue{}, &Error{Code: CodeValue, Detail: fmt.Sprintf(
				"字段 %s 的第 %d 项没有任何已声明的键（%s），无法渲染成对象", key, index+1, strings.Join(elementKeys, "、"))}
		}
		out = append(out, jsonObjectValue(members))
	}
	return jsonArrayValue(out), nil
}

func stripCarriageReturns(value string) string {
	if !strings.Contains(value, "\r") {
		return value
	}
	return strings.ReplaceAll(value, "\r", "")
}

func numberOf(raw any) (float64, bool) {
	switch value := raw.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	}
	return 0, false
}

func stringSlice(raw any) ([]string, bool) {
	switch items := raw.(type) {
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, text)
		}
		return out, true
	case []string:
		return items, true
	}
	return nil, false
}

func mapSlice(raw any) ([]map[string]any, bool) {
	switch items := raw.(type) {
	case []any:
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			entry, ok := item.(map[string]any)
			if !ok {
				return nil, false
			}
			out = append(out, entry)
		}
		return out, true
	case []map[string]any:
		return items, true
	}
	return nil, false
}

func sortedMapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type jsonEmitter struct {
	buf    strings.Builder
	blocks []FieldBlock
	line   int
}

func (e *jsonEmitter) raw(text string) {
	e.buf.WriteString(text)
	e.line += strings.Count(text, "\n")
}

func (e *jsonEmitter) writeIndent(depth int) {
	for i := 0; i < depth; i++ {
		e.raw("  ")
	}
}

func (e *jsonEmitter) writeObject(members []jsonMember, depth int, allowInline bool) error {
	if len(members) == 0 {
		e.raw("{}")
		return nil
	}
	if allowInline {
		if compact, ok := compactObject(members); ok && len(compact) <= jsonInlineLimit {
			e.raw("{")
			for index, member := range members {
				if index > 0 {
					e.raw(", ")
				}
				start := e.buf.Len()
				line := e.line
				e.raw(jsonKey(member.key))
				e.raw(": ")
				e.raw(jsonScalarText(member.value.scalar))
				e.blocks = append(e.blocks, FieldBlock{
					Key: member.key, Writable: member.writable,
					Start: start, End: e.buf.Len(), Line: line,
				})
			}
			e.raw("}")
			return nil
		}
	}
	e.raw("{\n")
	for index, member := range members {
		e.writeIndent(depth + 1)
		start := e.buf.Len()
		line := e.line
		e.raw(jsonKey(member.key))
		e.raw(": ")
		if err := e.writeValue(member.value, depth+1); err != nil {
			return err
		}
		end := e.buf.Len()
		if index < len(members)-1 {
			e.raw(",")
		}
		e.raw("\n")
		e.blocks = append(e.blocks, FieldBlock{
			Key: member.key, Writable: member.writable,
			Start: start, End: end, Line: line,
		})
	}
	e.writeIndent(depth)
	e.raw("}")
	return nil
}

func (e *jsonEmitter) writeArray(items []jsonValue, depth int, allowInline bool) error {
	if len(items) == 0 {
		e.raw("[]")
		return nil
	}
	if allowInline {
		if compact, ok := compactArray(items); ok && len(compact) <= jsonInlineLimit {
			e.raw(compact)
			return nil
		}
	}
	e.raw("[\n")
	for index, item := range items {
		e.writeIndent(depth + 1)
		if err := e.writeValue(item, depth+1); err != nil {
			return err
		}
		if index < len(items)-1 {
			e.raw(",")
		}
		e.raw("\n")
	}
	e.writeIndent(depth)
	e.raw("]")
	return nil
}

func (e *jsonEmitter) writeValue(value jsonValue, depth int) error {
	switch {
	case value.isObject:
		return e.writeObject(value.object, depth, true)
	case value.isArray:
		return e.writeArray(value.array, depth, true)
	default:
		e.raw(jsonScalarText(value.scalar))
		return nil
	}
}

func jsonQuote(text string) string {
	return jsonQuoteWithNewlines(text, true)
}

func jsonKey(text string) string {
	return jsonQuoteWithNewlines(text, false)
}

func jsonQuoteWithNewlines(text string, rawNewlines bool) string {
	var out strings.Builder
	out.Grow(len(text) + 2)
	out.WriteByte('"')
	for index := 0; index < len(text); {
		r, size := utf8.DecodeRuneInString(text[index:])
		if r == utf8.RuneError && size == 1 {
			out.WriteString(`\ufffd`)
			index++
			continue
		}
		index += size
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			if rawNewlines {
				out.WriteByte('\n')
				continue
			}
			out.WriteString(`\n`)
		case '\r':
		case '\t':
			out.WriteString(`\t`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\u2028':
			out.WriteString(`\u2028`)
		case '\u2029':
			out.WriteString(`\u2029`)
		default:
			if r < 0x20 {
				out.WriteString(`\u00`)
				out.WriteByte("0123456789abcdef"[byte(r)>>4])
				out.WriteByte("0123456789abcdef"[byte(r)&0x0f])
				continue
			}
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return out.String()
}

func jsonScalarText(value any) string {
	switch typed := value.(type) {
	case string:
		return jsonQuote(typed)
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case nil:
		return "null"
	}
	return "null"
}

func compactObject(members []jsonMember) (string, bool) {
	var out strings.Builder
	out.WriteString("{")
	for index, member := range members {
		if member.value.isObject || member.value.isArray || jsonValueHasNewline(member.value) {
			return "", false
		}
		if index > 0 {
			out.WriteString(", ")
		}
		out.WriteString(jsonKey(member.key))
		out.WriteString(": ")
		out.WriteString(jsonScalarText(member.value.scalar))
	}
	out.WriteString("}")
	return out.String(), true
}

func compactArray(items []jsonValue) (string, bool) {
	var out strings.Builder
	out.WriteString("[")
	for index, item := range items {
		if item.isObject || item.isArray || jsonValueHasNewline(item) {
			return "", false
		}
		if index > 0 {
			out.WriteString(", ")
		}
		out.WriteString(jsonScalarText(item.scalar))
	}
	out.WriteString("]")
	return out.String(), true
}

func jsonValueHasNewline(value jsonValue) bool {
	text, ok := value.scalar.(string)
	return ok && strings.ContainsRune(text, '\n')
}
