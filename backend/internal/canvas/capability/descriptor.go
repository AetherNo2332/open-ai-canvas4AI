package capability

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"

	"yingce/backend/internal/canvas/contract"
)

const (
	patchKindString  = "string"
	patchKindNumber  = "number"
	patchKindBoolean = "boolean"
	patchKindObject  = "object"
	patchKindArray   = "array"
)

type ConnectionPolicy struct {
	CanGraphSource     bool
	CanGraphTarget     bool
	CanSource          bool
	CanTarget          bool
	CanReference       bool
	AcceptedInputKinds []string
	RejectedInputKinds []string
	MaxInputCount      int
}

type PatchField struct {
	Path  string
	Kind  string
	Label string
	Order int
	// Limit > 0 时表示数值字段的绝对值上限（坐标用），避免模型写入离谱的几何值。
	Limit       float64
	Description string
	MaxRunes    int
	Min         *float64
	Max         *float64
	Integer     bool
	Enum        []string
	Properties  map[string]PatchField
	Required    []string
	Items       *PatchField
	MaxItems    int
	Format      string
	Nullable    bool
	// Resettable admits null only as an explicit removal of an optional node field.
	// Unlike Nullable, ApplyPatch never stores that null in the document.
	Resettable    bool
	AnyOf         []PatchField
	MapValues     *PatchField
	MaxProperties int
}

// NodeVariant 表示同一底层节点类型按 metadata.workflowKind 标记出的独立能力，
// 例如 text + workflowKind=character 的角色卡。变体可被发现、读取、引用和连线，
// 但不是可直接创建的节点类型：add_node 的 nodeType 枚举和 Resolve 都不会返回变体。
type NodeVariant struct {
	BaseType     string
	WorkflowKind string
}

// Descriptor is the server-owned canvas contract. Agent tools, creation,
// persistence and state projection all consume this registry; no caller owns a
// second node allow-list.
type Descriptor struct {
	Type           string
	Version        string
	Label          string
	Purpose        string
	GoodFor        []string
	NotIdealFor    []string
	Tradeoffs      []string
	Actions        []string
	DefaultWidth   float64
	DefaultHeight  float64
	InputKind      string
	GenerationMode string
	Connection     ConnectionPolicy
	CanUpdate      bool
	// Fields is the shortplay document/read/write contract. The legacy
	// PatchFields model remains during the incremental port so existing 4AI
	// commands keep their behaviour until S03 switches storage and commands.
	Fields          Fields
	VisualOnly      bool
	AddressField    string
	SummaryFields   []string
	DetailFields    []string
	ProjectionKind  string
	ProjectionField string
	PatchFields     map[string]PatchField
	CreateMetadata  func(content string) map[string]any
	Variant         *NodeVariant
}

func (d Descriptor) Metadata(content string) map[string]any {
	if d.CreateMetadata != nil {
		return d.CreateMetadata(content)
	}
	return map[string]any{"content": content, "status": "idle"}
}

func (d Descriptor) AllowsInput(kind string) bool {
	if !d.Connection.CanTarget {
		return false
	}
	kind = normalizeType(kind)
	for _, rejected := range d.Connection.RejectedInputKinds {
		if rejected == kind {
			return false
		}
	}
	if len(d.Connection.AcceptedInputKinds) == 0 {
		return true
	}
	for _, accepted := range d.Connection.AcceptedInputKinds {
		if accepted == kind {
			return true
		}
	}
	return false
}

func (d Descriptor) ValidatePatch(patch map[string]any) error {
	if !d.CanUpdate {
		return fmt.Errorf("%s 节点不支持更新", d.Label)
	}
	if len(patch) == 0 {
		return fmt.Errorf("%s 节点更新内容不能为空", d.Label)
	}
	for key, value := range patch {
		field, allowed := d.PatchFields[key]
		if !allowed {
			return fmt.Errorf("%s 节点不支持更新字段 %s", d.Label, key)
		}
		if err := field.validateValue(value, key, 0); err != nil {
			return fmt.Errorf("%s: %w", d.Label, err)
		}
	}
	if d.Type == "script" {
		if content, exists := patch["content"]; exists {
			if composer, also := patch["composerContent"]; also && content != composer {
				return fmt.Errorf("剧情正文 content 与 composerContent 不能冲突")
			}
		}
	}
	if rich, exists := patch["richText"]; exists && rich != nil {
		if text, also := patch["content"]; also && text != richTextPlainText(rich.(map[string]any)) {
			return fmt.Errorf("富文本与正文不能冲突")
		}
	}
	if raw, exists := patch["generationSpec"]; exists {
		encoded, _ := json.Marshal(raw)
		spec, _ := contract.Decode(encoded)
		if d.GenerationMode != "" && spec.Mode != d.GenerationMode {
			return fmt.Errorf("生成合同模式与节点类型不一致")
		}
		if text, also := patch["content"]; also && text != spec.Prompt {
			return fmt.Errorf("生成合同与提示词草稿不能冲突")
		}
		mirrors, _ := spec.NodeMetadata()
		mirrors["generationMode"] = spec.Mode
		for key, value := range patch {
			// prompt describes the asset; content/composer carries reference tokens
			// for the next generation and is already checked against spec.Prompt.
			if expected, exists := mirrors[key]; exists && key != "generationSpec" && key != "prompt" && fmt.Sprint(value) != fmt.Sprint(expected) {
				return fmt.Errorf("生成合同与字段 %s 不能冲突", key)
			}
		}
	}
	return nil
}

func (d Descriptor) ApplyPatch(node map[string]any, patch map[string]any) error {
	if err := d.ValidatePatch(patch); err != nil {
		return err
	}
	metadata, _ := node["metadata"].(map[string]any)
	conversionChanged := false
	if operation, exists := patch["conversionOperation"]; exists {
		state, _ := metadata["mediaConversion"].(map[string]any)
		conversionChanged = state["operation"] != operation
	}
	currentEngine := metadata["drawingEngine"]
	if document, ok := metadata["drawingDocument"].(map[string]any); ok {
		currentEngine = document["engine"]
	}
	if engine, exists := patch["drawingEngine"]; exists && (metadata["drawingId"] != nil || metadata["drawingDocument"] != nil) && currentEngine != engine {
		return fmt.Errorf("已绑定绘图文档不能更换引擎")
	}
	spec, err := updatedGenerationSpec(d, node, patch)
	if err != nil {
		return err
	}
	for key, value := range patch {
		field := d.PatchFields[key]
		parts := strings.Split(field.Path, ".")
		target := node
		for _, part := range parts[:len(parts)-1] {
			child, ok := target[part].(map[string]any)
			if !ok {
				child = map[string]any{}
				target[part] = child
			}
			target = child
		}
		leaf := parts[len(parts)-1]
		if value == nil && field.Resettable {
			delete(target, leaf)
			if key == "content" || key == "prompt" || key == "composerContent" {
				target[leaf] = ""
			}
		} else {
			target[leaf] = clonePatchValue(value)
		}
	}
	if value, exists := patch["richText"]; exists && value != nil {
		metadata, _ := node["metadata"].(map[string]any)
		metadata["content"] = richTextPlainText(value.(map[string]any))
	}
	if spec != nil {
		applyGenerationMirrors(node, *spec)
	}
	if conversionChanged {
		metadata, _ := node["metadata"].(map[string]any)
		state, _ := metadata["mediaConversion"].(map[string]any)
		state["schemaVersion"] = 1.0
		state["status"] = "idle"
		if key, _ := state["resultStorageKey"].(string); key != "" {
			state["status"] = "stale"
		}
		delete(state, "errorCode")
		delete(state, "errorMessage")
	}
	return nil
}

func clonePatchValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		copy := make(map[string]any, len(typed))
		for key, item := range typed {
			copy[key] = clonePatchValue(item)
		}
		return copy
	case []any:
		copy := make([]any, len(typed))
		for index, item := range typed {
			copy[index] = clonePatchValue(item)
		}
		return copy
	default:
		return value
	}
}

func (d Descriptor) ValidateConnection(fromKind string) error {
	if !d.Connection.CanTarget {
		return fmt.Errorf("%s 节点不能接收参考输入", d.Label)
	}
	if !d.AllowsInput(fromKind) {
		return fmt.Errorf("%s生成节点不接受%s输入", d.Label, inputKindLabel(fromKind))
	}
	return nil
}

func inputKindLabel(kind string) string {
	switch kind {
	case "image":
		return "图片"
	case "video":
		return "视频"
	case "audio":
		return "音频"
	case "character":
		return "角色卡"
	default:
		return "文本"
	}
}

func normalizeType(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func normalizeInputKind(value string) (string, error) {
	return normalizeCapabilityIdentifier(value, "canvas input kind")
}

func normalizeGenerationMode(value string) (string, error) {
	return normalizeCapabilityIdentifier(value, "canvas generation mode")
}

func normalizeProjectionKind(value string) (string, error) {
	return normalizeCapabilityIdentifier(value, "canvas projection kind")
}

func normalizeCapabilityIdentifier(value, label string) (string, error) {
	value = normalizeType(value)
	if value == "" {
		return "", nil
	}
	for index, r := range value {
		if index == 0 {
			if r < 'a' || r > 'z' {
				return "", fmt.Errorf("invalid %s %q", label, value)
			}
			continue
		}
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return "", fmt.Errorf("invalid %s %q", label, value)
		}
	}
	return value, nil
}

func normalizeConnectionKinds(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		kind, err := normalizeInputKind(value)
		if err != nil {
			return nil, err
		}
		if kind == "" {
			return nil, fmt.Errorf("canvas connection input kind cannot be empty")
		}
		if _, exists := seen[kind]; exists {
			return nil, fmt.Errorf("duplicate canvas connection input kind %q", kind)
		}
		seen[kind] = struct{}{}
		out = append(out, kind)
	}
	sort.Strings(out)
	return out, nil
}

func validatePatchPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("canvas patch field path cannot be empty")
	}
	for _, segment := range strings.Split(path, ".") {
		if segment == "" || segment == "." || segment == ".." || segment == "__proto__" || segment == "constructor" || segment == "prototype" {
			return fmt.Errorf("invalid canvas patch field path %q", path)
		}
		for index, r := range segment {
			if index == 0 {
				if r != '_' && !unicode.IsLetter(r) {
					return fmt.Errorf("invalid canvas patch field path %q", path)
				}
				continue
			}
			if r != '_' && r != '-' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				return fmt.Errorf("invalid canvas patch field path %q", path)
			}
		}
	}
	return nil
}

func normalizePatchFields(fields map[string]PatchField) (map[string]PatchField, error) {
	out := make(map[string]PatchField, len(fields))
	for key, field := range fields {
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("canvas patch field key cannot be empty")
		}
		if _, exists := out[key]; exists {
			return nil, fmt.Errorf("duplicate canvas patch field %q", key)
		}
		field.Path = strings.TrimSpace(field.Path)
		if err := validatePatchPath(field.Path); err != nil {
			return nil, err
		}
		field.Kind = normalizeType(field.Kind)
		field.Label = strings.TrimSpace(field.Label)
		field.Description = strings.TrimSpace(field.Description)
		if field.Label == "" {
			return nil, fmt.Errorf("canvas patch field %q requires a user-facing label", key)
		}
		if field.Order <= 0 {
			return nil, fmt.Errorf("canvas patch field %q requires a positive display order", key)
		}
		normalized, err := normalizeFieldSchema(field, 0)
		if err != nil {
			return nil, fmt.Errorf("canvas patch field %q: %w", key, err)
		}
		out[key] = normalized
	}
	return out, nil
}

func normalizeFieldSchema(field PatchField, depth int) (PatchField, error) {
	if depth > 64 {
		return PatchField{}, fmt.Errorf("patch schema nesting is too deep")
	}
	field.Kind = normalizeType(field.Kind)
	if field.Format != "" && field.Format != "rich-text" && field.Format != "generation-spec" && field.Format != "subtitles" && field.Format != "subtitle-highlights" && field.Format != "content-color" {
		return PatchField{}, fmt.Errorf("unknown field format")
	}
	if field.MaxRunes < 0 || field.MaxItems < 0 || math.IsNaN(field.Limit) || math.IsInf(field.Limit, 0) || field.Limit < 0 {
		return PatchField{}, fmt.Errorf("invalid schema limit")
	}
	for _, limit := range []*float64{field.Min, field.Max} {
		if limit != nil && (math.IsNaN(*limit) || math.IsInf(*limit, 0)) {
			return PatchField{}, fmt.Errorf("invalid number bound")
		}
	}
	if field.Min != nil && field.Max != nil && *field.Min > *field.Max {
		return PatchField{}, fmt.Errorf("inverted number bounds")
	}
	if (field.Min != nil || field.Max != nil || field.Integer || field.Limit > 0) && field.Kind != patchKindNumber {
		return PatchField{}, fmt.Errorf("number bounds on non-number")
	}
	if (field.MaxRunes > 0 || len(field.Enum) > 0) && field.Kind != patchKindString {
		return PatchField{}, fmt.Errorf("string constraints on non-string")
	}
	if len(field.Properties) > 0 && field.Kind != patchKindObject {
		return PatchField{}, fmt.Errorf("properties on non-object")
	}
	if field.Items != nil && field.Kind != patchKindArray {
		return PatchField{}, fmt.Errorf("items on non-array")
	}
	if field.MaxItems > 0 && field.Kind != patchKindArray {
		return PatchField{}, fmt.Errorf("array limits on non-array")
	}
	if (field.MapValues != nil || field.MaxProperties > 0) && field.Kind != patchKindObject {
		return PatchField{}, fmt.Errorf("dictionary contract on non-object")
	}
	if field.MaxProperties < 0 {
		return PatchField{}, fmt.Errorf("invalid object limit")
	}
	if len(field.AnyOf) > 0 {
		if field.Kind != "" || len(field.AnyOf) > 4 {
			return PatchField{}, fmt.Errorf("invalid field alternatives")
		}
		choices := make([]PatchField, len(field.AnyOf))
		for i, choice := range field.AnyOf {
			item, err := normalizeFieldSchema(choice, depth+1)
			if err != nil {
				return PatchField{}, err
			}
			choices[i] = item
		}
		field.AnyOf = choices
		return clonePatchField(field), nil
	}
	switch field.Kind {
	case patchKindString, patchKindNumber, patchKindBoolean:
	case patchKindObject:
		if len(field.Properties) == 0 && field.MapValues == nil {
			return PatchField{}, fmt.Errorf("object requires closed properties")
		}
		properties := map[string]PatchField{}
		for key, property := range field.Properties {
			if err := validatePatchPath(key); err != nil || strings.Contains(key, ".") {
				return PatchField{}, fmt.Errorf("invalid object property %q", key)
			}
			item, err := normalizeFieldSchema(property, depth+1)
			if err != nil {
				return PatchField{}, err
			}
			properties[key] = item
		}
		field.Properties = properties
		if field.MapValues != nil {
			item, err := normalizeFieldSchema(*field.MapValues, depth+1)
			if err != nil {
				return PatchField{}, err
			}
			field.MapValues = &item
		}
		field.Required = normalizeFields(field.Required)
		for _, key := range field.Required {
			if _, ok := properties[key]; !ok {
				return PatchField{}, fmt.Errorf("required property %q is undeclared", key)
			}
		}
	case patchKindArray:
		if field.Items == nil {
			return PatchField{}, fmt.Errorf("array requires typed items")
		}
		item, err := normalizeFieldSchema(*field.Items, depth+1)
		if err != nil {
			return PatchField{}, err
		}
		field.Items = &item
	default:
		return PatchField{}, fmt.Errorf("unsupported canvas patch field kind %q", field.Kind)
	}
	return clonePatchField(field), nil
}

func clonePatchField(field PatchField) PatchField {
	if field.AnyOf != nil {
		choices := make([]PatchField, len(field.AnyOf))
		for i, choice := range field.AnyOf {
			choices[i] = clonePatchField(choice)
		}
		field.AnyOf = choices
	}
	field.Enum = append([]string(nil), field.Enum...)
	field.Required = append([]string(nil), field.Required...)
	if field.Min != nil {
		value := *field.Min
		field.Min = &value
	}
	if field.Max != nil {
		value := *field.Max
		field.Max = &value
	}
	if field.Properties != nil {
		fields := map[string]PatchField{}
		for key, item := range field.Properties {
			fields[key] = clonePatchField(item)
		}
		field.Properties = fields
	}
	if field.Items != nil {
		item := clonePatchField(*field.Items)
		field.Items = &item
	}
	if field.MapValues != nil {
		item := clonePatchField(*field.MapValues)
		field.MapValues = &item
	}
	return field
}
