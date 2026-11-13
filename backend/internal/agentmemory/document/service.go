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

// conflict 与原 service 层冲突错误保持同一结构：HTTP 409 + 用户可读消息。
func conflict(message string) error {
	return &kernel.AppError{Status: 409, Code: 409, Message: message}
}

func isConflict(err error) bool {
	var appErr *kernel.AppError
	return errors.As(err, &appErr) && appErr.Status == 409
}

// Service 是记忆文件的文档行为归属；只依赖 repository，不感知 CloudAgent runtime。
type Service struct {
	repo *repository.Repository
}

// New 组合文档服务。
func New(repo *repository.Repository) *Service {
	return &Service{repo: repo}
}

func authenticated(userID string) error {
	if strings.TrimSpace(userID) == "" {
		return kernel.Unauthorized("请先登录")
	}
	return nil
}

// SeedEntries 在用户第一次启用记忆文件时导入旧版经验库里已批准的条目（按命中次数优先），
// 规则与 schema 迁移 v82 相同（agentmemory.AppendLessons）。
func (s *Service) SeedEntries(userID string, capacity int, now time.Time) ([]model.AgentMemoryEntry, int64, error) {
	lessons, err := s.repo.ApprovedAgentLessons(userID, 0)
	if err != nil {
		return nil, 1, err
	}
	entries, seq := agentmemory.AppendLessons(nil, 1, lessons, capacity, now)
	return entries, seq, nil
}

// EnsureDocument 返回用户的记忆文件，第一次访问时创建并导入旧版经验。
func (s *Service) EnsureDocument(userID string) (*model.AgentMemoryDocument, error) {
	if err := authenticated(userID); err != nil {
		return nil, err
	}
	doc, err := s.repo.AgentMemoryDocument(userID)
	if err == nil {
		return doc, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	now := time.Now()
	entries, nextSeq, err := s.SeedEntries(userID, DefaultCapacity, now)
	if err != nil {
		return nil, err
	}
	raw, err := EntriesJSON(entries)
	if err != nil {
		return nil, err
	}
	doc = &model.AgentMemoryDocument{
		UserID: userID, EntriesJSON: raw, NextEntrySeq: nextSeq, Revision: 1, Hash: agentmemory.Hash([]byte(raw)),
		CharCount: agentmemory.CharCount(entries), Mode: model.AgentMemoryDocumentModeAuto, Capacity: DefaultCapacity,
		CreatedAt: now, UpdatedAt: now,
	}
	var seed *model.AgentMemoryDocumentRevision
	if len(entries) > 0 {
		seed = &model.AgentMemoryDocumentRevision{
			ID: kernel.NewID(), UserID: userID, BaseRevision: 0, Revision: 1, Actor: model.AgentMemoryActorSeed,
			Status: model.AgentMemoryRevisionApplied, Summary: fmt.Sprintf("从旧版经验库导入 %d 条", len(entries)),
			OpsJSON: "[]", BeforeJSON: "[]", AfterJSON: raw, CreatedAt: now,
		}
	}
	return s.repo.CreateAgentMemoryDocument(doc, seed)
}

// ViewOf 渲染一份已有文档的视图（含待确认数量）；管理员聚合复用。
func (s *Service) ViewOf(doc *model.AgentMemoryDocument) (View, error) {
	entries, err := DocumentEntries(doc)
	if err != nil {
		return View{}, err
	}
	pending, err := s.repo.CountAgentMemoryDocumentRevisions(doc.UserID, model.AgentMemoryRevisionPending)
	if err != nil {
		return View{}, err
	}
	return View{
		Mode: ModeOf(doc.Mode), Capacity: CapacityOf(doc), CharCount: agentmemory.CharCount(entries),
		Revision: doc.Revision, Entries: entries, PendingCount: pending, UpdatedAt: doc.UpdatedAt,
	}, nil
}

// Document 返回当前用户的记忆文件，第一次访问时创建并导入旧版经验。
func (s *Service) Document(userID string) (View, error) {
	doc, err := s.EnsureDocument(userID)
	if err != nil {
		return View{}, err
	}
	return s.ViewOf(doc)
}

// UpdateSettings 只改记忆模式；关闭后不注入、不暴露写入工具，已有内容保留。
func (s *Service) UpdateSettings(userID string, req SettingsRequest) (View, error) {
	mode := strings.TrimSpace(req.Mode)
	if mode != model.AgentMemoryDocumentModeAuto && mode != model.AgentMemoryDocumentModeReview && mode != model.AgentMemoryDocumentModeOff {
		return View{}, kernel.BadAuthRequest("记忆模式只能是 auto（自动生效）、review（需我确认）或 off（关闭）")
	}
	doc, err := s.EnsureDocument(userID)
	if err != nil {
		return View{}, err
	}
	if err := s.repo.UpdateAgentMemoryDocumentSettings(userID, mode, CapacityOf(doc), time.Now()); err != nil {
		return View{}, err
	}
	return s.Document(userID)
}

// writeRevision 把 next 写成新版本并留一条已生效修订；markRestored 非空时同时标记被撤销的修订。
func (s *Service) writeRevision(doc *model.AgentMemoryDocument, before, next []model.AgentMemoryEntry, nextSeq int64, actor, summary, markRestored string, now time.Time) error {
	beforeJSON, err := EntriesJSON(before)
	if err != nil {
		return err
	}
	afterJSON, err := EntriesJSON(next)
	if err != nil {
		return err
	}
	_, err = s.repo.WriteAgentMemoryDocument(repository.AgentMemoryDocumentWrite{
		UserID: doc.UserID, ExpectedRevision: doc.Revision, EntriesJSON: afterJSON, NextEntrySeq: nextSeq,
		Hash: agentmemory.Hash([]byte(afterJSON)), CharCount: agentmemory.CharCount(next), Now: now,
		Revision: &model.AgentMemoryDocumentRevision{
			ID: kernel.NewID(), Actor: actor, Summary: kernel.TruncateRunes(summary, 480), OpsJSON: "[]",
			BeforeJSON: beforeJSON, AfterJSON: afterJSON, CreatedAt: now,
		},
		MarkRestoredID: markRestored, Keep: RevisionKeep,
	})
	if errors.Is(err, repository.ErrTaskStateConflict) {
		return conflict("记忆已经变化，请刷新后再试")
	}
	return err
}

// Update 用用户编辑后的完整条目列表替换记忆文件（按 revision 做 CAS）。
// 带已有 ID 的条目保留 ID 和来源，其余视为新增；同样经过长度、泄露和容量校验。
func (s *Service) Update(userID string, req UpdateRequest) (View, error) {
	doc, err := s.EnsureDocument(userID)
	if err != nil {
		return View{}, err
	}
	if req.Revision != doc.Revision {
		return View{}, conflict("记忆已经变化，请刷新后再保存")
	}
	if len(req.Entries) > EntriesMax {
		return View{}, kernel.BadAuthRequest(fmt.Sprintf("记忆最多 %d 条", EntriesMax))
	}
	current, err := DocumentEntries(doc)
	if err != nil {
		return View{}, err
	}
	byID := make(map[string]model.AgentMemoryEntry, len(current))
	for _, entry := range current {
		byID[entry.ID] = entry
	}
	now := time.Now()
	nextSeq := NextSeq(doc, current)
	next := make([]model.AgentMemoryEntry, 0, len(req.Entries))
	seen := map[string]bool{}
	for index, input := range req.Entries {
		label := fmt.Sprintf("第 %d 条", index+1)
		name, err := section(input.Section, label+"的分区")
		if err != nil {
			return View{}, err
		}
		text, err := agentmemory.ValidateText(input.Text, label)
		if err != nil {
			return View{}, err
		}
		id := strings.TrimSpace(input.ID)
		if old, ok := byID[id]; ok && !seen[id] {
			if old.Text != text || old.Section != name {
				old.Text, old.Section, old.UpdatedAt = text, name, now
			}
			seen[id] = true
			next = append(next, old)
			continue
		}
		id = agentmemory.EntryID(name, nextSeq)
		nextSeq++
		seen[id] = true
		next = append(next, model.AgentMemoryEntry{ID: id, Section: name, Text: text, Source: model.AgentMemoryActorUser, CreatedAt: now, UpdatedAt: now})
	}
	capacity := CapacityOf(doc)
	if used, after := agentmemory.CharCount(current), agentmemory.CharCount(next); after > capacity && after > used {
		return View{}, kernel.BadAuthRequest(fmt.Sprintf("记忆最多 %d 字，保存后为 %d 字；请删减后再保存", capacity, after))
	}
	changes := Diff(current, next)
	if len(changes) == 0 {
		return s.ViewOf(doc)
	}
	if err := s.writeRevision(doc, current, next, nextSeq, model.AgentMemoryActorUser, "手动编辑："+Summary(changes, ""), "", now); err != nil {
		return View{}, err
	}
	return s.Document(userID)
}

// CapacityDetailFor 在容量超限时把当前全部条目交还给 Agent，它才能在下一次调用里合并。
func CapacityDetailFor(entries []model.AgentMemoryEntry, capErr *CapacityError) *CapacityDetail {
	items := make([]EntrySummary, 0, len(entries))
	for _, entry := range entries {
		items = append(items, EntrySummary{ID: entry.ID, Section: entry.Section, Text: entry.Text})
	}
	return &CapacityDetail{
		Usage: fmt.Sprintf("%d/%d", capErr.Used, capErr.Capacity), OverBy: capErr.After - capErr.Capacity,
		Entries:  items,
		Guidance: "在同一次 memory_update 里用 replace 合并相近条目、remove 过时条目，再 add 新内容；容量只按整批结果检查",
	}
}

// AgentWrite 执行 Agent 的一次记忆修改。模式以文件当前设置为准（用户可能在运行中改过）：
// auto 直接生效并留可撤销修订，review 只生成待确认修订，off 拒绝。同一 callID 重放时返回原结果。
func (s *Service) AgentWrite(userID, runID, callID string, ops []Operation, reason string) (WriteOutcome, *CapacityDetail, error) {
	if strings.TrimSpace(callID) != "" {
		if prior, err := s.repo.AgentMemoryDocumentRevisionByCall(userID, runID, callID); err == nil {
			return outcomeFromRevision(prior, DefaultCapacity), nil, nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return WriteOutcome{}, nil, err
		}
	}
	for attempt := 0; attempt < WriteAttempts; attempt++ {
		doc, err := s.EnsureDocument(userID)
		if err != nil {
			return WriteOutcome{}, nil, err
		}
		mode := ModeOf(doc.Mode)
		if mode == model.AgentMemoryDocumentModeOff {
			return WriteOutcome{}, nil, kernel.BadAuthRequest("用户已关闭长期记忆，不能修改；不要再尝试记录")
		}
		entries, err := DocumentEntries(doc)
		if err != nil {
			return WriteOutcome{}, nil, err
		}
		capacity := CapacityOf(doc)
		now := time.Now()
		edit, err := ApplyOperations(entries, NextSeq(doc, entries), ops, capacity, now, model.AgentMemoryActorAgent, runID)
		var capErr *CapacityError
		if errors.As(err, &capErr) {
			return WriteOutcome{}, CapacityDetailFor(entries, capErr), kernel.BadAuthRequest(capErr.Error())
		}
		if err != nil {
			return WriteOutcome{}, nil, err
		}
		outcome := WriteOutcome{Changes: edit.Changes, Duplicates: edit.Duplicates, Capacity: capacity, CharCount: agentmemory.CharCount(entries)}
		if len(edit.Changes) == 0 {
			outcome.Status = OutcomeUnchanged
			return outcome, nil, nil
		}
		before, err := EntriesJSON(entries)
		if err != nil {
			return WriteOutcome{}, nil, err
		}
		after, err := EntriesJSON(edit.Entries)
		if err != nil {
			return WriteOutcome{}, nil, err
		}
		opsJSON, err := json.Marshal(ops)
		if err != nil {
			return WriteOutcome{}, nil, err
		}
		opsRaw := string(opsJSON)
		outcome.Summary = Summary(edit.Changes, reason)
		revision := &model.AgentMemoryDocumentRevision{
			ID: kernel.NewID(), UserID: userID, Actor: model.AgentMemoryActorAgent, RunID: runID, CallID: strings.TrimSpace(callID),
			Summary: outcome.Summary, OpsJSON: opsRaw, BeforeJSON: before, AfterJSON: after, CreatedAt: now,
		}
		outcome.RevisionID = revision.ID
		if mode == model.AgentMemoryDocumentModeReview {
			pending, err := s.repo.CountAgentMemoryDocumentRevisions(userID, model.AgentMemoryRevisionPending)
			if err != nil {
				return WriteOutcome{}, nil, err
			}
			if pending >= PendingMax {
				return WriteOutcome{}, nil, kernel.BadAuthRequest(fmt.Sprintf("已有 %d 条记忆修改等待用户确认，先不要再提交新的修改", pending))
			}
			revision.BaseRevision, revision.Revision, revision.Status = doc.Revision, doc.Revision, model.AgentMemoryRevisionPending
			if err := s.repo.CreateAgentMemoryDocumentRevision(revision); err != nil {
				return WriteOutcome{}, nil, err
			}
			outcome.Status = OutcomePending
			return outcome, nil, nil
		}
		_, err = s.repo.WriteAgentMemoryDocument(repository.AgentMemoryDocumentWrite{
			UserID: userID, ExpectedRevision: doc.Revision, EntriesJSON: after, NextEntrySeq: edit.NextSeq,
			Hash: agentmemory.Hash([]byte(after)), CharCount: agentmemory.CharCount(edit.Entries), Now: now,
			Revision: revision, Keep: RevisionKeep,
		})
		if errors.Is(err, repository.ErrTaskStateConflict) {
			continue
		}
		if err != nil {
			return WriteOutcome{}, nil, err
		}
		outcome.Status = OutcomeApplied
		outcome.CharCount = agentmemory.CharCount(edit.Entries)
		return outcome, nil, nil
	}
	return WriteOutcome{}, nil, conflict("记忆正被同时修改，请稍后再试")
}
