package app

func cloudAgentNodeUsageStatus(node map[string]any) string {
	metadata, _ := node["metadata"].(map[string]any)
	if metadata["usageStatus"] == "archived" {
		return "archived"
	}
	return "active"
}

func cloudAgentNodeUsageView(node map[string]any) map[string]any {
	metadata, _ := node["metadata"].(map[string]any)
	status := cloudAgentNodeUsageStatus(node)
	view := map[string]any{"status": status}
	if note := stringValue(metadata["usageNote"]); note != "" {
		view["note"] = truncateRunes(note, 500)
	}
	if successor := stringValue(metadata["supersededByNodeId"]); status == "archived" && successor != "" {
		view["supersededByNodeId"] = successor
	}
	return view
}

func validateCloudAgentAssetUsage(doc map[string]any, ops []agentCanvasOp) error {
	nodes := creationMaps(doc["nodes"])
	for _, op := range ops {
		if op.Type != "update_node" {
			continue
		}
		_, statusChanged := op.Patch["usageStatus"]
		_, successorChanged := op.Patch["supersededByNodeId"]
		if !statusChanged && !successorChanged {
			continue
		}
		index := cloudAgentNodeIndex(nodes, op.ID)
		if index < 0 {
			continue
		}
		node := nodes[index]
		metadata := cloudAgentNodeMetadata(node)
		if cloudAgentNodeUsageStatus(node) == "active" {
			if stringValue(op.Patch["supersededByNodeId"]) != "" {
				return cloudAgentFieldError("supersededByNodeId", "invalid_reference", "只有归档节点可声明替代版本")
			}
			delete(metadata, "supersededByNodeId")
			continue
		}
		owner, _ := cloudAgentNodeCapabilityForNode(node)
		seen := map[string]bool{op.ID: true}
		current := node
		for {
			id := stringValue(cloudAgentNodeMetadata(current)["supersededByNodeId"])
			if id == "" {
				break
			}
			if seen[id] {
				return cloudAgentFieldError("supersededByNodeId", "reference_cycle", "替代版本不能指向自身或形成循环")
			}
			seen[id] = true
			next := cloudAgentNodeIndex(nodes, id)
			if next < 0 {
				return cloudAgentFieldError("supersededByNodeId", "reference_missing", "替代版本必须是当前画布节点")
			}
			target, ok := cloudAgentNodeCapabilityForNode(nodes[next])
			if !ok || target.Type != owner.Type {
				return cloudAgentFieldError("supersededByNodeId", "invalid_reference", "替代版本必须与原节点的业务类型一致")
			}
			current = nodes[next]
		}
	}
	return nil
}
