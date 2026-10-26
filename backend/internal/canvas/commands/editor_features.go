package commands

import (
	"encoding/json"
	"strings"
)

// Feature relationships are checked against the final graph: producers may create
// an output before its container/scene in the same atomic command.
func validateEditorFeatureReferences(doc Document, ops []Operation) error {
	for _, op := range ops {
		if op.Type != "update_node" {
			continue
		}
		node := entity(doc, "node:"+op.ID)
		if node == nil {
			continue
		}
		meta, _ := node["metadata"].(map[string]any)
		ref := func(key string, kinds ...string) error {
			id := text(meta[key])
			if id == "" {
				return nil
			}
			target := entity(doc, "node:"+id)
			if id == op.ID || target == nil {
				return BadAuthRequest("节点引用无效: " + key)
			}
			for _, kind := range kinds {
				if text(target["type"]) == kind {
					return nil
				}
			}
			return BadAuthRequest("节点引用类型无效: " + key)
		}
		for _, key := range []string{"videoStartFrameNodeId", "videoEndFrameNodeId", "previsPreviewNodeId", "previsDepthNodeId", "previsNormalNodeId"} {
			if _, changed := op.Patch[key]; changed {
				if err := ref(key, "image", "drawing", "colorgrade"); err != nil {
					return err
				}
			}
		}
		for _, key := range []string{"videoFrameSourceNodeId", "previsClayVideoNodeId"} {
			if _, changed := op.Patch[key]; changed {
				if err := ref(key, "video"); err != nil {
					return err
				}
			}
		}
		if _, changed := op.Patch["referenceSetId"]; changed {
			if err := ref("referenceSetId", "frame"); err != nil {
				return err
			}
		}
		if _, changed := op.Patch["referenceAssetNodeIds"]; changed {
			for _, id := range stringList(meta["referenceAssetNodeIds"]) {
				if id == op.ID || entity(doc, "node:"+id) == nil {
					return BadAuthRequest("引用组素材不存在")
				}
			}
		}
		if _, changed := op.Patch["styleProfileJson"]; changed && text(meta["styleProfileJson"]) != "" && !json.Valid([]byte(text(meta["styleProfileJson"]))) {
			return BadAuthRequest("画风配置必须是有效 JSON")
		}
		batchChanged := false
		for _, key := range []string{"batchSourceNodeId", "batchRowId", "batchOperation", "batchInputNodeIds"} {
			if _, ok := op.Patch[key]; ok {
				batchChanged = true
			}
		}
		if batchChanged && text(meta["batchSourceNodeId"]) != "" {
			source := entity(doc, "node:"+text(meta["batchSourceNodeId"]))
			if source == nil || text(source["type"]) != "batch-table" || text(source["id"]) == op.ID {
				return BadAuthRequest("批量来源节点无效")
			}
			sourceMeta, _ := source["metadata"].(map[string]any)
			table, _ := sourceMeta["batchTable"].(map[string]any)
			found := false
			for _, row := range maps(table["rows"]) {
				if text(row["id"]) == text(meta["batchRowId"]) {
					found = true
					if !sameJSON(row["inputNodeIds"], meta["batchInputNodeIds"]) {
						return BadAuthRequest("批量来源素材与行输入不一致")
					}
				}
			}
			if !found || text(meta["batchOperation"]) != text(table["operation"]) {
				return BadAuthRequest("批量来源行或任务类型无效")
			}
			for _, id := range stringList(meta["batchInputNodeIds"]) {
				if id != "" {
					target := entity(doc, "node:"+id)
					if target == nil || text(target["type"]) != "image" || id == op.ID {
						return BadAuthRequest("批量来源素材无效")
					}
				}
			}
		}
		if batchChanged && text(meta["batchSourceNodeId"]) == "" && (text(meta["batchRowId"]) != "" || text(meta["batchOperation"]) != "" || len(stringList(meta["batchInputNodeIds"])) > 0) {
			return BadAuthRequest("批量产物缺少来源表")
		}
		previsChanged := false
		for _, key := range []string{"previsSceneId", "previsShotId"} {
			if _, ok := op.Patch[key]; ok {
				previsChanged = true
			}
		}
		if previsChanged && text(meta["previsSceneId"]) != "" {
			found := false
			for _, scene := range maps(doc["previsScenes"]) {
				if text(scene["id"]) == text(meta["previsSceneId"]) {
					for _, shot := range maps(scene["shots"]) {
						if text(shot["id"]) == text(meta["previsShotId"]) {
							found = true
						}
					}
				}
			}
			if !found {
				return BadAuthRequest("预演场景或镜头不存在")
			}
		}
		if previsChanged && text(meta["previsSceneId"]) == "" && text(meta["previsShotId"]) != "" {
			return BadAuthRequest("预演镜头缺少场景")
		}
		if state, changed := op.Patch["conversionState"].(map[string]any); changed {
			if id := text(state["sourceNodeId"]); id != "" {
				target := entity(doc, "node:"+id)
				if target == nil || id == op.ID || (text(target["type"]) != "image" && text(target["type"]) != "video") {
					return BadAuthRequest("转换来源无效")
				}
			}
			key := text(state["resultStorageKey"])
			if key != "" && (!strings.HasPrefix(key, "resource:") || key != text(meta["storageKey"])) {
				return BadAuthRequest("转换结果必须先绑定授权资源")
			}
			if text(state["status"]) == "completed" && key == "" {
				return BadAuthRequest("转换结果缺少资源")
			}
			if key != "" && !strings.HasPrefix(text(meta["mimeType"]), text(state["outputKind"])+"/") {
				return BadAuthRequest("转换结果类型与资源不一致")
			}
		}
	}
	return nil
}
