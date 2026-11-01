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
		if handle == "storyboard:context" {
			cloudAgentAddStoryboardContextReference(board, stringValue(linked["id"]))
			continue
		}
		rows := creationMaps(board["rows"])
		rowIndex := cloudAgentNodeIndex(rows, strings.TrimPrefix(handle, "row:"))
		if rowIndex < 0 {
			continue
		}
		row := rows[rowIndex]
		if side == "from" {
			if err := cloudAgentBindStoryboardRowOutput(nodes, nodes[index], row, linked); err != nil {
				return err
			}
		} else {
			cloudAgentBindStoryboardRowAsset(row, linked)
		}
	}
	return nil
}

func cloudAgentAddStoryboardContextReference(board map[string]any, linkedID string) {
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
}

// cloudAgentBindStoryboardRowOutput records an image or video output on the
// row; a video node also takes the row's shot metadata and composed prompt.
func cloudAgentBindStoryboardRowOutput(nodes []map[string]any, storyboard, row, linked map[string]any) error {
	linkedID := stringValue(linked["id"])
	switch stringValue(linked["type"]) {
	case "image":
		row["imageNodeId"] = linkedID
	case "video":
		row["videoNodeId"] = linkedID
		return cloudAgentPrepareStoryboardVideo(nodes, storyboard, row, linked)
	}
	return nil
}

func cloudAgentPrepareStoryboardVideo(nodes []map[string]any, storyboard, row, linked map[string]any) error {
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
	patch := map[string]any{"content": cloudAgentConnectionComposer(storyboard, row, nodes, prompt)}
	if seconds := row["durationSeconds"]; seconds != nil {
		patch["seconds"] = fmt.Sprint(seconds)
	}
	descriptor, _ := cloudAgentNodeCapabilityForNode(linked)
	if err := descriptor.ApplyPatch(linked, patch); err != nil {
		return BadAuthRequest("分镜提示词或时长与目标视频配置不一致：" + err.Error())
	}
	return nil
}

// cloudAgentBindStoryboardRowAsset adds the linked node as a row asset binding
// with a role and priority derived from its kind; existing bindings are kept.
func cloudAgentBindStoryboardRowAsset(row, linked map[string]any) {
	linkedID := stringValue(linked["id"])
	role := cloudAgentStoryboardAssetRole(linked)
	if role == "" {
		return
	}
	bindings := creationMaps(row["assetBindings"])
	for _, binding := range bindings {
		if binding["nodeId"] == linkedID {
			return
		}
	}
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

func cloudAgentStoryboardAssetRole(linked map[string]any) string {
	meta := cloudAgentNodeMetadata(linked)
	switch {
	case meta["workflowKind"] == "character" || meta["assetCategory"] == "character":
		return "character"
	case linked["type"] == "audio":
		return "audio"
	case linked["type"] == "video":
		return "motion"
	case meta["assetCategory"] == "environment" || meta["assetCategory"] == "prop":
		return stringValue(meta["assetCategory"])
	case linked["type"] == "image" || linked["type"] == "drawing":
		return "style"
	}
	return ""
}
