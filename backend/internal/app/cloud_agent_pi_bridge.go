package app

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

const piAgentLeaseDuration = 45 * time.Second

// PiAgentToolSpec is the transport-neutral snapshot of an eligible tool. The
// backend still checks each invocation against the run's permission contract.
type PiAgentToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Category    string         `json:"category,omitempty"`
	Allowed     bool           `json:"allowed"`
}

type PiPendingContextCompaction struct {
	OperationID     string `json:"operationId"`
	SessionRevision int64  `json:"sessionRevision"`
	ActiveLeafID    string `json:"activeLeafId"`
	Reason          string `json:"reason"`
	WillRetry       bool   `json:"willRetry"`
	TokensBefore    int    `json:"tokensBefore"`
}

type PiPendingInterjection struct {
	Source    string    `json:"source,omitempty"`
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
}

type PiAgentSnapshot struct {
	Subagent                 *SubagentRuntime             `json:"subagent,omitempty"`
	RunID                    string                       `json:"runId"`
	PiSessionID              string                       `json:"piSessionId"`
	PiSessionRevision        int64                        `json:"piSessionRevision"`
	PiSessionLeaseEpoch      int64                        `json:"piSessionLeaseEpoch"`
	PiSessionHeader          json.RawMessage              `json:"piSessionHeader"`
	PiSessionEntries         []PiAgentSessionEntry        `json:"piSessionEntries"`
	HistoricalToolReceipts   []PiHistoricalToolReceipt    `json:"historicalToolReceipts,omitempty"`
	VisionBatchBudget        *cloudAgentVisionBatchBudget `json:"visionBatchBudget,omitempty"`
	PiActiveLeafID           string                       `json:"piActiveLeafId,omitempty"`
	UserID                   string                       `json:"userId"`
	Revision                 int64                        `json:"revision"`
	Status                   string                       `json:"status"`
	Request                  CloudAgentRequest            `json:"request"`
	ModelLimits              PiAgentModelLimits           `json:"modelLimits"`
	Canonical                canonicalAgentRequest        `json:"canonical"`
	ActiveTask               string                       `json:"activeTaskId,omitempty"`
	LastTaskID               string                       `json:"lastTaskId,omitempty"`
	PendingToolBatch         *PiToolBatchRequest          `json:"pendingToolBatch,omitempty"`
	NoToolTaskID             string                       `json:"noToolTaskId,omitempty"`
	NoToolNudge              string                       `json:"noToolNudge,omitempty"`
	ModelFailureTaskID       string                       `json:"modelFailureTaskId,omitempty"`
	ModelFailureNudge        string                       `json:"modelFailureNudge,omitempty"`
	PendingInterjections     []PiPendingInterjection      `json:"pendingInterjections,omitempty"`
	PendingContextCompaction *PiPendingContextCompaction  `json:"pendingContextCompaction,omitempty"`
	Tools                    []PiAgentToolSpec            `json:"tools"`
	// ToolCatalog 是 Pi 会话的终身工具白名单（平台工具全集）。它不是授权：运行中切换
	// 权限后新允许的工具要能在下一边界注册，是否可用仍由 tools[].allowed 与服务端校验决定。
	ToolCatalog []string          `json:"toolCatalog,omitempty"`
	PiMessages  []json.RawMessage `json:"piMessages"`
	Opened      []string          `json:"openedCategories"`
	// Harness 是首步合同冻结的 Harness 正文（版本 2 运行在首步之后非空）。
	//
	// Node 恢复时优先用它而不是重读磁盘文件：运维改了 SYSTEM.md / AGENTS.md 再重启进程，
	// 在途运行的系统提示不能被静默换成新文件内容。
	Harness          *cloudAgentHarnessSnapshot `json:"harness,omitempty"`
	SkillRuntimeMode string                     `json:"skillRuntimeMode"`
	Skills           []PiSkillSnapshot          `json:"skills,omitempty"`
}

// PiAgentModelLimits mirrors the effective Go-side text capability into Pi's
// scheduler. Go remains authoritative for request admission and billing; these
// values keep Pi's automatic compaction threshold close to the route budget.
type PiAgentModelLimits = cloudAgentContextBudget

type PiAgentSessionEntry struct {
	RunID string          `json:"runId"`
	Entry json.RawMessage `json:"entry"`
}

type PiModelStepRequest struct {
	Canonical canonicalAgentRequest `json:"canonical"`
	// HarnessHash 是 Node 装配提示所用的 Harness 内容身份（sha256，见 harnessHash）。
	// 首个模型步把它固化进运行状态，之后每一步都必须一致，否则明确停止而不是换提示。
	// 注意：这个字段必须在 Go 侧声明 —— 路由用 DisallowUnknownFields 解码，
	// 未声明的字段会让整个请求变成空 400（阶段 1 就踩过这个坑）。
	HarnessHash string `json:"harnessHash,omitempty"`
	// Harness 是装配该系统提示所用的 Harness 正文。首个模型步必须提交：
	// 只记哈希无法在重启后按原样式恢复提示，重读磁盘等于换掉在途运行的系统提示。
	// 它同时受 HarnessHash 校验（Go 复算内容身份，见 cloudAgentHarnessBodyDigest），
	// 因此"报一个哈希、发另一份正文"会被拒绝。
	Harness *cloudAgentHarnessSnapshot `json:"harness,omitempty"`
}

type PiModelStepView struct {
	Decision        string             `json:"decision,omitempty"`
	ModelLimits     PiAgentModelLimits `json:"modelLimits"`
	ProjectedTokens int                `json:"projectedTokens,omitempty"`
	TaskID          string             `json:"taskId"`
	Status          string             `json:"status"`
	TextDraft       string             `json:"textDraft,omitempty"`
	Result          json.RawMessage    `json:"result,omitempty"`
	Error           string             `json:"error,omitempty"`
}

func (s *Service) ClaimPiAgent(owner string) (*PiAgentSnapshot, error) {
	return s.claimPiAgent(owner, "")
}

func (s *Service) ClaimPiAgentForWorker(owner, userScope string) (*PiAgentSnapshot, error) {
	return s.claimPiAgent(owner, userScope)
}

func (s *Service) claimPiAgent(owner, userScope string) (*PiAgentSnapshot, error) {
	if owner == "" {
		return nil, BadAuthRequest("Pi worker ID is required")
	}
	policy, err := s.AgentSchedulerConfig()
	if err != nil {
		return nil, err
	}
	run, err := s.repo.ClaimPiAgentFair(owner, time.Now().UTC().Add(piAgentLeaseDuration), policy, userScope)
	if err != nil || run == nil {
		return nil, err
	}
	protected, err := s.repo.WorkerRecoveryProtectedWait(*run)
	if err != nil {
		return nil, err
	}
	if run.WorkerRecoveryExhausted(time.Now()) && (!protected || run.RecoveryAttempts >= model.AgentWorkerRecoveryLimit || run.RecoveryOperationAttempts >= model.AgentWorkerRecoveryLimit) {
		err = s.mutatePiControl(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			return failWorkerRecovery(current, "pi_worker_recovery_exhausted", "Pi worker 连续接管仍无进展，本轮已停止，请重试")
		})
		return nil, err
	}
	return s.piAgentSnapshot(run)
}

func (s *Service) PiAgentSnapshot(userID, runID, owner string) (*PiAgentSnapshot, error) {
	// Polling control also drains terminal child reports after a worker crash.
	if err := s.reconcileDynamicSubagents(context.Background(), userID, runID); err != nil {
		return nil, err
	}
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		var terminal bool
		run, terminal = s.piTerminalRunAfterLeaseFailure(userID, runID, owner)
		if !terminal {
			return nil, err
		}
	}
	return s.piAgentSnapshot(run)
}

func (s *Service) RenewPiAgentLease(userID, runID, owner string) error {
	run, err := s.piAgentLeaseRow(userID, runID, owner, false)
	if err != nil {
		if _, terminal := s.piTerminalRunAfterLeaseFailure(userID, runID, owner); terminal {
			return nil
		}
		return err
	}
	if cloudAgentRunTerminal(run.Status) {
		return nil
	}
	workerID, epoch, _, err := parsePiAgentLeaseOwner(owner)
	if err != nil {
		return kernel.AgentLeaseLost("Pi Agent 运行租约无效")
	}
	ok, err := s.repo.RenewPiAgentLease(userID, runID, workerID, epoch, time.Now().UTC().Add(piAgentLeaseDuration))
	if err != nil {
		return err
	}
	if !ok {
		if _, terminal := s.piTerminalRunAfterLeaseFailure(userID, runID, owner); terminal {
			return nil
		}
		return kernel.AgentLeaseLost("Pi Agent 运行租约已失效")
	}
	return nil
}

func (s *Service) piAgentLeasedRun(userID, runID, owner string) (*model.CloudAgentExecution, error) {
	return s.piAgentLeaseRow(userID, runID, owner, true)
}

func (s *Service) piAgentLeaseRow(userID, runID, owner string, hydrate bool) (*model.CloudAgentExecution, error) {
	run, err := s.repo.CloudAgentControlRow(userID, runID)
	if hydrate {
		run, err = s.repo.CloudAgent(userID, runID)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, kernel.NotFound("Agent 运行不存在")
	}
	if err != nil {
		return nil, err
	}
	workerID, expectedEpoch, hasEpoch, tokenErr := parsePiAgentLeaseOwner(owner)
	if tokenErr != nil || run.Engine != "pi" || run.LeaseOwner != workerID || run.LeaseExpiresAt == nil || !run.LeaseExpiresAt.After(time.Now()) {
		return nil, kernel.AgentLeaseLost("Pi Agent 运行租约无效")
	}
	session, err := s.repo.CloudAgentPiLease(userID, firstNonEmpty(run.ConversationID, run.ID))
	if err != nil || session.ActiveRunID != runID || session.LeaseOwner != workerID || session.LeaseExpiresAt == nil || !session.LeaseExpiresAt.After(time.Now()) || (hasEpoch && session.LeaseEpoch != expectedEpoch) {
		return nil, kernel.AgentLeaseLost("Pi Agent 会话租约已失效")
	}
	return run, nil
}

func parsePiAgentLeaseOwner(owner string) (string, int64, bool, error) {
	separator := strings.LastIndex(owner, "@")
	if separator < 0 {
		if strings.TrimSpace(owner) == "" {
			return "", 0, false, errors.New("empty Pi worker owner")
		}
		return owner, 0, false, nil
	}
	workerID := strings.TrimSpace(owner[:separator])
	epochText := strings.TrimSpace(owner[separator+1:])
	epoch, err := strconv.ParseInt(epochText, 10, 64)
	if workerID == "" || err != nil || epoch < 1 {
		return "", 0, true, errors.New("invalid Pi session lease epoch")
	}
	return workerID, epoch, true, nil
}

func (s *Service) piAgentSnapshot(run *model.CloudAgentExecution) (*PiAgentSnapshot, error) {
	// 先回写已结束的生成：通知随这份快照的待送插话一起交给模型。
	if run, err := s.settleCloudAgentGenerationsAndReload(run); err != nil {
		return nil, err
	} else if run != nil {
		return s.piAgentSnapshotSettled(run)
	}
	return s.piAgentSnapshotSettled(run)
}

func (s *Service) piAgentSnapshotSettled(run *model.CloudAgentExecution) (*PiAgentSnapshot, error) {
	run, err := s.cloudAgentRefreshToolLoadout(run)
	if err != nil {
		return nil, err
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return nil, err
	}
	budget := s.cloudAgentContextBudgetForRequest(state.Request)
	// Recovery may impose a smaller request ceiling even when the policy is unlimited.
	if limits, limitErr := s.cloudAgentStepLimits(); limitErr == nil {
		if output := cloudAgentStepOutputBudget(limits, state.BoostStepOutputBudget); output > 0 {
			budget.MaxOutputTokens = min(budget.MaxOutputTokens, output)
		}
	}
	tools := piSnapshotToolSpecs(&state)
	session, sessionEntries, err := s.repo.CloudAgentPiSession(run.UserID, firstNonEmpty(run.ConversationID, run.ID))
	if err != nil {
		return nil, err
	}
	entryViews := make([]PiAgentSessionEntry, 0, len(sessionEntries))
	for _, entry := range sessionEntries {
		entryViews = append(entryViews, PiAgentSessionEntry{RunID: entry.RunID, Entry: json.RawMessage(entry.EntryJSON)})
	}
	historicalReceipts, err := s.piHistoricalFinishReceipts(run, entryViews)
	if err != nil {
		return nil, err
	}
	pendingCompaction := piSnapshotPendingCompaction(&state)
	mode := firstNonEmpty(state.SkillRuntimeMode, cloudAgentSkillRuntimeLegacy)
	nativeSkills, err := s.piSnapshotNativeSkills(run, &state, mode)
	if err != nil {
		return nil, err
	}
	pendingInterjections := make([]PiPendingInterjection, 0, len(state.PendingInterjections))
	var visionBudget *cloudAgentVisionBatchBudget
	if state.Request.VisionEnabled {
		budget, budgetErr := s.cloudAgentVisionBatchBudget(state.Request)
		if budgetErr == nil {
			visionBudget = &budget
		}
	}
	for _, item := range state.PendingInterjections {
		pendingInterjections = append(pendingInterjections, PiPendingInterjection{ID: item.ID, Text: item.Text, Source: item.Source, CreatedAt: item.CreatedAt})
	}
	var pendingBatch *PiToolBatchRequest
	if state.PiToolBatchTaskID != "" && len(state.Calls) > 0 {
		pendingBatch = &PiToolBatchRequest{TaskID: state.PiToolBatchTaskID, Calls: state.Calls}
	}
	return &PiAgentSnapshot{
		RunID: run.ID, UserID: run.UserID, Revision: run.Revision, Status: run.Status,
		Subagent:    state.Subagent,
		PiSessionID: session.ID, PiSessionRevision: session.Revision, PiSessionLeaseEpoch: session.LeaseEpoch, PiSessionHeader: json.RawMessage(session.HeaderJSON),
		PiSessionEntries: entryViews, HistoricalToolReceipts: historicalReceipts, PiActiveLeafID: session.ActiveLeafID,
		Request: state.Request, ModelLimits: budget, Canonical: state.Canonical, ActiveTask: state.ActiveTaskID, LastTaskID: state.LastStepTaskID, NoToolTaskID: state.PiNoToolTaskID, NoToolNudge: state.PiNoToolNudge, ModelFailureTaskID: state.PiModelFailureTaskID, ModelFailureNudge: state.PiModelFailureNudge, PendingInterjections: pendingInterjections, PendingContextCompaction: pendingCompaction, Tools: tools, ToolCatalog: cloudAgentPiToolCatalog(state.Canonical.Tools),
		Opened: state.ActivatedToolCategories, PiMessages: piAgentMessages(run),
		PendingToolBatch: pendingBatch,
		SkillRuntimeMode: mode, Skills: nativeSkills,
		VisionBatchBudget: visionBudget,
		// 冻结的 Harness 正文随快照回发：恢复的 worker 因此不必（也不允许）重读磁盘 Harness。
		Harness: cloudAgentFrozenHarness(state.Snapshot),
	}, nil
}

func piAgentMessages(run *model.CloudAgentExecution) []json.RawMessage {
	messages := make([]json.RawMessage, 0)
	for _, record := range run.Transcript {
		if record.Kind == cloudAgentMessageKindPi {
			messages = append(messages, json.RawMessage(record.MessageJSON))
		}
	}
	return messages
}

// piAssistantText 归一化 Pi 消息正文：既接受字符串，也接受 Pi 的 [{type:"text",text:...}]。
func piAssistantText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		parts := make([]string, 0, len(value))
		for _, item := range value {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := entry["text"].(string); ok && text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

// cloudAgentCommitPiInterjections closes the delivery loop only when the Pi user
// checkpoint and its v3 session entries commit in the same Go transaction. A
// worker crash before this checkpoint leaves the items pending for the next lease.
func cloudAgentCommitPiInterjections(runID string, state *cloudAgentRuntime, ids []string, content any) error {
	if state == nil || len(ids) == 0 {
		return nil
	}
	messageText := piAssistantText(content)
	requested := make(map[string]cloudAgentInterjection, len(ids))
	for _, id := range ids {
		for _, item := range state.PendingInterjections {
			if item.ID == id {
				requested[id] = item
				break
			}
		}
		item, ok := requested[id]
		if !ok || !strings.Contains(messageText, cloudAgentInterjectionContent(item)) {
			return kernel.Forbidden("Pi 用户消息与待送插话不匹配")
		}
	}
	remaining := make([]cloudAgentInterjection, 0, len(state.PendingInterjections)-len(requested))
	for _, item := range state.PendingInterjections {
		if _, delivered := requested[item.ID]; delivered {
			state.event(runID, cloudAgentInterjectionSource(item)+"_delivered", map[string]any{"messageId": item.ID, "text": item.Text})
			cloudAgentRememberInterjectionID(state, item.ID)
			continue
		}
		remaining = append(remaining, item)
	}
	state.PendingInterjections = remaining
	state.CompletionNudgeAttempt, state.CompletionNudges, state.CompletionNudgeFingerprint = 0, 0, ""
	state.Canonical.ToolChoice = "auto"
	return nil
}

// piReasoningText 提取 Pi 的 thinking 块正文（推理内容不进正文，但要保留既有 SSE 事件）。
func piReasoningText(content any) string {
	items, ok := content.([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok || entry["type"] != "thinking" {
			continue
		}
		if text, ok := entry["thinking"].(string); ok && text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// taskIDForPiMessage 让 assistant_message 的 messageId 与旧路径一致地指向产生它的模型任务；
// 没有绑定任务时退回运行 ID，保证前端至少能定位到本轮。
func taskIDForPiMessage(input PiMessageCheckpoint, runID string) string {
	if input.TaskID != "" {
		return input.TaskID
	}
	return runID
}

// PiFailRun 让 worker 上报"无法重试的启动期错误"（工具 schema 与制品不一致、
// Harness 读取失败等）。这类错误重试多少次都一样，如果只打 stderr，运行会一直停在
// running，前端表现为"Agent 输出完了却永远显示运行中"——本轮真实踩到这个。
// 只接受租约持有者上报，且只在本轮尚未终结时生效。
func (s *Service) PiFailRun(userID, runID, owner, reason string) error {
	run, err := s.piAgentLeaseRow(userID, runID, owner, false)
	if err != nil {
		var terminal bool
		run, terminal = s.piTerminalRunAfterLeaseFailure(userID, runID, owner)
		if !terminal {
			return err
		}
	}
	if cloudAgentRunTerminal(run.Status) {
		return nil
	}
	message := truncateRunes(strings.TrimSpace(reason), 300)
	if message == "" {
		message = "Pi worker 无法继续本轮（未提供原因）"
	}
	return s.mutatePiControl(userID, runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		current.Status = "failed"
		current.FailureMessage = message
		current.CleanupPending = true
		return appendWorkerEvent(current, "run_failed", map[string]any{
			"text": "Agent 运行无法继续：" + message, "reason": "pi_worker_fatal",
		})
	})
}

// Terminal transitions release their session lease. These idempotent worker
// callbacks may already be in flight when that happens; a user-scoped lookup
// is safe here because the fallback never mutates an active run.
func (s *Service) piTerminalRunAfterLeaseFailure(userID, runID, owner string) (*model.CloudAgentExecution, bool) {
	run, err := s.repo.CloudAgent(userID, runID)
	workerID, expectedEpoch, hasEpoch, ownerErr := parsePiAgentLeaseOwner(owner)
	if err != nil || ownerErr != nil || run.Engine != "pi" || run.LeaseOwner != workerID || !cloudAgentRunTerminal(run.Status) {
		return nil, false
	}
	conversationID := firstNonEmpty(run.ConversationID, run.ID)
	session, _, sessionErr := s.repo.CloudAgentPiSession(userID, conversationID)
	if sessionErr != nil || (hasEpoch && session.LeaseEpoch != expectedEpoch) {
		return nil, false
	}
	return run, true
}

// piStalledLeasePeriods 是"多久没进展就判定 worker 已死"的租约倍数。留出足够余量，
// 避免把正常的慢步骤（长模型调用、等待审批）误判为停滞。
const piStalledLeasePeriods = 6

// piUnclaimedRunTimeout 是"从未被任何 worker 领取"的运行在被判失败前的等待时间。
//
// 与 piStalledLeasePeriods 分开，是因为两者的语义完全不同：
//   - 租约过期 = worker 领取后失联，4.5 分钟内就能确定；
//   - 从未领取 = 根本没有 worker（未部署、token 未配置、容器在重启/重建镜像），
//     这是**部署状态**而不是崩溃。本地镜像重建实测要几分钟，重启也可能更久，
//     用 4.5 分钟会误杀，用 30 分钟既容忍重启又能让配置错误在可接受时间内暴露。
//
// 为什么不干脆让它永远排队：前端把 queued 与 running 渲染成同一种"运行中"，
// 永远排队就等于重现"Agent 一直显示运行中"——那正是看门狗存在的理由。
const piUnclaimedRunTimeout = 30 * time.Minute

// SweepStalledPiAgentRuns 终结"已领取但租约早已过期、无人推进"的 Pi 运行。
//
// 为什么必须有它：ClaimPiAgent 只靠 lease_expires_at 让运行被反复领取。当 worker
// 崩溃或配置错误（例如启动期校验失败）时，它会每 45 秒领回来、立刻失败、再等过期，
// 无限重复，而运行没有任何一方写入终态 —— 前端因此永远显示"Agent 正在运行"。
// 本轮真实事故（TOOL_SCHEMA 制品缺失导致每次领取都失败）就是这样卡了 50 分钟。
//
// 只处理**被领取过**的运行（lease_expires_at 非空）。从未领取的运行交给
// SweepUnclaimedPiAgentRuns，用不同阈值与不同原因，避免把"没有 worker"误报成"worker 失联"。
//
// 系统级动作：不要求租约归属，但只处理租约已过期远超一个周期的运行，避免与活跃
// worker 竞争。已终态的运行会被忽略（幂等）。
func (s *Service) SweepStalledPiAgentRuns() (int, error) {
	cutoff := time.Now().UTC().Add(-piStalledLeasePeriods * piAgentLeaseDuration)
	runs, err := s.repo.StalledPiAgentRuns(cutoff, 50)
	if err != nil {
		return 0, err
	}
	return s.sweepPiRuns(runs, "pi_worker_stalled",
		"Pi worker 长时间未推进本轮（租约过期且无进展），已停止；请检查 agent 服务日志后重试")
}

// SweepUnclaimedPiAgentRuns 终结"长时间没有任何 worker 领取"的 Pi 运行。
//
// 与 SweepStalledPiAgentRuns 的区别只有两点：阈值更长、失败原因不同。原因必须不同，
// 否则运维无法区分"agent 服务没起来"与"agent 服务跑挂了"。
func (s *Service) SweepUnclaimedPiAgentRuns() (int, error) {
	cutoff := time.Now().Add(-piUnclaimedRunTimeout)
	runs, err := s.repo.UnclaimedPiAgentRuns(cutoff, 50)
	if err != nil {
		return 0, err
	}
	return s.sweepPiRuns(runs, "pi_worker_unavailable",
		"本轮长时间没有 Pi worker 领取，已停止；请确认 agent 服务已启动且 CANVAS_AGENT_INTERNAL_TOKEN 与后端一致，然后重试")
}

// sweepPiRuns 是两种清扫共用的终结逻辑。
//
// 必须同时置 CleanupPending：finishCloudAgentCleanup 的入口条件是
// `CleanupPending && 终态`。此前只写 failed，于是被判停的运行既不取消在跑的子任务、
// 也不回写已完成的媒体结果 —— 那部分工作会永久丢失。
func (s *Service) sweepPiRuns(runs []model.CloudAgentExecution, reason, message string) (int, error) {
	swept := 0
	for index := range runs {
		run := runs[index]
		if cloudAgentRunTerminal(run.Status) {
			continue
		}
		if err := s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			state, decodeErr := cloudAgentDecode(current)
			if decodeErr != nil {
				return decodeErr
			}
			current.Status = "failed"
			current.FailureMessage = message
			current.CleanupPending = true
			state.event(run.ID, "run_failed", map[string]any{"text": message, "reason": reason})
			return cloudAgentSave(current, &state)
		}); err != nil {
			if errors.Is(err, repository.ErrCreationConflict) {
				continue // 有并发推进，交给它
			}
			return swept, err
		}
		swept++
	}
	return swept, nil
}

// cloudAgentPiToolCatalog 是平台工具全集与本轮目录的并集（含子代理工具与 native read）。
func cloudAgentPiToolCatalog(tools []map[string]any) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, name := range append(append(cloudAgentToolNames(tools), CloudAgentSupportedToolNames()...), cloudAgentToolNames(CloudAgentToolSchemaArtifact().Tools)...) {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
