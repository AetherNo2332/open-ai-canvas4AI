package repository

import "yingce/backend/internal/model"

// CanvasAgentActivity 是画布同步状态里的 Agent 占用摘要：只读控制字段，
// 轮询不得加载对话正文或画布全文。
type CanvasAgentActivity struct {
	RunID          string `json:"runId"`
	Status         string `json:"status"`
	CleanupPending bool   `json:"cleanupPending"`
}

// CanvasAgentActivity 返回该画布上最早仍在进行（或仍待收尾）的 Agent 运行。
// 运行以 cloud_agent_executions.canvas_id 归属画布；终态且已完成清理的运行不再占用。
func (r *Repository) CanvasAgentActivity(userID, canvasID string) (*CanvasAgentActivity, error) {
	if userID == "" || canvasID == "" {
		return nil, nil
	}
	var rows []model.CloudAgentExecution
	err := r.db.Select("id", "status", "cleanup_pending").
		Where("user_id = ? AND canvas_id = ? AND archived_at IS NULL", userID, canvasID).
		Where("status NOT IN ? OR cleanup_pending = ?", []string{"completed", "failed", "cancelled", "rejected"}, true).
		Order("created_at ASC, id ASC").Limit(1).Find(&rows).Error
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &CanvasAgentActivity{RunID: rows[0].ID, Status: rows[0].Status, CleanupPending: rows[0].CleanupPending}, nil
}
