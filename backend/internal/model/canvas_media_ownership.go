package model

import "sort"

type CanvasMediaOutputOwner struct {
	NodeID string `json:"nodeId"`
	RowID  string `json:"rowId"`
	Field  string `json:"field"`
}

func CanvasMediaOutputOwners(nodes []map[string]any, target string) []CanvasMediaOutputOwner {
	value := func(v any) string { s, _ := v.(string); return s }
	owners := []CanvasMediaOutputOwner{}
	for _, node := range nodes {
		m, _ := node["metadata"].(map[string]any)
		for _, key := range []string{"storyboard", "batchTable"} {
			table, _ := m[key].(map[string]any)
			rows, _ := table["rows"].([]any)
			for _, raw := range rows {
				row, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				for _, field := range []string{"imageNodeId", "videoNodeId", "outputNodeId"} {
					if value(row[field]) == target {
						owners = append(owners, CanvasMediaOutputOwner{value(node["id"]), value(row["id"]), field})
					}
				}
			}
		}
	}
	sort.Slice(owners, func(i, j int) bool {
		a, b := owners[i], owners[j]
		if a.NodeID != b.NodeID {
			return a.NodeID < b.NodeID
		}
		if a.RowID != b.RowID {
			return a.RowID < b.RowID
		}
		return a.Field < b.Field
	})
	return owners
}
