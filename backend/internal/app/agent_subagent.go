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

type AgentSubagentView struct {
	ID          string `json:"id"`
	ParentRunID string `json:"parentRunId"`
	ChildRunID  string `json:"childRunId"`
	DisplayName string `json:"displayName"`
	RoleLabel   string `json:"roleLabel"`
	Objective   string `json:"objective"`
	Depth       int    `json:"depth"`
	Status      string `json:"status"`
}

func (s *Service) ListAgentSubagents(userID, runID string) ([]AgentSubagentView, error) {
	if _, err := s.repo.CloudAgent(userID, runID); err != nil {
		return nil, err
	}
	links, err := s.repo.AgentSubagentLinkByParent(userID, runID, nil)
	if err != nil {
		return nil, err
	}
	views := make([]AgentSubagentView, 0, len(links))
	for _, link := range links {
		if child, childErr := s.repo.CloudAgent(userID, link.ChildRunID); childErr == nil {
			next := link.Status
			switch child.Status {
			case "completed":
				next = repository.SubagentStatusCompleted
			case "failed":
				next = repository.SubagentStatusFailed
			case "cancelled":
				next = repository.SubagentStatusCancelled
			case "running", "queued":
				if next == "" {
					next = child.Status
				}
			}
			if next != link.Status {
				_ = s.repo.UpdateAgentSubagentLinkStatus(userID, link.ID, next)
				link.Status = next
			}
		}
		views = append(views, AgentSubagentView{ID: link.ID, ParentRunID: link.ParentRunID, ChildRunID: link.ChildRunID, DisplayName: link.DisplayName, RoleLabel: link.RoleLabel, Objective: link.Objective, Depth: link.Depth, Status: link.Status})
	}
	return views, nil
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
