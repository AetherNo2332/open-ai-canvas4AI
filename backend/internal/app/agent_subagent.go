package app

import (
	"errors"
	"infinite-canvas/backend/internal/repository"
	"strings"
)

type AgentSubagentPolicyView struct {
	CanvasID string `json:"canvasId"`
	Enabled  bool   `json:"enabled"`
	Revision int64  `json:"revision"`
}

type AgentSubagentPolicyRequest struct {
	CanvasID         string `json:"canvasId"`
	Enabled          bool   `json:"enabled"`
	ExpectedRevision int64  `json:"expectedRevision"`
}

func (s *Service) AgentSubagentPolicy(userID, canvasID string) (*AgentSubagentPolicyView, error) {
	canvasID = strings.TrimSpace(canvasID)
	if canvasID == "" {
		return nil, repository.ErrTaskStateConflict
	}
	if _, err := s.repo.CanvasProjectForUser(userID, canvasID); err != nil {
		return nil, err
	}
	row, err := s.repo.AgentSubagentPolicy(userID, canvasID)
	if err != nil {
		return nil, err
	}
	return &AgentSubagentPolicyView{CanvasID: row.CanvasID, Enabled: row.Enabled, Revision: row.Revision}, nil
}

func (s *Service) UpdateAgentSubagentPolicy(userID string, req AgentSubagentPolicyRequest) (*AgentSubagentPolicyView, error) {
	req.CanvasID = strings.TrimSpace(req.CanvasID)
	if req.CanvasID == "" {
		return nil, repository.ErrTaskStateConflict
	}
	if _, err := s.repo.CanvasProjectForUser(userID, req.CanvasID); err != nil {
		return nil, err
	}
	row, err := s.repo.SaveAgentSubagentPolicy(userID, req.CanvasID, req.Enabled, req.ExpectedRevision)
	if err != nil {
		if errors.Is(err, repository.ErrTaskStateConflict) {
			return nil, creationConflict("子代理授权版本已变化，请刷新后重试")
		}
		return nil, err
	}
	return &AgentSubagentPolicyView{CanvasID: row.CanvasID, Enabled: row.Enabled, Revision: row.Revision}, nil
}
