package app

import (
	"encoding/json"
	"errors"
	"infinite-canvas/backend/internal/model"
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
	ID             string                     `json:"id"`
	ParentRunID    string                     `json:"parentRunId"`
	ChildRunID     string                     `json:"childRunId"`
	DisplayName    string                     `json:"displayName"`
	RoleLabel      string                     `json:"roleLabel"`
	Objective      string                     `json:"objective"`
	Depth          int                        `json:"depth"`
	Status         string                     `json:"status"`
	FailureMessage string                     `json:"failureMessage,omitempty"`
	Messages       []AgentSubagentMessageView `json:"messages"`
}

type AgentSubagentMessageView struct {
	ID        string `json:"id"`
	Direction string `json:"direction"`
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	Sequence  int64  `json:"sequence"`
}

func (s *Service) ListAgentSubagents(userID, runID string) ([]AgentSubagentView, error) {
	if _, err := s.repo.CloudAgent(userID, runID); err != nil {
		return nil, err
	}
	return agentSubagentViews(s.repo, userID, runID)
}

func agentSubagentViews(repo *repository.Repository, userID, runID string) ([]AgentSubagentView, error) {
	links, err := repo.AgentSubagentLinkByParent(userID, runID, nil)
	if err != nil {
		return nil, err
	}
	views := make([]AgentSubagentView, 0, len(links))
	messages, err := repo.AgentSubagentMessages(userID, runID)
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		child, childErr := repo.CloudAgent(userID, link.ChildRunID)
		if childErr != nil {
			return nil, childErr
		}
		view := AgentSubagentView{ID: link.ID, ParentRunID: link.ParentRunID, ChildRunID: link.ChildRunID, DisplayName: link.DisplayName, RoleLabel: link.RoleLabel, Objective: link.Objective, Depth: link.Depth, Status: child.Status, FailureMessage: child.FailureMessage, Messages: []AgentSubagentMessageView{}}
		for _, message := range messages {
			if message.LinkID != link.ID {
				continue
			}
			var payload struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal([]byte(message.PayloadJSON), &payload); err != nil {
				return nil, err
			}
			view.Messages = append(view.Messages, AgentSubagentMessageView{ID: message.ID, Direction: message.Direction, Kind: message.Kind, Text: payload.Text, Sequence: message.Sequence})
		}
		views = append(views, view)
	}
	return views, nil
}

func refreshActiveSubagents(repo *repository.Repository, run *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	state.ActiveSubagents = 0
	if !state.Request.SubagentEnabled || state.Subagent != nil {
		return nil
	}
	views, err := agentSubagentViews(repo, run.UserID, run.ID)
	if err != nil {
		return err
	}
	for _, view := range views {
		if !cloudAgentRunTerminal(view.Status) {
			state.ActiveSubagents++
		}
	}
	return nil
}

func (s *Service) AgentSubagentPolicy(userID, canvasID string) (*AgentSubagentPolicyView, error) {
	if err := s.RequireFeature(FeatureAgentSubagents); err != nil {
		return nil, err
	}
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
	if err := s.RequireFeature(FeatureAgentSubagents); err != nil {
		return nil, err
	}
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
