package commands

import (
	"encoding/json"
	"infinite-canvas/backend/internal/assets"
	"infinite-canvas/backend/internal/canvas/capability"
	"reflect"
	"sort"
	"strings"
	"time"
)

// Plan never mutates its input and never performs I/O. All identities come from
// the authenticated host, not command JSON. A failed batch returns the original.
func Plan(doc Document, req Command, ctx ValidationContext) (PlanResult, error) {
	result := PlanResult{Document: doc}
	if !ctx.Principal.HasScope("canvas.write") || ctx.Principal.Role == "viewer" || ctx.Principal.ID == "" || ctx.Principal.OwnerID == "" {
		return result, failure("permission_denied", "没有画布写入权限")
	}
	if req.Protocol != ProtocolVersion {
		return result, failure("protocol_mismatch", "画布命令协议版本不兼容")
	}
	for _, id := range []string{req.CanvasID, req.DocumentEpoch, req.OperationID, req.ClientID} {
		if err := validateCanvasID(id, "命令标识", 128); err != nil {
			return result, err
		}
	}
	if len(req.Operations) < 1 || len(req.Operations) > 100 {
		return result, BadAuthRequest("每条命令需要 1 至 100 项操作")
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return result, err
	}
	if len(raw) > 32<<20 {
		return result, BadAuthRequest("画布文档过大")
	}
	var next Document
	if err = json.Unmarshal(raw, &next); err != nil {
		return result, err
	}
	if next == nil {
		return result, BadAuthRequest("画布文档无效")
	}
	registry := ctx.Registry
	if registry == nil {
		registry = capability.BuiltinRegistry()
	}
	now := ctx.Now
	if now.IsZero() {
		now = time.Unix(0, 0).UTC()
	}
	p := &planner{registry: registry, now: func() time.Time { return now }}
	summary := []OperationSummary{}
	for _, op := range req.Operations {
		if ctx.AllowedOperations != nil && !ctx.AllowedOperations[op.Type] {
			return result, failure("permission_denied", "没有该操作权限")
		}
		items, err := p.applyOperation(next, op, ctx)
		if err != nil {
			return result, err
		}
		if op.Type == "update_node" && op.Patch["generationSpec"] != nil {
			if err = p.validateCanvasGenerationReferences(next, op.ID); err != nil {
				return result, err
			}
		}
		summary = append(summary, items...)
		if op.Type == "delete_node" {
			cleanTimelineNodeReferences(next)
		}
	}
	changes := Diff(doc, next)
	inverse := Diff(next, doc)
	reads := relatedReadSet(doc, req.Operations)
	writes := map[string]bool{}
	for _, change := range changes.Entities {
		writes[change.Key] = true
		reads[change.Key] = true
		if strings.HasPrefix(change.Key, "node:") && (drawingPresent(entity(doc, change.Key)) || drawingPresent(change.Value)) {
			key := "drawing:" + strings.TrimPrefix(change.Key, "node:")
			writes[key] = true
			reads[key] = true
		}
	}
	for field := range changes.Fields {
		writes["project:"+field] = true
		reads["project:"+field] = true
	}
	if changes.NodeOrder != nil {
		writes["project:nodeOrder"] = true
		reads["project:nodeOrder"] = true
	}
	if changes.ConnectionOrder != nil {
		writes["project:connectionOrder"] = true
		reads["project:connectionOrder"] = true
	}
	resources := resourceIDs(next)
	for _, id := range resources {
		if ctx.ResourceAllowed == nil || !ctx.ResourceAllowed(id) {
			return result, failure("resource_not_owned", "画布包含未授权资源")
		}
	}
	if err = validateResult(next); err != nil {
		return result, err
	}
	return PlanResult{Document: next, Changes: changes, ReadSet: reads, WriteSet: writes, Inverse: inverse, ResourceIDs: resources, Summary: summary}, nil
}
func drawingPresent(n map[string]any) bool { return n != nil && n["type"] == "drawing" }
func maps(value any) []map[string]any {
	switch values := value.(type) {
	case []map[string]any:
		return values
	case []any:
		result := []map[string]any{}
		for _, v := range values {
			if m, ok := v.(map[string]any); ok {
				result = append(result, m)
			}
		}
		return result
	}
	return []map[string]any{}
}
func text(v any) string { s, _ := v.(string); return s }
func entity(doc Document, key string) map[string]any {
	group, id, ok := strings.Cut(key, ":")
	if !ok {
		return nil
	}
	field := "nodes"
	if group == "connection" {
		field = "connections"
	} else if group != "node" {
		return nil
	}
	for _, n := range maps(doc[field]) {
		if text(n["id"]) == id {
			return n
		}
	}
	return nil
}
func Diff(before, after Document) ChangeSet {
	changes := ChangeSet{Entities: []EntityChange{}, Fields: map[string]FieldChange{}}
	for _, group := range []struct{ field, prefix string }{{"nodes", "node:"}, {"connections", "connection:"}} {
		old := map[string]map[string]any{}
		previous, current := []string{}, []string{}
		for _, n := range maps(before[group.field]) {
			id := text(n["id"])
			old[id] = n
			previous = append(previous, id)
		}
		for _, n := range maps(after[group.field]) {
			id := text(n["id"])
			current = append(current, id)
			if !sameJSON(old[id], n) {
				changes.Entities = append(changes.Entities, EntityChange{Key: group.prefix + id, Value: n})
			}
			delete(old, id)
		}
		ids := []string{}
		for id := range old {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			changes.Entities = append(changes.Entities, EntityChange{Key: group.prefix + id, Delete: true})
		}
		if !reflect.DeepEqual(previous, current) {
			if group.field == "nodes" {
				changes.NodeOrder = current
			} else {
				changes.ConnectionOrder = current
			}
		}
	}
	for key, value := range after {
		if key != "nodes" && key != "connections" && !sameJSON(before[key], value) {
			changes.Fields[key] = FieldChange{Value: value}
		}
	}
	for key := range before {
		if key != "nodes" && key != "connections" {
			if _, ok := after[key]; !ok {
				changes.Fields[key] = FieldChange{Delete: true}
			}
		}
	}
	return changes
}
func ApplyChanges(doc Document, changes ChangeSet) (Document, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var next Document
	if err = json.Unmarshal(raw, &next); err != nil {
		return nil, err
	}
	for _, change := range changes.Entities {
		group, id, ok := strings.Cut(change.Key, ":")
		if !ok || id == "" || (group != "node" && group != "connection") {
			return nil, BadAuthRequest("变更实体类型无效")
		}
		field := "nodes"
		if group == "connection" {
			field = "connections"
		}
		list := maps(next[field])
		found := false
		updated := []map[string]any{}
		for _, n := range list {
			if text(n["id"]) != id {
				updated = append(updated, n)
				continue
			}
			found = true
			if !change.Delete {
				if text(change.Value["id"]) != id {
					return nil, BadAuthRequest("变更实体 ID 无效")
				}
				updated = append(updated, change.Value)
			}
		}
		if !found && !change.Delete {
			if text(change.Value["id"]) != id {
				return nil, BadAuthRequest("变更实体 ID 无效")
			}
			updated = append(updated, change.Value)
		}
		next[field] = updated
	}
	p := &planner{}
	for _, pair := range []struct {
		field string
		order []string
	}{{"nodes", changes.NodeOrder}, {"connections", changes.ConnectionOrder}} {
		if pair.order != nil {
			list, err := p.canvasReorderItems(maps(next[pair.field]), pair.order)
			if err != nil {
				return nil, err
			}
			next[pair.field] = list
		}
	}
	for key, change := range changes.Fields {
		if key == "nodes" || key == "connections" {
			return nil, BadAuthRequest("变更字段无效")
		}
		if change.Delete {
			delete(next, key)
		} else {
			next[key] = change.Value
		}
	}
	// Return independently owned JSON, including newly applied entity maps.
	raw, err = json.Marshal(next)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &next); err != nil {
		return nil, err
	}
	return next, nil
}
func relatedReadSet(doc Document, ops []Operation) map[string]bool {
	reads := map[string]bool{}
	for _, op := range ops {
		key := "node:" + op.ID
		if op.Type == "connect_nodes" || op.Type == "delete_connection" || op.Type == "update_connection" {
			key = "connection:" + op.ID
		}
		reads[key] = true
		if op.Type == "edit_canvas_settings" {
			delete(reads, key)
			for field := range op.Patch {
				reads["project:"+field] = true
			}
		}
		if op.Type == "reorder_connections" {
			delete(reads, key)
			reads["project:connectionOrder"] = true
			for _, edge := range maps(doc["connections"]) {
				reads["connection:"+text(edge["id"])] = true
			}
		}
		if op.Type == "rename_canvas" {
			delete(reads, key)
			reads["project:title"] = true
		}
		if op.Type == "delete_canvas" {
			delete(reads, key)
			reads["project:_deleted"] = true
		}
		for _, id := range []string{op.FromNodeID, op.ToNodeID, op.SourceNodeID, op.ParentID} {
			if id != "" {
				reads["node:"+id] = true
			}
		}
		if op.Type == "reorder_nodes" || op.Type == "duplicate_node" || op.Type == "select_node_version" {
			for _, n := range maps(doc["nodes"]) {
				reads["node:"+text(n["id"])] = true
			}
		}
		if op.Type == "add_node" && (op.X == nil || op.Y == nil) {
			for _, n := range maps(doc["nodes"]) {
				reads["node:"+text(n["id"])] = true
			}
		}
		add := func(id string) {
			if id != "" {
				reads["node:"+id] = true
			}
		}
		walkNodeReferences(op.Patch, add)
		if op.Type == "edit_timeline" {
			delete(reads, key)
			reads["project:timeline"] = true
			walkNodeReferences(doc["timeline"], add)
			for _, change := range op.TimelineOperations {
				walkNodeReferences(change.Patch, add)
			}
		}
		if op.Relation == "storyboard-asset-reference" {
			for _, node := range maps(doc["nodes"]) {
				if text(node["type"]) == "script" {
					add(text(node["id"]))
				}
			}
		}
		for _, patch := range op.PrevisOperations {
			if patch.ReferenceNodeID != nil {
				add(*patch.ReferenceNodeID)
			}
		}
		if strings.Contains(op.Type, "previs_scene") {
			delete(reads, key)
			reads["project:previsScenes"] = true
			for _, scene := range maps(doc["previsScenes"]) {
				walkNodeReferences(scene, add)
			}
			for _, change := range op.PrevisEntityOperations {
				walkNodeReferences(change.Patch, add)
			}
		}
		if op.Type == "connect_nodes" || op.Type == "update_connection" || op.Type == "delete_connection" || op.Type == "delete_node" || op.Type == "duplicate_node" {
			for _, edge := range maps(doc["connections"]) {
				id, from, to := text(edge["id"]), text(edge["fromNodeId"]), text(edge["toNodeId"])
				if id == op.ID || to == op.ToNodeID || (op.Type == "delete_node" && (from == op.ID || to == op.ID)) || (op.Type == "duplicate_node" && (from == op.SourceNodeID || to == op.SourceNodeID)) {
					reads["connection:"+id] = true
					add(from)
					add(to)
				}
			}
		}
		if op.Type == "delete_node" || op.Type == "duplicate_node" || op.Type == "set_parent" {
			node := entity(doc, "node:"+op.ID)
			if op.Type == "duplicate_node" {
				node = entity(doc, "node:"+op.SourceNodeID)
			}
			if node != nil {
				add(text(node["parentId"]))
				walkNodeReferences(node, add)
			}
			for _, n := range maps(doc["nodes"]) {
				if text(n["parentId"]) == op.ID || text(n["parentId"]) == op.SourceNodeID && op.SourceNodeID != "" {
					add(text(n["id"]))
				}
			}
		}
	}
	return reads
}
func sameJSON(a, b any) bool {
	left, e1 := json.Marshal(a)
	right, e2 := json.Marshal(b)
	return e1 == nil && e2 == nil && string(left) == string(right)
}
func walkNodeReferences(value any, visit func(string)) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if strings.HasSuffix(key, "NodeId") || key == "nodeId" || key == "primaryImageId" || key == "batchRootId" {
				visit(text(child))
			}
			if strings.HasSuffix(key, "NodeIds") || key == "batchChildIds" {
				for _, id := range stringList(child) {
					visit(id)
				}
			}
			walkNodeReferences(child, visit)
		}
	case []any:
		for _, child := range v {
			walkNodeReferences(child, visit)
		}
	}
}
func stringList(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		r := []string{}
		for _, x := range v {
			if s, ok := x.(string); ok {
				r = append(r, s)
			}
		}
		return r
	}
	return nil
}
func resourceIDs(doc Document) []string {
	found := map[string]bool{}
	var walk func(any, string, bool)
	walk = func(v any, parent string, prose bool) {
		switch x := v.(type) {
		case map[string]any:
			if parent == "nodes" && (text(x["type"]) == "text" || text(x["type"]) == "markdown") {
				prose = true
			}
			for key, child := range x {
				if strings.HasSuffix(strings.ToLower(key), "resourceid") {
					if id := text(child); id != "" {
						found[id] = true
					}
				}
				if prose && key == "content" {
					continue
				}
				walk(child, key, prose)
			}
		case []any:
			for _, child := range x {
				walk(child, parent, prose)
			}
		case []map[string]any:
			for _, child := range x {
				walk(child, parent, prose)
			}
		case string:
			locators := map[string]bool{"storageKey": true, "content": true, "previewContent": true, "drawingPreviewStorageKey": true, "drawingPreviewUrl": true, "url": true, "dataUrl": true, "dataURL": true, "src": true, "coverUrl": true, "imageUrl": true, "videoUrl": true, "audioUrl": true, "referenceUrl": true, "referenceUrls": true, "artifactRef": true, "providerArtifactRef": true}
			if strings.HasSuffix(parent, "ResourceIds") || parent == "resourceIds" {
				if id := assets.ValidID(x); id != "" {
					found[id] = true
				}
			}
			if id := assets.ResourceID(x); id != "" && locators[parent] {
				found[id] = true
			}
		}
	}
	walk(map[string]any(doc), "", false)
	ids := []string{}
	for id := range found {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
func validateResult(doc Document) error {
	ids := map[string]bool{}
	for _, n := range maps(doc["nodes"]) {
		id := text(n["id"])
		if id == "" || ids[id] {
			return BadAuthRequest("节点 ID 无效或重复")
		}
		ids[id] = true
	}
	edges := map[string]bool{}
	for _, e := range maps(doc["connections"]) {
		id := text(e["id"])
		if id == "" || edges[id] || !ids[text(e["fromNodeId"])] || !ids[text(e["toNodeId"])] {
			return BadAuthRequest("连线存在重复 ID 或失效端点")
		}
		edges[id] = true
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if len(raw) > 32<<20 {
		return BadAuthRequest("画布文档过大")
	}
	return nil
}
