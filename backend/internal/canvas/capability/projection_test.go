package capability

import "testing"

// 旧数据里 nullable 字段可以存 null；读取投影原先在 string 分支直接断言，nil 值会让
// 后端 panic（canvas_get_state 按 nodeIds 读取节点即可触发）。这里覆盖顶层、对象属性、
// 数组元素与字典值四种位置。
func TestEditableValuesSkipsNullNullableFields(t *testing.T) {
	nullableString := PatchField{Kind: patchKindString, Nullable: true}
	descriptor := Descriptor{
		Type: "test",
		PatchFields: map[string]PatchField{
			"title":   {Path: "title", Kind: patchKindString, Nullable: true},
			"note":    {Path: "meta.note", Kind: patchKindString, Nullable: true},
			"labels":  {Path: "labels", Kind: patchKindArray, Items: &PatchField{Kind: patchKindString, Nullable: true}},
			"dict":    {Path: "dict", Kind: patchKindObject, MapValues: &nullableString},
			"profile": {Path: "profile", Kind: patchKindObject, Properties: map[string]PatchField{"voice": {Kind: patchKindString, Nullable: true}}},
		},
	}
	node := map[string]any{
		"title":   nil,
		"meta":    map[string]any{"note": nil},
		"labels":  []any{"a", nil, "b"},
		"dict":    map[string]any{"k": nil, "keep": "v"},
		"profile": map[string]any{"voice": nil},
	}

	values, truncated := descriptor.EditableValues(node, 0)

	if _, exists := values["title"]; exists {
		t.Fatalf("null title must be omitted: %#v", values["title"])
	}
	if _, exists := values["note"]; exists {
		t.Fatalf("null nested note must be omitted: %#v", values["note"])
	}
	labels, ok := values["labels"].([]any)
	if !ok || len(labels) != 2 || labels[0] != "a" || labels[1] != "b" {
		t.Fatalf("null array item must be skipped: %#v", values["labels"])
	}
	dict, ok := values["dict"].(map[string]any)
	if !ok || dict["keep"] != "v" {
		t.Fatalf("null dictionary value must be skipped: %#v", values["dict"])
	}
	if _, exists := dict["k"]; exists {
		t.Fatalf("null dictionary value must be omitted: %#v", dict)
	}
	profile, ok := values["profile"].(map[string]any)
	if !ok || len(profile) != 0 {
		t.Fatalf("null object property must be omitted: %#v", values["profile"])
	}
	if len(truncated) != 0 {
		t.Fatalf("unexpected truncation: %#v", truncated)
	}
}

// 声明为 string 但实际不是 string 的值同样不能 panic：读取投影跳过即可。
func TestEditableValuesSkipsValueWithUnexpectedType(t *testing.T) {
	descriptor := Descriptor{
		Type:        "test",
		PatchFields: map[string]PatchField{"title": {Path: "title", Kind: patchKindString}},
	}
	values, _ := descriptor.EditableValues(map[string]any{"title": true}, 0)
	if _, exists := values["title"]; exists {
		t.Fatalf("non-string value must be omitted: %#v", values["title"])
	}
}
