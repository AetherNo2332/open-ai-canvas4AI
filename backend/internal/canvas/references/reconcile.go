package references

import (
	"encoding/json"
	"reflect"
	"sort"

	"infinite-canvas/backend/internal/canvas/capability"
)

func Enabled(doc map[string]any) bool { return number(doc["referenceFieldsVersion"]) == Version }
func Clone(doc map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = json.Unmarshal(raw, &out)
	return out, err
}
func edgeIntent(edge map[string]any) EdgeIntent {
	return EdgeIntent{OwnerNodeID: str(edge["referenceOwnerNodeId"]), RowID: str(edge["storyboardRowId"]), BindingID: str(edge["referenceBindingId"]), FromNodeID: str(edge["fromNodeId"]), ToNodeID: str(edge["toNodeId"]), FromHandleID: str(edge["fromHandleId"]), ToHandleID: str(edge["toHandleId"]), Relation: str(edge["relation"])}
}
func scopesFor(nodes []map[string]any) []Scope {
	out := []Scope{}
	for _, n := range nodes {
		id := str(n["id"])
		m := meta(n)
		switch str(n["type"]) {
		case "script":
			out = append(out, Scope{Kind: "storyboard-context", NodeID: id})
			for _, r := range maps(obj(m["storyboard"])["rows"]) {
				out = append(out, Scope{Kind: "storyboard-row", NodeID: id, RowID: str(r["id"])})
			}
		case "batch-table":
			for _, r := range maps(obj(m["batchTable"])["rows"]) {
				out = append(out, Scope{Kind: "batch-row", NodeID: id, RowID: str(r["id"])})
			}
		default:
			if m["generationSpec"] != nil {
				out = append(out, Scope{Kind: "generation", NodeID: id})
			} else if _, ok := m["inputBindings"]; ok {
				out = append(out, Scope{Kind: "node", NodeID: id})
			}
		}
	}
	return out
}
func Reconcile(before, candidate map[string]any, registry *capability.Registry, nextID func() string) (Result, error) {
	next, err := Clone(candidate)
	if err != nil {
		return Result{}, err
	}
	if registry == nil {
		registry = capability.BuiltinRegistry()
	}
	result := Result{Document: next}
	idx := index(next)
	previous := index(before)
	desired := map[string]EdgeIntent{}
	ordered := []string{}
	changedOwners := map[string]bool{}
	reads := map[string]bool{}
	inputReads := map[string]bool{}
	for _, scope := range scopesFor(maps(next["nodes"])) {
		resolution, err := resolveIndexed(idx, scope, registry)
		if err != nil {
			return Result{}, err
		}
		changed := !reflect.DeepEqual(previous[scope.NodeID], idx[scope.NodeID])
		for _, id := range resolution.DependencyNodeIDs {
			if !reflect.DeepEqual(previous[id], idx[id]) {
				changed = true
			}
		}
		if changed {
			changedOwners[scope.NodeID] = true
			for _, id := range resolution.DependencyNodeIDs {
				reads[id] = true
				if id != scope.NodeID {
					inputReads[id] = true
				}
			}
		}
		for _, intent := range resolution.Edges {
			if _, exists := desired[intent.Key]; !exists {
				ordered = append(ordered, intent.Key)
				desired[intent.Key] = intent
			}
		}
	}
	// Only declared input edges participate in cycle detection. Visual free links
	// and storyboard outputs are not generation dependencies.
	adjacency := map[string][]string{}
	for _, key := range ordered {
		e := desired[key]
		if e.Relation != "storyboard-output" {
			adjacency[e.ToNodeID] = append(adjacency[e.ToNodeID], e.FromNodeID)
		}
	}
	active, done := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(id string) bool {
		if active[id] {
			return false
		}
		if done[id] {
			return true
		}
		active[id] = true
		for _, from := range adjacency[id] {
			if !visit(from) {
				return false
			}
		}
		delete(active, id)
		done[id] = true
		return true
	}
	for id := range adjacency {
		if !visit(id) {
			return Result{}, fail("reference_cycle", "inputBindings", "引用输入形成循环")
		}
	}
	edges := []any{}
	found := map[string]bool{}
	for _, old := range maps(next["connections"]) {
		if str(old["managedBy"]) != ManagedBy {
			edges = append(edges, old)
			continue
		}
		key := edgeKey(edgeIntent(old))
		_, keep := desired[key]
		if keep && !found[key] {
			edges = append(edges, old)
			found[key] = true
		}
		if changedOwners[str(old["referenceOwnerNodeId"])] || !keep {
			result.ReadEdgeIDs = append(result.ReadEdgeIDs, str(old["id"]))
		}
	}
	for _, key := range ordered {
		if found[key] {
			continue
		}
		e := desired[key]
		edge := map[string]any{"id": nextID(), "fromNodeId": e.FromNodeID, "toNodeId": e.ToNodeID, "managedBy": ManagedBy, "referenceOwnerNodeId": e.OwnerNodeID, "referenceBindingId": e.BindingID}
		if e.RowID != "" {
			edge["storyboardRowId"] = e.RowID
		}
		if e.FromHandleID != "" {
			edge["fromHandleId"] = e.FromHandleID
		}
		if e.ToHandleID != "" {
			edge["toHandleId"] = e.ToHandleID
		}
		if e.Relation != "" {
			edge["relation"] = e.Relation
		}
		edges = append(edges, edge)
	}
	next["connections"] = edges
	for id := range reads {
		result.ReadNodeIDs = append(result.ReadNodeIDs, id)
	}
	for id := range inputReads {
		result.ReadInputNodeIDs = append(result.ReadInputNodeIDs, id)
	}
	sort.Strings(result.ReadNodeIDs)
	sort.Strings(result.ReadInputNodeIDs)
	sort.Strings(result.ReadEdgeIDs)
	return result, nil
}
