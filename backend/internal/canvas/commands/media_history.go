package commands

import (
	"reflect"

	"yingce/backend/internal/canvas/mediaversions"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// 任务生命周期字段由任务写回管理；撤销/重做只恢复创作内容和版本选择，不能让已结束的任务重新变成生成中。
var taskLifecycleFields = []string{"taskId", "taskStatus", "taskProgress", "taskStage", "taskMediaStage", "taskStartedAt", "taskCompletedAt", "taskCreatedAt", "taskUpdatedAt", "status", "errorDetails"}

// Undo changes authoring and an explicit version selection, never a real task's
// lifecycle. In particular a completed task cannot become pending again.
func reconcileHistoryMedia(_ *repository.Repository, _, _, _ string, _ int64, before, after Document, recreated map[string]bool) ([]Operation, error) {
	allowed := []Operation{}
	for _, node := range maps(after["nodes"]) {
		id := text(node["id"])
		if !mediaversions.Supported(text(node["type"])) {
			continue
		}
		old := entity(before, "node:"+id)
		if old == nil || recreated["node:"+id] {
			mediaversions.ReleaseRestored(node)
			allowed = append(allowed, Operation{Type: "use_media_version", ID: id})
			continue
		}
		m, _ := node["metadata"].(map[string]any)
		if m == nil {
			m = map[string]any{}
			node["metadata"] = m
		}
		live, _ := old["metadata"].(map[string]any)
		for _, key := range taskLifecycleFields {
			if value, ok := live[key]; ok {
				m[key] = value
			} else {
				delete(m, key)
			}
		}
		if !reflect.DeepEqual(model.CanvasMediaProjection(old), model.CanvasMediaProjection(node)) || !reflect.DeepEqual(live["currentMediaVersionId"], m["currentMediaVersionId"]) || !reflect.DeepEqual(live["mediaVersions"], m["mediaVersions"]) {
			allowed = append(allowed, Operation{Type: "use_media_version", ID: id})
		}
	}
	return allowed, nil
}

// Only a successful same-client undo/redo uses this comparison to advance its
// neighbor's precondition. External edits still fail the entity-version guard.
func historyEntityValue(value, other map[string]any) map[string]any {
	// 媒体节点的媒体、版本与任务生命周期由写回和版本操作管理，撤销时按 reconcileHistoryMedia 规则处理，
	// 比较前统一去掉，其余创作字段仍须完全一致。
	if value == nil || other == nil || !mediaversions.Supported(text(value["type"])) && !mediaversions.Supported(text(other["type"])) {
		return value
	}
	raw, _ := cloneDocument(Document(value))
	out := map[string]any(raw)
	m, _ := out["metadata"].(map[string]any)
	for _, key := range append(append(append([]string{}, model.CanvasMediaFields...), taskLifecycleFields...), "mediaVersions", "currentMediaVersionId") {
		delete(m, key)
	}
	return out
}
