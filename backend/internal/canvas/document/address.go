package document

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	AddressReasonInvalid    = "invalid_address"
	AddressReasonRange      = "invalid_range"
	AddressReasonOutOfRange = "out_of_range"
)

type LineRange struct {
	Present bool
	Append  bool
	Single  bool
	From    int
	To      int
}

type Address struct {
	Path  string
	Field string
	Range LineRange
}

type AddressError struct {
	Reason string
	Detail string
}

func (e *AddressError) Error() string { return e.Detail }

var addressFieldPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

func ParseAddress(raw string) (Address, error) {
	if raw == "" {
		return Address{}, addressInvalid("地址不能为空")
	}
	head, rangeText, hasRange := cutAddressRange(raw)
	address, err := parseAddressHead(head)
	if err != nil {
		return Address{}, err
	}
	if hasRange {
		lineRange, err := parseLineRange(rangeText)
		if err != nil {
			return Address{}, err
		}
		address.Range = lineRange
	}
	return address, nil
}

func (a Address) ValidateSourceRange(lineCount int) error {
	if !a.Range.Present {
		return addressRange("源地址必须带行区间（:a 或 :a,b）")
	}
	if a.Range.Append {
		return addressRange("源地址不接受 :end；要整段请用别的读命令")
	}
	if a.Range.From < 1 || a.Range.To < 1 || a.Range.From > lineCount || a.Range.To > lineCount {
		return addressOutOfRange(fmt.Sprintf(
			"源区间 %d,%d 超出字段行数 %d", a.Range.From, a.Range.To, lineCount))
	}
	return nil
}

func (a Address) ValidateReadRange(lineCount int) error {
	if !a.Range.Present {
		return nil
	}
	if a.Range.Append {
		return addressRange("读命令不接受 :end；要读末尾请用 tail")
	}
	if a.Range.From < 1 || a.Range.To < 1 || a.Range.From > lineCount || a.Range.To > lineCount {
		return addressOutOfRange(fmt.Sprintf(
			"区间 %d,%d 超出字段行数 %d；要读末尾请用 tail", a.Range.From, a.Range.To, lineCount))
	}
	return nil
}

func (a Address) ReadSpan(lineCount int) (from, to int) {
	if !a.Range.Present {
		return 1, lineCount
	}
	return a.Range.From, a.Range.To
}

func (a Address) ValidateTargetPlacement(lineCount int) error {
	if !a.Range.Present {
		return addressRange("目标地址必须带落点（:a、:a,b 或 :end）")
	}
	if a.Range.Append {
		return nil
	}
	if a.Range.Single {
		if lineCount == 0 {
			if a.Range.From == 1 {
				return nil
			}
			return addressOutOfRange(fmt.Sprintf(
				"空字段的插入点只能是 :1，收到 :%d", a.Range.From))
		}
		if a.Range.From < 1 || a.Range.From > lineCount {
			return addressOutOfRange(fmt.Sprintf(
				"插入点 :%d 超出字段行数 %d；追加到末尾请用 :end", a.Range.From, lineCount))
		}
	}
	if a.Range.From < 1 || a.Range.To < 1 || a.Range.From > lineCount || a.Range.To > lineCount {
		return addressOutOfRange(fmt.Sprintf(
			"目标区间 %d,%d 超出字段行数 %d", a.Range.From, a.Range.To, lineCount))
	}
	return nil
}

func cutAddressRange(raw string) (head string, rangeText string, hasRange bool) {
	index := strings.IndexByte(raw, ':')
	if index < 0 {
		return raw, "", false
	}
	return raw[:index], raw[index+1:], true
}

func parseAddressHead(head string) (Address, error) {
	index := strings.LastIndex(head, ".txt")
	if index < 0 {
		return Address{}, addressInvalid("地址必须指向 ./<类型>/<节点ID>.txt")
	}
	path, err := canonicalAddressPath(head[:index+4])
	if err != nil {
		return Address{}, err
	}
	tail := head[index+4:]
	if tail == "" {
		return Address{Path: path}, nil
	}
	if !strings.HasPrefix(tail, ".") {
		return Address{}, addressInvalid(fmt.Sprintf("节点路径后只能接 .field、# 或 :区间，收到 %q", tail))
	}
	field := tail[1:]
	if err := validateAddressField(field); err != nil {
		return Address{}, err
	}
	return Address{Path: path, Field: field}, nil
}

func validateAddressField(field string) error {
	if !addressFieldPattern.MatchString(field) {
		return addressInvalid(fmt.Sprintf(
			"字段名 %q 无效；字段必须显式写成 .<field>，只能包含字母、数字、下划线和连字符，且不能以数字开头", field))
	}
	return nil
}

func canonicalAddressPath(raw string) (string, error) {
	path := raw
	if strings.HasPrefix(path, "./") {
		path = path[2:]
	}
	if path == "" || strings.HasPrefix(path, "/") {
		return "", addressInvalid(fmt.Sprintf("地址必须指向 ./<类型>/<节点ID>.txt，不能是绝对路径：%q", raw))
	}
	if !strings.HasSuffix(path, ".txt") {
		return "", addressInvalid(fmt.Sprintf("地址必须以 .txt 结尾：%q", raw))
	}
	parts := strings.Split(path[:len(path)-len(".txt")], "/")
	if len(parts) != 2 && !(len(parts) == 3 && parts[0] == "submap") {
		return "", addressInvalid(fmt.Sprintf("地址必须是 ./<类型>/<节点ID>.txt 或 ./submap/<submap-id>/<local-id>.txt：%q", raw))
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", addressInvalid(fmt.Sprintf("地址路径段无效：%q", raw))
		}
		for _, r := range part {
			if !addressPathRune(r) {
				return "", addressInvalid(fmt.Sprintf("地址路径段含有不允许的字符 %q：%q", r, raw))
			}
		}
	}
	return "./" + strings.Join(parts, "/") + ".txt", nil
}

func addressPathRune(r rune) bool {
	return r == '_' || r == '-' || r == '.' ||
		(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func SubmapAddressPath(path string) (submapID, localID string, ok bool) {
	trimmed := strings.TrimPrefix(path, "./")
	trimmed = strings.TrimSuffix(trimmed, ".txt")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 || parts[0] != "submap" || parts[1] == "" {
		return "", "", false
	}
	switch len(parts) {
	case 2:
		return parts[1], "", true
	case 3:
		if parts[2] == "" {
			return "", "", false
		}
		return parts[1], parts[2], true
	default:
		return "", "", false
	}
}

func parseLineRange(text string) (LineRange, error) {
	if text == "" {
		return LineRange{}, addressRange("区间不能为空；写成 :a、:a,b 或 :end")
	}
	if text == "end" {
		return LineRange{Present: true, Append: true}, nil
	}
	parts := strings.Split(text, ",")
	if len(parts) > 2 {
		return LineRange{}, addressRange(fmt.Sprintf("区间 %q 不是 :a、:a,b 或 :end", text))
	}
	from, err := parseAddressLine(parts[0])
	if err != nil {
		return LineRange{}, err
	}
	lineRange := LineRange{Present: true, Single: len(parts) == 1, From: from, To: from}
	if lineRange.Single {
		return lineRange, nil
	}
	to, err := parseAddressLine(parts[1])
	if err != nil {
		return LineRange{}, err
	}
	if from > to {
		return LineRange{}, addressRange(fmt.Sprintf("区间 %d,%d 的起点大于终点", from, to))
	}
	lineRange.To = to
	return lineRange, nil
}

func parseAddressLine(text string) (int, error) {
	if text == "" {
		return 0, addressRange("区间缺少行号")
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return 0, addressRange(fmt.Sprintf("行号 %q 不是十进制正整数", text))
		}
	}
	line, err := strconv.Atoi(text)
	if err != nil || line < 1 {
		return 0, addressRange(fmt.Sprintf("行号 %q 必须是 1 基正整数", text))
	}
	return line, nil
}

func addressInvalid(detail string) error {
	return &AddressError{Reason: AddressReasonInvalid, Detail: detail}
}

func addressRange(detail string) error {
	return &AddressError{Reason: AddressReasonRange, Detail: detail}
}

func addressOutOfRange(detail string) error {
	return &AddressError{Reason: AddressReasonOutOfRange, Detail: detail}
}
