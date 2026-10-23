package model

// CanvasMediaFields are the persisted media identity and provenance fields.
// Authoring prompts, layout and input bindings do not belong to this projection.
var CanvasMediaFields = []string{"content", "storageKey", "assetId", "naturalWidth", "naturalHeight", "bytes", "mimeType", "durationMs", "hasAudio", "videoPreview", "producedModel", "taskId", "taskStatus", "taskProgress", "taskStage", "taskMediaStage", "taskStartedAt", "taskCompletedAt", "taskDurationMs", "taskCreatedAt", "taskUpdatedAt"}

func CanvasMediaProjection(node map[string]any) map[string]any {
	metadata, _ := node["metadata"].(map[string]any)
	out := map[string]any{"type": node["type"]}
	for _, field := range append(append([]string{}, CanvasMediaFields...), "mediaVersion", "status", "errorDetails") {
		if value, ok := metadata[field]; ok {
			out[field] = value
		}
	}
	return out
}
