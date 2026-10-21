package references

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"infinite-canvas/backend/internal/canvas/capability"
	"infinite-canvas/backend/internal/canvas/contract"
)

func maps(v any) []map[string]any {
	out := []map[string]any{}
	switch a := v.(type) {
	case []any:
		for _, v := range a {
			if m, ok := v.(map[string]any); ok {
				out = append(out, m)
			}
		}
	case []map[string]any:
		out = a
	}
	return out
}
func stringsList(v any) []string {
	out := []string{}
	switch a := v.(type) {
	case []string:
		return append(out, a...)
	case []any:
		for _, v := range a {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
func str(v any) string { s, _ := v.(string); return s }
func meta(n map[string]any) map[string]any {
	m, _ := n["metadata"].(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}
func obj(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}
func number(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}
func fail(reason, field, message string) error {
	return &Error{Reason: reason, Field: field, Message: message}
}
func index(doc map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, n := range maps(doc["nodes"]) {
		out[str(n["id"])] = n
	}
	return out
}
func rowFor(node map[string]any, kind, id string) map[string]any {
	for _, row := range maps(obj(meta(node)[kind])["rows"]) {
		if str(row["id"]) == id {
			return row
		}
	}
	return nil
}
func descriptor(r *capability.Registry, n map[string]any) (capability.Descriptor, bool) {
	return r.ResolveNode(str(n["type"]), str(meta(n)["workflowKind"]))
}
func bindingID(id, role string) string { return "node:" + id + ":" + role }
func bindNode(idx map[string]map[string]any, r *capability.Registry, id, role string) (contract.ReferenceBinding, error) {
	n := idx[id]
	if n == nil {
		return contract.ReferenceBinding{}, fail("reference_missing", "nodeId", "引用节点不存在："+id)
	}
	d, ok := descriptor(r, n)
	if !ok || d.InputKind == "" {
		return contract.ReferenceBinding{}, fail("invalid_reference", "nodeId", "节点不支持引用："+id)
	}
	kind := d.InputKind
	if kind == "character" {
		if role == "source-text" {
			kind = "text"
		} else {
			kind = "image"
		}
	}
	if kind == "text" && role == "reference" {
		role = "source-text"
	}
	return contract.ReferenceBinding{ID: bindingID(id, role), NodeID: id, MediaType: kind, Role: role, Resolution: "latest"}, nil
}
func decodeBindings(v any) ([]contract.ReferenceBinding, error) {
	if v == nil {
		return []contract.ReferenceBinding{}, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := []contract.ReferenceBinding{}
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, fail("invalid_reference", "inputBindings", "引用必须是结构化数组")
	}
	spec := contract.GenerationSpec{Version: contract.GenerationVersion, Mode: "video", ReferenceBindings: out, TextInputMode: "prompt-only"}
	if err = spec.Validate(); err != nil {
		return nil, fail("invalid_reference", "inputBindings", err.Error())
	}
	return out, nil
}
func Resolve(doc map[string]any, scope Scope, registry *capability.Registry) (Resolution, error) {
	if registry == nil {
		registry = capability.BuiltinRegistry()
	}
	return resolveIndexed(index(doc), scope, registry)
}
func resolveIndexed(idx map[string]map[string]any, scope Scope, registry *capability.Registry) (Resolution, error) {
	result := Resolution{Bindings: []contract.ReferenceBinding{}, DependencyNodeIDs: []string{scope.NodeID}, Edges: []EdgeIntent{}}
	node := idx[scope.NodeID]
	if node == nil {
		return result, fail("reference_missing", "nodeId", "引用所属节点不存在")
	}
	metadata := meta(node)
	bindings := []contract.ReferenceBinding{}
	appendNode := func(id, role string) error {
		b, err := bindNode(idx, registry, id, role)
		if err == nil {
			bindings = append(bindings, b)
		}
		return err
	}
	switch scope.Kind {
	case "generation":
		raw, err := json.Marshal(metadata["generationSpec"])
		if err != nil {
			return result, err
		}
		spec, err := contract.Decode(raw)
		if err != nil {
			return result, fail("invalid_reference", "generationSpec", err.Error())
		}
		bindings = spec.ReferenceBindings
	case "node":
		var err error
		bindings, err = decodeBindings(metadata["inputBindings"])
		if err != nil {
			return result, err
		}
	case "storyboard-context":
		if str(node["type"]) != "script" {
			return result, fail("invalid_reference", "nodeId", "引用所属节点不是分镜表")
		}
		for _, id := range stringsList(obj(metadata["storyboard"])["referenceNodeIds"]) {
			if err := appendNode(id, "reference"); err != nil {
				return result, err
			}
		}
	case "storyboard-row":
		row := rowFor(node, "storyboard", scope.RowID)
		if str(node["type"]) != "script" || row == nil {
			return result, fail("reference_missing", "rowId", "分镜行不存在")
		}
		if scope.Mode == "video" && str(metadata["storyboardVideoInputMode"]) == "keyframe" {
			id := str(row["imageNodeId"])
			if id == "" {
				return result, fail("reference_missing", "imageNodeId", "关键帧模式需要已绑定的分镜图")
			}
			if err := appendNode(id, "first-frame"); err != nil {
				return result, err
			}
		}
		assets := append([]map[string]any{}, maps(row["assetBindings"])...)
		sort.SliceStable(assets, func(i, j int) bool { return number(assets[i]["priority"]) > number(assets[j]["priority"]) })
		seen := map[string]bool{}
		for _, asset := range assets {
			id, role := str(asset["nodeId"]), str(asset["role"])
			if seen[id] {
				return result, fail("invalid_reference", "assetBindings", "分镜素材重复")
			}
			seen[id] = true
			b, err := bindNode(idx, registry, id, "reference")
			if err != nil {
				return result, err
			}
			d, _ := descriptor(registry, idx[id])
			valid := false
			switch role {
			case "character":
				valid = d.InputKind == "character" || b.MediaType == "image"
			case "environment", "wardrobe", "prop", "weapon", "style":
				valid = b.MediaType == "image"
			case "motion":
				valid = b.MediaType == "image" || b.MediaType == "video"
			case "audio":
				valid = b.MediaType == "audio"
			}
			if !valid {
				return result, fail("invalid_reference", "assetBindings", "素材类型与分镜角色不一致："+id)
			}
			bindings = append(bindings, b)
		}
		for _, c := range maps(row["characters"]) {
			id := str(c["characterImageNodeId"])
			if id == "" && str(c["characterAssetId"]) != "" {
				for key, n := range idx {
					if str(meta(n)["characterAssetId"]) == str(c["characterAssetId"]) {
						if id != "" {
							return result, fail("reference_ambiguous", "characters", "角色资产匹配到多个节点，请明确选择引用")
						}
						id = key
					}
				}
			}
			if id != "" {
				if err := appendNode(id, "reference"); err != nil {
					return result, err
				}
			}
		}
		for _, id := range stringsList(obj(metadata["storyboard"])["referenceNodeIds"]) {
			if err := appendNode(id, "reference"); err != nil {
				return result, err
			}
		}
	case "batch-row":
		row := rowFor(node, "batchTable", scope.RowID)
		if row == nil {
			return result, fail("reference_missing", "rowId", "批量表行不存在")
		}
		ids := stringsList(row["inputNodeIds"])
		if len(ids) > 10 {
			return result, fail("reference_capacity", "inputNodeIds", "批量行最多10张参考图")
		}
		for _, id := range ids {
			if id == "" {
				continue
			}
			if err := appendNode(id, "reference"); err != nil {
				return result, err
			}
		}
		for _, id := range stringsList(row["textNodeIds"]) {
			if id == "" {
				continue
			}
			if err := appendNode(id, "source-text"); err != nil {
				return result, err
			}
		}
	default:
		return result, fail("invalid_reference", "scope", "未知引用作用域")
	}
	if len(bindings) > 100 {
		return result, fail("reference_capacity", "inputBindings", "引用数量超过100项")
	}
	special := map[string]bool{}
	for _, b := range bindings {
		if b.Role == "first-frame" || b.Role == "last-frame" || b.Role == "mask" {
			special[b.NodeID] = true
		}
	}
	seen := map[string]bool{}
	sourceIDs := map[string]bool{}
	target, _ := descriptor(registry, node)
	for _, b := range bindings {
		if b.Role == "reference" && special[b.NodeID] {
			continue
		}
		key := b.NodeID + "\x00" + b.Role
		if b.NodeID == "" {
			key = b.ID
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		if b.NodeID != "" {
			n := idx[b.NodeID]
			if n == nil || b.NodeID == scope.NodeID {
				return result, fail("reference_missing", "nodeId", "引用节点缺失或指向自身："+b.NodeID)
			}
			d, ok := descriptor(registry, n)
			if !ok {
				return result, fail("invalid_reference", "nodeId", "引用节点类型不受支持")
			}
			if d.InputKind != b.MediaType && !(d.InputKind == "character" && (b.MediaType == "image" || b.MediaType == "audio" || b.MediaType == "text")) {
				return result, fail("invalid_reference", "mediaType", "引用媒体类型与节点不符")
			}
			if (b.Role == "first-frame" || b.Role == "last-frame" || b.Role == "mask") && b.MediaType != "image" {
				return result, fail("invalid_reference", "role", "首尾帧与遮罩必须引用图片")
			}
			if scope.Kind == "node" || scope.Kind == "generation" {
				allowed := false
				for _, kind := range target.Connection.AcceptedInputKinds {
					if kind == d.InputKind {
						allowed = true
					}
				}
				if !target.Connection.CanTarget || !allowed {
					return result, fail("invalid_reference", "inputBindings", "目标节点不接受该引用类型")
				}
			}
			if scope.Kind == "batch-row" && b.Role != "source-text" && b.MediaType != "image" {
				return result, fail("invalid_reference", "inputNodeIds", "批量素材必须是图片")
			}
			sourceIDs[b.NodeID] = true
			result.DependencyNodeIDs = append(result.DependencyNodeIDs, b.NodeID)
		} else if b.ResourceID == "" && b.TransientID == "" {
			return result, fail("reference_missing", "nodeId", "引用缺少来源")
		}
		b.Order = len(result.Bindings)
		result.Bindings = append(result.Bindings, b)
	}
	if (scope.Kind == "node" || scope.Kind == "generation") && target.Connection.MaxInputCount > 0 && len(sourceIDs) > target.Connection.MaxInputCount {
		return result, fail("reference_capacity", "inputBindings", fmt.Sprintf("目标节点最多%d个输入", target.Connection.MaxInputCount))
	}
	edgeSources := map[string]bool{}
	for _, b := range result.Bindings {
		if b.NodeID == "" || edgeSources[b.NodeID] {
			continue
		}
		edgeSources[b.NodeID] = true
		edge := EdgeIntent{OwnerNodeID: scope.NodeID, RowID: scope.RowID, BindingID: b.ID, FromNodeID: b.NodeID, ToNodeID: scope.NodeID}
		switch scope.Kind {
		case "storyboard-row":
			edge.ToHandleID = "row:" + scope.RowID
		case "storyboard-context":
			edge.ToHandleID = "storyboard:context"
		case "batch-row":
			edge.Relation = "batch-input"
			table := obj(metadata["batchTable"])
			cols := maps(table["referenceColumns"])
			prefix := "batch-reference:"
			ids := stringsList(rowFor(node, "batchTable", scope.RowID)["inputNodeIds"])
			if b.Role == "source-text" {
				cols = maps(table["textColumns"])
				prefix = "batch-text:"
				ids = stringsList(rowFor(node, "batchTable", scope.RowID)["textNodeIds"])
			}
			pos := 0
			for i, id := range ids {
				if id == b.NodeID {
					pos = i
					break
				}
			}
			col := fmt.Sprintf("reference-%d", pos+1)
			if pos < len(cols) {
				col = str(cols[pos]["id"])
			}
			edge.ToHandleID = prefix + col
		}
		edge.Key = edgeKey(edge)
		result.Edges = append(result.Edges, edge)
	}
	if scope.Kind == "storyboard-row" {
		row := rowFor(node, "storyboard", scope.RowID)
		for _, kind := range []string{"image", "video"} {
			id := str(row[kind+"NodeId"])
			if id == "" {
				continue
			}
			out := idx[id]
			if out == nil || str(out["type"]) != kind {
				return result, fail("invalid_reference", kind+"NodeId", "分镜输出绑定无效")
			}
			e := EdgeIntent{OwnerNodeID: scope.NodeID, RowID: scope.RowID, BindingID: "output:" + kind, FromNodeID: scope.NodeID, ToNodeID: id, FromHandleID: "row:" + scope.RowID, Relation: "storyboard-output"}
			e.Key = edgeKey(e)
			result.Edges = append(result.Edges, e)
			result.DependencyNodeIDs = append(result.DependencyNodeIDs, id)
		}
	}
	return result, nil
}
func edgeKey(e EdgeIntent) string {
	return strings.Join([]string{e.OwnerNodeID, e.RowID, e.BindingID, e.FromNodeID, e.ToNodeID, e.FromHandleID, e.ToHandleID, e.Relation}, "\x00")
}
