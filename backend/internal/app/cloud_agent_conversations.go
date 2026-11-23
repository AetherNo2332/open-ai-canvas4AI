package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

type CloudAgentConversation struct {
	Scope             AgentBusinessScope `json:"scope"`
	AgentDefinitionID string             `json:"agentDefinitionId"`
	Resumable         bool               `json:"resumable"`
	ID                string             `json:"id"`
	CanvasID          string             `json:"canvasId"`
	Title             string             `json:"title"`
	LatestRunID       string             `json:"latestRunId"`
	Status            string             `json:"status"`
	Revision          int64              `json:"revision"`
	CleanupPending    bool               `json:"cleanupPending"`
	Archived          bool               `json:"archived"`
	QueueRevision     int64              `json:"queueRevision"`
	QueuedCount       int                `json:"queuedCount"`
	QueuePaused       bool               `json:"queuePaused"`
	CreatedAt         time.Time          `json:"createdAt"`
	UpdatedAt         time.Time          `json:"updatedAt"`
}

type AgentConversationSelector struct {
	Scope             AgentBusinessScope
	AgentDefinitionID string
}

type CloudAgentConversationPage struct {
	Items      []CloudAgentConversation `json:"items"`
	NextCursor string                   `json:"nextCursor,omitempty"`
}

type CloudAgentConversationRunPage struct {
	Items      []*CloudAgentRun `json:"items"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

func cloudAgentHistoryCursor(value string, limit int) (*repository.CloudAgentHistoryCursor, error) {
	if limit < 1 || limit > 100 {
		return nil, BadAuthRequest("会话页大小必须是 1–100 之间的整数")
	}
	if value == "" {
		return nil, nil
	}
	if len(value) > 512 {
		return nil, BadAuthRequest("会话分页游标无效")
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	var cursor repository.CloudAgentHistoryCursor
	if err != nil || json.Unmarshal(data, &cursor) != nil || cursor.CreatedAt.IsZero() || validateCloudAgentID(cursor.ID, "轮次 ID", 80) != nil {
		return nil, BadAuthRequest("会话分页游标无效")
	}
	return &cursor, nil
}

func encodeCloudAgentHistoryCursor(run model.CloudAgentExecution) string {
	data, _ := json.Marshal(repository.CloudAgentHistoryCursor{CreatedAt: run.CreatedAt, ID: run.ID})
	return base64.RawURLEncoding.EncodeToString(data)
}

func (s *Service) CloudAgentConversations(userID, canvasID, before string, limit int) (*CloudAgentConversationPage, error) {
	return s.ScopedCloudAgentConversations(userID, AgentConversationSelector{Scope: AgentBusinessScope{Type: "canvas", ID: canvasID}}, before, limit)
}

func (s *Service) agentConversationBinding(userID string, selector AgentConversationSelector) (model.AgentConversationBinding, error) {
	var binding model.AgentConversationBinding
	if userID == "" {
		return binding, kernel.Unauthorized("请先登录")
	}
	var err error
	selector, err = s.NormalizeAgentConversationSelector(userID, selector)
	if err != nil {
		return binding, err
	}
	scope := selector.Scope
	if validateCloudAgentID(scope.Type, "业务类型", 40) != nil || validateCloudAgentID(scope.ID, "业务对象 ID", 80) != nil {
		return binding, BadAuthRequest("需要有效的业务类型和对象 ID")
	}
	definition, err := s.ResolveAgentDefinition(scope.Type, selector.AgentDefinitionID)
	if err != nil {
		return binding, err
	}
	req := CloudAgentRequest{Scope: &scope, PermissionMode: "read_only"}
	if scope.Type == "canvas" {
		req.CanvasID = scope.ID
	}
	if _, err := s.authorizeAgentDefinition(definition, userID, req); err != nil {
		return binding, err
	}
	return model.AgentConversationBinding{ScopeType: scope.Type, ScopeID: scope.ID, AgentDefinitionID: definition.ID}, nil
}

func (s *Service) RequireAgentTargetScope(userID, id string, conversation bool, selector AgentConversationSelector) error {
	if err := validateCloudAgentID(id, "会话或运行 ID", 80); err != nil {
		return err
	}
	binding, err := s.agentConversationBinding(userID, selector)
	if err != nil {
		return err
	}
	var run *model.CloudAgentExecution
	if conversation {
		run, err = s.repo.LatestAgentConversationRun(userID, id)
	} else {
		run, err = s.repo.CloudAgentControlRow(userID, id)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return kernel.NotFound("Agent 会话或运行不存在")
	}
	if err != nil {
		return err
	}
	binding.AgentContextVersion = run.AgentContextVersion
	if run.AgentConversationBinding != binding {
		return kernel.NotFound("当前业务没有此 Agent 会话或运行")
	}
	return nil
}

func (s *Service) ScopedCloudAgentConversations(userID string, selector AgentConversationSelector, before string, limit int) (*CloudAgentConversationPage, error) {
	cursor, err := cloudAgentHistoryCursor(before, limit)
	if err != nil {
		return nil, err
	}
	binding, err := s.agentConversationBinding(userID, selector)
	if err != nil {
		return nil, err
	}
	rows, err := s.repo.CloudAgentConversations(userID, "", cursor, limit+1, binding)
	if err != nil {
		return nil, err
	}
	page := &CloudAgentConversationPage{Items: []CloudAgentConversation{}}
	if len(rows) > limit {
		page.NextCursor = encodeCloudAgentHistoryCursor(rows[limit-1])
		rows = rows[:limit]
	}
	ids := make([]string, 0, len(rows))
	for _, run := range rows {
		id := run.ConversationID
		if id == "" {
			id = run.ID
		}
		ids = append(ids, id)
	}
	queues, err := s.repo.AgentMessageQueueSummaries(userID, ids)
	if err != nil {
		return nil, err
	}
	for _, run := range rows {
		id := run.ConversationID
		if id == "" {
			id = run.ID
		}
		page.Items = append(page.Items, CloudAgentConversation{
			ID: id, CanvasID: run.CanvasID, Title: run.Title, LatestRunID: run.ID,
			Scope:             PublicAgentBusinessScope(AgentBusinessScope{Type: run.ScopeType, ID: run.ScopeID}),
			AgentDefinitionID: run.AgentDefinitionID, Resumable: run.AgentContextVersion == agentContextVersion,
			Status: run.Status, Revision: run.Revision, CleanupPending: run.CleanupPending,
			Archived:  run.ConversationArchived,
			CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
			QueueRevision: queues[id].Revision, QueuedCount: queues[id].QueuedCount, QueuePaused: queues[id].Paused,
		})
	}
	return page, nil
}

func (s *Service) ArchiveCloudAgentConversation(userID, conversationID string) error {
	if userID == "" {
		return kernel.Unauthorized("请先登录")
	}
	if err := validateCloudAgentID(conversationID, "会话 ID", 80); err != nil {
		return err
	}
	exists, err := s.repo.CloudAgentConversationExists(userID, conversationID)
	if err != nil {
		return err
	}
	if !exists {
		return kernel.NotFound("Agent 对话不存在")
	}
	return s.repo.ArchiveCloudAgentConversation(userID, conversationID)
}

func (s *Service) CloudAgentConversationRuns(userID, conversationID, after string, limit int) (*CloudAgentConversationRunPage, error) {
	if userID == "" {
		return nil, kernel.Unauthorized("请先登录")
	}
	if err := validateCloudAgentID(conversationID, "会话 ID", 80); err != nil {
		return nil, err
	}
	cursor, err := cloudAgentHistoryCursor(after, limit)
	if err != nil {
		return nil, err
	}
	exists, err := s.repo.CloudAgentConversationExists(userID, conversationID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, kernel.NotFound("Agent 对话不存在")
	}
	rows, err := s.repo.CloudAgentConversationRuns(userID, conversationID, cursor, limit+1)
	if err != nil {
		return nil, err
	}
	page := &CloudAgentConversationRunPage{Items: []*CloudAgentRun{}}
	if len(rows) > limit {
		page.NextCursor = encodeCloudAgentHistoryCursor(rows[limit-1])
		rows = rows[:limit]
	}
	for _, row := range rows {
		run, err := s.CloudAgentRun(userID, row.ID)
		if err != nil {
			return nil, err
		}
		page.Items = append(page.Items, run)
	}
	return page, nil
}
