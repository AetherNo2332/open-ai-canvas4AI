package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"yingce/backend/internal/canvas/capability"
)

const maxFieldBaselineBytes = 8 << 20

func decodeClosedBaseline(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("invalid field baseline JSON")
	}
	return nil
}

func (baseline *FieldBaseline) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Exists *bool           `json:"exists"`
		Value  json.RawMessage `json:"value"`
	}
	if err := decodeClosedBaseline(raw, &wire); err != nil {
		return err
	}
	if wire.Exists == nil || wire.Value == nil {
		return fmt.Errorf("field baseline requires exists and value")
	}
	var value any
	if err := json.Unmarshal(wire.Value, &value); err != nil {
		return err
	}
	baseline.Exists, baseline.Value = *wire.Exists, value
	return nil
}

func (baseline *NodeFieldBaselines) UnmarshalJSON(raw []byte) error {
	type plain NodeFieldBaselines
	var value plain
	if err := decodeClosedBaseline(raw, &value); err != nil {
		return err
	}
	*baseline = NodeFieldBaselines(value)
	return nil
}

func safeBaselineJSON(value any, depth int) bool {
	if depth > 64 {
		return false
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == "__proto__" || key == "prototype" || key == "constructor" || !safeBaselineJSON(child, depth+1) {
				return false
			}
		}
	case []any:
		for _, child := range typed {
			if !safeBaselineJSON(child, depth+1) {
				return false
			}
		}
	}
	return true
}

func baselinePath(node map[string]any, path string) (any, bool) {
	var value any = node
	for _, part := range strings.Split(path, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return value, true
}

// Baselines observe the document before the whole command. Extra registered
// aliases are guards for implicit writes (richText/content, generation mirrors).
func validateFieldBaselines(doc Document, ops []Operation, registry *capability.Registry) (map[string]map[string]bool, error) {
	paths := map[string]map[string]bool{}
	budget := 0
	for _, op := range ops {
		baseline := op.FieldBaselines
		if baseline == nil {
			continue
		}
		if op.Type != "update_node" || baseline.NodeType == "" || len(baseline.NodeType) > 80 || len(baseline.WorkflowKind) > 128 || len(baseline.Fields) == 0 || len(baseline.Fields) > 128 {
			return nil, BadAuthRequest("字段基线结构无效")
		}
		if baseline.Generation != nil && *baseline.Generation < 0 {
			return nil, BadAuthRequest("字段基线世代无效")
		}
		raw, err := json.Marshal(baseline)
		if err != nil {
			return nil, BadAuthRequest("字段基线无效")
		}
		budget += len(raw)
		if budget > maxFieldBaselineBytes {
			return nil, BadAuthRequest("字段基线过大")
		}
		var generic any
		if json.Unmarshal(raw, &generic) != nil || !safeBaselineJSON(generic, 0) {
			return nil, BadAuthRequest("字段基线包含无效嵌套值")
		}
		node := entity(doc, "node:"+op.ID)
		if node == nil {
			return nil, failure("entity_deleted", "目标节点已删除")
		}
		meta, _ := node["metadata"].(map[string]any)
		if text(node["type"]) != baseline.NodeType || text(meta["workflowKind"]) != baseline.WorkflowKind {
			return nil, canvasFieldError("node:"+op.ID, "entity_version_conflict", "节点类型已改变")
		}
		descriptor, ok := registry.ResolveNode(text(node["type"]), text(meta["workflowKind"]))
		if !ok {
			return nil, BadAuthRequest("节点类型不支持字段基线")
		}
		if paths[op.ID] == nil {
			paths[op.ID] = map[string]bool{}
		}
		for alias := range op.Patch {
			if _, ok := baseline.Fields[alias]; !ok {
				return nil, BadAuthRequest("补丁缺少字段基线")
			}
		}
		for alias, observed := range baseline.Fields {
			field, ok := descriptor.PatchFields[alias]
			if !ok || alias == "__proto__" || alias == "prototype" || alias == "constructor" || (!observed.Exists && observed.Value != nil) || !safeBaselineJSON(observed.Value, 0) {
				return nil, BadAuthRequest("字段基线包含未授权字段或无效值")
			}
			actual, exists := baselinePath(node, field.Path)
			if exists != observed.Exists || (exists && !sameJSON(actual, observed.Value)) {
				return nil, canvasFieldError("node:"+op.ID, "entity_version_conflict", "目标字段已被其他操作修改")
			}
			paths[op.ID][field.Path] = true
		}
	}
	return paths, nil
}

func mergePatchIsStructural(op Operation, registry *capability.Registry, doc Document) bool {
	node := entity(doc, "node:"+op.ID)
	meta, _ := node["metadata"].(map[string]any)
	descriptor, _ := registry.ResolveNode(text(node["type"]), text(meta["workflowKind"]))
	for alias := range op.Patch {
		path := descriptor.PatchFields[alias].Path
		for _, prefix := range []string{"metadata.inputBindings", "metadata.generationSpec", "metadata.supersededByNodeId", "metadata.drawing", "metadata.storyboard", "metadata.batchTable", "metadata.mediaConversion"} {
			if strings.HasPrefix(path, prefix) {
				return true
			}
		}
	}
	references := false
	walkNodeReferences(op.Patch, func(string) { references = true })
	return references
}

// A container replacement is not equivalent to a guarded child assignment.
// Missing parents can be created, but null/scalar parents cannot be replaced
// under a child-only baseline. Empty-container presence is also preserved.
func coveredFieldChanges(before, after any, beforeExists, afterExists bool, path string, allowed map[string]bool) bool {
	if allowed[path] {
		return true
	}
	if beforeExists == afterExists && sameJSON(before, after) {
		return true
	}
	left, leftMap := before.(map[string]any)
	right, rightMap := after.(map[string]any)
	if (!leftMap && beforeExists) || (!rightMap && afterExists) || (!leftMap && !rightMap) {
		return false
	}
	keys := map[string]bool{}
	for key := range left {
		keys[key] = true
	}
	for key := range right {
		keys[key] = true
	}
	if len(keys) == 0 {
		return false
	}
	for key := range keys {
		old, oldOK := left[key]
		next, nextOK := right[key]
		child := key
		if path != "" {
			child = path + "." + key
		}
		if !coveredFieldChanges(old, next, oldOK, nextOK, child, allowed) {
			return false
		}
	}
	return true
}

func guardedReferenceOwner(node map[string]any, paths map[string]bool) bool {
	if node["type"] == "script" || node["type"] == "batch-table" {
		return false
	}
	meta, _ := node["metadata"].(map[string]any)
	owner := false
	for _, field := range []string{"generationSpec", "inputBindings"} {
		if _, exists := meta[field]; exists {
			owner = true
			if !paths["metadata."+field] {
				return false
			}
		}
	}
	return owner
}

func fieldMergeTargets(doc, next Document, ops []Operation, registry *capability.Registry, paths map[string]map[string]bool, referenceReads, inputReads []string) map[string]bool {
	eligible := map[string]bool{}
	strict := map[string]bool{}
	for _, op := range ops {
		// Mixed structural batches retain all their original entity preconditions.
		if op.Type != "update_node" {
			return map[string]bool{}
		}
		if op.FieldBaselines == nil || op.FieldBaselines.Generation == nil || mergePatchIsStructural(op, registry, doc) {
			strict[op.ID] = true
		} else {
			eligible["node:"+op.ID] = true
		}
		for _, id := range []string{op.FromNodeID, op.ToNodeID, op.SourceNodeID, op.ParentID} {
			if id != "" {
				strict[id] = true
			}
		}
		walkNodeReferences(op.Patch, func(id string) { strict[id] = true })
	}
	for _, id := range referenceReads {
		if !guardedReferenceOwner(entity(doc, "node:"+id), paths[id]) {
			strict[id] = true
		}
	}
	for _, id := range inputReads {
		strict[id] = true
	}
	for key := range eligible {
		id := strings.TrimPrefix(key, "node:")
		if strict[id] || drawingPresent(entity(doc, key)) || !coveredFieldChanges(entity(doc, key), entity(next, key), true, true, "", paths[id]) {
			delete(eligible, key)
		}
	}
	return eligible
}
