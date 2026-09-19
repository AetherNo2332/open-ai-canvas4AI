package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/agentcontext"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/prompts"
	"infinite-canvas/backend/internal/repository"
)

// A deterministic checkpoint failure must not be retried forever like a transient DB error.
var errCloudAgentCheckpoint = errors.New("invalid Agent checkpoint")

type CloudAgentEvent struct {
	EventID   string         `json:"eventId"`
	RunID     string         `json:"runId"`
	Seq       int            `json:"seq"`
	Type      string         `json:"type"`
	Payload   map[string]any `json:"payload"`
	CreatedAt time.Time      `json:"createdAt"`
}
type cloudAgentCall struct {
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}
type cloudAgentApproval struct {
	ModelName string                    `json:"modelName,omitempty"`
	ID        string                    `json:"approvalId"`
	Call      cloudAgentCall            `json:"call"`
	CallHash  string                    `json:"callHash,omitempty"`
	Preview   cloudAgentApprovalPreview `json:"preview"`
	Decision  string                    `json:"decision,omitempty"`
	Reason    string                    `json:"reason,omitempty"`
}
type cloudAgentContextCompaction struct {
	Status      string `json:"status"`
	SourceBytes int    `json:"sourceBytes"`
	TurnCount   int    `json:"turnCount"`
	// Resume 表示这次是"中途暂停压缩"：压完继续本轮的步进，而不是收尾结束本轮。
	Resume bool `json:"resume,omitempty"`
	// 触发读数：下一步预计输入 token ÷ 模型可用输入（上游实测锚点优先）。
	ProjectedTokens   int     `json:"projectedTokens,omitempty"`
	UsableInputTokens int     `json:"usableInputTokens,omitempty"`
	Ratio             float64 `json:"ratio,omitempty"`
	TokenSource       string  `json:"tokenSource,omitempty"`
}
type cloudAgentRuntime struct {
	Request                CloudAgentRequest            `json:"request"`
	Policy                 cloudAgentPolicySnapshot     `json:"policy"`
	ParentID               string                       `json:"parentId,omitempty"`
	Fingerprint            string                       `json:"fingerprint,omitempty"`
	CreativeAnchor         cloudAgentCreativeAnchor     `json:"creativeAnchor,omitempty"`
	TextHistory            []providerTextMessage        `json:"textHistory,omitempty"`
	ContextCheckpoint      *agentcontext.Checkpoint     `json:"contextCheckpoint,omitempty"`
	ContextCompaction      *cloudAgentContextCompaction `json:"contextCompaction,omitempty"`
	HistoryIncludesCurrent bool                         `json:"historyIncludesCurrent,omitempty"`
	Skills                 []cloudAgentSkill            `json:"skills"`
	SkillReads             map[string]bool              `json:"skillReads,omitempty"`
	Profile                cloudAgentProfileSnapshot    `json:"profile"`
	ProfileReads           map[string]bool              `json:"profileReads,omitempty"`
	Canonical              canonicalAgentRequest        `json:"canonical"`
	ActiveTaskID           string                       `json:"activeTaskId"`
	ActiveTextDraft        string                       `json:"activeTextDraft,omitempty"`
	MediaTaskID            string                       `json:"mediaTaskId,omitempty"`
	TaskIDs                []string                     `json:"taskIds"`
	Step                   int                          `json:"step"`
	Generations            int                          `json:"generations"`
	VideoSeconds           int                          `json:"videoSeconds"`
	Calls                  []cloudAgentCall             `json:"calls"`
	CallIndex              int                          `json:"callIndex"`
	Approval               *cloudAgentApproval          `json:"approval,omitempty"`
	Decisions              map[string]string            `json:"decisions"`
	DecisionSettings       map[string]string            `json:"decisionSettings,omitempty"`
	ActionNudged           bool                         `json:"actionNudged,omitempty"`
	EmptyOutputNudged      int                          `json:"emptyOutputNudged,omitempty"`
	// EmptyOutputEscalated 记录"空输出已经升级重试过几次"（关思考 + 放大输出预算）。
	EmptyOutputEscalated int `json:"emptyOutputEscalated,omitempty"`
	// ForceThinkingOff 让本步请求强制关闭上游思考：思考模型偶发把整个输出预算花在推理上，
	// 结果正文与工具调用皆空（实测 output_tokens 正好等于 maxOutputTokens）。
	ForceThinkingOff bool `json:"forceThinkingOff,omitempty"`
	// BoostStepOutputBudget 让本步请求使用放大后的输出预算（配合关思考重试）。
	BoostStepOutputBudget bool   `json:"boostStepOutputBudget,omitempty"`
	StepSnapshotHash      string `json:"stepSnapshotHash,omitempty"`
	// EventSeqBase 是尾部缓存之前"已入库"的事件条数：新不变量
	// events[i].Seq == EventSeqBase + i + 1。事件全量落在 cloud_agent_run_events，
	// 状态只保留最近 cloudAgentEventTailLimit 条供阅读与摘要使用。
	EventSeqBase int `json:"eventSeqBase,omitempty"`
	// EventFlushedSeq 是已写入事件表的最高 seq（幂等写入的水位）。
	EventFlushedSeq int `json:"eventFlushedSeq,omitempty"`
	// EventDegradeLevel 记录事件日志已降到哪一级（避免每次保存重复遍历）。
	EventDegradeLevel int `json:"eventDegradeLevel,omitempty"`
	// ContextCompactionCount 是本轮已经压过几次：压完仍超阈值时不要无限暂停。
	ContextCompactionCount int `json:"contextCompactionCount,omitempty"`
	// ImageInspectCounts 记录本轮内每张图被查看的次数，用于"同一张图不要反复看"的护栏。
	ImageInspectCounts map[string]int `json:"imageInspectCounts,omitempty"`
	// PendingVisualNodeID 是刚看过、还没写观察的那张图；模型下一次输出正文时把观察记到锚点。
	PendingVisualNodeID  string                   `json:"pendingVisualNodeId,omitempty"`
	StoryboardTaskID     string                   `json:"storyboardTaskId,omitempty"`
	Plan                 []cloudAgentPlanItem     `json:"plan,omitempty"`
	PendingInterjections []cloudAgentInterjection `json:"pendingInterjections,omitempty"`
	InterjectionIDs      []string                 `json:"interjectionIds,omitempty"`
	// 最近一次已发出的步骤请求（模型调用）的本地计价，与上游实测用量配成锚点用。
	// 估算与实测指向同一个 canonical：估算取自任务 input 里实际发出的那份，
	// 因此"信封一致"是构造保证，不需要额外比对。
	LastStepTaskID      string `json:"lastStepTaskId,omitempty"`
	LastStepOperation   string `json:"lastStepOperation,omitempty"`
	LastStepEstimate    int    `json:"lastStepEstimate,omitempty"`
	LastStepSourceBytes int    `json:"lastStepSourceBytes,omitempty"`
	// TokenAnchor 是上一步上游上报的用量（模型自己的分词器计数），上下文压力的权威锚点。
	TokenAnchor *cloudAgentTokenAnchor `json:"tokenAnchor,omitempty"`
	// CanvasBatchHashes 记录本批（同一个助手消息内的多次工具调用）已经消费与产出的画布版本：
	// 首元素是首个写入被校验时看到的版本，末元素是最近一次写入产出的版本。模型是在同一次读取的
	// 基础上并发提交这批写入的，首个写入必然改变版本，因此同批后续写入需要据此重基。
	CanvasBatchHashes []string          `json:"canvasBatchHashes,omitempty"`
	Events            []CloudAgentEvent `json:"events"`
}

// cloudAgentTokenAnchor 是"上游实测 + 本地估算"的一对读数。
// 上游用量来自模型自己的分词器（provider 上报），是计费与压力可以采信的权威值；
// 本地估算记录的是发出同一次请求时的读法，二者的差就是投影后续请求所需的换算基准。
type cloudAgentTokenAnchor struct {
	TaskID          string `json:"taskId"`
	Step            int    `json:"step"`
	InputTokens     int64  `json:"inputTokens"`
	CachedTokens    int64  `json:"cachedTokens"`
	OutputTokens    int64  `json:"outputTokens"`
	EstimatedTokens int    `json:"estimatedTokens"`
	SourceBytes     int    `json:"sourceBytes"`
	Accepted        bool   `json:"accepted"`
	RejectReason    string `json:"rejectReason,omitempty"`
}

// cloudAgentAnchorMinRatio / MaxRatio 是采信上游用量的合理区间。
// 实测与自估差出一个量级时通常意味着换了模型或计量口径（例如上游只报 cached、
// 或走了不同的协议分支），此时宁可继续用估算，也不要把压力曲线锚到错误基准上。
const (
	cloudAgentAnchorMinRatio = 0.5
	cloudAgentAnchorMaxRatio = 2.0
)

// recordCloudAgentTokenAnchor 用上一步的上游实测用量给上下文压力定锚。
// 幂等：同一任务只采信一次；没有实测或比值离谱时记录拒绝原因并保留估算。
func (s *Service) recordCloudAgentTokenAnchor(state *cloudAgentRuntime) {
	if s == nil || s.repo == nil || state == nil || state.LastStepTaskID == "" || state.LastStepEstimate <= 0 {
		return
	}
	if state.LastStepOperation != cloudAgentStepOperation {
		return
	}
	if state.TokenAnchor != nil && state.TokenAnchor.TaskID == state.LastStepTaskID {
		return
	}
	log, ok, err := s.repo.APICallLogUsageForTask(state.LastStepTaskID)
	if err != nil || !ok {
		return
	}
	anchor := &cloudAgentTokenAnchor{
		TaskID: state.LastStepTaskID, Step: state.Step, InputTokens: log.InputTokens,
		CachedTokens: log.CachedTokens, OutputTokens: log.OutputTokens,
		EstimatedTokens: state.LastStepEstimate, SourceBytes: state.LastStepSourceBytes,
	}
	ratio := float64(anchor.InputTokens) / float64(anchor.EstimatedTokens)
	switch {
	case ratio < cloudAgentAnchorMinRatio:
		anchor.RejectReason = "上游实测远低于本地估算，可能换了模型或口径"
	case ratio > cloudAgentAnchorMaxRatio:
		anchor.RejectReason = "上游实测远高于本地估算，可能换了模型或口径"
	default:
		anchor.Accepted = true
	}
	state.TokenAnchor = anchor
}

func (s *Service) ensureCloudAgentExecution(task *model.Task, initial cloudAgentState) error {
	var input struct {
		TextHistory []providerTextMessage `json:"textHistory"`
		Requests    struct {
			Canonical canonicalAgentRequest `json:"canonical"`
		} `json:"agentRequests"`
	}
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		return err
	}
	canonical := input.Requests.Canonical
	canonical.SystemPrompt = stripCloudAgentPlanBlock(canonical.SystemPrompt)
	canonical.Messages = stripCloudAgentRuntimeContext(canonical.Messages)
	state := cloudAgentRuntime{Request: initial.Request, Policy: initial.Policy, ParentID: initial.ParentID, Fingerprint: initial.Fingerprint, CreativeAnchor: initial.CreativeAnchor, TextHistory: input.TextHistory, Skills: initial.Skills, Profile: initial.Profile, Canonical: canonical, ActiveTaskID: task.ID, TaskIDs: []string{task.ID}, Step: 1, Decisions: map[string]string{}, Plan: initial.Plan, Events: []CloudAgentEvent{}}
	if len(initial.Skills) > 0 {
		state.event(task.ID, "tool_completed", map[string]any{"toolName": "skills_load", "text": fmt.Sprintf("已启用 %d 个技能，正文将按需读取", len(initial.Skills))})
	}
	pressure := s.cloudAgentContextPressure(task, input.Requests.Canonical, initial.Request.Prompt, initial.Request)
	// 第一步的模型调用就是根任务本身（不经过 enqueueCloudAgentTask）：
	// 在这里登记任务 id 与本次请求的本地计价，它回来时才能与上游实测配成锚点。
	state.LastStepTaskID = task.ID
	// 根任务的操作名是 cloud_agent，但它就是第一步的模型调用：按"步骤"口径登记，
	// 否则回来配锚点时会被操作名守卫挡掉（实测踩过）。
	state.LastStepOperation = cloudAgentStepOperation
	state.LastStepEstimate = pressure.EstimatedInputTokens
	state.LastStepSourceBytes = pressure.SourceBytes
	state.event(task.ID, "context_pressure", cloudAgentContextPressurePayload(pressure, &state))
	run := &model.CloudAgentExecution{ID: task.ID, UserID: task.UserID, Status: "running", Revision: 1, CreatedAt: task.CreatedAt, UpdatedAt: time.Now()}
	if err := cloudAgentSave(run, &state); err != nil {
		return err
	}
	return s.repo.EnsureCloudAgent(run)
}
func (state *cloudAgentRuntime) event(id, kind string, payload map[string]any) {
	// 序号连续于"尾部缓存 + 已入库水位"：events[i].Seq == EventSeqBase+i+1 是不变式。
	seq := state.EventSeqBase + len(state.Events) + 1
	state.Events = append(state.Events, CloudAgentEvent{EventID: fmt.Sprintf("%s:%d", id, seq), RunID: id, Seq: seq, Type: kind, Payload: payload, CreatedAt: time.Now()})
}
func cloudAgentDecode(run *model.CloudAgentExecution) (cloudAgentRuntime, error) {
	var state cloudAgentRuntime
	if run == nil || strings.TrimSpace(run.StateJSON) == "" {
		return state, errors.New("Agent runtime state is empty")
	}
	if err := json.Unmarshal([]byte(run.StateJSON), &state); err != nil {
		return state, fmt.Errorf("decode Agent runtime state: %w", err)
	}
	if err := validateCloudAgentRuntime(run, &state); err != nil {
		return state, err
	}
	return state, nil
}

// cloudAgentDecodeForExecution is the only decoder for paths that may resume
// model/tool execution. Historical reads intentionally use cloudAgentDecode:
// a completed turn keeps its frozen policy and capability snapshot and remains
// valid conversation context after the current runtime contract advances.
func cloudAgentDecodeForExecution(run *model.CloudAgentExecution) (cloudAgentRuntime, error) {
	state, err := cloudAgentDecode(run)
	if err != nil {
		return state, WrapAppError(409, "Agent 运行记录无法安全恢复；请新建一轮消息", err)
	}
	if err := validateCloudAgentPolicySnapshot(state.Policy); err != nil {
		return state, WrapAppError(409, "Agent 运行使用旧版执行合同，无法继续原运行；请新建一轮消息", err)
	}
	return state, nil
}

func validateCloudAgentRuntime(run *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	if run == nil || state == nil {
		return errors.New("Agent runtime state is missing")
	}
	if run.ID == "" {
		// Package-level tool tests use an in-memory runtime without a durable
		// execution identity. Durable rows are always validated below.
		return nil
	}
	if state.Request.CanvasID == "" || state.Request.Prompt == "" || state.Request.PermissionMode == "" {
		return errors.New("Agent runtime request is incomplete")
	}
	if err := validateCloudAgentRequest(&state.Request); err != nil {
		return fmt.Errorf("invalid Agent runtime request: %w", err)
	}
	if err := validateCloudAgentPolicySnapshotStructure(state.Policy); err != nil {
		return err
	}
	if err := validateCloudAgentProfileSnapshot(state.Profile, state.Policy); err != nil {
		return err
	}
	for scope, read := range state.ProfileReads {
		found := false
		for _, layer := range state.Profile.Layers {
			found = found || layer.Scope == scope
		}
		if !read || !found {
			return errors.New("Agent runtime profile read history is invalid")
		}
	}
	if state.Step < 0 || state.Generations < 0 || state.VideoSeconds < 0 {
		return errors.New("Agent runtime budget or step is invalid")
	}
	if (state.Request.Budget.MaxGenerationTasks > 0 && state.Generations > state.Request.Budget.MaxGenerationTasks) || (state.Request.Budget.MaxVideoSeconds > 0 && state.VideoSeconds > state.Request.Budget.MaxVideoSeconds) {
		return errors.New("Agent runtime generation budget is invalid")
	}
	if state.CallIndex < 0 || state.CallIndex > len(state.Calls) || len(state.Calls) > cloudAgentMaxToolCalls {
		return errors.New("Agent runtime call cursor is invalid")
	}
	if state.ActiveTaskID != "" && state.MediaTaskID != "" {
		return errors.New("Agent runtime has multiple active tasks")
	}
	if state.ContextCompaction != nil {
		if state.ContextCompaction.Status != "requested" && state.ContextCompaction.Status != "running" {
			return errors.New("Agent context compaction state is invalid")
		}
		if state.ContextCompaction.SourceBytes < 0 || state.ContextCompaction.TurnCount < 0 {
			return errors.New("Agent context compaction budget is invalid")
		}
		if state.ContextCompaction.Status == "requested" && state.ActiveTaskID != "" {
			return errors.New("Agent context compaction request has an active task")
		}
		if state.ContextCompaction.Status == "running" && state.ActiveTaskID == "" {
			return errors.New("Agent context compaction task is missing")
		}
	}
	if state.HistoryIncludesCurrent && state.ContextCheckpoint == nil {
		return errors.New("Agent compacted history is missing its checkpoint")
	}
	if state.ContextCheckpoint != nil {
		if _, err := agentcontext.Frame(*state.ContextCheckpoint); err != nil {
			return errors.New("Agent context checkpoint is invalid")
		}
	}
	if len(state.TaskIDs) == 0 {
		return errors.New("Agent runtime task history is invalid")
	}
	seenTasks := make(map[string]struct{}, len(state.TaskIDs))
	for _, taskID := range state.TaskIDs {
		if err := validateCloudAgentID(taskID, "任务 ID", 80); err != nil {
			return err
		}
		if _, exists := seenTasks[taskID]; exists {
			return errors.New("Agent runtime task history contains duplicates")
		}
		seenTasks[taskID] = struct{}{}
	}
	if state.ActiveTaskID != "" && !cloudAgentContainsString(state.TaskIDs, state.ActiveTaskID) {
		return errors.New("Agent runtime active task is not in task history")
	}
	if state.MediaTaskID != "" {
		if !cloudAgentContainsString(state.TaskIDs, state.MediaTaskID) || state.CallIndex >= len(state.Calls) || state.Calls[state.CallIndex].Function.Name != "generate_media" {
			return errors.New("Agent runtime media task is not attached to current call")
		}
	}
	if state.Decisions == nil || state.Events == nil {
		return errors.New("Agent runtime maps are missing")
	}
	if state.EventSeqBase < 0 || state.EventFlushedSeq < state.EventSeqBase {
		return errors.New("Agent runtime event watermark is invalid")
	}
	// 事件全量在 cloud_agent_run_events，状态里只留尾部缓存。
	if len(state.Events) > cloudAgentEventTailSanityLimit {
		return errors.New("Agent runtime event tail is too large")
	}
	for index, event := range state.Events {
		if event.RunID != run.ID || event.Seq != state.EventSeqBase+index+1 || event.EventID == "" || event.Type == "" || event.Payload == nil || event.CreatedAt.IsZero() {
			return errors.New("Agent runtime event history is invalid")
		}
		if err := validateCloudAgentID(event.EventID, "事件 ID", 240); err != nil {
			return err
		}
		raw, err := json.Marshal(event.Payload)
		if err != nil || len(raw) > cloudAgentEventPayloadLimitBytes {
			return errors.New("Agent runtime event payload is too large")
		}
	}
	for _, call := range state.Calls {
		if err := validateCloudAgentID(call.ID, "工具调用 ID", 160); err != nil || call.Function.Name == "" || utf8.RuneCountInString(call.Function.Name) > 80 || !utf8.ValidString(call.Function.Name) {
			return errors.New("Agent runtime tool call is invalid")
		}
		if len(call.Function.Arguments) > 32000 {
			return errors.New("Agent runtime tool arguments are too large")
		}
		if err := decodeCloudAgentJSONObject(call.Function.Arguments, &map[string]any{}); err != nil {
			return errors.New("Agent runtime tool arguments are invalid")
		}
	}
	if state.Approval != nil {
		if err := validateCloudAgentID(state.Approval.ID, "审批 ID", 200); err != nil || state.CallIndex >= len(state.Calls) {
			return errors.New("Agent runtime approval is invalid")
		}
		current := state.Calls[state.CallIndex]
		if state.Approval.Call.ID != current.ID || state.Approval.Call.Function.Name != current.Function.Name || state.Approval.Call.Function.Arguments != current.Function.Arguments {
			return errors.New("Agent runtime approval does not match current call")
		}
		if state.Approval.Decision != "" && state.Approval.Decision != "approve" && state.Approval.Decision != "reject" {
			return errors.New("Agent runtime approval decision is invalid")
		}
	}
	if state.CallIndex == len(state.Calls) && state.Approval != nil {
		return errors.New("Agent runtime has approval without a pending call")
	}
	return nil
}

func validateCloudAgentPolicySnapshot(snapshot cloudAgentPolicySnapshot) error {
	if err := validateCloudAgentPolicySnapshotStructure(snapshot); err != nil {
		return err
	}
	if snapshot.CompilerVersion != cloudAgentCompilerVersion {
		return errors.New("Agent runtime policy compiler is unsupported")
	}
	if snapshot.CapabilitySetVersion != cloudAgentCapabilitySetVersion {
		return errors.New("Agent runtime capability contract is unsupported")
	}
	if snapshot.ReasoningMode != "off" && snapshot.ReasoningMode != "auto" && snapshot.ReasoningMode != "deep" {
		return errors.New("Agent runtime reasoning mode is invalid")
	}
	system, media, err := prompts.LoadAgentPolicies()
	if err != nil {
		return fmt.Errorf("load Agent runtime policies: %w", err)
	}
	if snapshot.SystemPolicyID != system.ID || snapshot.SystemPolicyVersion != system.Version || snapshot.MediaPolicyID != media.ID || snapshot.MediaPolicyVersion != media.Version {
		return errors.New("Agent runtime policy version is unsupported")
	}
	// An existing run keeps its admitted prompt and tool schema. Never resume it
	// against changed policies or capabilities under an unchanged version label.
	if snapshot.SystemPolicyHash != system.Hash || snapshot.MediaPolicyHash != media.Hash || snapshot.CapabilitySetHash != cloudAgentCapabilitySetHash() {
		return errors.New("Agent runtime policy or capability contract has changed")
	}
	return nil
}

// validateCloudAgentPolicySnapshotStructure validates the frozen historical
// record without treating the current binary as its authorization source.
// Version/hash equality with the current runtime is checked separately, only
// when an old run is about to execute again.
func validateCloudAgentPolicySnapshotStructure(snapshot cloudAgentPolicySnapshot) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{"policy compiler", snapshot.CompilerVersion},
		{"system policy ID", snapshot.SystemPolicyID},
		{"media policy ID", snapshot.MediaPolicyID},
		{"capability set version", snapshot.CapabilitySetVersion},
	} {
		if strings.TrimSpace(field.value) == "" || !utf8.ValidString(field.value) || utf8.RuneCountInString(field.value) > 120 {
			return fmt.Errorf("Agent runtime %s is invalid", field.name)
		}
	}
	if snapshot.SystemPolicyVersion <= 0 || snapshot.MediaPolicyVersion <= 0 {
		return errors.New("Agent runtime policy version is invalid")
	}
	if snapshot.ReasoningMode != "off" && snapshot.ReasoningMode != "auto" && snapshot.ReasoningMode != "deep" {
		return errors.New("Agent runtime reasoning mode is invalid")
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"system policy hash", snapshot.SystemPolicyHash},
		{"media policy hash", snapshot.MediaPolicyHash},
		{"capability set hash", snapshot.CapabilitySetHash},
		{"profile revision", snapshot.ProfileRevision},
		{"profile hash", snapshot.ProfileHash},
	} {
		if !cloudAgentSHA256(field.value) {
			return fmt.Errorf("Agent runtime %s is invalid", field.name)
		}
	}
	return nil
}

func cloudAgentSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func cloudAgentContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func cloudAgentSave(run *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	if run == nil || state == nil {
		return errors.New("Agent runtime state is missing")
	}
	// 事件日志瘦身：删掉重复/已被取代的载荷（不动 seq 与 EventID）。
	// 它必须在下一次 marshal 之前跑，否则 512KB 守卫会先一步把整轮判死。
	cloudAgentSlimEventHistory(state, run.Status != "running" && run.Status != "queued")
	if evicted, before, after := compactCloudAgentContext(&state.Canonical, state.cloudAgentVisualNotes()); evicted {
		state.SkillReads = nil
		state.ProfileReads = nil
		// 治理动作必须留痕：这条路径过去是静默的，实测全库 context_evicted 事件数为 0，
		// 于是"到底卸载过没有"在运行记录里根本查不到。
		if run.ID != "" {
			state.event(run.ID, "context_evicted", map[string]any{
				"messagesBytesBefore": before, "messagesBytesAfter": after,
				"thresholdBytes": cloudAgentEvictionThresholdBytes, "messageLimit": cloudAgentEvictionMessageLimit,
				"text": "已移出可重新读取的历史工具正文（不含模型调用），需要时重新读取",
			})
		}
	}
	// 已入库的前缀可以安全丢掉（内容在 cloud_agent_run_events 里）；尚未入库的事件先留在状态里，
	// 由下一次转移开头的 flush 入库后再裁 —— 顺序保证不会丢审计。
	cloudAgentTrimFlushedEvents(state)
	if run.ID != "" {
		if err := validateCloudAgentRuntime(run, state); err != nil {
			return fmt.Errorf("%w: %v", errCloudAgentCheckpoint, err)
		}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("%w: %v", errCloudAgentCheckpoint, err)
	}
	// 体积逼近上限时按级降级事件载荷（只动载荷，不动 seq/eventId），
	// 让长流程"变淡"而不是"突然死"；只有降到极致仍超限才终止本轮。
	for level := 0; ; {
		needed := cloudAgentEventHistoryDegradeLevel(len(raw))
		if needed <= level {
			break
		}
		if !cloudAgentDegradeEventHistory(state, needed) {
			break
		}
		level = needed
		if raw, err = json.Marshal(state); err != nil {
			return fmt.Errorf("%w: %v", errCloudAgentCheckpoint, err)
		}
	}
	// 单步增长预算：这一步涨得太猛时就地折叠最旧的画布增量（客户端会改为拉全量），
	// 避免"某一步突然跳几十 KB"把状态直接顶过硬上限。
	if cloudAgentFoldEventPayloadsToBudget(state, len(run.StateJSON)) {
		if raw, err = json.Marshal(state); err != nil {
			return fmt.Errorf("%w: %v", errCloudAgentCheckpoint, err)
		}
	}
	if len(raw) > cloudAgentStateHardLimitBytes {
		return fmt.Errorf("%w: Agent 状态超过 512KB 上限", errCloudAgentCheckpoint)
	}
	run.CanvasID, run.ActiveTaskID, run.MediaTaskID = state.Request.CanvasID, state.ActiveTaskID, state.MediaTaskID
	run.StateJSON = string(raw)
	return nil
}
func (s *Service) cloudAgentExecutionOutput(task *model.Task, initial cloudAgentState, options ...CloudAgentRunViewOptions) (*CloudAgentRun, error) {
	run, err := s.repo.CloudAgent(task.UserID, task.ID)
	if err != nil {
		return nil, err
	}
	state, stateErr := cloudAgentDecode(run)
	if stateErr != nil {
		// A terminal failed run must remain readable even if its durable runtime
		// blob was damaged. Do not invent permissions or approval state; expose
		// only the identity available from the original task input.
		state = cloudAgentRuntime{Request: initial.Request, ParentID: initial.ParentID, CreativeAnchor: initial.CreativeAnchor, Skills: initial.Skills, Profile: initial.Profile, TaskIDs: []string{task.ID}, Events: []CloudAgentEvent{}}
	}
	out := agentRunOutput(task, initial)
	out.Status = run.Status
	out.Revision, out.CleanupPending, out.FailureMessage = run.Revision, run.CleanupPending, run.FailureMessage
	out.UpdatedAt = run.UpdatedAt
	view := CloudAgentRunViewOptions{}
	if len(options) > 0 {
		view = options[0]
	}
	// 事件已全量落库：默认返回最近一页（尾部缓存 + 事件表补齐），sinceSeq 只取增量。
	out.Events = s.cloudAgentRunEventsForView(task.UserID, run, &state, view.SinceSeq, view.EventLimit)
	out.EventSeqBase = state.EventSeqBase
	out.EventCount = s.cloudAgentRunEventCount(task.UserID, run, &state)
	if len(out.Events) > 0 {
		out.LatestSeq = out.Events[len(out.Events)-1].Seq
	}
	if len(out.Events) < out.EventCount && view.SinceSeq == 0 {
		out.EventsTruncated = true
	}
	out.Approval = state.Approval
	if cloudAgentRunTerminal(run.Status) {
		out.Approval = nil
	}
	out.Step = state.Step
	// 压缩期间把压缩态暴露给界面：步进循环是暂停的，用户要能看到"正在压缩"而不是以为卡住了。
	out.ContextCompaction = state.ContextCompaction
	if stateErr == nil && state.ActiveTaskID != "" && state.ContextCompaction == nil && (run.Status == "running" || run.Status == "queued") {
		active, err := s.repo.TaskForUser(task.UserID, state.ActiveTaskID)
		if err != nil {
			return nil, err
		}
		if active.TextDraft != "" {
			out.ActiveMessage = map[string]string{"messageId": active.ID, "text": active.TextDraft}
		}
	}
	out.Skills = make([]cloudAgentSkill, 0, len(state.Skills))
	for _, skill := range state.Skills {
		skill.Instruction = ""
		skill.Files = nil
		out.Skills = append(out.Skills, skill)
	}
	orders, err := s.repo.BillingOrdersByTaskIDs(task.UserID, state.TaskIDs)
	if err != nil {
		return nil, err
	}
	for _, order := range orders {
		out.SpentCredits += float64(order.AmountMicrocredits) / float64(CreditScale)
	}
	return out, nil
}

// Runs one bounded transition at a time; no model HTTP call or approval wait holds a DB lock.
func (s *Service) advanceCloudAgentByID(userID, id string) error {
	// Do not decode the task input/runtime before checking for an existing
	// execution. A damaged runtime must be terminally recoverable, not
	// accidentally replaced by a fresh execution row.
	task, err := s.repo.TaskForUser(userID, id)
	if err != nil {
		return err
	}
	if task.Operation != cloudAgentOperation {
		return kernel.NotFound("Agent 运行不存在")
	}
	run, lookupErr := s.repo.CloudAgent(userID, id)
	if errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		_, initial, taskErr := s.cloudAgentTask(userID, id)
		if taskErr != nil {
			return taskErr
		}
		if err := s.ensureCloudAgentExecution(task, initial); err != nil {
			return err
		}
		run, lookupErr = s.repo.CloudAgent(userID, id)
	}
	if lookupErr != nil {
		return lookupErr
	}
	return s.advanceCloudAgent(run)
}
func (s *Service) advanceCloudAgents() {
	s.agentSchedulerMu.Lock()
	defer s.agentSchedulerMu.Unlock()
	s.purgeCloudAgentRunEvents()
	roots, err := s.repo.CloudAgentRoots()
	if err != nil {
		log.Printf("agent recovery: %v", err)
		return
	}
	for _, task := range roots {
		_, state, e := s.cloudAgentTask(task.UserID, task.ID)
		if e == nil {
			e = s.ensureCloudAgentExecution(&task, state)
		}
		if e != nil {
			log.Printf("agent recovery %s: %v", task.ID, e)
		}
	}
	runs, err := s.repo.ActiveCloudAgentsAfter(s.agentSchedulerCursor, 50)
	if err == nil && len(runs) == 0 && s.agentSchedulerCursor != "" {
		s.agentSchedulerCursor = ""
		runs, err = s.repo.ActiveCloudAgentsAfter("", 50)
	}
	if err != nil {
		log.Printf("agent scheduler: %v", err)
		return
	}
	for i := range runs {
		s.agentSchedulerCursor = runs[i].ID
		if s.terminateStuckCloudAgent(&runs[i]) {
			continue
		}
		err = s.advanceCloudAgent(&runs[i])
		if err == nil {
			s.clearCloudAgentSchedulerConflict(runs[i].ID)
			continue
		}
		if errors.Is(err, repository.ErrCreationConflict) {
			s.noteCloudAgentSchedulerConflict(runs[i].ID)
			continue
		}
		log.Printf("agent transition %s: %v", runs[i].ID, err)
	}
}

func (s *Service) advanceCloudAgent(run *model.CloudAgentExecution) (err error) {
	defer func() {
		if errors.Is(err, errCloudAgentCheckpoint) {
			// 具体原因（状态校验失败 / 超过 512KB / 上下文检查点错误）必须留痕：否则线上只能
			// 看到一句"超过安全限制"，无法定位是哪一类问题。用户可见文案保持不变。
			log.Printf("agent checkpoint failure %s: %v", run.ID, err)
			err = s.terminateCloudAgent(run, "Agent 上下文或执行记录超过安全限制，本轮已停止；已有任务结果保留在任务中心")
		}
	}()
	if run.CleanupPending {
		return s.finishCloudAgentCleanup(context.Background(), run)
	}
	if run.Status != "running" && run.Status != "queued" {
		return nil
	}
	state, err := cloudAgentDecodeForExecution(run)
	if err != nil {
		var appErr *AppError
		if errors.As(err, &appErr) && appErr != nil {
			return s.terminateCloudAgent(run, appErr.Message)
		}
		return s.terminateCloudAgent(run, "Agent 运行状态损坏，本轮已停止")
	}
	// 事件全量入库（与状态解耦）：先把上一步产生的事件写进 cloud_agent_run_events，
	// 再把状态里的事件裁剪到尾部缓存。失败只留痕、不阻断运行 —— 尾巴仍在状态里，下次再试。
	s.cloudAgentFlushRunEventsLogged(run, &state)
	if state.ActiveTaskID != "" {
		task, err := s.repo.TaskForUser(run.UserID, state.ActiveTaskID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return s.terminateCloudAgent(run, "Agent 模型任务已不存在，本轮已停止")
		}
		if err != nil {
			return err // Transient database failures must not terminate a live task.
		}
		if state.ContextCompaction != nil {
			return s.advanceCloudAgentContextCompaction(run, &state, task)
		}
		if task.Status == model.TaskStatusQueued || task.Status == model.TaskStatusRunning {
			// 将已持久化的模型增量转成 Agent 事件；不拆分完整答案伪装成流式。
			if task.TextDraft != state.ActiveTextDraft {
				delta := ""
				eventType := "assistant_snapshot"
				payload := map[string]any{"messageId": task.ID, "text": task.TextDraft, "replace": true}
				if strings.HasPrefix(task.TextDraft, state.ActiveTextDraft) {
					delta = strings.TrimPrefix(task.TextDraft, state.ActiveTextDraft)
					if delta == "" {
						return nil
					}
					eventType = "assistant_delta"
					payload = map[string]any{"messageId": task.ID, "text": delta}
				}
				if err := s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
					state.event(run.ID, eventType, payload)
					state.ActiveTextDraft = task.TextDraft
					return cloudAgentSave(current, &state)
				}); err != nil {
					return err
				}
			}
			return nil
		}
		var result struct {
			Text      string           `json:"text"`
			Reasoning string           `json:"reasoning"`
			ToolCalls []cloudAgentCall `json:"toolCalls"`
			Legacy    []cloudAgentCall `json:"tool_calls"`
		}
		if task.Status == model.TaskStatusSucceeded {
			if err := json.Unmarshal([]byte(task.ResultJSON), &result); err != nil {
				return s.terminateCloudAgent(run, "模型任务结果损坏，本轮已停止")
			}
			calls := result.ToolCalls
			if len(calls) == 0 {
				calls = result.Legacy
			}
			if violation := cloudAgentOutputViolation(result.Text, len(calls)); violation != "" {
				return s.correctCloudAgentOutput(run, &state, violation)
			}
			if err := validateCloudAgentCalls(calls); err != nil {
				return s.correctCloudAgentOutput(run, &state, "工具调用无效或重复（callId 不能重复、参数必须是 JSON 对象）")
			}
			result.ToolCalls = calls
		}
		if task.Status != model.TaskStatusSucceeded && cloudAgentTruncatedToolArguments(task) {
			return s.correctCloudAgentTruncatedCalls(run, &state)
		}
		if cloudAgentEmptyModelOutput(task) {
			if state.EmptyOutputNudged < cloudAgentMaxEmptyOutputNudges {
				return s.correctCloudAgentEmptyOutput(run, &state)
			}
			// 催过仍然空：改为"关思考 + 放大输出预算"重试同一步，而不是把整轮判死。
			if state.EmptyOutputEscalated < cloudAgentMaxEmptyOutputEscalations {
				return s.correctCloudAgentEmptyOutputEscalation(run, &state)
			}
		}
		return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
			if task.Status != model.TaskStatusSucceeded {
				current.Status = "failed"
				text, reason := cloudAgentModelFailure(task)
				current.FailureMessage = truncateRunes(text, 1000)
				cloudAgentDropInterjections(run.ID, "本轮已结束："+truncateRunes(text, 120), &state)
				state.event(run.ID, "run_failed", map[string]any{"text": text, "reason": reason, "taskId": task.ID})
				return cloudAgentSave(current, &state)
			}
			calls := result.ToolCalls
			if result.Reasoning != "" {
				state.event(run.ID, "reasoning_message", map[string]any{"messageId": task.ID + ":reasoning", "text": truncateRunes(result.Reasoning, 8000)})
			}
			if result.Text != "" {
				state.event(run.ID, "assistant_message", map[string]any{"messageId": task.ID, "text": result.Text})
				// 刚看过图时，模型在正文里写下的那句话就是可复用的视觉证据：
				// 推理内容不回灌上下文，只有正文留得下来。
				state.recordCloudAgentVisualNote(result.Text)
				if len(calls) == 0 {
					state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "assistant", "content": result.Text})
				}
			}
			state.ActiveTaskID = ""
			if len(calls) > 0 || strings.TrimSpace(result.Text) != "" {
				state.EmptyOutputNudged = 0
			}
			state.Canonical.ToolChoice = "auto"
			state.Calls = calls
			state.CallIndex = 0
			// 新的一批：清空上一批的画布版本记录，并固定本步读取时的画布版本。
			state.CanvasBatchHashes = nil
			state.StepSnapshotHash = cloudAgentCaptureStepSnapshotHash(calls)
			if len(calls) > 0 {
				state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "assistant", "content": result.Text, "tool_calls": calls})
			}
			if len(calls) == 0 {
				if len(state.PendingInterjections) > 0 {
					if !cloudAgentStepBudgetExhausted(&state) {
						return cloudAgentSave(current, &state)
					}
					cloudAgentDropInterjections(run.ID, "本轮已达到模型调用上限", &state)
				}
				if !cloudAgentStepBudgetExhausted(&state) && !state.ActionNudged {
					if pending := cloudAgentPendingPlanItems(state.Plan); len(pending) > 0 {
						state.ActionNudged = true
						state.Canonical.Messages = append(state.Canonical.Messages, cloudAgentPlanNudgeMessage(&state, pending[0]))
						return cloudAgentSave(current, &state)
					}
				}
				if needed, sourceBytes, turnCount, reading, hasReading := cloudAgentCompactionDecision(repo, &state, state.Canonical); needed {
					state.ContextCompaction = &cloudAgentContextCompaction{
						Status: "requested", SourceBytes: sourceBytes, TurnCount: turnCount,
						ProjectedTokens: reading.ProjectedTokens, UsableInputTokens: reading.UsableInputTokens,
						Ratio: reading.Ratio, TokenSource: reading.TokenSource,
					}
					state.event(run.ID, "context_compaction_requested", cloudAgentCompactionEventPayload(reading, hasReading, sourceBytes, turnCount))
				} else {
					current.Status = "completed"
				}
			}
			return cloudAgentSave(current, &state)
		})
	}
	if state.CallIndex < len(state.Calls) {
		return s.advanceCloudAgentTool(run, &state)
	}
	if state.ContextCompaction != nil && state.ContextCompaction.Status == "requested" {
		return s.enqueueCloudAgentContextCompaction(run, &state)
	}
	// 正文卸载与它的事件都在 cloudAgentSave 里统一处理（同一次变更只留一条记录）。
	if evicted, _, _ := compactCloudAgentContext(&state.Canonical, state.cloudAgentVisualNotes()); evicted {
		// Evicted read bodies must be obtainable again after compaction.
		state.SkillReads = nil
		state.ProfileReads = nil
	}
	if stepLimit := cloudAgentStepLimit(state.Request); stepLimit > 0 && state.Step >= stepLimit {
		return s.failCloudAgent(run, &state, fmt.Sprintf("达到 %d 次模型调用上限，本轮已停止", stepLimit))
	}
	cloudAgentDrainInterjections(run.ID, &state)
	// 上一步的模型调用已经回来，先用它的上游实测用量更新压力锚点，再发下一步。
	s.recordCloudAgentTokenAnchor(&state)
	canonical := cloudAgentCanonicalWithPlan(&state)
	s.attachCloudAgentLessons(&canonical, run.UserID, cloudAgentLessonTaskText(&state))
	cloudAgentRecordMemorySegment(&state.Policy, canonical.SystemPrompt)
	// 上下文利用率达标（默认 80% 用户配置的模型上限，上游实测 token 口径）就先暂停步进：
	// 把历史压成检查点，压完再用压缩后的上下文继续本轮，而不是带着超高占用再发一次请求。
	if requested, err := s.cloudAgentRequestCompaction(run, &state, canonical); err != nil || requested {
		return err
	}
	// 空输出升级重试：关思考 + 放大输出预算，避免"思考吃满预算、正文为空"再次发生。
	stepThinking := cloudAgentReasoningEnabled(state.Policy.ReasoningMode) && !state.ForceThinkingOff
	stepOutputTokens := cloudAgentStepMaxOutputTokens
	if state.BoostStepOutputBudget {
		stepOutputTokens = cloudAgentStepEscalatedMaxOutputTokens
	}
	input := map[string]any{"mode": "text", "prompt": state.Request.Prompt, "agentRequests": map[string]any{"canonical": canonical}, "config": map[string]any{"channelId": state.Request.ChannelID, "channelModelKey": state.Request.ChannelModelKey, "model": firstNonEmpty(state.Request.ChannelModelKey, state.Request.Model)}, "textOptions": map[string]any{"stream": true, "thinking": stepThinking, "maxOutputTokens": stepOutputTokens}}
	raw, _ := json.Marshal(canonical)
	if len(raw) > cloudAgentRequestHardLimitBytes {
		return s.failCloudAgent(run, &state, "模型上下文超过 192KB 上限")
	}
	req := CreateTaskRequest{ProjectID: state.Request.CanvasID, Type: "canvas_text", Operation: cloudAgentStepOperation, Prompt: state.Request.Prompt, Model: state.Request.Model, LogicalModelID: state.Request.LogicalModelID, Input: input}
	return s.enqueueCloudAgentTask(run, &state, req, nil)
}

// The deterministic body eviction is the cheap lever: it removes re-readable
// bodies with no model call. Its threshold therefore sits below the semantic
// compaction threshold and both judge the same object — the conversation
// messages — so the expensive path only runs after the cheap one had its
// chance. Judging the whole canonical here made a 36 KiB read look safe at
// 0.91x while the semantic path was already past its own threshold.
// cloudAgentStepOperation 是一次"模型调用"任务的操作名；只有它会与上游实测配锚点
// （压缩任务发的是另一份请求，拿它当锚点会把压力算到错误的信封上）。
const cloudAgentStepOperation = "cloud_agent_step"

const (
	// cloudAgentEventTailLimit 是状态里保留的事件条数"裁剪目标"：事件已全量落库，
	// 状态只带"最近这么多条"供运行视图首屏、续轮摘要、压缩事实与记忆提取直接读。
	cloudAgentEventTailLimit = 40
	// cloudAgentEventTailSanityLimit 是校验用的健全上限（明显高于裁剪目标）：一次转移里会追加
	// 若干事件，裁剪发生在转移开始时，所以状态里短期超过裁剪目标是正常的；这里只拦真正的异常。
	cloudAgentEventTailSanityLimit = 256
	// cloudAgentRunEventPageLimit 是运行详情默认返回的事件条数（尾部缓存之外再从事件表补齐）。
	cloudAgentRunEventPageLimit = 100

	cloudAgentEvictionThresholdBytes = 40 << 10
	cloudAgentEvictionMessageLimit   = 24
	cloudAgentRequestHardLimitBytes  = 192 << 10
)

// cloudAgentStepMaxOutputTokens 是单步模型调用的输出上限（思考 + 正文 + 工具调用参数）。
//
// 不设上限时上游按"剩余上下文"放行：实测部署是 262144 上下文的思考模型，
// 解码约 28 tok/s，一次跑满就是几分钟——那一轮 892s 里有 324s 是用户等一个
// 无限思考的调用直到手动取消。
//
// 取值依据：实测正常步骤输出 65–3306 tok，但整合四张图那种重规划步骤会顶到 4096
// 并被截断（截断后要再花一次往返补救），所以留出余量取 6144（最坏单步 ≈ 220s，
// 仍然是有限上界，而不是靠用户手动取消）。
const cloudAgentStepMaxOutputTokens = 6144

// cloudAgentStepEscalatedMaxOutputTokens 是"空输出升级重试"用的输出预算：思考模型在 6144 下
// 偶发把预算全花在推理上（实测 output_tokens 正好等于 6144、正文为空、连续三次），
// 这一档同时关思考并放大预算，让同一步有机会产出正文或工具调用。
const cloudAgentStepEscalatedMaxOutputTokens = 16384

// compactCloudAgentContext reports whether it changed anything, plus the
// conversation-message size before and after, for the observability event.
// notes 是 nodeID → 模型自己写下的观察：图片被移出上下文时用它替代像素。
func compactCloudAgentContext(request *canonicalAgentRequest, notes map[string]string) (bool, int, int) {
	if request == nil {
		return false, 0, 0
	}
	// 图片先按保留窗口裁剪：它是"看过就够了"的内容，不该等到超阈值才处理。
	prunedImages := cloudAgentPruneInspectedImages(request, notes)
	raw, err := json.Marshal(request.Messages)
	if err != nil {
		return prunedImages, 0, 0
	}
	before := len(raw)
	if before < cloudAgentEvictionThresholdBytes && len(request.Messages) <= cloudAgentEvictionMessageLimit {
		return prunedImages, before, before
	}
	// Retain the latest complete tool turn. Never remove call/result envelopes,
	// user instructions, call arguments or write receipts to fabricate a summary.
	cut := len(request.Messages) - 1
	for cut > 0 && stringField(request.Messages[cut], "role") == "tool" {
		cut--
	}
	// 技能正文不可卸载：它是可复用却不可再生的任务剧本，卸掉之后模型只能重新读取，
	// 形成"读了被吞、再读"的循环（实测一次运行里同一个 SKILL.md 被读了 5 次）。
	toolNames := cloudAgentToolNamesByCallID(request.Messages)
	changed := false
	for _, message := range request.Messages[:max(0, cut)] {
		if stringField(message, "role") != "tool" {
			continue
		}
		if toolNames[stringField(message, "tool_call_id")] == "skill_read_file" {
			continue
		}
		var result map[string]any
		if json.Unmarshal([]byte(stringField(message, "content")), &result) != nil || result["contextCompacted"] == true {
			continue
		}
		// Only omit re-readable bodies. Preserve IDs, errors, generation status,
		// approvals and all other structured facts verbatim.
		if !cloudAgentEvictResultBody(result) {
			continue
		}
		result["contextCompacted"] = true
		result["guidance"] = cloudAgentEvictionGuidance
		body, err := json.Marshal(result)
		if err != nil || len(body) >= len(stringField(message, "content")) {
			continue
		}
		message["content"] = string(body)
		changed = true
	}
	if !changed {
		return prunedImages, before, before
	}
	if after, err := json.Marshal(request.Messages); err == nil {
		return true, before, len(after)
	}
	return true, before, before
}

// cloudAgentTrimFlushedEvents 丢掉状态里"已经入库"的事件前缀并推进序号水位。
// 只动 Seq <= EventFlushedSeq 的部分，因此不可能丢掉还没写进事件表的记录。
func cloudAgentTrimFlushedEvents(state *cloudAgentRuntime) {
	if state == nil {
		return
	}
	// 保留最近 cloudAgentEventTailLimit 条（无论是否已入库），只丢弃"超出目标且已入库"的前缀：
	// 这样既不会把还没写进事件表的事件丢掉，也不会让状态无谓地留着完整历史。
	excess := len(state.Events) - cloudAgentEventTailLimit
	if excess <= 0 {
		return
	}
	drop := 0
	for drop < excess && state.Events[drop].Seq <= state.EventFlushedSeq {
		drop++
	}
	if drop == 0 {
		return
	}
	// 必须保持非 nil：空切片代表"事件都在表里"，nil 会被校验当成状态损坏。
	state.Events = append(make([]CloudAgentEvent, 0, len(state.Events)-drop), state.Events[drop:]...)
	state.EventSeqBase += drop
}

func validateCloudAgentCalls(calls []cloudAgentCall) error {
	seen := make(map[string]bool, len(calls))
	for _, call := range calls {
		if err := validateCloudAgentID(call.ID, "工具调用 ID", 160); err != nil || seen[call.ID] || call.Function.Name == "" || len(call.Function.Name) > 80 || !utf8.ValidString(call.Function.Name) {
			return errors.New("invalid Agent tool call")
		}
		if err := decodeCloudAgentJSONObject(call.Function.Arguments, &map[string]any{}); err != nil || len(call.Function.Arguments) > 32000 {
			return errors.New("invalid Agent tool arguments")
		}
		seen[call.ID] = true
	}
	return nil
}

func (s *Service) terminateCloudAgent(run *model.CloudAgentExecution, message string) error {
	if run == nil {
		return errors.New(message)
	}
	if err := s.repo.MarkCloudAgentFailed(run.UserID, run.ID, run.Revision, message); err != nil && !errors.Is(err, repository.ErrCreationConflict) {
		return err
	}
	return nil
}

func (s *Service) failCloudAgent(run *model.CloudAgentExecution, state *cloudAgentRuntime, message string) error {
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		current.Status = "failed"
		current.FailureMessage = truncateRunes(message, 1000)
		cloudAgentDropInterjections(run.ID, "本轮已结束："+truncateRunes(message, 120), state)
		state.event(run.ID, "run_failed", map[string]any{"text": message})
		return cloudAgentSave(current, state)
	})
}

// failCloudAgentAdmission records deterministic tool admission failures in the
// same checkpoint transaction. Transient repository errors must still escape
// the caller so the scheduler can retry them.
func failCloudAgentAdmission(current *model.CloudAgentExecution, state *cloudAgentRuntime, runID string, err error) error {
	message := cloudAgentSafeToolError(err)
	current.Status = "failed"
	current.FailureMessage = message
	state.Approval = nil
	state.event(runID, "run_failed", map[string]any{
		"text":   message,
		"reason": "tool_admission_failed",
	})
	return cloudAgentSave(current, state)
}

// Only expose known failure categories; raw provider errors can contain URLs and credentials.
func cloudAgentModelFailure(task *model.Task) (string, string) {
	detail, reason := "模型任务未成功", "model_task_failed"
	raw := strings.ToLower(task.Error)
	switch {
	case strings.Contains(task.Error, "没有返回内容"):
		// 思考模型的典型失败：整个输出预算被推理吃掉，正文与工具调用皆空。
		detail, reason = "上游连续返回空内容（通常是思考占满输出预算）；已自动关思考并放大预算重试仍失败，建议换用非思考模型或调小上下文", "model_empty_output"
	case strings.Contains(raw, "connection reset by peer"):
		detail, reason = "模型连接被对端或中间网络设备重置", "model_connection_reset"
	case strings.Contains(raw, "timeout"), strings.Contains(raw, "deadline exceeded"):
		detail, reason = "模型请求超时", "model_request_timeout"
	case strings.Contains(raw, "connection refused"):
		detail, reason = "无法连接模型服务（连接被拒绝）", "model_connection_refused"
	}
	return detail + "；本轮已停止。请在任务中心检查模型任务 " + task.ID, reason
}

func cloudAgentSafeToolError(err error) string {
	if err == nil {
		return ""
	}
	var appErr *AppError
	if errors.As(err, &appErr) && appErr != nil {
		message := strings.TrimSpace(appErr.Message)
		if cloudAgentSafeUserMessage(message) {
			return message
		}
	}
	return "工具执行失败，请检查输入或稍后重试"
}

func cloudAgentSafeUserMessage(message string) bool {
	if message == "" || !utf8.ValidString(message) || strings.ContainsAny(message, "\x00\r\n") || utf8.RuneCountInString(message) > 240 {
		return false
	}
	lower := strings.ToLower(message)
	for _, marker := range []string{
		"http://", "https://", "ftp://", "file://", "authorization", "cookie", "secret", "token", "api_key", "apikey", "x-api-key",
		"/var/", "/tmp/", "\\", "stack trace", "traceback", " at ", "sql:", "sqlite", "postgres",
	} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}

// cloudAgentSafeMediaTaskError preserves a short, user-facing task diagnostic
// while refusing provider details that commonly contain URLs, credentials, or
// internal request metadata. Task.Error is not a safe presentation field.
func cloudAgentSafeMediaTaskError(task *model.Task) string {
	if task == nil {
		return "媒体任务未成功"
	}
	detail := strings.TrimSpace(task.Error)
	if detail == "" || !utf8.ValidString(detail) || strings.ContainsAny(detail, "\r\n\x00") {
		return "媒体任务未成功"
	}
	lower := strings.ToLower(detail)
	for _, marker := range []string{
		"http://", "https://", "ftp://", "authorization", "cookie", "secret", "token", "api_key", "apikey", "x-api-key",
	} {
		if strings.Contains(lower, marker) {
			return "媒体任务未成功"
		}
	}
	runes := []rune(detail)
	if len(runes) > 240 {
		detail = string(runes[:240]) + "…"
	}
	return detail
}

func cloudAgentToolResult(runID string, state *cloudAgentRuntime, call cloudAgentCall, result any, err error) {
	payload := map[string]any{"toolName": call.Function.Name, "callId": call.ID, "arguments": call.Function.Arguments}
	if call.Function.Name == "skill_read_file" {
		var args struct {
			SkillID string `json:"skillId"`
			Path    string `json:"path"`
		}
		if json.Unmarshal([]byte(call.Function.Arguments), &args) == nil {
			payload["skillId"], payload["path"] = args.SkillID, args.Path
			for _, skill := range state.Skills {
				if skill.ID == args.SkillID {
					payload["skillName"] = skill.Name
					break
				}
			}
		}
	}
	kind := "tool_completed"
	if err != nil {
		detail, ok := result.(map[string]any)
		if !ok {
			detail = map[string]any{}
		}
		message := cloudAgentSafeToolError(err)
		detail["error"] = message
		// 参数错误要把"这一轮实际暴露的契约"回给模型：它看不到 schema 就只会重复同一个错
		// （实测 canvas_get_state 被塞过 canvasId / limit，模型反复重试直到被 nudge 打断）。
		var argumentErr *cloudAgentArgumentError
		if errors.As(err, &argumentErr) {
			detail["reason"] = "invalid_tool_arguments"
			// Use this run's advertised contract, including its permission scope.
			for _, tool := range state.Canonical.Tools {
				function, _ := tool["function"].(map[string]any)
				if function["name"] == call.Function.Name {
					detail["parameters"] = function["parameters"]
					break
				}
			}
			detail["guidance"] = "本次调用未执行，请按 parameters 修正参数后重试，不要重复提交相同的错误参数"
			if call.Function.Name == "canvas_get_state" {
				// 这个工具的"什么都不传"就是合法调用，直接给个例子省掉一轮试错。
				detail["exampleArguments"] = map[string]any{}
			}
		}
		result = detail
		kind = "tool_failed"
		payload["text"] = message
	} else {
		payload["text"] = "工具执行成功"
	}
	if inspection, ok := result.(cloudAgentImageInspection); ok && err == nil {
		receipt, _ := json.Marshal(inspection.Receipt)
		payload["result"] = inspection.Receipt
		state.event(runID, kind, payload)
		// tool 角色只接受字符串内容（四种上游图式都是纯文本），因此工具回执照常入历史，
		// 图片另起一条 user 消息携带，并显式标注为数据而非指令。
		state.Canonical.Messages = append(state.Canonical.Messages,
			map[string]any{"role": "tool", "tool_call_id": call.ID, "content": string(receipt)})
		// 重复查看时只回执文字（ImageURL 为空），不再附图。
		if strings.TrimSpace(inspection.ImageURL) != "" {
			state.Canonical.Messages = append(state.Canonical.Messages,
				map[string]any{"role": "user", "content": cloudAgentImageContentParts(inspection)})
		}
		state.CallIndex++
		state.Approval = nil
		return
	}
	raw, _ := json.Marshal(cloudAgentModelToolResult(call.Function.Name, result))
	payload["result"] = result
	if call.Function.Name == "skill_read_file" && err == nil {
		// SSE/UI needs the read receipt, not another durable copy of skill text.
		if fields, ok := result.(map[string]any); ok {
			receipt := make(map[string]any, len(fields))
			for key, value := range fields {
				if key != "content" {
					receipt[key] = value
				}
			}
			payload["result"] = receipt
		}
	}
	state.event(runID, kind, payload)
	state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "tool", "tool_call_id": call.ID, "content": string(raw)})
	state.CallIndex++
	state.Approval = nil
}

// errCloudAgentSnapshotConflict marks the recoverable "the canvas moved since
// the model read it" outcome. It is attached only to snapshot checks, so the
// approval path can hand it back to the model as a tool result while genuine
// admission failures still stop the run.
var errCloudAgentSnapshotConflict = errors.New("canvas snapshot conflict")

func cloudAgentSnapshotConflictError(message string) error {
	return &AppError{Status: 409, Code: 409, Message: message, Cause: errCloudAgentSnapshotConflict}
}

func cloudAgentSnapshotConflict(err error) bool {
	return errors.Is(err, errCloudAgentSnapshotConflict)
}

// cloudAgentToolNamesByCallID 从助手消息的工具调用里还原 callId → 工具名，
// 用于让上下文治理按工具区分可卸载的正文。
func cloudAgentToolNamesByCallID(messages []map[string]any) map[string]string {
	names := make(map[string]string, len(messages))
	for _, message := range messages {
		if stringField(message, "role") != "assistant" {
			continue
		}
		raw, ok := message["tool_calls"].([]any)
		if !ok {
			continue
		}
		for _, item := range raw {
			call, ok := item.(map[string]any)
			if !ok {
				continue
			}
			function, _ := call["function"].(map[string]any)
			id, name := stringValue(call["id"]), stringValue(function["name"])
			if id != "" && name != "" {
				names[id] = name
			}
		}
	}
	return names
}

// cloudAgentModelToolResult is the single model-facing copy of a tool result.// The approval preview is authored for the human approval card and already
// reaches the SSE stream through approval_requested, so the durable context
// keeps only the receipt the model actually needs: what changed, which node and
// which snapshot. This follows the skill_read_file precedent (receipt on SSE,
// body withheld from context) in the opposite direction.
func cloudAgentModelToolResult(toolName string, result any) any {
	fields, ok := result.(map[string]any)
	if !ok {
		return result
	}
	preview, hasPreview := fields["preview"]
	if !hasPreview {
		return result
	}
	receipt := make(map[string]any, len(fields)+2)
	for key, value := range fields {
		if key == "preview" {
			continue
		}
		receipt[key] = value
	}
	// 审批卡文案是写给人看的（"准备…批准后才会写入画布"）。模型收到的这份工具结果只在写入
	// 真正发生后才会产生，所以必须给出结果口径，否则模型会以为还在等审批并反复重读重试。
	if cloudAgentCanvasWriteTool(toolName) {
		receipt["applied"] = true
		receipt["outcome"] = "已写入画布；本回执的 snapshotHash 是最新版本，可继续提交同一批的其它写入"
		if item := cloudAgentReceiptItemSummary(preview); item != "" {
			receipt["summary"] = item
		}
	}
	receipt["previewOmitted"] = true
	receipt["previewNote"] = "审批卡明细只发给用户；本回执保留变更条目、nodeId 与 snapshotHash"
	return receipt
}

// cloudAgentReceiptItemSummary 取审批预览里面向节点的短句（"修改分镜脚本《…》"），
// 它描述的是发生了什么，而不是"准备做什么"。
func cloudAgentReceiptItemSummary(preview any) string {
	value, ok := preview.(cloudAgentApprovalPreview)
	if !ok || len(value.Items) == 0 {
		return ""
	}
	return strings.TrimSpace(value.Items[0].Summary)
}

// cloudAgentCanvasWriteTool 标记"调用返回即表示已落到画布上"的写入工具。
// generate_media 不在此列：它创建草稿并进入独立审批，回执口径不同。
func cloudAgentCanvasWriteTool(name string) bool {
	switch name {
	case "canvas_apply_ops", "canvas_arrange_nodes", "canvas_create_storyboard", "canvas_edit_storyboard", "canvas_edit_batch_table":
		return true
	default:
		return false
	}
}

// cloudAgentRebaseWriteSnapshot 把同一批内后续写入的 snapshotHash 换成该批自己产出的最新版本。
// 只在模型提交的版本属于本批已出现过的版本时才替换，替换后仍要与当前文档一致才会通过校验，
// 因此浏览器/其他端的并发改动依旧会被"画布已变化"拒绝。
func cloudAgentRebaseWriteSnapshot(state *cloudAgentRuntime, call cloudAgentCall) cloudAgentCall {
	if state == nil || len(state.CanvasBatchHashes) == 0 || !cloudAgentCanvasWriteTool(call.Function.Name) {
		return call
	}
	var args map[string]any
	if decodeCloudAgentJSONObject(call.Function.Arguments, &args) != nil {
		return call
	}
	requested, _ := args["snapshotHash"].(string)
	latest := state.CanvasBatchHashes[len(state.CanvasBatchHashes)-1]
	if requested == "" || requested == latest || latest == "" {
		return call
	}
	known := false
	for _, hash := range state.CanvasBatchHashes {
		if hash == requested {
			known = true
			break
		}
	}
	if !known {
		return call
	}
	args["snapshotHash"] = latest
	encoded, err := json.Marshal(args)
	if err != nil {
		return call
	}
	call.Function.Arguments = string(encoded)
	return call
}

// recordCanvasBatchHash 在写入成功后登记本批的版本链（写入前的版本 + 写入产出的版本）。
func (state *cloudAgentRuntime) recordCanvasBatchHash(result any) {
	fields, ok := result.(map[string]any)
	if !ok {
		return
	}
	produced, _ := fields["snapshotHash"].(string)
	if produced == "" {
		return
	}
	if len(state.CanvasBatchHashes) == 0 {
		if before, _ := fields["beforeSnapshotHash"].(string); before != "" {
			state.CanvasBatchHashes = append(state.CanvasBatchHashes, before)
		}
	}
	if last := state.CanvasBatchHashes; len(last) == 0 || last[len(last)-1] != produced {
		state.CanvasBatchHashes = append(state.CanvasBatchHashes, produced)
	}
}
func (s *Service) advanceCloudAgentTool(run *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	if state.CallIndex < 0 || state.CallIndex >= len(state.Calls) {
		return s.failCloudAgent(run, state, "Agent 工具调用状态无效，本轮已停止")
	}
	call := state.Calls[state.CallIndex]
	// 同一批内的后续写入要基于本批自己产出的版本，否则第二个写入必然被"画布已变化"拒绝；
	// 之后再按本步读取基线把仍然匹配的哈希接到当前画布上（跨轮或模型自己换过的哈希不接）。
	call = cloudAgentRebaseWriteSnapshot(state, call)
	call = s.cloudAgentRefreshStepSnapshotHash(run, state, call)
	state.Calls[state.CallIndex] = call
	if state.Approval != nil && state.Approval.Decision == "" {
		return nil
	}
	if state.Approval != nil && state.Approval.Decision != "" && state.Approval.CallHash != "" && state.Approval.CallHash != cloudAgentApprovalCallHash(call) {
		return s.terminateCloudAgent(run, "审批内容与待执行操作不一致，本轮已停止")
	}
	allowed := cloudAgentToolAllowed(state.Request, call.Function.Name)
	if allowed && cloudAgentWrite(call.Function.Name) && (state.Request.PermissionMode == "request_approval" || call.Function.Name == "generate_media") && state.Approval == nil {
		var plan *cloudAgentMediaPlan
		var modelName string
		policy, err := s.RuntimePolicy()
		if err != nil {
			return s.terminateCloudAgent(run, "Agent 运行策略不可用，本轮已停止")
		}
		if call.Function.Name == "generate_media" {
			req, prepared, err := s.prepareCloudAgentMedia(run, state, call)
			if err != nil {
				return s.cloudAgentMediaError(run, state, "admission", false, false, err)
			}
			// Dry admission validates the selected model and prompt limits without a task or charge.
			req.creationPrepare = &creationTaskPreparation{}
			if _, err := s.CreateTask(run.UserID, req); err != nil {
				return s.cloudAgentMediaError(run, state, "admission", false, false, err)
			}
			plan = prepared
			modelName, err = s.cloudAgentMediaModelName(plan.Args)
			if err != nil {
				return s.cloudAgentMediaError(run, state, "admission", false, false, err)
			}
		}
		s.storageMu.Lock()
		defer s.storageMu.Unlock()
		return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
			var preview cloudAgentApprovalPreview
			if plan != nil {
				if err := createCloudAgentMediaNode(repo, run.UserID, state.Request.CanvasID, plan, nil, policy, cloudAgentCanvasEventRecorder(run.ID, state)); err != nil {
					return err
				}
				canvas, err := repo.CanvasProjectForUser(run.UserID, state.Request.CanvasID)
				if err != nil {
					return err
				}
				doc, err := creationDocument(canvas.PayloadJSON)
				if err != nil {
					return err
				}
				plan.Args.SnapshotHash = cloudAgentMediaContentHash(doc)
				raw, err := json.Marshal(plan.Args)
				if err != nil {
					return err
				}
				call.Function.Arguments = string(raw)
				state.Calls[state.CallIndex] = call
				preview = cloudAgentMediaApprovalPreview(plan, modelName)
			} else {
				var mutationErr error
				switch call.Function.Name {
				case "canvas_create_storyboard":
					storyboardPlan, err := prepareCloudAgentStoryboardCreate(repo, run.UserID, state.Request.CanvasID, call)
					mutationErr = err
					if err == nil {
						preview = storyboardPlan.Preview
					}
				case "canvas_edit_storyboard":
					storyboardPlan, err := prepareCloudAgentStoryboardEdit(repo, run.UserID, state.Request.CanvasID, call)
					mutationErr = err
					if err == nil {
						preview = storyboardPlan.Preview
					}
				case "canvas_edit_batch_table":
					batchPlan, err := prepareCloudAgentBatchTableEdit(repo, run.UserID, state.Request.CanvasID, call)
					mutationErr = err
					if err == nil {
						preview = batchPlan.Preview
					}
				case "canvas_arrange_nodes":
					arrangePlan, err := prepareCloudAgentArrangeNodes(repo, run.UserID, state.Request.CanvasID, call)
					mutationErr = err
					if err == nil {
						preview = arrangePlan.Preview
					}
				default:
					canvasPlan, err := prepareCloudAgentCanvasMutation(repo, run.UserID, state.Request.CanvasID, call)
					mutationErr = err
					if err == nil {
						preview = canvasPlan.Preview
					}
				}
				if mutationErr != nil {
					var argumentErr *cloudAgentArgumentError
					if errors.As(mutationErr, &argumentErr) {
						cloudAgentToolResult(run.ID, state, call, nil, mutationErr)
						return cloudAgentSave(current, state)
					}
					// A stale snapshot is a normal outcome of concurrent editing,
					// not an admission failure: hand it back as a tool result so the
					// model re-reads and retries inside this run instead of losing
					// the whole round. Genuine admission failures still stop the run.
					if cloudAgentSnapshotConflict(mutationErr) {
						cloudAgentToolResult(run.ID, state, call, nil, mutationErr)
						return cloudAgentSave(current, state)
					}
					var appErr *AppError
					if errors.As(mutationErr, &appErr) && appErr != nil {
						return failCloudAgentAdmission(current, state, run.ID, mutationErr)
					}
					return mutationErr
				}
			}
			state.Approval = &cloudAgentApproval{ID: fmt.Sprintf("%s-%d-%d", run.ID, state.Step, state.CallIndex), Call: call, CallHash: cloudAgentApprovalCallHash(call), Preview: preview, ModelName: modelName}
			current.Status = "waiting_approval"
			state.event(run.ID, "approval_requested", map[string]any{"approvalId": state.Approval.ID, "toolName": call.Function.Name, "modelName": modelName, "arguments": json.RawMessage(call.Function.Arguments), "preview": preview, "text": preview.Description})
			return cloudAgentSave(current, state)
		})
	}
	if allowed && call.Function.Name == "plan_update" && cloudAgentPlanRequiresFirstApproval(state, call) {
		if preview, ok := cloudAgentPlanApprovalPreview(call); ok {
			return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
				approvalID := fmt.Sprintf("%s-%d-%d", run.ID, state.Step, state.CallIndex)
				state.Approval = &cloudAgentApproval{ID: approvalID, Call: call, CallHash: cloudAgentApprovalCallHash(call), Preview: preview}
				current.Status = "waiting_approval"
				state.event(run.ID, "approval_requested", map[string]any{"approvalId": approvalID, "toolName": call.Function.Name, "arguments": json.RawMessage(call.Function.Arguments), "preview": preview, "text": preview.Description})
				return cloudAgentSave(current, state)
			})
		}
	}
	if allowed && call.Function.Name == "generate_media" && state.Approval != nil && state.Approval.Decision == "approve" {
		return s.advanceCloudAgentMedia(run, state, call)
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		return s.terminateCloudAgent(run, "Agent 运行策略不可用，本轮已停止")
	}
	var modelList any
	var modelListErr error
	if allowed && call.Function.Name == "model_list" {
		intent, verbose, e := s.cloudAgentModelIntent(run.UserID, state.Request.CanvasID, call.Function.Arguments)
		modelListErr = e
		if e == nil {
			modelList, modelListErr = s.cloudAgentModelList(intent, verbose)
		}
	}
	// 看图需要读取资源并签发链接，放在事务外完成，避免把网络/文件 IO 塞进 SQLite 写事务。
	var inspectionResult any
	var inspectionErr error
	if allowed && call.Function.Name == "canvas_inspect_image" && state.Request.VisionEnabled {
		inspectionResult, inspectionErr = s.prepareCloudAgentImageInspection(run.UserID, state.Request.CanvasID, state, call)
	}
	// Skill reads use the domain repository and filesystem, not the checkpoint
	// transaction's connection. Read first to avoid nesting DB reads on SQLite.
	var skillResult any
	var skillErr error
	if allowed && call.Function.Name == "skill_read_file" {
		skillResult, skillErr = cloudAgentReadTool(s.repo, run.UserID, state, call, s)
	}
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		var result any
		var toolErr error
		switch {
		case !allowed:
			toolErr = BadAuthRequest("工具未获本轮权限授权")
		case call.Function.Name == "canvas_apply_ops":
			result, toolErr = applyCloudAgentCanvas(repo, run.UserID, state.Request.CanvasID, call, policy, cloudAgentCanvasEventRecorder(run.ID, state))
		case call.Function.Name == "canvas_arrange_nodes":
			result, toolErr = applyCloudAgentArrangeNodes(repo, run.UserID, state.Request.CanvasID, call, policy, cloudAgentCanvasEventRecorder(run.ID, state))
		case call.Function.Name == "canvas_create_storyboard", call.Function.Name == "canvas_edit_storyboard":
			result, toolErr = applyCloudAgentStoryboardMutation(repo, run.UserID, state.Request.CanvasID, call, policy, cloudAgentCanvasEventRecorder(run.ID, state))
		case call.Function.Name == "canvas_edit_batch_table":
			result, toolErr = applyCloudAgentBatchTableMutation(repo, run.UserID, state.Request.CanvasID, call, policy, cloudAgentCanvasEventRecorder(run.ID, state))
		case call.Function.Name == "model_list":
			result, toolErr = modelList, modelListErr
		case call.Function.Name == "canvas_inspect_image":
			result, toolErr = inspectionResult, inspectionErr
			if toolErr == nil && inspectionResult != nil {
				if inspection, ok := inspectionResult.(cloudAgentImageInspection); ok {
					state.markCanvasAssetInspected(stringValue(inspection.Receipt["nodeId"]))
				}
			}
		case call.Function.Name == "skill_read_file":
			result, toolErr = skillResult, skillErr
		default:
			result, toolErr = cloudAgentReadTool(repo, run.UserID, state, call)
		}
		if toolErr == nil {
			state.recordCanvasBatchHash(result)
		}
		if call.Function.Name == "plan_update" && toolErr == nil {
			state.event(run.ID, "plan_updated", map[string]any{"items": state.Plan, "pendingTitles": cloudAgentPendingPlanItems(state.Plan)})
		}
		if call.Function.Name == "ask_user" && toolErr == nil {
			payload, _ := result.(map[string]any)
			state.event(run.ID, "user_question", payload)
			cloudAgentToolResult(run.ID, state, call, result, nil)
			skipRemainingCloudAgentCalls(run.ID, state)
			current.Status = "completed"
			return cloudAgentSave(current, state)
		}
		cloudAgentToolResult(run.ID, state, call, result, toolErr)
		return cloudAgentSave(current, state)
	})
}

func (s *Service) enqueueCloudAgentTask(run *model.CloudAgentExecution, state *cloudAgentRuntime, req CreateTaskRequest, media *cloudAgentMediaPlan) error {
	orders, err := s.repo.BillingOrdersByTaskIDs(run.UserID, state.TaskIDs)
	if err != nil {
		return err
	}
	remaining := int64(math.Floor(state.Request.Budget.MaxCredits * float64(CreditScale)))
	for _, order := range orders {
		remaining -= order.AmountMicrocredits
	}
	if remaining < 0 {
		if req.Operation == cloudAgentContextCompactionOperation {
			return s.completeCloudAgentContextFallback(run, state, "本轮预算不足，已使用服务端保底检查点")
		}
		return s.failCloudAgent(run, state, "Agent 累计预算已耗尽")
	}
	prepare := &creationTaskPreparation{}
	req.admission = &taskAdmission{ID: cloudAgentID(run.UserID, fmt.Sprintf("%s:task:%d", run.ID, len(state.TaskIDs))), MaxCharge: remaining}
	req.creationPrepare = prepare
	task, err := s.CreateTask(run.UserID, req)
	if err != nil {
		if req.Operation == cloudAgentContextCompactionOperation {
			return s.completeCloudAgentContextFallback(run, state, "压缩任务无法准入，已使用服务端保底检查点")
		}
		if media != nil {
			return s.cloudAgentMediaError(run, state, "admission", false, false, err)
		}
		return s.failCloudAgent(run, state, cloudAgentSafeToolError(err))
	}
	var input map[string]any
	if err = json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		if media != nil {
			return s.cloudAgentMediaError(run, state, "admission", false, false, err)
		}
		return err
	}
	if media != nil {
		requested, _ := req.Input["config"].(map[string]any)
		resolved, _ := input["config"].(map[string]any)
		if err := validateCloudAgentResolvedMediaOptions(requested, resolved); err != nil {
			return s.cloudAgentMediaError(run, state, "admission", false, false, err)
		}
	}
	if err = s.protectTaskSecrets(input); err != nil {
		if media != nil {
			return s.cloudAgentMediaError(run, state, "admission", false, false, err)
		}
		return err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		if media != nil {
			return s.cloudAgentMediaError(run, state, "admission", false, false, err)
		}
		return err
	}
	task.InputJSON = string(raw)
	var contextPressure *cloudAgentContextPressure
	if media == nil && req.Operation != cloudAgentContextCompactionOperation {
		if canonical, ok := canonicalAgentRequestFromInput(input); ok {
			value := s.cloudAgentContextPressure(task, canonical, state.Request.Prompt, state.Request)
			contextPressure = &value
		}
	}
	if prepare.Order != nil {
		task.BillingOrderID = prepare.Order.ID
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		if media != nil {
			return s.cloudAgentMediaError(run, state, "admission", false, false, err)
		}
		return err
	}
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	err = s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		if media != nil {
			if err := createCloudAgentMediaNode(repo, run.UserID, state.Request.CanvasID, media, task, policy, cloudAgentCanvasEventRecorder(run.ID, state)); err != nil {
				return err
			}
		}
		if err := createTaskWithStorageQuotaRepository(repo, task, prepare.Order, policy); err != nil {
			return err
		}
		state.TaskIDs = append(state.TaskIDs, task.ID)
		if media != nil {
			state.MediaTaskID = task.ID
			state.Generations++
			state.VideoSeconds += media.Args.Duration
			state.event(run.ID, "generation_task_created", map[string]any{"toolName": "generate_media", "taskId": task.ID, "nodeId": media.Args.NodeID, "title": media.Args.Title, "mode": media.Args.Mode, "canvasId": state.Request.CanvasID, "referenceNodeIds": media.Args.ReferenceNodeIDs, "text": "媒体节点与引用连线已创建，生成任务已提交"})
		} else {
			state.ActiveTaskID = task.ID
			if state.ContextCompaction != nil && req.Operation == cloudAgentContextCompactionOperation {
				state.ContextCompaction.Status = "running"
			} else {
				if contextPressure != nil {
					state.event(run.ID, "context_pressure", cloudAgentContextPressurePayload(*contextPressure, state))
				}
				// 记下发出去这份 canonical 的本地计价：任务回来时用它和上游实测配成锚点。
				state.LastStepTaskID = task.ID
				state.LastStepOperation = req.Operation
				state.LastStepEstimate = contextPressure.EstimatedInputTokens
				state.LastStepSourceBytes = contextPressure.SourceBytes
				state.Step++
			}
		}
		return cloudAgentSave(current, state)
	})
	if err != nil && media != nil {
		// Rollback may have happened after checkpoint edits. Reload before recording
		// a tool failure; never turn a stale worker revision into a second result.
		latest, readErr := s.repo.CloudAgent(run.UserID, run.ID)
		if readErr != nil || latest.Revision != run.Revision {
			return err
		}
		fresh, decodeErr := cloudAgentDecode(latest)
		if decodeErr != nil {
			return decodeErr
		}
		return s.cloudAgentMediaError(latest, &fresh, "admission", false, false, err)
	}
	return err
}

func (s *Service) cloudAgentMediaError(run *model.CloudAgentExecution, state *cloudAgentRuntime, phase string, submitted, terminal bool, err error) error {
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		if state.CallIndex < 0 || state.CallIndex >= len(state.Calls) {
			current.Status = "failed"
			state.event(run.ID, "run_failed", map[string]any{"text": "Agent 媒体调用状态无效，本轮已停止"})
			return cloudAgentSave(current, state)
		}
		cloudAgentToolResult(run.ID, state, state.Calls[state.CallIndex], map[string]any{"phase": phase, "taskSubmitted": submitted}, err)
		if submitted {
			state.MediaTaskID = ""
		}
		if terminal {
			current.Status = "failed"
			state.event(run.ID, "run_failed", map[string]any{"text": "媒体任务已提交，但结果处理失败；任务不会自动重试"})
		}
		return cloudAgentSave(current, state)
	})
}

func (s *Service) advanceCloudAgentMedia(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) error {
	if state.MediaTaskID != "" {
		task, err := s.repo.TaskForUser(run.UserID, state.MediaTaskID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return s.cloudAgentMediaError(run, state, "completion", true, true, BadAuthRequest("媒体任务不存在或已失去归属，结果未回写画布"))
		}
		if err != nil {
			return err
		}
		if task.Status == model.TaskStatusQueued || task.Status == model.TaskStatusRunning {
			return nil
		}
		policy, err := s.RuntimePolicy()
		if err != nil {
			return err
		}
		var target struct {
			NodeID string `json:"nodeId"`
		}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &target); err != nil || validateCloudAgentID(target.NodeID, "生成节点ID", 80) != nil {
			return s.cloudAgentMediaError(run, state, "completion", true, true, BadAuthRequest("已提交媒体任务的目标节点记录无效，未回写画布"))
		}
		s.storageMu.Lock()
		defer s.storageMu.Unlock()
		return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
			before, readErr := repo.CanvasProjectForUser(run.UserID, state.Request.CanvasID)
			nodeID, writeErr := completeCloudAgentMediaNode(repo, run.UserID, state.Request.CanvasID, target.NodeID, task, policy)
			if writeErr != nil {
				var appErr *AppError
				if !errors.Is(writeErr, gorm.ErrRecordNotFound) && !(errors.As(writeErr, &appErr) && (appErr.Status == 400 || appErr.Status == 409)) {
					return writeErr // Retry persistence, never resubmit the billed generation.
				}
			}
			// A deleted canvas/node must still checkpoint the tool failure. Only
			// a matched node can have changed and require an atomic canvas delta.
			if nodeID != "" {
				if readErr != nil {
					return readErr
				}
				if err := emitCloudAgentCanvasChange(repo, run.ID, state, cloudAgentMutationInput{UserID: run.UserID, CanvasID: state.Request.CanvasID, BeforeJSON: before.PayloadJSON, Operation: "generate_media_complete"}); err != nil {
					return err
				}
			}
			result := map[string]any{"phase": "completion", "taskSubmitted": true, "taskId": task.ID, "nodeId": nodeID, "targetNodeId": target.NodeID, "status": task.Status}
			var toolErr error
			generationMessage := ""
			if task.Status != model.TaskStatusSucceeded {
				generationMessage = truncateRunes(cloudAgentSafeMediaTaskError(task), 90)
				if !cloudAgentSafeUserMessage(generationMessage) {
					generationMessage = "媒体任务未成功"
				}
				result["generationError"] = generationMessage
				toolErr = BadAuthRequest("媒体任务未成功：" + generationMessage + "；请在任务中心查看任务详情，不会自动重试收费生成")
				result["summary"] = "媒体任务未成功；任务记录保留在任务中心"
			}
			if writeErr != nil {
				writebackMessage := truncateRunes(cloudAgentSafeToolError(writeErr), 60)
				reason := "canvas_writeback_failed"
				var writeback *cloudAgentMediaWritebackError
				if errors.As(writeErr, &writeback) {
					reason = writeback.reason
				}
				result["writebackError"], result["writebackReason"] = writebackMessage, reason
				message := "媒体任务已成功，但画布回写未完成：" + writebackMessage
				if generationMessage != "" {
					message = "媒体任务未成功：" + generationMessage + "；任务状态也未回写画布：" + writebackMessage
				}
				toolErr = BadAuthRequest(message + "。请在任务中心查看详情，不会自动重试收费生成")
				result["summary"] = "画布回写未完成；任务记录保留在任务中心"
			}
			if task.Status == model.TaskStatusSucceeded && writeErr == nil {
				result["summary"] = "生成结果已回写画布节点"
			}
			// A billed generation failure is a tool result, not a dead run: the
			// model must still be able to tell the user what happened. Only a
			// canvas write that cannot land is terminal for the whole turn.
			if writeErr != nil {
				if current.Status != "cancelled" {
					current.Status = "failed"
				}
				current.FailureMessage = cloudAgentSafeToolError(toolErr)
				state.event(run.ID, "run_failed", map[string]any{
					"text": current.FailureMessage, "taskId": task.ID, "nodeId": target.NodeID,
					"reason": result["writebackReason"], "generationStatus": task.Status,
					"generationError": generationMessage, "writebackError": result["writebackError"],
				})
			}
			cloudAgentToolResult(run.ID, state, call, result, toolErr)
			state.MediaTaskID = ""
			return cloudAgentSave(current, state)
		})
	}
	req, plan, err := s.prepareCloudAgentMedia(run, state, call)
	if err != nil {
		return s.cloudAgentMediaError(run, state, "admission", false, false, err)
	}
	return s.enqueueCloudAgentTask(run, state, req, plan)
}

func (s *Service) DecideCloudAgentApproval(userID, id, approvalID, decision, reason string, mediaSettings ...*CloudAgentMediaSettings) error {
	var settings *CloudAgentMediaSettings
	if len(mediaSettings) > 1 {
		return BadAuthRequest("只能提交一组生成参数")
	}
	if len(mediaSettings) == 1 {
		settings = mediaSettings[0]
	}
	if settings != nil && decision != "approve" {
		return BadAuthRequest("仅批准生成时可修改生成参数")
	}
	if decision != "approve" && decision != "reject" {
		return BadAuthRequest("无效审批决定")
	}
	if len(reason) > 2000 {
		return BadAuthRequest("审批理由过长")
	}
	if _, _, err := s.cloudAgentTask(userID, id); err != nil {
		return err
	}
	run, err := s.repo.CloudAgent(userID, id)
	if err != nil {
		return err
	}
	state, err := cloudAgentDecodeForExecution(run)
	if err != nil {
		return err
	}
	if previous, ok := state.Decisions[approvalID]; ok {
		if previous == decision && (settings == nil || state.DecisionSettings[approvalID] == creationHash(settings)) {
			return nil
		}
		return creationConflict("该审批已有不同决定")
	}
	if run.Status != "waiting_approval" || state.Approval == nil || state.Approval.ID != approvalID {
		return creationConflict("审批不存在或已过期")
	}
	if settings != nil {
		if err := s.updateCloudAgentMediaApproval(run, &state, *settings); err != nil {
			return err
		}
	}
	return s.repo.MutateCloudAgent(userID, id, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state.Approval.Decision = decision
		state.Approval.Reason = reason
		state.Decisions[approvalID] = decision
		if settings != nil {
			if state.DecisionSettings == nil {
				state.DecisionSettings = map[string]string{}
			}
			state.DecisionSettings[approvalID] = creationHash(settings)
		}
		if decision == "reject" {
			// Rejection is a user control-plane decision, not a failed tool
			// invocation. Make it terminal before the scheduler can advance the
			// pending call; no tool result, canvas mutation, generation task or
			// follow-up model request may be produced from this decision.
			current.Status = "rejected"
			current.FailureMessage = ""
			state.Approval = nil
			state.event(id, "approval_decided", map[string]any{
				"approvalId": approvalID,
				"decision":   decision,
				"reason":     reason,
				"text":       "已拒绝本次操作，未写入画布。你可以告诉 Agent 修改方向后重新申请。",
			})
			return cloudAgentSave(current, &state)
		}
		current.Status = "running"
		state.event(id, "approval_decided", map[string]any{"approvalId": approvalID, "decision": decision, "arguments": json.RawMessage(state.Approval.Call.Function.Arguments), "preview": state.Approval.Preview, "modelName": state.Approval.ModelName})
		return cloudAgentSave(current, &state)
	})
}
func (s *Service) CancelCloudAgent(ctx context.Context, userID, id string) error {
	// Cancellation is a control-plane operation. It must remain available even
	// when the user-facing runtime blob is damaged, so authenticate/authorize
	// from the task row first instead of calling CloudAgentRun up front.
	task, err := s.repo.TaskForUser(userID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return kernel.NotFound("Agent 运行不存在")
		}
		return err
	}
	if task.Operation != cloudAgentOperation {
		return kernel.NotFound("Agent 运行不存在")
	}
	run, err := s.repo.CloudAgent(userID, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Legacy root tasks may not have an execution row yet. The normal read
		// path validates the signed/deterministic task identity before creating it.
		if _, err = s.CloudAgentRun(userID, id); err != nil {
			return err
		}
		run, err = s.repo.CloudAgent(userID, id)
	}
	if err != nil {
		return err
	}
	if run.Status == "completed" || (run.Status == "failed" && !run.CleanupPending) {
		return nil
	}
	if run.Status != "failed" {
		// Persist intent independently of the transcript. Retrying also repairs
		// legacy cancelled rows that crashed before cancelling their children.
		state, decodeErr := cloudAgentDecode(run)
		err = s.repo.MutateCloudAgent(userID, id, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			current.Status = "cancelled"
			current.CleanupPending = true
			if decodeErr == nil {
				current.CanvasID, current.ActiveTaskID, current.MediaTaskID = state.Request.CanvasID, state.ActiveTaskID, state.MediaTaskID
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	latest, err := s.repo.CloudAgent(userID, id)
	if err != nil {
		return err
	}
	return s.finishCloudAgentCleanup(ctx, latest)
}

// purgeCloudAgentRunEvents 按保留期清理运行事件（默认 30 天）。带时间闸：调度 tick 很密，
// 没必要每次都查库；清理失败只留痕，不影响调度。
func (s *Service) purgeCloudAgentRunEvents() {
	now := time.Now()
	if !s.agentEventPurgeAt.IsZero() && now.Sub(s.agentEventPurgeAt) < 10*time.Minute {
		return
	}
	s.agentEventPurgeAt = now
	deleted, err := s.repo.PurgeExpiredCloudAgentRunEvents(now, 500)
	if err != nil {
		log.Printf("agent event purge: %v", err)
		return
	}
	if deleted > 0 {
		log.Printf("agent event purge: removed %d expired events", deleted)
	}
}
