package capability

import (
	"fmt"
	"math"
	"sort"
)

// titleField is the common node title field used by the kernel contract.
func titleField() Field {
	return Field{
		Path: "title", Kind: KindString, Label: "节点名称", Order: 10,
		MaxRunes: maxAgentNodeTitleRunes, Writable: true, Editable: true,
		Description: "节点标题",
	}
}

// lockedField is a program-owned marker: visible to readers, never writable.
func lockedField() Field {
	return Field{
		Path: "metadata.locked", Kind: KindBoolean, Label: "锁定", Order: 5,
		Projected: true, Summary: true,
		Description: "程序托管标记；锁定后 agent 不得增删改该节点，需用户解锁",
	}
}

func withLockedField(fields Fields) Fields {
	if fields == nil {
		fields = Fields{}
	}
	fields["locked"] = lockedField()
	return fields
}

func textBodyFields(contentPath, contentLabel, contentDescription string) Fields {
	return Fields{
		"title": titleField(),
		"content": {
			Path: contentPath, Kind: KindString, Label: contentLabel, Order: 20,
			MaxRunes: maxAgentNodeContentRunes, Editable: true, Projected: true,
			Summary: true, Searchable: true, Description: contentDescription,
		},
	}
}

// kernelFieldsFromPatch turns the representable subset of 4AI's legacy patch
// fields into the new declaration. Complex objects stay in the legacy contract
// until a consumer-specific adapter is introduced; they are not silently
// flattened into an unrelated Kind.
func kernelFieldsFromPatch(patch map[string]PatchField, summaryFields, detailFields []string) Fields {
	keys := make([]string, 0, len(patch))
	for key := range patch {
		keys = append(keys, key)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		left, right := kernelFieldPriority(keys[i]), kernelFieldPriority(keys[j])
		if left != right {
			return left < right
		}
		return keys[i] < keys[j]
	})
	out := Fields{}
	paths := map[string]string{}
	for _, key := range keys {
		field, ok := kernelFieldFromPatch(key, patch[key], summaryFields, detailFields)
		if !ok {
			continue
		}
		if existing, duplicate := paths[field.Path]; duplicate {
			if kernelFieldPriority(key) >= kernelFieldPriority(existing) {
				continue
			}
			delete(out, existing)
		}
		paths[field.Path] = key
		out[key] = field
	}
	return out
}

func kernelFieldPriority(key string) int {
	switch key {
	case "content":
		return 0
	case "title":
		return 1
	case "prompt":
		return 2
	case "composerContent":
		return 3
	default:
		return 10
	}
}

func kernelFieldFromPatch(key string, legacy PatchField, summaryFields, detailFields []string) (Field, bool) {
	field := Field{
		Path: legacy.Path, Label: legacy.Label, Order: legacy.Order,
		Description: legacy.Description, MaxRunes: legacy.MaxRunes, MaxItems: legacy.MaxItems,
		Enum: append([]string(nil), legacy.Enum...), Writable: true,
	}
	if field.Label == "" {
		field.Label = key
	}
	if field.Order <= 0 {
		field.Order = 100
	}
	if kernelFieldListed(key, legacy.Path, detailFields) {
		field.Projected = true
	}
	if kernelFieldListed(key, legacy.Path, summaryFields) {
		field.Summary = true
	}
	if !kernelFieldKind(key, legacy, &field) {
		return Field{}, false
	}
	return field, field.Path != ""
}

func kernelFieldKind(key string, legacy PatchField, field *Field) bool {
	switch legacy.Kind {
	case patchKindString:
		field.Kind = KindString
		field.Editable = true
	case patchKindNumber:
		field.Kind = KindNumber
		field.Bounds = kernelBounds(legacy)
		if legacy.Integer {
			field.Normalize = kernelIntegerNormalizer(*field)
		}
	case patchKindBoolean:
		field.Kind = KindBoolean
	case patchKindArray:
		if legacy.Items == nil || legacy.Items.Kind != patchKindString {
			element, ok := kernelCollectionElement(legacy.Items)
			if !ok {
				return false
			}
			field.Kind = KindCollection
			field.Element = element
		} else {
			field.Kind = KindStrings
		}
	case patchKindObject:
		if legacy.MapValues == nil || legacy.MapValues.Kind != patchKindString {
			return false
		}
		field.Kind = KindMap
		field.MaxItems = legacy.MaxProperties
	default:
		return false
	}
	if legacy.Kind == patchKindString && (key == "referenceNodeIds" ||
		key == "referenceTextNodeIds" ||
		key == "videoStartFrameNodeId" ||
		key == "videoEndFrameNodeId") {
		field.Reference = true
	}
	return true
}

func kernelIntegerNormalizer(field Field) func(any, string) (any, error) {
	raw := field
	raw.Normalize = nil
	return func(value any, path string) (any, error) {
		coerced, err := raw.Coerce(value, path)
		if err != nil {
			return nil, err
		}
		number := coerced.(float64)
		if number != math.Trunc(number) {
			return nil, fmt.Errorf("%s 必须是整数", path)
		}
		return number, nil
	}
}

func kernelBounds(legacy PatchField) *NumberBounds {
	if legacy.Min == nil && legacy.Max == nil && legacy.Limit <= 0 {
		return nil
	}
	minimum, maximum := 0.0, 0.0
	if legacy.Min != nil {
		minimum = *legacy.Min
	} else if legacy.Limit > 0 {
		minimum = -legacy.Limit
	}
	if legacy.Max != nil {
		maximum = *legacy.Max
	} else if legacy.Limit > 0 {
		maximum = legacy.Limit
	}
	return &NumberBounds{Min: minimum, Max: maximum}
}

func kernelCollectionElement(item *PatchField) (Fields, bool) {
	if item == nil || item.Kind != patchKindObject || len(item.Properties) == 0 {
		return nil, false
	}
	out := Fields{}
	for _, key := range sortedPatchKeys(item.Properties) {
		property := item.Properties[key]
		converted, ok := kernelElementField(key, property)
		if !ok {
			return nil, false
		}
		out[key] = converted
	}
	return out, true
}

func sortedPatchKeys(fields map[string]PatchField) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func kernelElementField(key string, legacy PatchField) (Field, bool) {
	field := Field{Path: key, Label: key, Order: len(key) + 1, Description: legacy.Description, MaxRunes: legacy.MaxRunes, MaxItems: legacy.MaxItems, Enum: append([]string(nil), legacy.Enum...)}
	switch legacy.Kind {
	case patchKindString:
		field.Kind = KindString
	case patchKindNumber:
		field.Kind = KindNumber
		field.Bounds = kernelBounds(legacy)
	case patchKindBoolean:
		field.Kind = KindBoolean
	case patchKindArray:
		if legacy.Items == nil || legacy.Items.Kind != patchKindString {
			return Field{}, false
		}
		field.Kind = KindStrings
	case patchKindObject:
		if legacy.MapValues == nil || legacy.MapValues.Kind != patchKindString {
			return Field{}, false
		}
		field.Kind = KindMap
	default:
		return Field{}, false
	}
	return field, true
}

func kernelFieldListed(key, path string, listed []string) bool {
	for _, item := range listed {
		if item == key || item == path || item == "metadata."+key || item == "metadata."+path {
			return true
		}
	}
	return false
}

// enrichKernelDescriptor adds the shortplay contract without changing the
// legacy PatchFields consumed by current commands. Explicit declarations win
// over converted legacy fields.
func enrichKernelDescriptor(descriptor *Descriptor) {
	fields := kernelFieldsFromPatch(descriptor.PatchFields, descriptor.SummaryFields, descriptor.DetailFields)
	for key, field := range descriptor.Fields {
		fields[key] = field
	}
	switch descriptor.Type {
	case "text", "markdown":
		fields["title"] = titleField()
		fields["content"] = Field{
			Path: "metadata.content", Kind: KindString, Label: "正文", Order: 20,
			MaxRunes: maxAgentNodeContentRunes, Editable: true, Projected: true,
			Summary: true, Searchable: true, Wrap: true, Description: "节点正文",
		}
		descriptor.AddressField = "content"
	case "image", "video", "audio":
		fields["title"] = titleField()
		fields["content"] = Field{
			Path: "metadata.composerContent", Kind: KindString, Label: "下一版提示词", Order: 20,
			MaxRunes: maxAgentNodeContentRunes, Writable: true, Editable: true,
			Projected: true, Summary: true, Searchable: true,
			Description: "下次生成使用的提示词草稿；不覆盖已提交提示词或媒体结果",
		}
		fields["referenceNodeIds"] = Field{
			Path: "metadata.referenceNodeIds", Kind: KindStrings, Label: "参考节点", Order: 60,
			MaxItems: 16, Writable: true, Projected: true, Summary: true, Reference: true,
			Description: "作为本节点生成输入的素材/角色节点 ID 数组（整组替换，[] 清空）",
		}
		fields["referenceTextNodeIds"] = Field{
			Path: "metadata.referenceTextNodeIds", Kind: KindStrings, Label: "参考文本", Order: 61,
			MaxItems: 16, Writable: true, Projected: true, Summary: true, Reference: true,
			Description: "作为本节点文本输入的文本节点 ID 数组（整组替换，[] 清空）；顺序即 @文本N 编号",
		}
		fields["derivedFromNodeIds"] = Field{
			Path: "metadata.derivedFromNodeIds", Kind: KindStrings, Label: "派生来源", Order: 62,
			MaxItems: 16, Projected: true, Summary: true,
			Description: "派生血缘：裁剪、切图、抽帧、合片等本地产物的来源节点 ID；只读，不作为生成参考输入",
		}
		descriptor.AddressField = "content"
	case "script":
		fields["title"] = titleField()
		fields["content"] = Field{
			Path: "metadata.composerContent", Kind: KindString, Label: "剧情/大纲", Order: 20,
			MaxRunes: maxAgentDocumentRunes, Writable: true, Editable: true,
			Projected: true, Summary: true, Searchable: true, Description: "剧情/大纲",
		}
		descriptor.AddressField = "content"
	case "config":
		fields["title"] = titleField()
		fields["content"] = Field{
			Path: "metadata.composerContent", Kind: KindString, Label: "生成提示词", Order: 20,
			MaxRunes: maxAgentNodeContentRunes, Writable: true, Editable: true,
			Projected: true, Summary: true, Searchable: true,
			Description: "下一次生成使用的提示词草稿",
		}
		fields["referenceNodeIds"] = Field{
			Path: "metadata.referenceNodeIds", Kind: KindStrings, Label: "参考节点", Order: 60,
			MaxItems: 16, Writable: true, Projected: true, Summary: true, Reference: true,
			Description: "作为生成输入的素材/角色节点 ID 数组",
		}
		descriptor.AddressField = "content"
	case "batch-table":
		fields["globalPrompt"] = Field{
			Path: "metadata.batchTable.globalPrompt", Kind: KindString, Label: "全局提示词", Order: 40,
			MaxRunes: maxAgentNodeContentRunes, Writable: true, Editable: true,
			Projected: true, Summary: true, Searchable: true,
			Description: "覆盖每一行的全局提示词；为空时只看行提示词",
		}
	case "frame":
		descriptor.VisualOnly = true
	}
	if descriptor.Type == "video" {
		for index, item := range []struct {
			key   string
			label string
		}{
			{"videoStartFrameNodeId", "首帧"},
			{"videoEndFrameNodeId", "尾帧"},
		} {
			fields[item.key] = Field{
				Path: "metadata." + item.key, Kind: KindString, Label: item.label,
				Order: 41 + index, MaxRunes: 80, Writable: true, Projected: true,
				Summary: true, Reference: true, Description: "已连接图片节点ID；空字符串取消",
			}
		}
	}
	// Lock state is a program-owned marker in the kernel surface even though
	// the legacy patch model still exposes the lock toggle to human/editor
	// commands.
	if _, exists := fields["locked"]; exists {
		fields["locked"] = lockedField()
	}
	descriptor.Fields = fields
}

func shortplayAssetDescriptor() Descriptor {
	fields := withLockedField(Fields{
		"title":   titleField(),
		"content": {Path: "metadata.content", Kind: KindString, Label: "设定正文", Order: 20, MaxRunes: maxAgentNodeContentRunes, Editable: true, Projected: true, Summary: true, Searchable: true, Description: "资产设定正文"},
		"shortplayAssetAppearance": {
			Path: "metadata.shortplayAssetAppearance", Kind: KindString, Label: "外貌描述", Order: 30,
			MaxRunes: 2000, Writable: true, Editable: true, Projected: true, Summary: true,
			Description: "可直接用于生图的外貌描述；与叙事设定分开，生图只用这个字段",
		},
		"shortplayAssetAliases": {
			Path: "metadata.shortplayAssetAliases", Kind: KindStrings, Label: "别名", Order: 40,
			MaxItems: 12, Writable: true, Projected: true, Description: "资产的其它称谓",
		},
		"shortplayAssetVoice": {
			Path: "metadata.shortplayAssetVoice", Kind: KindString, Label: "音色", Order: 50,
			MaxRunes: 500, Writable: true, Projected: true, Description: "角色专属的音色描述提示词",
		},
		"shortplayAssetLocked": {
			Path: "metadata.shortplayAssetLocked", Kind: KindBoolean, Label: "锁定", Order: 60,
			Writable: true, Projected: true, Description: "锁定后设定类字段不得由 agent 或画布回写改写",
		},
		"shortplayAssetCategory": {
			Path: "metadata.shortplayAssetCategory", Kind: KindString, Label: "资产分类", Order: 70,
			Enum: []string{"character", "prop", "scene"}, Writable: true, Projected: true, Summary: true,
			Description: "资产分类",
		},
		"shortplayAssetLook": {
			Path: "metadata.shortplayAssetLook", Kind: KindString, Label: "造型名", Order: 80,
			MaxRunes: 32, Writable: true, Projected: true, Summary: true,
			Description: "要给哪套造型出图；未命中按基础造型",
		},
		"shortplayAssetId": {
			Path: "metadata.shortplayAssetId", Kind: KindNumber, Label: "项目资产", Order: 90,
			Projected: true, Summary: true, Description: "资产表行 ID；为空表示局部卡",
		},
	})
	return Descriptor{
		Type: "shortplay-asset", Version: "1", Label: "短剧资产卡", DefaultWidth: 264, DefaultHeight: 352,
		Purpose:      "引用短剧项目资产（角色/道具/场景）并承载其设定。",
		GoodFor:      []string{"多集之间保持同一人物、道具、场景一致", "把资产外貌描述作为生图提示词来源"},
		NotIdealFor:  []string{"承载分镜结构与逐镜关系（应使用分镜表节点）", "只写一次性的临时描述"},
		Tradeoffs:    []string{"集画布上的节点字段是显示快照：以资产表为准", "shortplayAssetId 为空即局部卡"},
		InputKind:    "shortplay-asset",
		Connection:   ConnectionPolicy{CanSource: true, CanReference: true},
		CanUpdate:    true,
		AddressField: "content",
		Fields:       fields,
		PatchFields: map[string]PatchField{
			"title":   {Path: "title", Kind: patchKindString, Label: "节点名称", Order: 10, MaxRunes: maxAgentNodeTitleRunes},
			"content": {Path: "metadata.content", Kind: patchKindString, Label: "设定正文", Order: 20, MaxRunes: maxAgentNodeContentRunes},
		},
		CreateMetadata: func(content string) map[string]any {
			return map[string]any{"status": "idle", "content": content}
		},
	}
}
