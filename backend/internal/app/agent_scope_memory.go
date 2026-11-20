package app

import (
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/agentmemory"
	"yingce/backend/internal/agentmemory/document"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func (s *Service) loadAgentScopeMemory(scope AgentContextScope) (model.AgentScopeMemory, []model.AgentMemoryEntry, error) {
	row, err := s.repo.AgentScopeMemory(scope.memoryKey())
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, nil, nil
	}
	if err != nil {
		return row, nil, err
	}
	entries, err := document.DocumentEntries(&model.AgentMemoryDocument{EntriesJSON: row.EntriesJSON})
	return row, entries, err
}

func (s *Service) agentScopeMemorySnapshot(scope AgentContextScope) (cloudAgentMemorySnapshot, error) {
	row, entries, err := s.loadAgentScopeMemory(scope)
	if err != nil {
		return cloudAgentMemorySnapshot{}, err
	}
	mode := model.AgentMemoryDocumentModeAuto
	return cloudAgentMemorySnapshot{
		Source: "scope", Mode: mode, Revision: row.Revision,
		Text: agentMemorySnapshotText(mode, document.DefaultCapacity, entries, false),
	}, nil
}

func (s *Service) agentWriteScopeMemory(scope AgentContextScope, runID, callID string, ops []document.Operation, reason string) (document.WriteOutcome, *document.CapacityDetail, error) {
	if runID == "" || callID == "" {
		return document.WriteOutcome{}, nil, BadAuthRequest("缺少记忆修改调用标识")
	}
	for attempt := 0; attempt < document.WriteAttempts; attempt++ {
		outcome, detail, err := s.tryAgentScopeMemoryWrite(scope, runID, callID, ops, reason)
		if !errors.Is(err, repository.ErrTaskStateConflict) {
			return outcome, detail, err
		}
	}
	return document.WriteOutcome{}, nil, creationConflict("业务记忆已变化，请重试")
}

func (s *Service) tryAgentScopeMemoryWrite(scope AgentContextScope, runID, callID string, ops []document.Operation, reason string) (document.WriteOutcome, *document.CapacityDetail, error) {
	prior, err := s.repo.AgentScopeMemoryWrite(scope.memoryKey(), runID, callID)
	if err == nil {
		var outcome document.WriteOutcome
		err = json.Unmarshal([]byte(prior.OutcomeJSON), &outcome)
		return outcome, nil, err
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return document.WriteOutcome{}, nil, err
	}
	row, entries, err := s.loadAgentScopeMemory(scope)
	if err != nil {
		return document.WriteOutcome{}, nil, err
	}
	edit, err := document.ApplyOperations(entries, agentmemory.NextSeq(row.NextEntrySeq, entries), ops, document.DefaultCapacity, time.Now(), model.AgentMemoryActorAgent, runID)
	if err != nil {
		var capErr *document.CapacityError
		if errors.As(err, &capErr) {
			return document.WriteOutcome{}, document.CapacityDetailFor(entries, capErr), err
		}
		return document.WriteOutcome{}, nil, err
	}
	outcome := document.WriteOutcome{
		Status: document.OutcomeUnchanged, CharCount: agentmemory.CharCount(edit.Entries),
		Capacity: document.DefaultCapacity, Duplicates: edit.Duplicates, Changes: edit.Changes,
	}
	if len(edit.Changes) > 0 {
		outcome.Status = document.OutcomeApplied
		outcome.Summary = document.Summary(edit.Changes, reason)
	}
	row.EntriesJSON, err = document.EntriesJSON(edit.Entries)
	if err != nil {
		return document.WriteOutcome{}, nil, err
	}
	row.NextEntrySeq = edit.NextSeq
	row.Revision++
	raw, err := json.Marshal(outcome)
	if err != nil {
		return document.WriteOutcome{}, nil, err
	}
	write := model.AgentScopeMemoryWrite{AgentScopeMemoryKey: scope.memoryKey(), RunID: runID, CallID: callID, OutcomeJSON: string(raw)}
	return outcome, nil, s.repo.SaveAgentScopeMemory(row, write)
}
