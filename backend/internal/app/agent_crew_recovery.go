package app

import (
	"context"
	"encoding/json"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"time"
)

type CrewBudgetUsage struct {
	Credits              float64 `json:"credits"`
	Steps                int     `json:"steps"`
	GenerationTasks      int     `json:"generationTasks"`
	VideoSeconds         int     `json:"videoSeconds"`
	ActiveMembers        int     `json:"activeMembers"`
	MaxConcurrentMembers int     `json:"maxConcurrentMembers"`
}

func (s *Service) CrewBudgetUsage(userID, runID string) (CrewBudgetUsage, error) {
	var usage CrewBudgetUsage
	run, err := s.repo.AgentCrewRunInternal(runID)
	if err != nil {
		return usage, err
	}
	if run.UserID != userID {
		return usage, NotFound("Crew Run 不存在")
	}
	snapshot, err := s.repo.AgentCrewRunSnapshot(userID, runID)
	if err != nil {
		return usage, err
	}
	var budget CrewRunBudget
	_ = json.Unmarshal([]byte(snapshot.Run.BudgetJSON), &budget)
	usage.MaxConcurrentMembers = budget.MaxConcurrentMembers
	for _, member := range snapshot.Members {
		if member.Status == model.MemberRunQueued || member.Status == model.MemberRunRunning {
			usage.ActiveMembers++
		}
		execution, readErr := s.repo.CloudAgent(userID, member.AgentRunID)
		if readErr != nil {
			continue
		}
		state, decodeErr := cloudAgentDecode(execution)
		if decodeErr != nil {
			continue
		}
		usage.Steps += state.Step
		usage.GenerationTasks += state.Generations
		usage.VideoSeconds += state.VideoSeconds
	}
	return usage, nil
}

func (s *Service) CancelCrewRun(ctx context.Context, userID, runID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	run, err := s.repo.AgentCrewRunInternal(runID)
	if err != nil {
		return err
	}
	if run.UserID != userID {
		return NotFound("Crew Run 不存在")
	}
	if err = s.repo.MutateAgentCrewRun(userID, runID, func(snapshot *model.AgentCrewRunSnapshot, _ *repository.Repository) error {
		if snapshot.Run.Status == model.CrewRunCancelled || snapshot.Run.Status == model.CrewRunCompleted {
			return nil
		}
		snapshot.Run.Status = model.CrewRunCancelled
		for i := range snapshot.Members {
			if snapshot.Members[i].Status != model.MemberRunCompleted && snapshot.Members[i].Status != model.MemberRunFailed {
				snapshot.Members[i].Status = model.MemberRunCancelled
				snapshot.Members[i].ErrorCode = "crew_cancelled"
			}
		}
		return nil
	}); err != nil {
		return err
	}
	snapshot, err := s.repo.AgentCrewRunSnapshot(userID, runID)
	if err != nil {
		return err
	}
	for _, member := range snapshot.Members {
		execution, readErr := s.repo.CloudAgent(userID, member.AgentRunID)
		if readErr != nil || cloudAgentRunTerminal(execution.Status) {
			continue
		}
		if cancelErr := s.CancelCloudAgent(ctx, userID, execution.ID); cancelErr != nil && !isTerminalCancelError(cancelErr) {
			return cancelErr
		}
	}
	return nil
}

func isTerminalCancelError(err error) bool {
	return err == nil || err == repository.ErrCreationConflict
}

func (s *Service) RecoverCrewRuns(now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	runs, err := s.repo.AllAgentCrewRuns()
	if err != nil {
		return err
	}
	for _, run := range runs {
		snapshot, readErr := s.repo.AgentCrewRunSnapshot(run.UserID, run.ID)
		if readErr != nil {
			return readErr
		}
		for _, member := range snapshot.Members {
			execution, readErr := s.repo.CloudAgent(run.UserID, member.AgentRunID)
			if readErr != nil || execution.LeaseExpiresAt == nil || execution.LeaseExpiresAt.After(now) || cloudAgentRunTerminal(execution.Status) {
				continue
			}
			if failErr := s.terminateCloudAgent(execution, "crew_member_lease_expired"); failErr != nil && !isTerminalCancelError(failErr) {
				return failErr
			}
		}
	}
	return nil
}
