package app

import (
	"encoding/json"
	"errors"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// Lease rejection must first leave the normal executor as an error: its
// MutateCloudAgent transaction rolls back document, asset and billing effects.
// Only afterwards may a new CAS checkpoint acknowledge a business rejection.
func (s *Service) executeCloudAgentToolCall(run *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	if run == nil || state == nil || state.CallIndex < 0 || state.CallIndex >= len(state.Calls) || !cloudAgentWrite(state.Calls[state.CallIndex].Function.Name) {
		return s.executeCloudAgentToolCallAttempt(run, state)
	}
	baseline := *state
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	call := state.Calls[state.CallIndex]
	err = s.executeCloudAgentToolCallAttempt(run, state)
	var lease *repository.CanvasEditLeaseError
	if !errors.As(err, &lease) {
		return err
	}
	// The failed attempt may have mutated in-memory maps before hitting the
	// final document guard. Restore just this call's input, including transient
	// bookkeeping omitted by JSON, rather than persisting its speculative state.
	restored := cloudAgentRuntime{RuntimeRunID: baseline.RuntimeRunID, StepLimits: baseline.StepLimits, EventSeqBase: baseline.EventSeqBase, readCacheExecution: baseline.readCacheExecution}
	if decodeErr := json.Unmarshal(encoded, &restored); decodeErr != nil {
		return decodeErr
	}
	restored.Request.subagent = baseline.Request.subagent
	// Use the original revision. If another call committed after rollback, the
	// existing CAS reports a conflict and the caller must reread; never overwrite
	// that call's observations, receipts, or subagent state with our snapshot.
	err = s.persistCloudAgentLeaseRejection(run, &restored, call, lease)
	if err == nil {
		*state = restored
	}
	return err
}

// Called only after the failed tool transaction returned (and rolled back).
func (s *Service) persistCloudAgentLeaseRejection(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, lease *repository.CanvasEditLeaseError) error {
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		cloudAgentRecordToolResult(current, state, call, nil, lease)
		return cloudAgentSave(current, state)
	})
}
