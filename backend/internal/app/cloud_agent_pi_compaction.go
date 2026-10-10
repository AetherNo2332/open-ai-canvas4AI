package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"infinite-canvas/backend/internal/agentcontext"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

const cloudAgentPiCompactionProtocol = "canvas-pi-compaction/v1"

type PiContextCompactionStart struct {
	SessionRevision int64           `json:"sessionRevision"`
	ActiveLeafID    string          `json:"activeLeafId"`
	Reason          string          `json:"reason"`
	WillRetry       bool            `json:"willRetry"`
	TokensBefore    int             `json:"tokensBefore"`
	Preparation     json.RawMessage `json:"preparation,omitempty"`
}

type PiContextCompactionView struct {
	OperationID       string          `json:"operationId"`
	Status            string          `json:"status"`
	TaskID            string          `json:"taskId,omitempty"`
	Summary           string          `json:"summary,omitempty"`
	FirstKeptEntryID  string          `json:"firstKeptEntryId,omitempty"`
	TokensBefore      int             `json:"tokensBefore,omitempty"`
	SourceDigest      string          `json:"sourceDigest,omitempty"`
	CheckpointDigest  string          `json:"checkpointDigest,omitempty"`
	Mode              string          `json:"mode,omitempty"`
	Reason            string          `json:"reason,omitempty"`
	Fallback          bool            `json:"fallback,omitempty"`
	Details           map[string]any  `json:"details,omitempty"`
	SessionRevision   int64           `json:"sessionRevision,omitempty"`
	NativePreparation json.RawMessage `json:"nativePreparation,omitempty"`
	SummaryMaxTokens  int             `json:"summaryMaxTokens,omitempty"`
	Usage             json.RawMessage `json:"usage,omitempty"`
}

type piCompactionEntry struct {
	Type             string          `json:"type"`
	ID               string          `json:"id"`
	ParentID         *string         `json:"parentId"`
	Summary          string          `json:"summary"`
	FirstKeptEntryID string          `json:"firstKeptEntryId"`
	TokensBefore     int             `json:"tokensBefore"`
	Details          json.RawMessage `json:"details"`
	Usage            json.RawMessage `json:"usage,omitempty"`
}

type piCompactionDetails struct {
	ProtocolVersion  string `json:"protocolVersion"`
	OperationID      string `json:"operationId"`
	SourceDigest     string `json:"sourceDigest"`
	CheckpointDigest string `json:"checkpointDigest"`
}

func (s *Service) PiBeginContextCompaction(userID, runID, owner string, input PiContextCompactionStart) (*PiContextCompactionView, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		return nil, err
	}
	if input.SessionRevision < 1 || input.TokensBefore < 0 || input.TokensBefore > 10_000_000 {
		return nil, BadAuthRequest("Pi 压缩会话版本或 token 读数无效")
	}
	if input.Reason != "manual" && input.Reason != "threshold" && input.Reason != "overflow" {
		return nil, BadAuthRequest("Pi 压缩原因无效")
	}
	session, entries, err := s.repo.CloudAgentPiSession(userID, run.ConversationID)
	if err != nil {
		return nil, err
	}
	branch, err := cloudAgentPiActiveBranch(entries, input.ActiveLeafID)
	if err != nil {
		return nil, BadAuthRequest("Pi 压缩活动分支无效")
	}
	source, err := cloudAgentPiCompactionSourceForBranch(branch, input.ActiveLeafID)
	if err != nil {
		return nil, BadAuthRequest("Pi 压缩上下文无效")
	}
	var native *cloudAgentPiNativeCompaction
	if len(input.Preparation) > 0 {
		native, err = cloudAgentPrepareNativeCompaction(input.Preparation, input.TokensBefore, &source, branch)
		if err != nil {
			return nil, BadAuthRequest(err.Error())
		}
	}
	operationID := cloudAgentPiCompactionOperationID(session.ID, runID, input, source.SourceDigest)
	if existing, ok := cloudAgentPiCompactionEntryForOperation(entries, operationID); ok {
		return piCompactionViewFromEntry(existing, session.Revision), nil
	}
	if session.ActiveRunID != runID || session.Revision != input.SessionRevision || session.ActiveLeafID != input.ActiveLeafID {
		return nil, kernel.Forbidden("Pi 压缩源会话已变化")
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return nil, err
	}
	if state.ContextCompaction != nil {
		if state.ContextCompaction.PiOperationID != operationID {
			return nil, kernel.Forbidden("另一个上下文压缩操作仍在进行")
		}
		return s.PiContextCompaction(userID, runID, owner, operationID)
	}
	if state.ContextCompactionCount >= cloudAgentMaxCompactionsPerRun {
		return nil, kernel.Forbidden("本轮上下文压缩次数已达到上限")
	}
	for _, message := range source.Messages {
		if cloudAgentIsDeliveredInterjection(message, &state) {
			message[cloudAgentContextSourceKey] = "user_interjection"
		}
	}
	state.Canonical.Messages = source.Messages
	state.ContextCompaction = &cloudAgentContextCompaction{
		Status: "requested", SourceBytes: source.SourceBytes, TurnCount: source.CompactedTurnCount, Resume: true,
		PiOperationID: operationID, PiSourceDigest: source.SourceDigest,
		PiSessionRevision: session.Revision, PiSourceLeafID: session.ActiveLeafID,
		PiFirstKeptEntryID: source.FirstKeptEntryID, PiFirstKeptIndex: source.FirstKeptIndex,
		PiReason: input.Reason, PiWillRetry: input.WillRetry, PiTokensBefore: input.TokensBefore,
		PiNative: native,
	}
	state.event(runID, "context_compaction_requested", map[string]any{
		"kind": "semantic_compaction", "reason": input.Reason, "sourceBytes": source.SourceBytes,
		"turns": source.CompactedTurnCount, "operationId": operationID, "sourceDigest": source.SourceDigest,
		"piReason": input.Reason, "willRetry": input.WillRetry,
	})
	if native != nil {
		budget := s.cloudAgentContextBudgetForRequest(state.Request)
		native.MaxTokens = budget.SummaryOutputTokens
		if err := s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			return cloudAgentSave(current, &state)
		}); err != nil {
			return nil, err
		}
	} else if err := s.enqueueCloudAgentContextCompaction(run, &state); err != nil {
		return nil, err
	}
	return s.PiContextCompaction(userID, runID, owner, operationID)
}

func (s *Service) PiContextCompaction(userID, runID, owner, operationID string) (*PiContextCompactionView, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		return nil, err
	}
	if cloudAgentRunTerminal(run.Status) {
		return &PiContextCompactionView{OperationID: operationID, Status: run.Status}, nil
	}
	session, entries, err := s.repo.CloudAgentPiSession(userID, run.ConversationID)
	if err != nil {
		return nil, err
	}
	if entry, ok := cloudAgentPiCompactionEntryForOperation(entries, operationID); ok {
		return piCompactionViewFromEntry(entry, session.Revision), nil
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return nil, err
	}
	compaction := state.ContextCompaction
	if compaction == nil || compaction.PiOperationID != operationID {
		return nil, kernel.NotFound("Pi 压缩操作不存在")
	}
	if compaction.PiNative != nil {
		return cloudAgentNativeCompactionView(compaction)
	}
	if compaction.PiTaskID == "" {
		return nil, kernel.NotFound("Pi 压缩任务不存在")
	}
	task, err := s.repo.TaskForUser(userID, compaction.PiTaskID)
	if err != nil {
		return nil, err
	}
	if task.Operation != cloudAgentContextCompactionOperation {
		return nil, kernel.Forbidden("Pi 压缩任务类型无效")
	}
	view := &PiContextCompactionView{
		OperationID: operationID, TaskID: task.ID, Status: string(task.Status),
		FirstKeptEntryID: compaction.PiFirstKeptEntryID, TokensBefore: compaction.PiTokensBefore,
		SourceDigest: compaction.PiSourceDigest, SessionRevision: compaction.PiSessionRevision,
	}
	if task.Status == model.TaskStatusQueued || task.Status == model.TaskStatusRunning {
		return view, nil
	}
	if cloudAgentStepTimedOut(task) {
		view.Status = "failed"
		return view, s.failCloudAgentStepTimeout(run, &state, task)
	}
	checkpoint, mode, reason := cloudAgentContextCompactionResult(&state, task)
	framed, err := agentcontext.Frame(checkpoint)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(framed))
	view.Status = "succeeded"
	view.Summary = framed
	view.CheckpointDigest = hex.EncodeToString(digest[:])
	view.Mode = mode
	view.Reason = reason
	view.Fallback = mode != "model"
	view.Details = map[string]any{
		"protocolVersion": cloudAgentPiCompactionProtocol, "operationId": operationID,
		"sourceDigest": compaction.PiSourceDigest, "checkpointDigest": view.CheckpointDigest,
	}
	return view, nil
}

func (s *Service) PiCommitContextCompaction(userID, runID, owner, operationID string, input PiContextCompactionCommit) (int64, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		return 0, err
	}
	if cloudAgentRunTerminal(run.Status) {
		return 0, kernel.Forbidden("Agent 运行已结束")
	}
	if input.SessionRevision < 1 || len(input.Entry) == 0 || len(input.Entry) > 1<<20 || !json.Valid(input.Entry) {
		return 0, BadAuthRequest("Pi 压缩检查点无效")
	}
	var entry piCompactionEntry
	if err := json.Unmarshal(input.Entry, &entry); err != nil || entry.Type != "compaction" || strings.TrimSpace(entry.ID) == "" || len(entry.ID) > 160 {
		return 0, BadAuthRequest("Pi 压缩条目格式无效")
	}
	if entry.ParentID == nil {
		return 0, BadAuthRequest("Pi 压缩条目缺少父节点")
	}
	var details piCompactionDetails
	if err := json.Unmarshal(entry.Details, &details); err != nil || details.ProtocolVersion != cloudAgentPiCompactionProtocol || details.OperationID != operationID {
		return 0, BadAuthRequest("Pi 压缩操作身份无效")
	}
	if details.CheckpointDigest != cloudAgentTextDigest(entry.Summary) {
		return 0, BadAuthRequest("Pi 压缩检查点摘要校验失败")
	}
	if _, err := agentcontext.ParseFrame(entry.Summary); err != nil {
		return 0, BadAuthRequest("Pi 压缩检查点不符合服务端合同")
	}
	session, entries, err := s.repo.CloudAgentPiSession(userID, run.ConversationID)
	if err != nil {
		return 0, err
	}
	if existing, ok := cloudAgentPiCompactionEntryForOperation(entries, operationID); ok {
		var oldDetails piCompactionDetails
		_ = json.Unmarshal(existing.Details, &oldDetails)
		parentID := ""
		if existing.ParentID != nil {
			parentID = *existing.ParentID
		}
		if existing.ID != entry.ID || parentID != *entry.ParentID || existing.Summary != entry.Summary ||
			existing.FirstKeptEntryID != entry.FirstKeptEntryID || existing.TokensBefore != entry.TokensBefore || oldDetails != details || !cloudAgentNativeUsageMatches(existing.Usage, entry.Usage) {
			return 0, kernel.Forbidden("Pi 压缩操作重试内容不一致")
		}
		return session.Revision, nil
	}
	var updatedRevision int64
	err = s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		compaction := state.ContextCompaction
		if compaction == nil || compaction.PiOperationID != operationID {
			return kernel.Forbidden("Pi 压缩操作已失效")
		}
		if input.SessionRevision != compaction.PiSessionRevision || *entry.ParentID != compaction.PiSourceLeafID ||
			entry.FirstKeptEntryID != compaction.PiFirstKeptEntryID || entry.TokensBefore != compaction.PiTokensBefore ||
			details.SourceDigest != compaction.PiSourceDigest {
			return kernel.Forbidden("Pi 压缩条目与服务端源上下文不一致")
		}
		var expected agentcontext.Checkpoint
		var mode, reason string
		if compaction.PiNative != nil {
			if compaction.PiNative.Checkpoint == nil {
				return BadAuthRequest("Pi 原生摘要尚未完成")
			}
			expected, mode, reason = *compaction.PiNative.Checkpoint, compaction.PiNative.Mode, compaction.PiNative.Reason
			if !cloudAgentNativeUsageMatches(entry.Usage, compaction.PiNative.Usage) {
				return kernel.Forbidden("Pi 摘要 usage 与模型任务记录不一致")
			}
		} else {
			task, err := repo.TaskForUser(userID, compaction.PiTaskID)
			if err != nil {
				return err
			}
			if task.Status == model.TaskStatusQueued || task.Status == model.TaskStatusRunning {
				return BadAuthRequest("Pi 压缩任务尚未完成")
			}
			expected, mode, reason = cloudAgentContextCompactionResult(&state, task)
		}
		expectedFrame, err := agentcontext.Frame(expected)
		if err != nil {
			return err
		}
		if entry.Summary != expectedFrame || cloudAgentTextDigest(expectedFrame) != details.CheckpointDigest {
			return kernel.Forbidden("Pi 压缩结果与服务端任务结果不一致")
		}
		session, _, err := repo.CloudAgentPiSession(userID, current.ConversationID)
		if err != nil {
			return err
		}
		if session.Revision != input.SessionRevision || session.ActiveRunID != runID || session.ActiveLeafID != compaction.PiSourceLeafID {
			return kernel.Forbidden("Pi 压缩会话版本已变化")
		}
		parentID := *entry.ParentID
		modelEntry := model.CloudAgentPiEntry{
			SessionID: session.ID, EntryID: entry.ID, ParentID: parentID,
			UserID: userID, RunID: runID, EntryJSON: string(input.Entry),
		}
		updatedRevision, err = repo.AppendCloudAgentPiSessionEntries(userID, current.ConversationID, runID,
			input.SessionRevision, entry.ID, []model.CloudAgentPiEntry{modelEntry})
		if err != nil {
			return err
		}
		checkpoint, err := agentcontext.ParseFrame(expectedFrame)
		if err != nil {
			return err
		}
		return applyCloudAgentContextCheckpoint(runID, current, &state, checkpoint, mode, reason, false)
	})
	if err != nil {
		return 0, err
	}
	return updatedRevision, nil
}

type PiContextCompactionCommit struct {
	SessionRevision int64           `json:"sessionRevision"`
	Entry           json.RawMessage `json:"entry"`
}

func cloudAgentPiCompactionOperationID(sessionID, runID string, input PiContextCompactionStart, sourceDigest string) string {
	identity := fmt.Sprintf("%s\n%s\n%s\n%d\n%s\n%s\n%t\n%d", cloudAgentPiCompactionProtocol,
		sessionID, runID, input.SessionRevision, input.ActiveLeafID, sourceDigest, input.WillRetry, input.TokensBefore)
	identity += "\n" + input.Reason
	if len(input.Preparation) > 0 {
		identity += "\n" + cloudAgentTextDigest(string(input.Preparation))
	}
	digest := sha256.Sum256([]byte(identity))
	return "pi-compact-" + hex.EncodeToString(digest[:])
}

func cloudAgentPiCompactionEntryForOperation(entries []model.CloudAgentPiEntry, operationID string) (piCompactionEntry, bool) {
	for _, stored := range entries {
		var entry piCompactionEntry
		if json.Unmarshal([]byte(stored.EntryJSON), &entry) != nil || entry.Type != "compaction" {
			continue
		}
		var details piCompactionDetails
		if json.Unmarshal(entry.Details, &details) == nil && details.OperationID == operationID && details.ProtocolVersion == cloudAgentPiCompactionProtocol {
			return entry, true
		}
	}
	return piCompactionEntry{}, false
}

func piCompactionViewFromEntry(entry piCompactionEntry, revision int64) *PiContextCompactionView {
	var details piCompactionDetails
	_ = json.Unmarshal(entry.Details, &details)
	return &PiContextCompactionView{OperationID: details.OperationID, Status: "committed", Summary: entry.Summary,
		FirstKeptEntryID: entry.FirstKeptEntryID, TokensBefore: entry.TokensBefore, SourceDigest: details.SourceDigest,
		CheckpointDigest: details.CheckpointDigest, SessionRevision: revision,
		Details: map[string]any{"protocolVersion": details.ProtocolVersion, "operationId": details.OperationID,
			"sourceDigest": details.SourceDigest, "checkpointDigest": details.CheckpointDigest}}
}
