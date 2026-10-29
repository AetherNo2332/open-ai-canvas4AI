package database

import (
	"time"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// previsRenderRetiredMigrationVersion 接在 fork v75 之后：服务端白模渲染器已下线，
// 预演改由浏览器导演台视口录制。仍在队列或运行中的 previs_render 任务不会再有执行者，
// 这里一次性标记为失败，避免它们永远挂在 queued/running。
const previsRenderRetiredMigrationVersion int64 = 76

const previsRenderRetiredChecksum = "sha256:previs-render-retired-v76-20261029"

const previsRenderRetiredError = "服务端预演渲染已下线，请在导演台中重新预览"

func migratePrevisRenderRetired(tx *gorm.DB) error {
	now := time.Now().UTC()
	return tx.Model(&model.Task{}).
		Where("type = ? AND status IN ?", model.TaskTypePrevisRender, []model.TaskStatus{model.TaskStatusQueued, model.TaskStatusRunning}).
		Updates(map[string]any{"status": model.TaskStatusFailed, "error": previsRenderRetiredError, "stage": "已下线", "completed_at": now, "updated_at": now}).Error
}
