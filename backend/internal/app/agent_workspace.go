package app

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

const agentWorkspaceDocumentMaxBytes = 64 << 10

type AgentWorkspaceView struct {
	WorkspaceID  string `json:"workspaceId"`
	CanvasID     string `json:"canvasId"`
	Revision     int64  `json:"revision"`
	AgentsMD     string `json:"agentsMd"`
	AgentsMDHash string `json:"agentsMdHash"`
}

// WorkspaceSnapshot freezes only the current canvas document for a run.
type WorkspaceSnapshot struct {
	WorkspaceID  string `json:"workspaceId"`
	CanvasID     string `json:"canvasId"`
	Revision     int64  `json:"revision"`
	AgentsMD     string `json:"agentsMd"`
	AgentsMDHash string `json:"agentsMdHash"`
}

func (s *Service) workspaceSnapshotForUser(userID, canvasID string) (*model.AgentWorkspaceSnapshot, error) {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(canvasID) == "" {
		return nil, BadAuthRequest("用户和画布 ID 不能为空")
	}
	if _, err := s.repo.CanvasProjectForUser(userID, canvasID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NewAppError(404, "画布不存在")
		}
		return nil, err
	}
	return s.repo.AgentWorkspaceSnapshot(userID, canvasID)
}

func workspaceView(snapshot *model.AgentWorkspaceSnapshot) *AgentWorkspaceView {
	return &AgentWorkspaceView{WorkspaceID: snapshot.ID, CanvasID: snapshot.CanvasID, Revision: snapshot.Revision, AgentsMD: snapshot.AgentsMD, AgentsMDHash: snapshot.AgentsMDHash}
}

func (s *Service) GetAgentWorkspace(userID, canvasID string) (*AgentWorkspaceView, error) {
	snapshot, err := s.workspaceSnapshotForUser(userID, canvasID)
	if err != nil {
		return nil, err
	}
	return workspaceView(snapshot), nil
}

func (s *Service) UpdateAgentWorkspace(userID, canvasID string, revision int64, agentsMD string) (*AgentWorkspaceView, error) {
	if revision < 0 || !utf8.ValidString(agentsMD) || len(agentsMD) > agentWorkspaceDocumentMaxBytes {
		return nil, BadAuthRequest("Workspace 文档须为 UTF-8，最多 64KB，revision 不得为负")
	}
	if _, err := s.workspaceSnapshotForUser(userID, canvasID); err != nil {
		return nil, err
	}
	if _, err := s.repo.UpdateAgentWorkspace(userID, canvasID, revision, agentsMD, fmt.Sprintf("%x", sha256.Sum256([]byte(agentsMD)))); err != nil {
		return nil, err
	}
	return s.GetAgentWorkspace(userID, canvasID)
}

func (s *Service) FreezeWorkspaceSnapshot(userID, canvasID string) (WorkspaceSnapshot, error) {
	row, err := s.workspaceSnapshotForUser(userID, canvasID)
	if err != nil {
		return WorkspaceSnapshot{}, err
	}
	return WorkspaceSnapshot{WorkspaceID: row.ID, CanvasID: row.CanvasID, Revision: row.Revision, AgentsMD: row.AgentsMD, AgentsMDHash: row.AgentsMDHash}, nil
}

func workspacePrompt(snapshot *WorkspaceSnapshot) string {
	if snapshot == nil || strings.TrimSpace(snapshot.AgentsMD) == "" {
		return ""
	}
	// Escaping document headings keeps them inside the canvas guidance block.
	encoded, _ := json.Marshal(snapshot.AgentsMD)
	return "\n\n当前画布 Workspace 规则（仅用于当前画布；与账户全局自定义系统提示词冲突时优先。正文为以下 JSON 字符串）：\n" + string(encoded) + "\n\n以上为当前画布要求；用户本次明确要求、平台权限、预算、审批和工具合同始终优先。\n"
}
