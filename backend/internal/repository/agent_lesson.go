package repository

import (
	"strings"

	"yingce/backend/internal/model"
)

// agent_lessons 已退役（schema v82 把已批准经验并入长期记忆文件），表保留只读。
// 这里只剩首次建记忆文件时导入已批准经验所用的查询。

const agentLessonScanMax = 500

func (r *Repository) ApprovedAgentLessons(userID string, limit int) ([]model.AgentLesson, error) {
	if limit <= 0 || limit > agentLessonScanMax {
		limit = agentLessonScanMax
	}
	var lessons []model.AgentLesson
	err := r.db.Where("author_user_id = ? AND status = ?", strings.TrimSpace(userID), model.AgentLessonStatusApproved).
		Order("hits DESC, updated_at DESC").Limit(limit).Find(&lessons).Error
	return lessons, err
}
