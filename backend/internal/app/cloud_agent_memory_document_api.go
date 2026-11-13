package app

import (
	"yingce/backend/internal/agentmemory/document"
)

// 记忆文件的门面：视图与请求类型归 document 所有，别名保持对外 JSON 与 handler
// 合同不变；方法体只做透传。跨用户聚合见 cloud_agent_memory_admin.go，组合根
// 接线见 service.go。业务实现在 internal/agentmemory/document。
type (
	AgentMemoryDocumentView       = document.View
	AgentMemoryDocumentAdminView  = document.AdminView
	AgentMemoryDocRevisionView    = document.RevisionView
	AgentMemoryEntryInput         = document.EntryInput
	AgentMemoryDocUpdateRequest   = document.UpdateRequest
	AgentMemoryDocSettingsRequest = document.SettingsRequest
	AgentMemoryDocDecisionRequest = document.DecisionRequest
)

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
