package app

import (
	"encoding/json"
	"fmt"
	"infinite-canvas/backend/internal/canvas/connection"
	"infinite-canvas/backend/internal/repository"
	"strings"
)

func validateCloudAgentConnectionCapacities(repo *repository.Repository, doc map[string]any, ops []agentCanvasOp) error {
	targets := map[string]bool{}
	for _, op := range ops {
		if op.Type == "connect_nodes" {
			targets[op.ToNodeID] = true
		}
	}
	if len(targets) == 0 {
		return nil
	}
	catalog, err := (&Service{repo: repo}).ModelCatalog(nil)
	if err != nil {
		return err
	}
	limits := map[string]map[string]int{}
	for _, channel := range catalog.Channels {
		for _, m := range channel.Models {
			mode := normalizeCapability(m.Capability)
			if !m.Available || mode != "image" && mode != "video" {
				continue
			}
			raw, err := json.Marshal(m.CapabilityConfig)
			if err != nil {
				return err
			}
			var profile ModelCapabilityConfig
			if err := json.Unmarshal(raw, &profile); err != nil {
				return err
			}
			values := map[string]int{}
			if mode == "image" && profile.Image != nil {
				values["image"] = profile.Image.References.MaxImages
			}
			if mode == "video" && profile.Video != nil {
				values["image"] = profile.Video.References.MaxImages
				values["video"] = profile.Video.References.MaxVideos
				values["audio"] = profile.Video.References.MaxAudios
			}
			if limits[mode] == nil {
				limits[mode] = map[string]int{"image": 0, "video": 0, "audio": 0}
			}
			for kind, value := range values {
				if value > limits[mode][kind] {
					limits[mode][kind] = value
				}
			}
		}
	}
	nodes := cloudAgentConnectionNodes(creationMaps(doc["nodes"]))
	edges := []connection.Edge{}
	for _, e := range creationMaps(doc["connections"]) {
		edges = append(edges, cloudAgentConnectionEdge(e))
	}
	for _, node := range nodes {
		if !targets[node.ID] {
			continue
		}
		mode := connection.Mode(node)
		maximum, ok := limits[mode]
		if !ok {
			continue
		}
		input := connection.Inputs(nodes, edges, connection.Edge{To: node.ID})
		counts := map[string]int{"image": input.Image + input.Character, "video": input.Video, "audio": input.Audio}
		for _, kind := range []string{"image", "video", "audio"} {
			if counts[kind] > maximum[kind] {
				return cloudAgentFieldError("ops", "invalid_connection", fmt.Sprintf("已配置%s模型最多支持 %d 个%s参考输入", mode, maximum[kind], kind))
			}
		}
	}
	return nil
}

func cloudAgentConnectionNodes(nodes []map[string]any) []connection.Node {
	result := make([]connection.Node, 0, len(nodes))
	for _, n := range nodes {
		m, _ := n["metadata"].(map[string]any)
		result = append(result, connection.Node{ID: stringValue(n["id"]), Type: stringValue(n["type"]), WorkflowKind: stringValue(m["workflowKind"]), Mode: stringValue(m["generationMode"])})
	}
	return result
}
func cloudAgentConnectionEdge(edge map[string]any) connection.Edge {
	return connection.Edge{From: stringValue(edge["fromNodeId"]), To: stringValue(edge["toNodeId"]), FromHandle: stringValue(edge["fromHandleId"]), ToHandle: stringValue(edge["toHandleId"])}
}
func validateCloudAgentCanvasConnection(nodes, edges []map[string]any, op agentCanvasOp) error {
	candidate := connection.Edge{From: op.FromNodeID, To: op.ToNodeID, FromHandle: op.FromHandleID, ToHandle: op.ToHandleID}
	existing := make([]connection.Edge, 0, len(edges))
	for _, e := range edges {
		existing = append(existing, cloudAgentConnectionEdge(e))
	}
	if err := connection.Validate(cloudAgentConnectionNodes(nodes), existing, candidate); err != nil {
		return BadAuthRequest(err.Error())
	}
	for _, h := range []struct{ nodeID, handle string }{{op.FromNodeID, op.FromHandleID}, {op.ToNodeID, op.ToHandleID}} {
		if h.handle == "" {
			continue
		}
		if strings.HasPrefix(h.handle, "batch-reference:") {
			node := nodes[cloudAgentNodeIndex(nodes, h.nodeID)]
			m, _ := node["metadata"].(map[string]any)
			table, _ := m["batchTable"].(map[string]any)
			columns := creationMaps(table["referenceColumns"])
			if len(columns) == 0 {
				columns = []map[string]any{{"id": "reference-1"}, {"id": "reference-2"}, {"id": "reference-3"}}
			}
			found := false
			for _, c := range columns {
				if h.handle == "batch-reference:"+stringValue(c["id"]) {
					found = true
				}
			}
			if !found {
				return BadAuthRequest("批量表参考列不存在")
			}
			continue
		}
		if len(h.handle) > 100 {
			return BadAuthRequest("分镜 handle 过长")
		}
		node := nodes[cloudAgentNodeIndex(nodes, h.nodeID)]
		metadata, _ := node["metadata"].(map[string]any)
		if metadata["locked"] == true {
			return BadAuthRequest("不能修改锁定分镜节点")
		}
		if strings.HasPrefix(h.handle, "row:") && cloudAgentConnectionRow(node, strings.TrimPrefix(h.handle, "row:")) == nil {
			return BadAuthRequest("分镜行不存在")
		}
	}
	if op.FromHandleID != "" && strings.HasPrefix(op.FromHandleID, "row:") {
		target := nodes[cloudAgentNodeIndex(nodes, op.ToNodeID)]
		metadata, _ := target["metadata"].(map[string]any)
		if stringValue(target["type"]) == "video" && metadata["locked"] == true {
			return BadAuthRequest("不能修改锁定视频节点")
		}
	}
	return nil
}
func cloudAgentConnectionRow(node map[string]any, rowID string) map[string]any {
	metadata, _ := node["metadata"].(map[string]any)
	storyboard, _ := metadata["storyboard"].(map[string]any)
	for _, r := range creationMaps(storyboard["rows"]) {
		if stringValue(r["id"]) == rowID {
			return r
		}
	}
	return nil
}
func cloudAgentConnectionAsset(node map[string]any) map[string]any {
	metadata, _ := node["metadata"].(map[string]any)
	role, priority := "", 60
	switch {
	case stringValue(metadata["workflowKind"]) == "character" || stringValue(metadata["assetCategory"]) == "character":
		role, priority = "character", 100
	case stringValue(node["type"]) == "audio":
		role, priority = "audio", 70
	case stringValue(node["type"]) == "video":
		role, priority = "motion", 70
	case stringValue(metadata["assetCategory"]) == "environment":
		role, priority = "environment", 90
	case stringValue(metadata["assetCategory"]) == "prop":
		role, priority = "prop", 80
	case stringValue(node["type"]) == "image" || stringValue(node["type"]) == "drawing":
		role = "style"
	}
	if role == "" {
		return nil
	}
	return map[string]any{"nodeId": node["id"], "role": role, "priority": priority}
}

// Apply the same row/context updates as manual attachNodeToStoryboardRow.
func attachCloudAgentConnection(nodes []map[string]any, op agentCanvasOp) []string {
	scriptID, linkedID, handle := op.ToNodeID, op.FromNodeID, op.ToHandleID
	output := op.FromHandleID != ""
	if output {
		scriptID, linkedID, handle = op.FromNodeID, op.ToNodeID, op.FromHandleID
	}
	if handle == "" || stringValue(nodes[cloudAgentNodeIndex(nodes, scriptID)]["type"]) != "script" {
		return nil
	}
	script := nodes[cloudAgentNodeIndex(nodes, scriptID)]
	linked := nodes[cloudAgentNodeIndex(nodes, linkedID)]
	metadata, _ := script["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
		script["metadata"] = metadata
	}
	storyboard, _ := metadata["storyboard"].(map[string]any)
	if storyboard == nil {
		storyboard = map[string]any{"rows": []any{}}
		metadata["storyboard"] = storyboard
	}
	if storyboard["visibleColumns"] == nil {
		storyboard["visibleColumns"] = []string{"shotNumber", "durationSeconds", "videoMotionPrompt", "dialogue", "assets"}
	}
	if storyboard["referenceNodeIds"] == nil {
		storyboard["referenceNodeIds"] = []any{}
	}
	if handle == "storyboard:context" {
		ids, _ := storyboard["referenceNodeIds"].([]any)
		for _, id := range ids {
			if stringValue(id) == linkedID {
				return []string{"关联分镜上下文"}
			}
		}
		storyboard["referenceNodeIds"] = append(ids, linkedID)
		return []string{"关联分镜上下文"}
	}
	row := cloudAgentConnectionRow(script, strings.TrimPrefix(handle, "row:"))
	if row == nil {
		return nil
	}
	if !output {
		binding := cloudAgentConnectionAsset(linked)
		if binding != nil {
			bindings, _ := row["assetBindings"].([]any)
			for _, b := range creationMaps(row["assetBindings"]) {
				if stringValue(b["nodeId"]) == linkedID {
					return []string{"关联分镜行资产"}
				}
			}
			row["assetBindings"] = append(bindings, binding)
		}
		return []string{"关联分镜行资产"}
	}
	kind := stringValue(linked["type"])
	if kind == "image" {
		row["imageNodeId"] = linkedID
	}
	if kind == "video" {
		row["videoNodeId"] = linkedID
		shot := fmt.Sprint(row["shotNumber"])
		prompt := stringValue(row["videoMotionPrompt"])
		if prompt == "" {
			prompt = stringValue(row["plotDescription"])
		}
		prompt = strings.TrimSpace(prompt)
		m, _ := linked["metadata"].(map[string]any)
		if m == nil {
			m = map[string]any{}
			linked["metadata"] = m
		}
		linked["title"] = "镜头 " + shot + " · 视频"
		m["prompt"] = prompt
		m["composerContent"] = cloudAgentConnectionComposer(script, row, nodes, prompt)
		m["workflowKind"] = "shot"
		m["workflowTitle"] = "镜头 " + shot + " 视频"
		m["shotIndex"] = row["shotNumber"]
		m["generationMode"] = "video"
		m["seconds"] = fmt.Sprint(row["durationSeconds"])
		if stringValue(m["videoEditOperation"]) == "" {
			m["videoEditOperation"] = "text_to_video"
		}
		if variables, ok := row["videoPromptTemplateVariables"].(map[string]any); ok {
			m["promptTemplateOperation"] = "storyboard_video"
			m["promptTemplateVariables"] = variables
		} else {
			delete(m, "promptTemplateOperation")
			delete(m, "promptTemplateVariables")
		}
	}
	return []string{"关联分镜行产物"}
}

func cloudAgentConnectionComposer(script, row map[string]any, nodes []map[string]any, prompt string) string {
	metadata, _ := script["metadata"].(map[string]any)
	storyboard, _ := metadata["storyboard"].(map[string]any)
	ids := []string{}
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && id != stringValue(script["id"]) && id != stringValue(row["imageNodeId"]) && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	referenceIDs, _ := storyboard["referenceNodeIds"].([]any)
	for _, v := range referenceIDs {
		add(stringValue(v))
	}
	for _, b := range creationMaps(row["assetBindings"]) {
		add(stringValue(b["nodeId"]))
	}
	characterAssets := map[string]bool{}
	for _, ch := range creationMaps(row["characters"]) {
		if id := stringValue(ch["characterAssetId"]); id != "" {
			characterAssets[id] = true
		}
	}
	for _, n := range nodes {
		m, _ := n["metadata"].(map[string]any)
		if stringValue(m["workflowKind"]) == "character" && characterAssets[stringValue(m["characterAssetId"])] {
			add(stringValue(n["id"]))
		}
	}
	mentions := []string{}
	counts := map[string]int{}
	for _, id := range ids {
		index := cloudAgentNodeIndex(nodes, id)
		if index < 0 {
			continue
		}
		n := nodes[index]
		kind := cloudAgentConnectionResourceKind(n)
		labels := map[string]string{"image": "图片", "video": "视频", "audio": "音频", "text": "文本", "character": "角色"}
		label := labels[kind]
		if label == "" {
			continue
		}
		counter := kind
		if stringValue(n["type"]) == "drawing" {
			label = "绘图"
			counter = "drawing"
		}
		counts[counter]++
		mentions = append(mentions, fmt.Sprintf("@%s%d", label, counts[counter]))
	}
	if len(mentions) == 0 {
		return prompt
	}
	return strings.TrimSpace("参考资产：" + strings.Join(mentions, " ") + "\n" + prompt)
}

func cloudAgentConnectionResourceKind(node map[string]any) string {
	m, _ := node["metadata"].(map[string]any)
	if stringValue(m["workflowKind"]) == "character" && stringValue(m["characterAssetId"]) != "" {
		return "character"
	}
	content := stringValue(m["content"]) != ""
	storage := stringValue(m["storageKey"]) != ""
	switch stringValue(node["type"]) {
	case "image":
		if content || storage {
			return "image"
		}
	case "video":
		if content || storage {
			return "video"
		}
	case "text":
		if content || stringValue(m["prompt"]) != "" {
			return "text"
		}
	case "drawing":
		if stringValue(m["drawingId"]) != "" {
			return "image"
		}
	case "audio":
		if content {
			return "audio"
		}
	case "skill":
		if content || m["skillSnapshot"] != nil {
			return "text"
		}
	case "markdown", "svg", "html":
		if content {
			return "text"
		}
	case "colorgrade":
		return "image"
	case "media-conversion":
		c, _ := m["mediaConversion"].(map[string]any)
		if stringValue(c["status"]) == "completed" && stringValue(c["resultStorageKey"]) != "" {
			if stringValue(c["outputKind"]) == "video" {
				return "video"
			}
			return "image"
		}
	}
	return ""
}
