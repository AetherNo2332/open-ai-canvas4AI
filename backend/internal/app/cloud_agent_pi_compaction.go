package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"yingce/backend/internal/agentcontext"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
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
	start, err := s.piStartCompactionSource(run, entries, input)
	if err != nil {
		return nil, err
	}
	state, budget, source, native, nativeFallback := start.state, start.budget, start.source, start.native, start.nativeFallback
	operationID := cloudAgentPiCompactionOperationID(session.ID, runID, input, source.SourceDigest)
	if existing, ok := cloudAgentPiCompactionEntryForOperation(entries, operationID); ok {
		return piCompactionViewFromEntry(existing, session.Revision), nil
	}
	if inProgress, err := piStartCompactionGate(session, &state, runID, operationID, input); err != nil || inProgress {
		if err != nil {
			return nil, err
		}
		return s.PiContextCompaction(userID, runID, owner, operationID)
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
	state.event(runID, "context_compaction_requested", piStartCompactionRequestedPayload(input, source, operationID, native, nativeFallback))
	if native != nil {
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

// piStartCompaction holds what PiBeginContextCompaction derives from the active
// branch before it checks the session and records the request.
type piStartCompaction struct {
	state          cloudAgentRuntime
	budget         cloudAgentContextBudget
	source         cloudAgentPiCompactionSource
	native         *cloudAgentPiNativeCompaction
	nativeFallback string
}

// piStartCompactionSource projects the active branch into the compaction source
// and validates Pi's optional native preparation against it.
func (s *Service) piStartCompactionSource(run *model.CloudAgentExecution, entries []model.CloudAgentPiEntry, input PiContextCompactionStart) (piStartCompaction, error) {
	var start piStartCompaction
	branch, err := cloudAgentPiActiveBranch(entries, input.ActiveLeafID)
	if err != nil {
		return start, BadAuthRequest("Pi 压缩活动分支无效")
	}
	start.state, err = cloudAgentDecode(run)
	if err != nil {
		return start, err
	}
	start.budget = s.cloudAgentContextBudgetForRequest(start.state.Request)
	start.source, err = cloudAgentPiCompactionSourceForBranch(branch, input.ActiveLeafID, start.budget.KeepRecentTokens)
	if err != nil {
		return start, BadAuthRequest("Pi 压缩上下文无效")
	}
	start.native, start.nativeFallback, err = piNativeCompactionForStart(input, &start.source, branch)
	return start, err
}

// piStartCompactionGate checks the source session is unchanged and that no
// other compaction runs. inProgress=true means this operation already started.
func piStartCompactionGate(session *model.CloudAgentPiSession, state *cloudAgentRuntime, runID, operationID string, input PiContextCompactionStart) (bool, error) {
	if session.ActiveRunID != runID || session.Revision != input.SessionRevision || session.ActiveLeafID != input.ActiveLeafID {
		return false, kernel.Forbidden("Pi 压缩源会话已变化")
	}
	if state.ContextCompaction != nil {
		if state.ContextCompaction.PiOperationID != operationID {
			return false, kernel.Forbidden("另一个上下文压缩操作仍在进行")
		}
		return true, nil
	}
	if state.ContextCompactionCount >= cloudAgentMaxCompactionsPerRun {
		return false, kernel.Forbidden("本轮上下文压缩次数已达到上限")
	}
	return false, nil
}

// piNativeCompactionForStart validates Pi's optional native preparation. An
// invalid preparation is advisory and falls back to the server compactor.
func piNativeCompactionForStart(input PiContextCompactionStart, source *cloudAgentPiCompactionSource, branch []cloudAgentPiCompactionEntry) (*cloudAgentPiNativeCompaction, string, error) {
	var native *cloudAgentPiNativeCompaction
	nativeFallback := ""
	if len(input.Preparation) > 0 {
		var err error
		native, err = cloudAgentPrepareNativeCompaction(input.Preparation, input.TokensBefore, source, branch)
		if err != nil {
			if errors.Is(err, errPiNativePreparationInvalid) {
				// Pi's preparation is advisory. If its boundary metadata is stale or
				// malformed, keep the server-owned source projection and use the
				// semantic compactor instead of terminating the Agent run.
				native, nativeFallback = nil, "preparation_invalid"
				if errors.Is(err, errPiNativePreparationTooLarge) {
					nativeFallback = "preparation_too_large"
				}
			} else {
				return nil, "", BadAuthRequest(err.Error())
			}
		}
	}
	return native, nativeFallback, nil
}

func piStartCompactionRequestedPayload(input PiContextCompactionStart, source cloudAgentPiCompactionSource, operationID string, native *cloudAgentPiNativeCompaction, nativeFallback string) map[string]any {
	requested := map[string]any{
		"kind": "semantic_compaction", "reason": input.Reason, "sourceBytes": source.SourceBytes,
		"turns": source.CompactedTurnCount, "operationId": operationID, "sourceDigest": source.SourceDigest,
		"piReason": input.Reason, "willRetry": input.WillRetry, "engine": "go-semantic",
		"retainedMessages": len(source.Messages) - source.FirstKeptIndex,
	}
	if native != nil {
		requested["engine"] = "pi-native"
	}
	if nativeFallback != "" {
		requested["nativeFallback"] = nativeFallback
	}
	return requested
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
	entry, details, err := validatePiCompactionCommitEntry(operationID, input)
	if err != nil {
		return 0, err
	}
	session, entries, err := s.repo.CloudAgentPiSession(userID, run.ConversationID)
	if err != nil {
		return 0, err
	}
	if existing, ok := cloudAgentPiCompactionEntryForOperation(entries, operationID); ok {
		return piCompactionCommitReplay(existing, entry, details, session.Revision)
	}
	var updatedRevision int64
	err = s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		return piCommitCompactionInState(current, repo, piCompactionCommit{userID: userID, runID: runID, operationID: operationID, input: input, entry: entry, details: details, updatedRevision: &updatedRevision})
	})
	if err != nil {
		return 0, err
	}
	return updatedRevision, nil
}

// validatePiCompactionCommitEntry decodes the compaction entry Pi wants to
// append and checks its identity, digest and checkpoint contract.
func validatePiCompactionCommitEntry(operationID string, input PiContextCompactionCommit) (piCompactionEntry, piCompactionDetails, error) {
	var entry piCompactionEntry
	var details piCompactionDetails
	if input.SessionRevision < 1 || len(input.Entry) == 0 || len(input.Entry) > 1<<20 || !json.Valid(input.Entry) {
		return entry, details, BadAuthRequest("Pi 压缩检查点无效")
	}
	if err := json.Unmarshal(input.Entry, &entry); err != nil || entry.Type != "compaction" || strings.TrimSpace(entry.ID) == "" || len(entry.ID) > 160 {
		return entry, details, BadAuthRequest("Pi 压缩条目格式无效")
	}
	if entry.ParentID == nil {
		return entry, details, BadAuthRequest("Pi 压缩条目缺少父节点")
	}
	if err := json.Unmarshal(entry.Details, &details); err != nil || details.ProtocolVersion != cloudAgentPiCompactionProtocol || details.OperationID != operationID {
		return entry, details, BadAuthRequest("Pi 压缩操作身份无效")
	}
	if details.CheckpointDigest != cloudAgentTextDigest(entry.Summary) {
		return entry, details, BadAuthRequest("Pi 压缩检查点摘要校验失败")
	}
	if _, err := agentcontext.ParseFrame(entry.Summary); err != nil {
		return entry, details, BadAuthRequest("Pi 压缩检查点不符合服务端合同")
	}
	return entry, details, nil
}

// piCompactionCommitReplay accepts an identical retry of an already appended
// compaction entry and rejects a retry whose content differs.
func piCompactionCommitReplay(existing, entry piCompactionEntry, details piCompactionDetails, revision int64) (int64, error) {
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
	return revision, nil
}

// piCompactionCommit carries one compaction commit into its checkpoint
// transaction; updatedRevision receives the appended session revision.
type piCompactionCommit struct {
	userID, runID, operationID string
	input                      PiContextCompactionCommit
	entry                      piCompactionEntry
	details                    piCompactionDetails
	updatedRevision            *int64
}

// piCommitCompactionInState runs inside the checkpoint transaction: it checks
// the entry against the server source, appends it to the Pi session and
// applies the checkpoint to the run.
func piCommitCompactionInState(current *model.CloudAgentExecution, repo *repository.Repository, commit piCompactionCommit) error {
	state, err := cloudAgentDecode(current)
	if err != nil {
		return err
	}
	compaction := state.ContextCompaction
	if compaction == nil || compaction.PiOperationID != commit.operationID {
		return kernel.Forbidden("Pi 压缩操作已失效")
	}
	entry := commit.entry
	if commit.input.SessionRevision != compaction.PiSessionRevision || *entry.ParentID != compaction.PiSourceLeafID ||
		entry.FirstKeptEntryID != compaction.PiFirstKeptEntryID || entry.TokensBefore != compaction.PiTokensBefore ||
		commit.details.SourceDigest != compaction.PiSourceDigest {
		return kernel.Forbidden("Pi 压缩条目与服务端源上下文不一致")
	}
	expected, mode, reason, err := piCompactionCommitExpected(repo, commit.userID, &state, entry)
	if err != nil {
		return err
	}
	expectedFrame, err := agentcontext.Frame(expected)
	if err != nil {
		return err
	}
	if entry.Summary != expectedFrame || cloudAgentTextDigest(expectedFrame) != commit.details.CheckpointDigest {
		return kernel.Forbidden("Pi 压缩结果与服务端任务结果不一致")
	}
	session, _, err := repo.CloudAgentPiSession(commit.userID, current.ConversationID)
	if err != nil {
		return err
	}
	if session.Revision != commit.input.SessionRevision || session.ActiveRunID != commit.runID || session.ActiveLeafID != compaction.PiSourceLeafID {
		return kernel.Forbidden("Pi 压缩会话版本已变化")
	}
	parentID := *entry.ParentID
	modelEntry := model.CloudAgentPiEntry{
		SessionID: session.ID, EntryID: entry.ID, ParentID: parentID,
		UserID: commit.userID, RunID: commit.runID, EntryJSON: string(commit.input.Entry),
	}
	*commit.updatedRevision, err = repo.AppendCloudAgentPiSessionEntries(commit.userID, current.ConversationID, commit.runID,
		commit.input.SessionRevision, entry.ID, []model.CloudAgentPiEntry{modelEntry})
	if err != nil {
		return err
	}
	checkpoint, err := agentcontext.ParseFrame(expectedFrame)
	if err != nil {
		return err
	}
	return applyCloudAgentContextCheckpoint(commit.runID, current, &state, checkpoint, mode, reason, false)
}

// piCompactionCommitExpected returns the checkpoint the server expects: the
// finished native result, or the semantic compaction task's result.
func piCompactionCommitExpected(repo *repository.Repository, userID string, state *cloudAgentRuntime, entry piCompactionEntry) (agentcontext.Checkpoint, string, string, error) {
	compaction := state.ContextCompaction
	if compaction.PiNative != nil {
		if compaction.PiNative.Checkpoint == nil {
			return agentcontext.Checkpoint{}, "", "", BadAuthRequest("Pi 原生摘要尚未完成")
		}
		if !cloudAgentNativeUsageMatches(entry.Usage, compaction.PiNative.Usage) {
			return agentcontext.Checkpoint{}, "", "", kernel.Forbidden("Pi 摘要 usage 与模型任务记录不一致")
		}
		return *compaction.PiNative.Checkpoint, compaction.PiNative.Mode, compaction.PiNative.Reason, nil
	}
	task, err := repo.TaskForUser(userID, compaction.PiTaskID)
	if err != nil {
		return agentcontext.Checkpoint{}, "", "", err
	}
	if task.Status == model.TaskStatusQueued || task.Status == model.TaskStatusRunning {
		return agentcontext.Checkpoint{}, "", "", BadAuthRequest("Pi 压缩任务尚未完成")
	}
	expected, mode, reason := cloudAgentContextCompactionResult(state, task)
	return expected, mode, reason, nil
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
