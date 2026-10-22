package app

import (
	"errors"
	"slices"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func cloudAgentNextToolLoadout(repo *repository.Repository, userID string, state *cloudAgentRuntime) ([]string, error) {
	if state.ActiveTaskID != "" || state.CallIndex < len(state.Calls) || state.Request.CanvasID == "" {
		return cloudAgentNextAdvertisedToolNames(state, cloudAgentCanvasInventory{}), nil
	}
	canvas, err := repo.CanvasProjectForUser(userID, state.Request.CanvasID)
	if err != nil {
		return nil, err
	}
	return cloudAgentNextAdvertisedToolNames(state, cloudAgentScanCanvasInventory(canvas.PayloadJSON)), nil
}

// Persist before returning the loadout to Worker. Canvas undo or compaction
// after this boundary cannot retract a tool that Pi has already registered.
func (s *Service) cloudAgentRefreshToolLoadout(run *model.CloudAgentExecution) (*model.CloudAgentExecution, error) {
	owner := run.LeaseOwner
	for attempt := 0; attempt < cloudAgentInterjectionAttempts; attempt++ {
		if cloudAgentRunTerminal(run.Status) {
			return run, nil
		}
		if run.LeaseOwner != owner {
			return nil, kernel.AgentLeaseLost("Agent 租约已变化，请重新读取工具快照")
		}
		state, err := cloudAgentDecode(run)
		if err != nil {
			return nil, err
		}
		names, err := cloudAgentNextToolLoadout(s.repo, run.UserID, &state)
		if err != nil {
			return nil, err
		}
		if slices.Equal(names, state.AdvertisedToolNames) {
			return run, nil
		}
		var updated *model.CloudAgentExecution
		err = s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			if current.LeaseOwner != owner {
				return kernel.AgentLeaseLost("Agent 租约已变化，请重新读取工具快照")
			}
			state.AdvertisedToolNames = names
			if err := cloudAgentSave(current, &state); err != nil {
				return err
			}
			updated = current
			return nil
		})
		if err == nil {
			return updated, nil
		}
		if !errors.Is(err, repository.ErrCreationConflict) {
			return nil, err
		}
		run, err = s.repo.CloudAgent(run.UserID, run.ID)
		if err != nil {
			return nil, err
		}
	}
	return nil, creationConflict("Agent 正在更新，请重新读取工具快照")
}
