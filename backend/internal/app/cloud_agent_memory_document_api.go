package app

import (
	"yingce/backend/internal/agentmemory/document"
)

// 记忆文件的视图与请求类型归 document 所有；别名保持对外 JSON 与 handler 合同不变。
type (
	AgentMemoryDocumentView       = document.View
	AgentMemoryDocumentAdminView  = document.AdminView
	AgentMemoryDocRevisionView    = document.RevisionView
	AgentMemoryEntryInput         = document.EntryInput
	AgentMemoryDocUpdateRequest   = document.UpdateRequest
	AgentMemoryDocSettingsRequest = document.SettingsRequest
	AgentMemoryDocDecisionRequest = document.DecisionRequest
)

// memoryDocuments 返回记忆文档服务；空 Service（纯登录校验测试）也只做参数校验。
func (s *Service) memoryDocuments() *document.Service {
	if s.memoryDoc == nil {
		s.memoryDoc = document.New(s.repo)
	}
	return s.memoryDoc
}

// AgentMemoryDocument 返回当前用户的记忆文件，第一次访问时创建并导入旧版经验。
func (s *Service) AgentMemoryDocument(userID string) (AgentMemoryDocumentView, error) {
	return s.memoryDocuments().Document(userID)
}

// UpdateAgentMemoryDocumentSettings 只改记忆模式；关闭后不注入、不暴露写入工具，已有内容保留。
func (s *Service) UpdateAgentMemoryDocumentSettings(userID string, req AgentMemoryDocSettingsRequest) (AgentMemoryDocumentView, error) {
	return s.memoryDocuments().UpdateSettings(userID, req)
}

// UpdateAgentMemoryDocument 用用户编辑后的完整条目列表替换记忆文件（按 revision 做 CAS）。
func (s *Service) UpdateAgentMemoryDocument(userID string, req AgentMemoryDocUpdateRequest) (AgentMemoryDocumentView, error) {
	return s.memoryDocuments().Update(userID, req)
}

// AgentMemoryDocumentRevisions 列出当前用户的修订记录。
func (s *Service) AgentMemoryDocumentRevisions(userID, status string, limit int) ([]AgentMemoryDocRevisionView, error) {
	return s.memoryDocuments().Revisions(userID, status, limit)
}

// UndoAgentMemoryRevision 撤销一条已生效的修改，撤销本身也留一条修订。
func (s *Service) UndoAgentMemoryRevision(userID, revisionID string) (AgentMemoryDocumentView, error) {
	return s.memoryDocuments().Undo(userID, revisionID)
}

// DecideAgentMemoryRevision 采纳或拒绝需确认模式下的待定修改。
func (s *Service) DecideAgentMemoryRevision(userID, revisionID string, req AgentMemoryDocDecisionRequest) (AgentMemoryDocumentView, error) {
	return s.memoryDocuments().Decide(userID, revisionID, req)
}

// AdminAgentMemoryDocuments 供管理员按用户查看记忆文件正文（只读，不替用户创建或修改）。
// 跨用户聚合与用户信息拼装留 app；单份文档渲染归 document。
func (s *Service) AdminAgentMemoryDocuments(userID string, limit int) ([]AgentMemoryDocumentAdminView, error) {
	rows, err := s.repo.AdminAgentMemoryDocuments(userID, limit)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.UserID)
	}
	users, err := s.repo.UsersByIDs(ids)
	if err != nil {
		return nil, err
	}
	views := make([]AgentMemoryDocumentAdminView, 0, len(rows))
	for index := range rows {
		view, err := s.memoryDocuments().ViewOf(&rows[index])
		if err != nil {
			return nil, err
		}
		admin := AgentMemoryDocumentAdminView{UserID: rows[index].UserID, View: view}
		if user, ok := users[rows[index].UserID]; ok {
			admin.Username, admin.DisplayName = user.Username, user.DisplayName
		}
		views = append(views, admin)
	}
	return views, nil
}
