package commands

import "strings"

func versionRoot(node map[string]any) string {
	metadata, _ := node["metadata"].(map[string]any)
	if id := text(metadata["versionOfNodeId"]); id != "" {
		return id
	}
	return text(node["id"])
}
func (p *planner) duplicateNativeNode(doc Document, op Operation) ([]OperationSummary, error) {
	if op.Action != "" && op.Action != "copy" && op.Action != "variant" {
		return nil, BadAuthRequest("复制模式无效")
	}
	nodes, edges, item, err := p.canvasCopyGraph(maps(doc["nodes"]), maps(doc["connections"]), op)
	if err != nil {
		return nil, err
	}
	doc["nodes"], doc["connections"] = p.mapsAsAny(nodes), p.mapsAsAny(edges)
	source := entity(doc, "node:"+op.SourceNodeID)
	if op.Action != "variant" || text(source["type"]) == "frame" {
		return []OperationSummary{item}, nil
	}
	root := versionRoot(source)
	label := 'A'
	for _, node := range nodes {
		if text(node["id"]) != op.ID && versionRoot(node) == root {
			m := p.canvasNodeMetadata(node)
			l := []rune(text(m["versionLabel"]))
			if len(l) > 0 && l[0] > label {
				label = l[0]
			}
		}
	}
	if label < 'Z' {
		label++
	}
	metadata := p.canvasNodeMetadata(source)
	if text(metadata["versionLabel"]) == "" {
		metadata["versionOfNodeId"], metadata["versionLabel"], metadata["versionPrimary"], metadata["generationResultPlacement"] = root, "A", true, "replace-node"
		source["title"] = text(source["title"]) + " · A"
	}
	copy := entity(doc, "node:"+op.ID)
	copyMetadata := p.canvasNodeMetadata(copy)
	copyMetadata["versionOfNodeId"], copyMetadata["versionLabel"], copyMetadata["versionPrimary"] = root, string(label), false
	if op.Title == nil {
		title := text(source["title"])
		r := []rune(title)
		if len(r) >= 4 && r[len(r)-1] >= 'A' && r[len(r)-1] <= 'Z' && strings.HasSuffix(string(r[:len(r)-1]), " · ") {
			title = string(r[:len(r)-4])
		}
		copy["title"] = title + " · " + string(label)
	}
	return []OperationSummary{item}, nil
}
func (p *planner) selectNodeVersion(doc Document, op Operation) error {
	target := entity(doc, "node:"+op.ID)
	if target == nil {
		return failure("entity_deleted", "版本节点不存在")
	}
	root := versionRoot(target)
	for _, node := range maps(doc["nodes"]) {
		if versionRoot(node) == root {
			p.canvasNodeMetadata(node)["versionPrimary"] = text(node["id"]) == op.ID
		}
	}
	return nil
}
