package app

import (
	"context"
	"encoding/json"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

type CrewApprovalView struct {
	ApprovalID   string                    `json:"approvalId"`
	SnapshotHash string                    `json:"snapshotHash"`
	Summary      string                    `json:"summary"`
	Preview      cloudAgentApprovalPreview `json:"preview"`
	Decision     string                    `json:"decision,omitempty"`
	OperationID  string                    `json:"operationId,omitempty"`
}

func (s *Service) DecideCrewApproval(userID, runID, approvalID, decision, reason string) error {
	if decision != "approve" && decision != "reject" || len(reason) > 2000 {
		return BadAuthRequest("Crew 审批决定无效")
	}
	view, err := s.GetCrewRun(userID, runID)
	if err != nil {
		return err
	}
	err = s.repo.MutateAgentCrewRun(userID, runID, func(snapshot *model.AgentCrewRunSnapshot, _ *repository.Repository) error {
		if snapshot.Run.ApprovalID != approvalID || snapshot.Run.Status != model.CrewRunWaitingApproval {
			return creationConflict("审批状态已变化")
		}
		var proposal CrewProposal
		if err := json.Unmarshal([]byte(snapshot.Run.ProposalJSON), &proposal); err != nil {
			return err
		}
		if proposal.Decision != "" {
			if proposal.Decision == decision && proposal.Reason == reason {
				return nil
			}
			return creationConflict("审批已经决定")
		}
		proposal.Decision, proposal.Reason = decision, reason
		raw, err := json.Marshal(proposal)
		if err != nil {
			return err
		}
		snapshot.Run.ProposalJSON = string(raw)
		if decision == "reject" {
			snapshot.Run.Status = model.CrewRunCancelled
		}
		return nil
	})
	if err == nil && decision == "reject" {
		return s.CancelCloudAgent(context.Background(), userID, view.CoordinatorRunID)
	}
	return err
}
