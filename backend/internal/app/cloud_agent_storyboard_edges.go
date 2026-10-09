package app

import (
	"fmt"
	"strings"
)

// Mirrors the editor's attachNodeToStoryboardRow. Output identities are derived
// from actual endpoints, never supplied as writable row fields by the model.
func cloudAgentAttachStoryboardEdge(nodes []map[string]any, edge map[string]any) error {
	for _, side := range []string{"from", "to"} {
		handle := stringValue(edge[side+"HandleId"])
		if !strings.HasPrefix(handle, "row:") && handle != "storyboard:context" {
			continue
		}
		otherSide := "from"
		if side == "from" {
			otherSide = "to"
		}
		index := cloudAgentNodeIndex(nodes, stringValue(edge[side+"NodeId"]))
		other := cloudAgentNodeIndex(nodes, stringValue(edge[otherSide+"NodeId"]))
		if index < 0 || other < 0 || stringValue(nodes[index]["type"]) != "script" {
			continue
		}
		board, _ := cloudAgentNodeMetadata(nodes[index])["storyboard"].(map[string]any)
		if board == nil {
			continue
		}
		linked := nodes[other]
		linkedID := stringValue(linked["id"])
		if handle == "storyboard:context" {
			refs := cloudAgentStrings(board["referenceNodeIds"])
			found := false
			for _, id := range refs {
				if id == linkedID {
					found = true
				}
			}
			if !found {
				refs = append(refs, linkedID)
			}
			board["referenceNodeIds"] = refs
			continue
		}
		rows := creationMaps(board["rows"])
		rowIndex := cloudAgentNodeIndex(rows, strings.TrimPrefix(handle, "row:"))
		if rowIndex < 0 {
			continue
		}
		row := rows[rowIndex]
		if side == "from" {
			switch stringValue(linked["type"]) {
			case "image":
				row["imageNodeId"] = linkedID
			case "video":
				row["videoNodeId"] = linkedID
				prompt := stringValue(row["videoMotionPrompt"])
				if strings.TrimSpace(prompt) == "" {
					prompt = stringValue(row["plotDescription"])
				}
				meta := cloudAgentNodeMetadata(linked)
				meta["workflowKind"], meta["shotIndex"], meta["generationMode"] = "shot", row["shotNumber"], "video"
				prompt = strings.TrimSpace(prompt)
				linked["title"] = "镜头 " + fmt.Sprint(row["shotNumber"]) + " · 视频"
				meta["workflowTitle"] = "镜头 " + fmt.Sprint(row["shotNumber"]) + " 视频"
				// Keep the last submitted prompt while editing the next generation draft.
				if _, submitted := meta["prompt"]; !submitted {
					meta["prompt"] = prompt
				}
				if stringValue(meta["videoEditOperation"]) == "" {
					meta["videoEditOperation"] = "text_to_video"
				}
				if variables, ok := row["videoPromptTemplateVariables"].(map[string]any); ok {
					meta["promptTemplateOperation"] = "storyboard_video"
					meta["promptTemplateVariables"] = variables
				} else {
					delete(meta, "promptTemplateOperation")
					delete(meta, "promptTemplateVariables")
				}
				patch := map[string]any{"content": cloudAgentConnectionComposer(nodes[index], row, nodes, prompt)}
				if seconds := row["durationSeconds"]; seconds != nil {
					patch["seconds"] = fmt.Sprint(seconds)
				}
				descriptor, _ := cloudAgentNodeCapabilityForNode(linked)
				if err := descriptor.ApplyPatch(linked, patch); err != nil {
					return BadAuthRequest("分镜提示词或时长与目标视频配置不一致：" + err.Error())
				}
			}
		} else {
			meta := cloudAgentNodeMetadata(linked)
			role := ""
			switch {
			case meta["workflowKind"] == "character" || meta["assetCategory"] == "character":
				role = "character"
			case linked["type"] == "audio":
				role = "audio"
			case linked["type"] == "video":
				role = "motion"
			case meta["assetCategory"] == "environment" || meta["assetCategory"] == "prop":
				role = stringValue(meta["assetCategory"])
			case linked["type"] == "image" || linked["type"] == "drawing":
				role = "style"
			}
			if role != "" {
				bindings := creationMaps(row["assetBindings"])
				exists := false
				for _, binding := range bindings {
					if binding["nodeId"] == linkedID {
						exists = true
					}
				}
				if !exists {
					priority := 60
					switch role {
					case "character":
						priority = 100
					case "environment":
						priority = 90
					case "prop":
						priority = 80
					case "motion", "audio":
						priority = 70
					}
					bindings = append(bindings, map[string]any{"nodeId": linkedID, "role": role, "priority": priority})
					row["assetBindings"] = mapsAsAny(bindings)
				}
			}
		}
	}
	return nil
}
