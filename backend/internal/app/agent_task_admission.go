package app

import (
	"yingce/backend/internal/model"
)

// cloudAgentTaskForAdmissionID 只认 Agent 准入登记的确定性任务 ID：每次重投
// 都把 ID 归属于同一用户与同一运行。任何一处不匹配都返回 nil，避免把冲突 ID
// 当成幂等命中而绕过准入校验。
func (s *Service) cloudAgentTaskForAdmissionID(userID string, req CreateTaskRequest) *model.Task {
	if req.admission == nil || req.admission.ID == "" || req.admission.AgentRunID == "" {
		return nil
	}
	task, err := s.repo.TaskForUser(userID, req.admission.ID)
	if err != nil || task == nil {
		return nil
	}
	if task.AgentRunID != req.admission.AgentRunID {
		return nil
	}
	return task
}
