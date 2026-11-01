package app

// Mirrors canvas-connection-policy.ts structural admission. A graph association
// never by itself makes an unrendered drawing/filter a generation resource.
func cloudAgentGraphMaxInputs(kind string) int {
	if kind == "media-conversion" {
		return 1
	}
	return 0
}

func cloudAgentManualGraphInputError(nodes []map[string]any, from, to map[string]any, fromKind string, connections []map[string]any) error {
	targetType := stringValue(to["type"])
	if fromKind == "character" && targetType == "text" && stringValue(cloudAgentNodeMetadata(to)["generationMode"]) == "" {
		return BadAuthRequest("角色卡只能连接生成节点，不能连接普通文本节点")
	}
	if targetType == "media-conversion" && stringValue(from["type"]) != "image" && stringValue(from["type"]) != "video" {
		return BadAuthRequest("转换节点只接受图片或视频节点")
	}
	if targetType == "batch-table" && fromKind != "image" {
		return BadAuthRequest("批量创作表只接受图片输入")
	}
	mode := cloudAgentManualGraphTargetMode(to, targetType)
	if mode == "image" && (fromKind == "video" || fromKind == "audio") {
		return BadAuthRequest("图片生成节点不能连接参考视频或音频")
	}
	if mode == "text" && fromKind == "audio" {
		return BadAuthRequest("文本生成节点不能连接参考音频")
	}
	if mode == "audio" {
		if fromKind != "text" && fromKind != "character" {
			return BadAuthRequest("音频生成只接受文本或角色卡")
		}
		if fromKind == "character" && cloudAgentCharacterInputCount(nodes, from, to, connections) > 1 {
			return BadAuthRequest("角色配音一次只能连接一个角色卡")
		}
	}
	return nil
}

// cloudAgentManualGraphTargetMode is the generation mode a target node takes
// its inputs for; config nodes default to image.
func cloudAgentManualGraphTargetMode(to map[string]any, targetType string) string {
	switch targetType {
	case "image", "video", "audio", "text":
		return targetType
	case "script":
		return "text"
	case "config":
		mode := stringValue(cloudAgentNodeMetadata(to)["generationMode"])
		if mode == "" {
			mode = "image"
		}
		return mode
	}
	return ""
}

// cloudAgentCharacterInputCount counts character-card inputs of the target,
// including the new source.
func cloudAgentCharacterInputCount(nodes []map[string]any, from, to map[string]any, connections []map[string]any) int {
	ids := map[string]bool{stringValue(from["id"]): true}
	for _, edge := range connections {
		if stringValue(edge["toNodeId"]) == stringValue(to["id"]) {
			ids[stringValue(edge["fromNodeId"])] = true
		}
	}
	count := 0
	for id := range ids {
		index := cloudAgentNodeIndex(nodes, id)
		if index >= 0 {
			descriptor, known := cloudAgentNodeCapabilityForNode(nodes[index])
			if known && descriptor.InputKind == "character" {
				count++
			}
		}
	}
	return count
}
