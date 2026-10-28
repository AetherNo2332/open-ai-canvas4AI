package canvas

import (
	"yingce/backend/internal/assets"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// CaptureDocumentHistory 供 Agent 画布命令使用：画布行已由命令层以 revision CAS 写入，
// 这里只在同一事务里按与浏览器保存相同的间隔和条数上限记录历史快照。
func CaptureDocumentHistory(repo *repository.Repository, before *model.CanvasProject, after model.CanvasProject, reason string) error {
	snapshot, resourceIDs, err := buildCanvasSnapshot(before, after, reason)
	if err != nil {
		return err
	}
	current := map[string]struct{}{}
	if err := assets.CollectOwnedDocumentReferences(after.PayloadJSON, current); err != nil {
		return err
	}
	return repo.CaptureCanvasSnapshot(&after, snapshot, resourceIDs, assets.SortedIDs(current), after.UpdatedAt.Add(-canvasHistoryInterval), canvasHistoryLimit, reason == "before_restore" || reason == "before_resource_repair")
}
