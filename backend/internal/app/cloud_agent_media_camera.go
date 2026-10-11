package app

import (
	"fmt"
	"strings"
)

// cloudAgentMediaCameraPrompt 读取来源/参考节点上由前端摄像机控制持久化的镜头提示词
// （metadata.cameraPrompt，buildCameraControlPrompt 的编译产物），与浏览器生成链路
// 保持同构；旧节点没有编译词时用原始参数拼一句通用兜底，不猜目录文案。
// 仅在 image/video 模式且 cameraControl 启用时生效；多个节点都带参数时按
// source → references 顺序取第一个，与参考资产的主次关系一致。
func cloudAgentMediaCameraPrompt(doc map[string]any, a cloudAgentMediaArgs) string {
	if a.Mode != "image" && a.Mode != "video" {
		return ""
	}
	byID := make(map[string]map[string]any)
	for _, node := range creationMaps(doc["nodes"]) {
		byID[stringValue(node["id"])] = node
	}
	for _, id := range append([]string{a.SourceNodeID}, a.ReferenceNodeIDs...) {
		if id == "" {
			continue
		}
		node := byID[id]
		if node == nil {
			continue
		}
		metadata, _ := node["metadata"].(map[string]any)
		control, _ := metadata["cameraControl"].(map[string]any)
		if !boolValue(control["enabled"], false) {
			continue
		}
		if prompt := strings.TrimSpace(stringValue(metadata["cameraPrompt"])); prompt != "" {
			return prompt
		}
		camera, lens := stringValue(control["camera"]), stringValue(control["lens"])
		focal, aperture := numberValue(control["focalLength"], 0), numberValue(control["aperture"], 0)
		if camera == "" || lens == "" || focal <= 0 || aperture <= 0 {
			return ""
		}
		return fmt.Sprintf("镜头语言参考：%s 机身、%s 镜头，%gmm 焦段，f/%g 光圈。", camera, lens, focal, aperture)
	}
	return ""
}
