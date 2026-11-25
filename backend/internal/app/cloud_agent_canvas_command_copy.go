package app

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func canvasCommandDup(doc map[string]any, counters map[string]int64, fs *canvasCommandFS, step canvasCommandStep) (cloudAgentApprovalPreviewItem, string, error) {
	if len(step.Args) < 1 {
		return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "required", "dup 需要 <nodedir> [title...]")
	}
	source, ok := fs.lookupNode(step.Args[0])
	if !ok {
		return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "not_found", canvasCommandPathMissing(fs, step.Args[0]))
	}
	if canvasCommandLocked(source.Node) {
		return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "locked", fmt.Sprintf("节点 %s 已锁定；请让用户解锁后再复制", source.Path))
	}
	title := strings.TrimSpace(strings.Join(step.Args[1:], " "))
	if title == "" {
		title = strings.TrimSpace(stringValue(source.Node["title"])) + " 副本"
	}
	if canvasCommandTitleTaken(doc, source.ScopeID, title, "") {
		return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "invalid_value", fmt.Sprintf("同一作用域已有标题《%s》的节点；请换标题", title))
	}
	if source.Type == "submap" {
		return canvasCommandDupSubmap(doc, counters, fs, source, title)
	}
	return canvasCommandDupNode(doc, counters, fs, source, title)
}

func canvasCommandDupNode(doc map[string]any, counters map[string]int64, fs *canvasCommandFS, source canvasCommandNode, title string) (cloudAgentApprovalPreviewItem, string, error) {
	prefix := source.Type
	if source.ScopeID != "" {
		prefix = source.ScopeID + "__" + source.Type
	}
	id := canvasCommandNextID(doc, counters, prefix)
	clone, err := canvasCommandCloneNode(source.Node, id, title, source.ScopeID != "", false)
	if err != nil {
		return cloudAgentApprovalPreviewItem{}, "", err
	}
	nodes := creationMaps(doc["nodes"])
	nodes = append(nodes, clone)
	doc["nodes"] = nodes
	path := "./" + id + "/"
	if source.ScopeID != "" {
		path = "./" + source.ScopeID + "/" + strings.TrimPrefix(id, source.ScopeID+"__") + "/"
	}
	item := cloudAgentApprovalPreviewItem{
		Operation: "dup", NodeID: id, NodeTitle: title, NodeType: source.Type, NodeTypeLabel: source.Descriptor.Label,
		TargetNodeID: source.ID, TargetNodeTitle: strings.TrimSpace(stringValue(source.Node["title"])),
		Summary: fmt.Sprintf("从 %s 复制出 %s（%s）；指向其他节点的引用未复制", source.Path, path, title),
	}
	return item, fmt.Sprintf("已从 %s 复制出 %s（%s）", source.Path, path, id), nil
}

func canvasCommandDupSubmap(doc map[string]any, counters map[string]int64, fs *canvasCommandFS, source canvasCommandNode, title string) (cloudAgentApprovalPreviewItem, string, error) {
	newID := canvasCommandNextID(doc, counters, "submap")
	container, err := canvasCommandCloneNode(source.Node, newID, title, true, false)
	if err != nil {
		return cloudAgentApprovalPreviewItem{}, "", err
	}
	nodes := creationMaps(doc["nodes"])
	nodes = append(nodes, container)
	children := fs.children(source.ID)
	idMap := map[string]string{source.ID: newID}
	clones := make([]map[string]any, 0, len(children))
	for _, child := range children {
		if canvasCommandLocked(child.Node) {
			return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "locked", fmt.Sprintf("子节点 %s 已锁定；请让用户解锁后再复制整个子画布", child.Path))
		}
		childPrefix := newID + "__" + child.Type
		childID := canvasCommandNextIDIn(doc, counters, nodes, childPrefix)
		copy, err := canvasCommandCloneNode(child.Node, childID, strings.TrimSpace(stringValue(child.Node["title"])), true, true)
		if err != nil {
			return cloudAgentApprovalPreviewItem{}, "", err
		}
		copy["scopeId"] = newID
		idMap[child.ID] = childID
		clones = append(clones, copy)
		nodes = append(nodes, copy)
	}
	dropped := 0
	for _, clone := range clones {
		dropped += canvasCommandRemapReferences(clone, idMap)
	}
	doc["nodes"] = nodes
	path := "./" + newID + "/"
	item := cloudAgentApprovalPreviewItem{
		Operation: "dup", NodeID: newID, NodeTitle: title, NodeType: "submap", NodeTypeLabel: source.Descriptor.Label,
		TargetNodeID: source.ID, TargetNodeTitle: strings.TrimSpace(stringValue(source.Node["title"])),
		Summary: fmt.Sprintf("从 %s 复制出 %s（%s），连同 %d 个子节点；%d 个外部引用未复制", source.Path, path, title, len(clones), dropped),
	}
	return item, fmt.Sprintf("已从 %s 复制出 %s（%s），连同 %d 个子节点；%d 个外部引用未复制", source.Path, path, newID, len(clones), dropped), nil
}

func canvasCommandMove(doc map[string]any, fs *canvasCommandFS, step canvasCommandStep) (cloudAgentApprovalPreviewItem, string, error) {
	if len(step.Args) < 2 {
		return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "required", "mv 需要 <nodedir> <title...>")
	}
	entry, ok := fs.lookupNode(step.Args[0])
	if !ok {
		return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "not_found", canvasCommandPathMissing(fs, step.Args[0]))
	}
	if canvasCommandLocked(entry.Node) {
		return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "locked", fmt.Sprintf("节点 %s 已锁定；请让用户解锁后再重命名", entry.Path))
	}
	title := strings.TrimSpace(strings.Join(step.Args[1:], " "))
	if title == "" {
		return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "required", "mv 的新标题不能为空")
	}
	if utf8.RuneCountInString(title) > canvasCommandMaxTitleRunes {
		return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "too_long", fmt.Sprintf("标题超过 %d 字", canvasCommandMaxTitleRunes))
	}
	if canvasCommandTitleTaken(doc, entry.ScopeID, title, entry.ID) {
		return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "invalid_value", fmt.Sprintf("同一作用域已有标题《%s》的节点", title))
	}
	before := strings.TrimSpace(stringValue(entry.Node["title"]))
	entry.Node["title"] = title
	item := cloudAgentApprovalPreviewItem{
		Operation: "update", NodeID: entry.ID, NodeTitle: before, ResultTitle: title,
		NodeType: entry.Type, NodeTypeLabel: entry.Descriptor.Label, Fields: []string{"title"},
		Summary: fmt.Sprintf("把%s《%s》重命名为《%s》", entry.Descriptor.Label, before, title),
	}
	return item, fmt.Sprintf("已把 %s 重命名为《%s》", entry.Path, title), nil
}

func canvasCommandRemove(doc map[string]any, fs *canvasCommandFS, step canvasCommandStep) (cloudAgentApprovalPreviewItem, string, error) {
	if len(step.Args) != 1 {
		return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "invalid_value", "rm 只接受一个节点路径")
	}
	entry, ok := fs.lookupNode(step.Args[0])
	if !ok {
		return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "not_found", canvasCommandPathMissing(fs, step.Args[0]))
	}
	if canvasCommandLocked(entry.Node) {
		return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "locked", fmt.Sprintf("节点 %s 已锁定；请让用户解锁后再删除", entry.Path))
	}
	removed := map[string]bool{entry.ID: true}
	childCount := 0
	if entry.Type == "submap" {
		for _, child := range fs.children(entry.ID) {
			if canvasCommandLocked(child.Node) {
				return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "locked", fmt.Sprintf("子节点 %s 已锁定；请让用户解锁后再删除整个子画布", child.Path))
			}
			removed[child.ID] = true
			childCount++
		}
	}
	if blocked := canvasCommandLockedReferrers(doc, removed); len(blocked) > 0 {
		return cloudAgentApprovalPreviewItem{}, "", canvasCommandError("command", "locked", fmt.Sprintf("节点 %s 正被锁定节点引用：%s；请让用户先解锁再删", entry.Path, strings.Join(blocked, "、")))
	}
	nodes := creationMaps(doc["nodes"])
	kept := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		id := stringValue(node["id"])
		if removed[id] {
			continue
		}
		cloudAgentCleanNodeReferences(node, removed)
		kept = append(kept, node)
	}
	doc["nodes"] = kept
	edges := creationMaps(doc["connections"])
	keptEdges := make([]map[string]any, 0, len(edges))
	removedEdges := 0
	for _, edge := range edges {
		if removed[stringValue(edge["fromNodeId"])] || removed[stringValue(edge["toNodeId"])] {
			removedEdges++
			continue
		}
		keptEdges = append(keptEdges, edge)
	}
	doc["connections"] = keptEdges
	title := strings.TrimSpace(stringValue(entry.Node["title"]))
	item := cloudAgentApprovalPreviewItem{
		Operation: "delete", NodeID: entry.ID, NodeTitle: title, NodeType: entry.Type, NodeTypeLabel: entry.Descriptor.Label,
		Summary: fmt.Sprintf("删除 %s（%s），级联删除 %d 个子节点与 %d 条连线；删除不可撤销", entry.Path, title, childCount, removedEdges),
	}
	return item, fmt.Sprintf("已删除 %s（%s），级联删除 %d 个子节点与 %d 条连线", entry.Path, title, childCount, removedEdges), nil
}
