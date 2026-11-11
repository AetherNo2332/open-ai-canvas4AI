package document

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"yingce/backend/internal/agentmemory"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/gorm"
)

func outcomeFromRevision(revision *model.AgentMemoryDocumentRevision, capacity int) WriteOutcome {
	after := EntriesFromJSON(revision.AfterJSON)
	status := OutcomeApplied
	if revision.Status == model.AgentMemoryRevisionPending {
		status = OutcomePending
	}
	return WriteOutcome{
		Status: status, RevisionID: revision.ID, Summary: revision.Summary,
		Changes:   Diff(EntriesFromJSON(revision.BeforeJSON), after),
		CharCount: agentmemory.CharCount(after), Capacity: capacity,
	}
}

func revisionView(revision model.AgentMemoryDocumentRevision) RevisionView {
	return RevisionView{
		ID: revision.ID, Actor: revision.Actor, RunID: revision.RunID, Status: revision.Status, Summary: revision.Summary,
		BaseRevision: revision.BaseRevision, Revision: revision.Revision,
		Changes:   Diff(EntriesFromJSON(revision.BeforeJSON), EntriesFromJSON(revision.AfterJSON)),
		CanUndo:   revision.Status == model.AgentMemoryRevisionApplied && revision.RestoredAt == nil && revision.Actor != model.AgentMemoryActorRestore,
		CreatedAt: revision.CreatedAt, DecidedAt: revision.DecidedAt, RestoredAt: revision.RestoredAt,
	}
}

// Revisions 列出当前用户的修订记录。
func (s *Service) Revisions(userID, status string, limit int) ([]RevisionView, error) {
	if err := authenticated(userID); err != nil {
		return nil, err
	}
	status = strings.TrimSpace(status)
	if status != "" && status != model.AgentMemoryRevisionApplied && status != model.AgentMemoryRevisionPending && status != model.AgentMemoryRevisionRejected {
		return nil, kernel.BadAuthRequest("修订状态无效")
	}
	rows, err := s.repo.AgentMemoryDocumentRevisions(userID, status, limit)
	if err != nil {
		return nil, err
	}
	views := make([]RevisionView, 0, len(rows))
	for _, row := range rows {
		views = append(views, revisionView(row))
	}
	return views, nil
}

func (s *Service) ownedRevision(userID, revisionID string) (*model.AgentMemoryDocumentRevision, error) {
	if err := authenticated(userID); err != nil {
		return nil, err
	}
	revision, err := s.repo.AgentMemoryDocumentRevision(userID, revisionID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, kernel.NotFound("记忆修改记录不存在")
	}
	return revision, err
}

// Undo 撤销一条已生效的修改，撤销本身也留一条修订。
func (s *Service) Undo(userID, revisionID string) (View, error) {
	revision, err := s.ownedRevision(userID, revisionID)
	if err != nil {
		return View{}, err
	}
	if revision.Status != model.AgentMemoryRevisionApplied || revision.RestoredAt != nil || revision.Actor == model.AgentMemoryActorRestore {
		return View{}, kernel.BadAuthRequest("这条修改不能撤销（已撤销、未生效或本身就是撤销）")
	}
	doc, err := s.EnsureDocument(userID)
	if err != nil {
		return View{}, err
	}
	current, err := DocumentEntries(doc)
	if err != nil {
		return View{}, err
	}
	now := time.Now()
	next, err := Inverse(current, EntriesFromJSON(revision.BeforeJSON), EntriesFromJSON(revision.AfterJSON), now)
	if err != nil {
		return View{}, conflict("这条修改之后相关记忆又变化了，无法自动撤销；请在记忆列表里手动调整")
	}
	capacity := CapacityOf(doc)
	if used, after := agentmemory.CharCount(current), agentmemory.CharCount(next); after > capacity && after > used {
		return View{}, kernel.BadAuthRequest("撤销后会超过记忆容量，请先删除一些条目再撤销")
	}
	if err := s.writeRevision(doc, current, next, NextSeq(doc, current), model.AgentMemoryActorRestore, "撤销："+revision.Summary, revision.ID, now); err != nil {
		return View{}, err
	}
	return s.Document(userID)
}

// Decide 采纳或拒绝需确认模式下的待定修改。采纳时在当前记忆上重放原操作并重新校验。
func (s *Service) Decide(userID, revisionID string, req DecisionRequest) (View, error) {
	revision, err := s.ownedRevision(userID, revisionID)
	if err != nil {
		return View{}, err
	}
	if revision.Status != model.AgentMemoryRevisionPending {
		return View{}, kernel.BadAuthRequest("这条修改已经处理过")
	}
	switch strings.TrimSpace(req.Decision) {
	case "reject":
		if err := s.repo.RejectAgentMemoryDocumentRevision(userID, revision.ID, time.Now()); err != nil {
			return View{}, decisionConflict(err)
		}
		return s.Document(userID)
	case "approve":
	default:
		return View{}, kernel.BadAuthRequest("decision 只能是 approve 或 reject")
	}
	var ops []Operation
	if err := json.Unmarshal([]byte(revision.OpsJSON), &ops); err != nil || len(ops) == 0 {
		return View{}, kernel.BadAuthRequest("这条修改缺少可执行的操作，只能拒绝")
	}
	for attempt := 0; attempt < WriteAttempts; attempt++ {
		doc, err := s.EnsureDocument(userID)
		if err != nil {
			return View{}, err
		}
		entries, err := DocumentEntries(doc)
		if err != nil {
			return View{}, err
		}
		now := time.Now()
		edit, err := ApplyOperations(entries, NextSeq(doc, entries), ops, CapacityOf(doc), now, model.AgentMemoryActorAgent, revision.RunID)
		var capErr *CapacityError
		if errors.As(err, &capErr) {
			return View{}, kernel.BadAuthRequest(fmt.Sprintf("采纳后记忆会有 %d 字，超过上限 %d 字；请先删除一些条目再采纳", capErr.After, capErr.Capacity))
		}
		if err != nil {
			return View{}, conflict("这条修改依赖的记忆已经变化，无法采纳；可以拒绝后让 Agent 重新整理")
		}
		if len(edit.Changes) == 0 {
			if err := s.repo.RejectAgentMemoryDocumentRevision(userID, revision.ID, now); err != nil {
				return View{}, decisionConflict(err)
			}
			return s.Document(userID)
		}
		before, err := EntriesJSON(entries)
		if err != nil {
			return View{}, err
		}
		after, err := EntriesJSON(edit.Entries)
		if err != nil {
			return View{}, err
		}
		_, err = s.repo.WriteAgentMemoryDocument(repository.AgentMemoryDocumentWrite{
			UserID: userID, ExpectedRevision: doc.Revision, EntriesJSON: after, NextEntrySeq: edit.NextSeq,
			Hash: agentmemory.Hash([]byte(after)), CharCount: agentmemory.CharCount(edit.Entries), Now: now,
			Revision:       &model.AgentMemoryDocumentRevision{Summary: revision.Summary, BeforeJSON: before, AfterJSON: after},
			ApplyPendingID: revision.ID, Keep: RevisionKeep,
		})
		if errors.Is(err, repository.ErrTaskStateConflict) {
			continue
		}
		if err != nil {
			return View{}, err
		}
		return s.Document(userID)
	}
	return View{}, conflict("记忆正被同时修改，请稍后再试")
}

func decisionConflict(err error) error {
	if errors.Is(err, repository.ErrTaskStateConflict) {
		return conflict("这条修改已经被处理，请刷新")
	}
	return err
}
