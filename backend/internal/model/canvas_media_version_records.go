package model

// CanvasMediaVersionRecordChanged 报告是否有已存在的 metadata.mediaVersions[] 记录换了媒体（storageKey 或 content）。
// 版本记录可以新增、删除或裁剪，但同一 versionId 必须始终指向同一份媒体：固定到 versionId 的引用依赖这一点。
func CanvasMediaVersionRecordChanged(before, after map[string]any) bool {
	prior := map[string]map[string]string{}
	for _, node := range canvasNodes(before) {
		prior[stringField(node["id"])] = canvasVersionIdentities(node)
	}
	for _, node := range canvasNodes(after) {
		previous := prior[stringField(node["id"])]
		for id, identity := range canvasVersionIdentities(node) {
			if old, ok := previous[id]; ok && old != identity {
				return true
			}
		}
	}
	return false
}

func stringField(value any) string { s, _ := value.(string); return s }

func canvasNodes(doc map[string]any) []map[string]any {
	out := []map[string]any{}
	switch nodes := doc["nodes"].(type) {
	case []any:
		for _, value := range nodes {
			if node, ok := value.(map[string]any); ok {
				out = append(out, node)
			}
		}
	case []map[string]any:
		out = nodes
	}
	return out
}

func canvasVersionIdentities(node map[string]any) map[string]string {
	out := map[string]string{}
	meta, _ := node["metadata"].(map[string]any)
	versions, _ := meta["mediaVersions"].([]any)
	for _, value := range versions {
		version, _ := value.(map[string]any)
		media, _ := version["media"].(map[string]any)
		identity := stringField(media["storageKey"])
		if identity == "" {
			identity = stringField(media["content"])
		}
		if id := stringField(version["id"]); id != "" {
			out[id] = identity
		}
	}
	return out
}
