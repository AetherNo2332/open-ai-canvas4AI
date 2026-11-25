package document

import (
	"reflect"
	"strings"
	"testing"

	"yingce/backend/internal/canvas/capability"
)

func sampleFieldValue(t *testing.T, key string, field capability.Field) any {
	t.Helper()
	if len(field.Enum) > 0 {
		return field.Enum[0]
	}
	switch field.Kind {
	case capability.KindString:
		switch key {
		case "content":
			return "甲：你来了。\n乙：我一直在等。\n\n雨夜 | `霓虹` 🙂"
		case "title":
			return "第三镜说明"
		default:
			return "样例文本"
		}
	case capability.KindNumber:
		for _, candidate := range []float64{1, 5, 10, 0, -1, 100} {
			if _, err := field.Coerce(candidate, field.Path); err == nil {
				return candidate
			}
		}
		t.Fatalf("字段 %s 找不到满足声明的数字样例", key)
	case capability.KindBoolean:
		return true
	case capability.KindStrings:
		return []any{"第一项", "第二项"}
	case capability.KindMap:
		return map[string]any{"k1": "v1", "k2": "v2"}
	case capability.KindCollection:
		element := map[string]any{}
		for _, name := range field.Element.Ordered() {
			element[name] = sampleFieldValue(t, name, field.Element[name])
		}
		return []any{element}
	}
	t.Fatalf("字段 %s 的 Kind %q 不该进文档", key, field.Kind)
	return nil
}

func sampleNode(t *testing.T, descriptor capability.Descriptor) (map[string]any, map[string]any) {
	t.Helper()
	node := map[string]any{"id": "n_sample", "type": descriptor.Type}
	expected := map[string]any{}
	for _, key := range documentFields(descriptor.Fields).Ordered() {
		field := descriptor.Fields[key]
		coerced, err := field.Coerce(sampleFieldValue(t, key, field), field.Path)
		if err != nil {
			t.Fatalf("%s.%s 的样例值不满足声明：%v", descriptor.Type, key, err)
		}
		field.Write(node, coerced)
		expected[key] = coerced
	}
	return node, expected
}

func nodeFromPatch(t *testing.T, descriptor capability.Descriptor, patch Patch) map[string]any {
	t.Helper()
	node := map[string]any{}
	if patch.ID != "" {
		node["id"] = patch.ID
	}
	for key, value := range patch.Values {
		field, declared := documentFields(descriptor.Fields)[key]
		if !declared {
			t.Fatalf("%s 解析出了不进文档的键 %s", descriptor.Type, key)
		}
		field.Write(node, value)
	}
	return node
}

func TestRenderParseAllAgentDescriptors(t *testing.T) {
	registry := capability.BuiltinRegistry()
	for _, descriptor := range registry.AgentList() {
		t.Run(descriptor.Type, func(t *testing.T) {
			node, expected := sampleNode(t, descriptor)
			text, err := Render(descriptor, node)
			if err != nil {
				t.Fatalf("Render 失败：%v", err)
			}
			if !strings.HasPrefix(text, "{\n  \"type\": \""+descriptor.Type+"\",") {
				t.Fatalf("文件头 type 应是 %q，实际：\n%s", descriptor.Type, text)
			}
			patch, repairs, err := ParseWithOptions(descriptor, text, ParseOptions{Node: node})
			if err != nil {
				t.Fatalf("Parse 失败：%v\n文件：\n%s", err, text)
			}
			if len(repairs) != 0 {
				t.Fatalf("打印器产出的文件不应需要修复，实际：%v", repairs)
			}
			if patch.Type != descriptor.Type || patch.ID != "n_sample" {
				t.Fatalf("文件身份错误：%#v", patch)
			}
			if !reflect.DeepEqual(patch.Values, expected) {
				t.Fatalf("补丁不一致\n得到：%#v\n期望：%#v", patch.Values, expected)
			}
			second, err := Render(descriptor, nodeFromPatch(t, descriptor, patch))
			if err != nil {
				t.Fatalf("重新 Render 失败：%v", err)
			}
			if text != second {
				t.Fatalf("往返后字节不同\n第一次：\n%s\n第二次：\n%s", text, second)
			}
		})
	}
}

func TestParseAddressAndSubmapScope(t *testing.T) {
	address, err := ParseAddress("./submap/submap-1/image-1.txt.prompt:1,2")
	if err != nil {
		t.Fatal(err)
	}
	if address.Path != "./submap/submap-1/image-1.txt" || address.Field != "prompt" {
		t.Fatalf("地址解析错误：%#v", address)
	}
	if address.Range.From != 1 || address.Range.To != 2 || address.Range.Single || address.Range.Append {
		t.Fatalf("行区间错误：%#v", address.Range)
	}
	submapID, localID, ok := SubmapAddressPath(address.Path)
	if !ok || submapID != "submap-1" || localID != "image-1" {
		t.Fatalf("submap 作用域 = %q/%q/%v", submapID, localID, ok)
	}
	if _, err := ParseAddress("/tmp/text-1.txt"); err == nil {
		t.Fatal("绝对路径应被拒绝")
	}
	if _, err := ParseAddress("./text/text-1.txt.bad.field"); err == nil {
		t.Fatal("字段名里的点应被拒绝")
	}
}

func TestBareNewlinesAndFieldBlocks(t *testing.T) {
	descriptor, ok := capability.BuiltinRegistry().Resolve("text")
	if !ok {
		t.Fatal("缺少 text 能力")
	}
	node := map[string]any{
		"id": "text-1", "type": "text", "title": "多行",
		"metadata": map[string]any{"content": "第一行。\n第二行。\n\n第三行。"},
	}
	text, err := Render(descriptor, node)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "\"content\": \"第一行。\n第二行。\n\n第三行。\"") {
		t.Fatalf("正文应保留裸换行：\n%s", text)
	}
	patch, _, err := ParseWithOptions(descriptor, text, ParseOptions{Node: node})
	if err != nil {
		t.Fatal(err)
	}
	if patch.Values["content"] != "第一行。\n第二行。\n\n第三行。" {
		t.Fatalf("正文往返错误：%#v", patch.Values["content"])
	}
	blocks, err := FieldBlocks(descriptor, node)
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range blocks {
		if block.Start < 0 || block.End > len(text) || block.Start >= block.End {
			t.Fatalf("字段块越界：%#v", block)
		}
		if got := text[block.Start:block.End]; !strings.HasPrefix(got, jsonKey(block.Key)+": ") {
			t.Fatalf("字段块 %s 与文本不一致：%q", block.Key, got)
		}
	}
}

func TestParseErrorsAreClosedAndLocated(t *testing.T) {
	textDescriptor, _ := capability.BuiltinRegistry().Resolve("text")
	imageDescriptor, _ := capability.BuiltinRegistry().Resolve("image")
	cases := []struct {
		name string
		d    capability.Descriptor
		text string
		code string
	}{
		{"unknown", textDescriptor, `{"type":"text","missing":"x"}`, CodeUnknownField},
		{"duplicate", textDescriptor, "{\n\"type\":\"text\",\n\"content\":\"a\",\n\"content\":\"b\"}", CodeDuplicate},
		{"type", textDescriptor, `{"type":"text","content":123}`, CodeType},
		{"value", textDescriptor, `{"type":"text","content":` + strconvQuote(strings.Repeat("界", 16001)) + `}`, CodeValue},
		{"identity", textDescriptor, `{"type":"image","referenceNodeIds":[]}`, CodeNodeMismatch},
		{"readonly", textDescriptor, `{"type":"text","locked":true}`, CodeNotInDocument},
		{"collection", imageDescriptor, `{"type":"image","referenceNodeIds":"image-1"}`, CodeType},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := Parse(test.d, test.text)
			failure, ok := err.(*Error)
			if !ok || failure.Code != test.code {
				t.Fatalf("错误 = %#v，期望 %s", err, test.code)
			}
		})
	}
}

func strconvQuote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func TestNormalizeTemplateAndValueLines(t *testing.T) {
	normalized, repairs := normalize("\ufeffa\r\nb")
	if normalized != "a\nb\n" || len(repairs) != 3 {
		t.Fatalf("规范化 = %q，修复 = %#v", normalized, repairs)
	}
	if got := Normalize(""); got != "" {
		t.Fatalf("空文本规范化 = %q", got)
	}
	if got := ValueLines("a\n\nb\n"); !reflect.DeepEqual(got, []string{"a", "", "b"}) {
		t.Fatalf("ValueLines = %#v", got)
	}
	if got := JoinValueLines([]string{"a", "", "b"}, true); got != "a\n\nb\n" {
		t.Fatalf("JoinValueLines = %q", got)
	}
	descriptor, _ := capability.BuiltinRegistry().Resolve("text")
	template, err := Template(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(template, `"id"`) {
		t.Fatalf("模板不应声明 id：\n%s", template)
	}
}

func TestWrapRepairDoesNotMutateParseValue(t *testing.T) {
	descriptor := capability.Descriptor{
		Type: "wrap-test", Label: "测试", Version: "1", DefaultWidth: 1, DefaultHeight: 1,
		Fields: capability.Fields{
			"content": {
				Path: "content", Kind: capability.KindString, Label: "正文", Order: 1,
				MaxRunes: 100, Editable: true, Wrap: true,
			},
		},
	}
	fields, err := capability.NewRegistry([]capability.Descriptor{descriptor})
	if err != nil {
		t.Fatal(err)
	}
	descriptor, _ = fields.Resolve("wrap-test")
	patch, repairs, err := Parse(descriptor, `{"type":"wrap-test","content":"第一句。第二句。"}`)
	if err != nil {
		t.Fatal(err)
	}
	if patch.Values["content"] != "第一句。第二句。" {
		t.Fatalf("Parse 不应改值：%#v", patch.Values["content"])
	}
	foundWrap := false
	for _, repair := range repairs {
		if repair.Kind == RepairSentenceWrap {
			foundWrap = true
		}
	}
	if !foundWrap {
		t.Fatalf("缺少切句修复回执：%#v", repairs)
	}
	node := map[string]any{}
	descriptor.Fields["content"].Write(node, patch.Values["content"])
	if node["content"] != "第一句。\n第二句。" {
		t.Fatalf("Field.Write 未切句：%#v", node["content"])
	}
}
