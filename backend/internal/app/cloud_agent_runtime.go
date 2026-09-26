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

type cloudAgentCachedToolResult struct {
	Result        json.RawMessage `json:"result,omitempty"`
	Error         string          `json:"error,omitempty"`
	ArgumentError bool            `json:"argumentError,omitempty"`
	// ReplayCount is diagnostic only. Replaying a cached read is harmless and
	// must not fail the run; the per-run real-read budget limits cache misses.
	ReplayCount int `json:"replayCount,omitempty"`
}

type cloudAgentReadLoopError struct {
	ToolName   string
	Count      int
	Budget     bool
	ReasonCode string
}

func (e *cloudAgentReadLoopError) reasonCode() string {
	if e == nil || e.ReasonCode == "" {
		if e != nil && e.Budget {
			return "read_budget_exceeded"
		}
		return "repeated_read_guard"
	}
	return e.ReasonCode
}

func (e *cloudAgentReadLoopError) Error() string {
	if e == nil {
		return "Agent 重复读取护栏已触发"
	}
	if e.Budget {
		return fmt.Sprintf("Agent 本轮只读工具调用已达到安全上限（%d 次），本轮已停止以避免继续消耗模型调用；请使用已有结果继续，不要继续读取", e.Count)
	}
	return fmt.Sprintf("Agent 连续重复读取同一份%s结果，本轮已停止以避免继续消耗模型调用；请使用已有结果继续，不要再次读取", e.ToolName)
}

type cloudAgentApproval struct {
	Prepared  *cloudAgentPreparedMedia  `json:"prepared,omitempty"`
	ModelName string                    `json:"modelName,omitempty"`
	ID        string                    `json:"approvalId"`
	Call      cloudAgentCall            `json:"call"`
	CallHash  string                    `json:"callHash,omitempty"`
	Preview   cloudAgentApprovalPreview `json:"preview"`
	Decision  string                    `json:"decision,omitempty"`
	Reason    string                    `json:"reason,omitempty"`
}
type cloudAgentRuntime struct {
	RuntimeRunID            string                    `json:"-"`
	Request                 CloudAgentRequest         `json:"request"`
	Policy                  cloudAgentPolicySnapshot  `json:"policy"`
	ParentID                string                    `json:"parentId,omitempty"`
	Fingerprint             string                    `json:"fingerprint,omitempty"`
	CreativeAnchor          cloudAgentCreativeAnchor  `json:"creativeAnchor,omitempty"`
	TextHistory             []providerTextMessage     `json:"textHistory,omitempty"`
	Skills                  []cloudAgentSkill         `json:"skills"`
	SkillReads              map[string]bool           `json:"skillReads,omitempty"`
	Profile                 cloudAgentProfileSnapshot `json:"profile"`
	ProfileReads            map[string]bool           `json:"profileReads,omitempty"`
	Canonical               canonicalAgentRequest     `json:"canonical"`
	DisclosureVersion       int                       `json:"disclosureVersion,omitempty"`
	SelectedToolCategory    string                    `json:"selectedToolCategory,omitempty"`
	ActivatedToolCategories []string                  `json:"activatedToolCategories,omitempty"`
	AdvertisedToolNames     []string                  `json:"advertisedToolNames,omitempty"`
	ActiveTaskID            string                    `json:"activeTaskId"`
	ActiveTextDraft         string                    `json:"activeTextDraft,omitempty"`
	MediaTaskID             string                    `json:"mediaTaskId,omitempty"`
	TaskIDs                 []string                  `json:"taskIds"`
	Step                    int                       `json:"step"`
	Generations             int                       `json:"generations"`
	VideoSeconds            int                       `json:"videoSeconds"`
	Calls                   []cloudAgentCall          `json:"calls"`
	CallIndex               int                       `json:"callIndex"`
	// ToolRepairs is counted per tool so reads do not reset write-argument repairs.
	ToolRepairs            map[string]cloudAgentToolRepair `json:"toolRepairs,omitempty"`
	Approval               *cloudAgentApproval             `json:"approval,omitempty"`
	AutoPreparedMedia      *cloudAgentPreparedMedia        `json:"autoPreparedMedia,omitempty"`
	AutoPreparedCallHash   string                          `json:"autoPreparedCallHash,omitempty"`
	Decisions              map[string]string               `json:"decisions"`
	DecisionSettings       map[string]string               `json:"decisionSettings,omitempty"`
	DecisionPreparedHashes map[string]string               `json:"decisionPreparedHashes,omitempty"`
	ActionNudged           bool                            `json:"actionNudged,omitempty"`
	EmptyOutputNudged      int                             `json:"emptyOutputNudged,omitempty"`
	// EmptyOutputEscalated 记录"空输出已经升级重试过几次"（关思考 + 放大输出预算）。
	EmptyOutputEscalated int `json:"emptyOutputEscalated,omitempty"`
	// TruncatedStepEscalated 记录"输出被输出上限截断后已经升级重试过几次"。
	// 与空输出、单步超时同一条阶梯：关思考 + 放大输出预算，重发同一步。
	TruncatedStepEscalated int `json:"truncatedStepEscalated,omitempty"`
	// EscalationRestore 保存"升级之前"的 ForceThinkingOff / BoostStepOutputBudget，
	// 重试被接受（或撞上别的失败阶梯）后复位，避免一次截断让本轮余下所有步骤都受开关影响。
	EscalationRestore *cloudAgentEscalationRestore `json:"escalationRestore,omitempty"`
	// StepTimeoutEscalated 记录"单步墙钟到点后已经关思考重试过几次"。
	StepTimeoutEscalated int `json:"stepTimeoutEscalated,omitempty"`
	// ForceThinkingOff 让本步请求强制关闭上游思考：思考模型偶发把整个输出预算花在推理上，
	// 结果正文与工具调用皆空（实测 output_tokens 正好等于 maxOutputTokens）。
	ForceThinkingOff bool `json:"forceThinkingOff,omitempty"`
	// BoostStepOutputBudget 让本步请求使用放大后的输出预算（配合关思考重试）。
	BoostStepOutputBudget bool   `json:"boostStepOutputBudget,omitempty"`
	StepSnapshotHash      string `json:"stepSnapshotHash,omitempty"`
	// StepLimits 是本步实际生效的执行边界（管理员策略解析结果），只用于构造请求与展示压力读数，
	// 因此不进状态 JSON：每次推进都按当时的策略重新解析，改配置无需重发本轮。
	StepLimits cloudAgentStepLimits `json:"-"`
	// ContextCompactionCount 是本轮已经压过几次：压完仍超阈值时不要无限暂停。
	ContextCompactionCount int `json:"contextCompactionCount,omitempty"`
	// ContextCheckpoint / ContextCompaction / HistoryIncludesCurrent 是语义压缩的状态面。
	ContextCheckpoint      *agentcontext.Checkpoint     `json:"contextCheckpoint,omitempty"`
	ContextCompaction      *cloudAgentContextCompaction `json:"contextCompaction,omitempty"`
	HistoryIncludesCurrent bool                         `json:"historyIncludesCurrent,omitempty"`
	// ImageInspectCounts 记录本轮内每张图被查看的次数，用于"同一张图不要反复看"的护栏。
	ImageInspectCounts map[string]int `json:"imageInspectCounts,omitempty"`
	// ToolReadResults / ToolReadReplays 是只读快照工具的同参缓存与重放计数（上游护栏）。
	ToolReadResults    map[string]cloudAgentCachedToolResult `json:"toolReadResults,omitempty"`
	ToolReadReplays    map[string]int                        `json:"toolReadReplays,omitempty"`
	readCacheExecution bool                                  `json:"-"`
	// ImageObservations 是本轮的视觉事实账本：节点 ID → 模型为该节点写下的那句话。
	// 它是图片被裁掉之后模型还能依据什么的唯一来源（裁剪占位符直接引用这里的内容），
	// 也是"这张图看过、不必再看"的判据 —— 附图次数不是，附图成功也不代表识别成功。
	ImageObservations map[string]cloudAgentImageObservation `json:"imageObservations,omitempty"`
	// ImageObservationSignatures 是"附图时这张图的内容指纹"（bytes/width/height 组合）：
	// 同一个节点 ID 换了图时，用它让旧观察立即失效，不然旧画面事实会一直挂在同一 ID 上。
	ImageObservationSignatures map[string]string `json:"imageObservationSignatures,omitempty"`
	// PendingImageObservations 是"刚附图、还等模型写观察"的节点清单；模型下一步的正文
	// 才会被记为观察。它必须进检查点：一次转移只执行一个工具调用，跨转移会重新解码状态，
	// 进程内字段必然为空（同 PendingImageInspections）。
	PendingImageObservations []string `json:"pendingImageObservations,omitempty"`
	// AgentImagesInContext 是本步请求里实际带图的数量（装配后统计）。它决定"这张图"这类
	// 指代能否安全归属：上下文里还留着更早的图时，指代可能指着上一张。
	AgentImagesInContext int `json:"agentImagesInContext,omitempty"`
	// ImageInspectionReads 以“节点 + 资源 + 画布 revision”为 key，避免同一张图在
	// 同一版本的画布里反复触发视觉输入。画布内容变化后 key 自然变化，允许重新识别。
	ImageInspectionReads map[string]int `json:"imageInspectionReads,omitempty"`
	// ImageInspectCalls 记录本轮所有图片识别工具调用次数（包括只回执文字的重复调用）。
	// 它与 ImageInspectCounts 一起进检查点，防止模型通过 refresh 或切换节点绕过总预算。
	ImageInspectCalls int `json:"imageInspectCalls,omitempty"`
	// ReadToolCalls 记录本轮会读取运行时只读快照的工具调用次数。除了同参缓存护栏，
	// 还需要一个跨参数的总上限，防止模型通过不断变化 offset/nodeIds 绕过重复读取保护。
	ReadToolCalls int `json:"readToolCalls,omitempty"`
	// PendingImageInspections 暂存"本批还有工具结果没入历史"的看图结果，等整批 tool
	// 结果都入历史后合并成一条 user 图片消息（见 cloudAgentFlushPendingImages）。
	//
	// 它必须进检查点，不能标 `json:"-"`：一次 advanceCloudAgent 只执行一个工具调用
	// （advanceCloudAgentTool 执行完就 return，下一批调用走下一次转移），而每次转移都
	// 从 StateJSON 重新解码（cloudAgentDecode）。进程内字段在下一个调用到来时必然为空，
	// 缓冲就白缓冲了。载荷只有回执与签名链接，几十字节级。
	PendingImageInspections []cloudAgentImageInspection `json:"pendingImageInspections,omitempty"`
	// ToolScope 是"自动纠错进入收紧档"时本轮只开放的工具有限集合（见
	// cloud_agent_tool_repair.go）。为空表示不限制；它在下一步生效、修好后清空，
	// 与 callAdmissions 同理必须进检查点（一次推进一个调用，跨多次转移）。
	ToolScope []string `json:"toolScope,omitempty"`
	// CallAdmissions 是本批每个调用的预检结论（见 cloud_agent_tool_preflight.go）。
	// 与 PendingImageInspections 同理必须进检查点：执行是一个调用一次转移，进程内字段
	// 活不到下一个调用。旧检查点缺这个字段时按"未预检、直接放行"处理。
	CallAdmissions       []cloudAgentCallAdmission               `json:"callAdmissions,omitempty"`
	StoryboardTaskID     string                                  `json:"storyboardTaskId,omitempty"`
	TransientReferences  map[string]cloudAgentTransientReference `json:"transientReferences,omitempty"`
	Plan                 []cloudAgentPlanItem                    `json:"plan,omitempty"`
	PendingInterjections []cloudAgentInterjection                `json:"pendingInterjections,omitempty"`
	InterjectionIDs      []string                                `json:"interjectionIds,omitempty"`
	// 最近一次已发出的步骤请求（模型调用）的本地计价，与上游实测用量配成锚点用。
	// 估算与实测指向同一个 canonical：估算取自任务 input 里实际发出的那份，
	// 因此"信封一致"是构造保证，不需要额外比对。
	LastStepTaskID      string `json:"lastStepTaskId,omitempty"`
	LastStepOperation   string `json:"lastStepOperation,omitempty"`
	LastStepEstimate    int    `json:"lastStepEstimate,omitempty"`
	LastStepSourceBytes int    `json:"lastStepSourceBytes,omitempty"`
	// TokenAnchor 是上一步上游上报的用量（模型自己的分词器计数），上下文压力的权威锚点。
	TokenAnchor *cloudAgentTokenAnchor `json:"tokenAnchor,omitempty"`
	// EventSeqBase 是本次载入的事件窗口之前"已入库"的条数（只在内存里，不落检查点）：
	// 不变量是 events[i].Seq == EventSeqBase + i + 1。事件全量在上游的
	// cloud_agent_event_records，内存只保留最近一窗供摘要、记忆提取与卡死判定使用，
	// 运行详情仍按 seq 分页读表。
	EventSeqBase int `json:"-"`
	// LastStepSignature / Model / ChannelID 记录"发出去的那份信封"的口径（上游 #601 新增）：
	// 锚点作废要按实际信封比对，不能拿运行态副本替代；LastStepWindowTokens 是定锚时的窗口，
	// 窗口变了同一个 token 数含义就不同。
	LastStepSignature    string `json:"lastStepSignature,omitempty"`
	LastStepModel        string `json:"lastStepModel,omitempty"`
	LastStepChannelID    string `json:"lastStepChannelId,omitempty"`
	LastStepWindowTokens int    `json:"lastStepWindowTokens,omitempty"`
	// ContextWindowKnown 记录本轮是否已经看到过"模型窗口已确认"的读数：从"未确认"变为
	// "已确认"时要落一条 context_transition，消费方据此标"模型窗口已识别"，
	// 而不是把口径切换画成上下文骤降。
	ContextWindowKnown bool `json:"contextWindowKnown,omitempty"`
	// CanvasBatchHashes 记录本批（同一个助手消息内的多次工具调用）已经消费与产出的画布版本：
	// 首元素是首个写入被校验时看到的版本，末元素是最近一次写入产出的版本。模型是在同一次读取的
	// 基础上并发提交这批写入的，首个写入必然改变版本，因此同批后续写入需要据此重基。
	CanvasBatchHashes []string          `json:"canvasBatchHashes,omitempty"`
	Events            []CloudAgentEvent `json:"events"`
	// CompletionNudges 是本轮"候选收尾被闸门拦下"的累计次数，CompletionNudgeAttempt 是
	// **同一阻塞原因**（指纹）下的次数，CompletionNudgeFingerprint 是当前指纹。
	// 三者都要进检查点：一次推进是一次转移，进程内字段活不到下一次（工作项 A）。
	CompletionNudges           int    `json:"completionNudges,omitempty"`
	CompletionNudgeAttempt     int    `json:"completionNudgeAttempt,omitempty"`
	CompletionNudgeFingerprint string `json:"completionNudgeFingerprint,omitempty"`
}

type cloudAgentTransientReference struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	MIMEType   string    `json:"mimeType"`
	ResourceID string    `json:"resourceId"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

func (s *Service) ensureCloudAgentExecution(task *model.Task, initial cloudAgentState) error {
	var input struct {
		TextHistory []providerTextMessage `json:"textHistory"`
		Config      map[string]any        `json:"config"`
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
	advertisedNames := cloudAgentToolNames(canonical.Tools)
	// Rebuild the complete eligible catalog for server-side authorization. The
	// root task contains only the parent schemas sent to the model.
	canonical.Tools = compileCloudAgentTools(initial.Request, len(initial.Profile.Layers) > 0)
	stepLimits, err := s.cloudAgentStepLimits()
	if err != nil {
		return err
	}
	state := cloudAgentRuntime{Request: initial.Request, Policy: initial.Policy, ParentID: initial.ParentID, Fingerprint: initial.Fingerprint, CreativeAnchor: initial.CreativeAnchor, TextHistory: input.TextHistory, Skills: initial.Skills, Profile: initial.Profile, Canonical: canonical, ActiveTaskID: task.ID, TaskIDs: []string{task.ID}, Step: 1, Decisions: map[string]string{}, Plan: initial.Plan, Events: []CloudAgentEvent{}, StepLimits: stepLimits}
	for _, name := range advertisedNames {
		if cloudAgentIsToolCategory(name) {
			state.DisclosureVersion = cloudAgentToolDisclosureVersion
			state.AdvertisedToolNames = advertisedNames
			break
		}
	}
	if len(initial.Skills) > 0 {
		// skillIds makes the enablement auditable: usage telemetry can attribute a
		// run to the skills it actually loaded instead of only counting the total.
		skillIDs := make([]string, 0, len(initial.Skills))
		for _, skill := range initial.Skills {
			skillIDs = append(skillIDs, skill.ID)
		}
		state.event(task.ID, "tool_completed", map[string]any{"toolName": "skills_load", "skillIds": skillIDs, "text": fmt.Sprintf("已启用 %d 个技能，正文将按需读取", len(initial.Skills))})
	}
	pressure := s.cloudAgentContextPressure(input.Requests.Canonical, initial.Request.Prompt, initial.Request)
	// 第一步的模型调用就是根任务本身（不经过 enqueueCloudAgentTask）：在这里登记任务 id
	// 与本次请求的本地计价，它回来时才能与上游实测配成锚点。根任务的操作名是 cloud_agent，
	// 但它就是第一步的模型调用：按"步骤"口径登记，否则回来配锚点时会被操作名守卫挡掉（实测踩过）。
	state.LastStepTaskID = task.ID
	state.LastStepOperation = cloudAgentStepOperation
	state.LastStepEstimate = pressure.EstimatedInputTokens
	state.LastStepSourceBytes = pressure.SourceBytes
	// 记下发出去这份信封的口径与窗口（上游 #601 的锚点治理）：模型/线路取自任务 input，
	// 窗口只在真的解析到能力时填，未知就留 0（不拿兜底默认窗口冒充真实能力）。
	state.LastStepModel = stringValue(input.Config["model"])
	state.LastStepChannelID = stringValue(input.Config["channelId"])
	state.LastStepSignature = cloudAgentRequestSignature(&state, input.Requests.Canonical, state.LastStepChannelID, state.LastStepModel)
	if pressure.ModelLimitConfigured {
		state.LastStepWindowTokens = pressure.ContextWindowTokens
	}
	// 第一步的模型调用就是根任务本身，requestId 就是它；窗口在这里是"起始状态"而不是
	// "刚刚识别"，因此只播种标记、不落 window_resolved 过渡（否则每轮开头都会报一次"已识别"）。
	state.ContextWindowKnown = pressure.ModelLimitConfigured
	// 占用分布对着"实际要发出去的那份信封"算（上游 #601 的 actual 参数）：准入改写之后的
	// canonical 才是真实请求，不能拿内存里的运行态副本顶替。
	firstPressure := cloudAgentContextPressurePayload(pressure, &state, input.Requests.Canonical)
	firstPressure["requestId"] = task.ID
	state.event(task.ID, "context_pressure", firstPressure)
	run := &model.CloudAgentExecution{ID: task.ID, UserID: task.UserID, Status: "running", Revision: 1, CreatedAt: task.CreatedAt, UpdatedAt: time.Now()}
	if err := cloudAgentSave(run, &state); err != nil {
		return err
	}
	return s.repo.EnsureCloudAgent(run)
}
func (state *cloudAgentRuntime) event(id, kind string, payload map[string]any) {
	// 序号连续于"事件窗口 + 已入库条数"：events[i].Seq == EventSeqBase+i+1 是不变式。
	seq := state.EventSeqBase + len(state.Events) + 1
	state.Events = append(state.Events, CloudAgentEvent{EventID: fmt.Sprintf("%s:%d", id, seq), RunID: id, Seq: seq, Type: kind, Payload: cloudAgentBoundEventPayload(payload), CreatedAt: time.Now()})
}
func cloudAgentDecode(run *model.CloudAgentExecution) (cloudAgentRuntime, error) {
	var state cloudAgentRuntime
	if run == nil || strings.TrimSpace(run.StateJSON) == "" {
		return state, errors.New("Agent runtime state is empty")
	}
	if err := json.Unmarshal([]byte(run.StateJSON), &state); err != nil {
		return state, fmt.Errorf("decode Agent runtime state: %w", err)
	}
	state.RuntimeRunID = run.ID
	if run.CheckpointVersion >= cloudAgentCheckpointVersion {
		if err := cloudAgentRestoreTranscript(run, &state); err != nil {
			return state, err
		}
	}
	if state.Events == nil {
		state.Events = []CloudAgentEvent{}
	}
	if err := validateCloudAgentRuntime(run, &state); err != nil {
		return state, err
	}
	return state, nil
}

// cloudAgentRestoreTranscript 从消息表与事件窗口重建内存态。
//
// 事件全量在上游 cloud_agent_event_records；hydrate 只载入最近一窗
// （repository.CloudAgentJournalWindow），所以这里校验的是"窗口的一致性"而不是全量：
//   - 消息按 kind 分别连续 1..n，且条数等于 run.MessageCount；
//   - 事件窗口的**最后一条** seq 必须等于 run.EventCount —— 否则说明有行没读到
//     （删一半、写失败），宁可判"记录不完整"也不带着缺口继续跑；
//   - EventSeqBase = EventCount - len(窗口)，窗口为空时只能是 EventCount==0。
func cloudAgentRestoreTranscript(run *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	canonical := make([]map[string]any, 0, len(run.Transcript))
	history := make([]providerTextMessage, 0, len(run.Transcript))
	for _, record := range run.Transcript {
		switch record.Kind {
		case cloudAgentMessageKindCanonical:
			var message map[string]any
			if err := json.Unmarshal([]byte(record.MessageJSON), &message); err != nil {
				return fmt.Errorf("decode Agent message: %w", err)
			}
			if record.Sequence != len(canonical)+1 {
				return errors.New("Agent message sequence is incomplete")
			}
			canonical = append(canonical, message)
		case cloudAgentMessageKindHistory:
			var message providerTextMessage
			if err := json.Unmarshal([]byte(record.MessageJSON), &message); err != nil {
				return fmt.Errorf("decode Agent history: %w", err)
			}
			if record.Sequence != len(history)+1 {
				return errors.New("Agent history sequence is incomplete")
			}
			history = append(history, message)
		default:
			return errors.New("Agent message kind is unsupported")
		}
	}
	if len(run.Transcript) != run.MessageCount {
		return fmt.Errorf("Agent execution transcript is incomplete: rows=%d count=%d", len(run.Transcript), run.MessageCount)
	}
	events := make([]CloudAgentEvent, 0, len(run.Journal))
	for _, row := range run.Journal {
		var event CloudAgentEvent
		if err := json.Unmarshal([]byte(row.EventJSON), &event); err != nil {
			return fmt.Errorf("decode Agent event: %w", err)
		}
		// 事件身份必须自洽：seq/runId/eventId 任何一处与行不一致，就说明记录被动过。
		if event.Seq != row.Sequence || event.RunID != run.ID || event.EventID != fmt.Sprintf("%s:%d", run.ID, row.Sequence) {
			return errors.New("Agent event identity is invalid")
		}
		events = append(events, event)
	}
	// 上游修正（#600）：窗口为空但水位非零 = 有行没读到，不能把"一行都没有"当成空日志。
	if len(events) == 0 && run.EventCount != 0 {
		return errors.New("Agent execution journal is incomplete")
	}
	if len(events) > 0 && events[len(events)-1].Seq != run.EventCount {
		return fmt.Errorf("Agent execution journal is incomplete: lastSeq=%d count=%d", events[len(events)-1].Seq, run.EventCount)
	}
	state.EventSeqBase = run.EventCount - len(events)
	if state.EventSeqBase < 0 {
		return fmt.Errorf("Agent execution journal watermark is invalid: count=%d rows=%d", run.EventCount, len(events))
	}
	state.Canonical.Messages, state.TextHistory = canonical, history
	state.Events = events
	return nil
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
	if state.ImageInspectCalls < 0 {
		return errors.New("Agent runtime image inspection budget is invalid")
	}
	if state.ReadToolCalls < 0 {
		return errors.New("Agent runtime read tool budget is invalid")
	}
	for nodeID, count := range state.ImageInspectCounts {
		if strings.TrimSpace(nodeID) == "" || count < 0 {
			return errors.New("Agent runtime image inspection counts are invalid")
		}
	}
	for key, count := range state.ImageInspectionReads {
		if strings.TrimSpace(key) == "" || count < 0 {
			return errors.New("Agent runtime image inspection read history is invalid")
		}
	}
	for key, count := range state.ToolReadReplays {
		if strings.TrimSpace(key) == "" || count < 0 {
			return errors.New("Agent runtime read replay history is invalid")
		}
	}
	if (state.Request.Budget.MaxGenerationTasks > 0 && state.Generations > state.Request.Budget.MaxGenerationTasks) || (state.Request.Budget.MaxVideoSeconds > 0 && state.VideoSeconds > state.Request.Budget.MaxVideoSeconds) {
		return errors.New("Agent runtime generation budget is invalid")
	}
	if state.CallIndex < 0 || state.CallIndex > len(state.Calls) || len(state.Calls) > cloudAgentMaxToolCalls {
		return errors.New("Agent runtime call cursor is invalid")
	}
	for toolName, repair := range state.ToolRepairs {
		if toolName == "" || utf8.RuneCountInString(toolName) > 80 || repair.Attempt < 1 || repair.Attempt > cloudAgentToolAttemptLimit || repair.GroupID == "" {
			return errors.New("Agent runtime tool repair state is invalid")
		}
		if err := validateCloudAgentID(repair.GroupID, "工具纠错组 ID", 240); err != nil {
			return err
		}
	}
	if state.ActiveTaskID != "" && state.MediaTaskID != "" {
		return errors.New("Agent runtime has multiple active tasks")
	}
	if state.HistoryIncludesCurrent && len(state.Canonical.Messages) < len(state.TextHistory) {
		return errors.New("Agent compacted history exceeds canonical transcript")
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
		if !cloudAgentContainsString(state.TaskIDs, state.MediaTaskID) || state.CallIndex >= len(state.Calls) || (state.Calls[state.CallIndex].Function.Name != "generate_media" && state.Calls[state.CallIndex].Function.Name != "image_layer_split") {
			return errors.New("Agent runtime media task is not attached to current call")
		}
	}
	if state.AutoPreparedMedia != nil {
		if state.AutoPreparedCallHash == "" || state.CallIndex < 0 || state.CallIndex >= len(state.Calls) {
			return errors.New("Agent runtime auto media preparation is invalid")
		}
		current := state.Calls[state.CallIndex]
		if current.Function.Name != "generate_media" && current.Function.Name != "image_layer_split" {
			return errors.New("Agent runtime auto media preparation is not attached to a media call")
		}
		if state.AutoPreparedCallHash != cloudAgentApprovalCallHash(current) {
			return errors.New("Agent runtime auto media preparation does not match current call")
		}
	}
	if state.Decisions == nil || state.Events == nil {
		return errors.New("Agent runtime maps are missing")
	}
	if state.EventSeqBase < 0 {
		return errors.New("Agent runtime event watermark is invalid")
	}
	// 内存里只有"最近一窗 + 本次转移新产生的部分"，超过健全上限说明窗口没有按
	// CloudAgentJournalWindow 载入（或序号基准算错），此时不能继续推进。
	if len(state.Events) > cloudAgentEventWindowSanityLimit {
		return errors.New("Agent runtime event window is too large")
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
	// 轮内唯一裁剪 = 图片：超出保留轮次的看图结果换成文字回执（正文一律保留）。
	// 它必须在压缩判定之前跑：图片是最贵的一类内容，先移出再评估 token 压力才有意义。
	// 传运行态：裁剪占位符要带上模型为这些节点写下的观察，否则模型会把"系统没接上观察"
	// 读成"我没看过"，从而反复重看同一批图（真机实测 35 张图 140 次看图）。
	if changed, pruned := cloudAgentPruneInspectedImages(&state.Canonical, state); changed && run.ID != "" {
		state.event(run.ID, "context_images_pruned", map[string]any{
			"prunedImages": pruned, "retentionRounds": cloudAgentImageRetentionRounds,
			"text": "已把超出保留轮次的看图结果移出模型上下文（保留文字回执与 nodeId）",
		})
		// 同一件事再落一条统一的过渡事件（设计 §4 的 context_transition）：前端趋势图与
		// "最近变化"都不必再为每种治理动作各写一套解析。
		afterRaw, _ := json.Marshal(state.Canonical.Messages)
		state.event(run.ID, "context_transition", map[string]any{
			"kind": "body_eviction", "reason": "image_prune",
			"after":        map[string]any{"sourceBytes": len(afterRaw), "estimatedTokens": estimateCloudAgentTokens(afterRaw), "historyMessages": len(state.Canonical.Messages)},
			"prunedImages": pruned, "retentionRounds": cloudAgentImageRetentionRounds,
			"text": "图片裁剪：移出超出保留轮次的看图结果",
		})
	}
	if run.ID != "" {
		if err := validateCloudAgentRuntime(run, state); err != nil {
			return fmt.Errorf("%w: %v", errCloudAgentCheckpoint, err)
		}
		for index, event := range state.Events {
			sequence := state.EventSeqBase + index + 1
			if event.Seq != sequence || event.RunID != run.ID || event.EventID != fmt.Sprintf("%s:%d", run.ID, sequence) {
				return fmt.Errorf("%w: Agent event sequence or identity is invalid", errCloudAgentCheckpoint)
			}
		}
	}
	checkpoint := *state
	checkpoint.Canonical.Messages = nil
	checkpoint.TextHistory = nil
	checkpoint.Events = nil
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		return fmt.Errorf("%w: %v", errCloudAgentCheckpoint, err)
	}
	if len(raw) > cloudAgentStateHardLimitBytes {
		return fmt.Errorf("%w: Agent 状态超过 512KB 上限", errCloudAgentCheckpoint)
	}
	run.CanvasID, run.ActiveTaskID, run.MediaTaskID = state.Request.CanvasID, state.ActiveTaskID, state.MediaTaskID
	run.ParentID = state.ParentID
	if run.Title == "" {
		run.Title = truncateRunes(state.Request.Prompt, 80)
	}
	// 事件按"窗口 + 水位"落库：run.Journal 只是内存窗口的映射（窗口之外的历史仍在
	// 事件表里），写入侧（repository.MutateCloudAgent）只追加 seq > 旧水位的行，
	// 因此窗口左移不会丢历史。
	run.Journal = make([]model.CloudAgentEventRecord, 0, len(state.Events))
	for _, event := range state.Events {
		body, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("%w: encode Agent event: %v", errCloudAgentCheckpoint, err)
		}
		run.Journal = append(run.Journal, model.CloudAgentEventRecord{RunID: run.ID, UserID: run.UserID, Sequence: event.Seq, EventJSON: string(body), CreatedAt: event.CreatedAt})
	}
	run.Transcript = make([]model.CloudAgentMessageRecord, 0, len(state.Canonical.Messages)+len(state.TextHistory))
	for index, message := range state.Canonical.Messages {
		body, err := json.Marshal(message)
		if err != nil {
			return fmt.Errorf("%w: encode Agent message: %v", errCloudAgentCheckpoint, err)
		}
		run.Transcript = append(run.Transcript, model.CloudAgentMessageRecord{RunID: run.ID, UserID: run.UserID, Kind: cloudAgentMessageKindCanonical, Sequence: index + 1, MessageJSON: string(body)})
	}
	for index, message := range state.TextHistory {
		body, err := json.Marshal(message)
		if err != nil {
			return fmt.Errorf("%w: encode Agent history: %v", errCloudAgentCheckpoint, err)
		}
		run.Transcript = append(run.Transcript, model.CloudAgentMessageRecord{RunID: run.ID, UserID: run.UserID, Kind: cloudAgentMessageKindHistory, Sequence: index + 1, MessageJSON: string(body)})
	}
	// 水位 = 窗口之前已入库的条数 + 本次载入/新增的窗口条数，必须与事件表里的
	// 最大 seq 一致，否则下一次载入的窗口与水位会对不上。
	run.CheckpointVersion, run.EventCount, run.MessageCount = cloudAgentCheckpointVersion, state.EventSeqBase+len(state.Events), len(run.Transcript)
	run.StateJSON = string(raw)
	return nil
}

// cloudAgentMessageKindCanonical / cloudAgentMessageKindHistory 是消息表的两种 kind。
const (
	cloudAgentMessageKindCanonical = "canonical"
	cloudAgentMessageKindHistory   = "history"
)

// cloudAgentBoundEventPayload 给单条事件载荷封顶。
//
// 事件不再进检查点，但一条超限载荷仍会撞校验上限（validateCloudAgentRuntime 会判
// "event payload is too large" 并终止整轮）。这里只保留必要的可读回执并标记
// requiresRefresh：让前端去拉全量，而不是判死整轮。
func cloudAgentBoundEventPayload(payload map[string]any) map[string]any {
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) <= cloudAgentEventPayloadLimitBytes {
		return payload
	}
	bounded := map[string]any{"requiresRefresh": true, "payloadSlimmedBytes": len(raw)}
	for _, key := range []string{"canvasId", "operation", "callId", "text"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			bounded[key] = truncateRunes(value, 400)
		}
	}
	if _, ok := bounded["text"]; !ok {
		bounded["text"] = "这一步的事件载荷过大，明细已省略；画布内容以服务端为准"
	}
	return bounded
}

// cloudAgentCheckpointVersion 是"消息与事件都在表里"的检查点形态版本（定义在 model 里）。
const cloudAgentCheckpointVersion = model.CloudAgentCheckpointVersion

const (
	// cloudAgentStateHardLimitBytes 是检查点（只含控制面）的硬上限。
	cloudAgentStateHardLimitBytes = 512 << 10
	// cloudAgentEventPayloadLimitBytes 是单条事件载荷的上限。
	cloudAgentEventPayloadLimitBytes = 128 << 10
	// cloudAgentEventWindowSanityLimit 是校验用的健全上限：内存里的事件窗口 + 一次转移新产生的事件
	// 不应超过它，超过就是异常（真正的历史都在事件表里）。
	cloudAgentEventWindowSanityLimit = 512
	// cloudAgentRunEventPageLimit 是运行详情默认返回的事件条数：等于内存里保留的尾部窗口
	// （repository.CloudAgentJournalWindow），因此默认读取不额外查事件表；要更早的记录由
	// 客户端用 eventLimit 显式索取。这个数必须与 openapi 的默认页说明保持一致。
	cloudAgentRunEventPageLimit = repository.CloudAgentJournalWindow
)

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
	// 事件已全量落库，运行详情只返回一页：默认是尾部窗口，sinceSeq 只取增量。
	// 四个位置字段与 events 一起返回，客户端据此判断"是否还有更早的记录"。
	out.Events = s.cloudAgentRunEventsForView(task.UserID, run, &state, view.SinceSeq, view.EventLimit)
	// EventSeqBase 必须与真正返回的这一页对齐（eventLimit 把它收窄时也一样），
	// 不变量 events[i].seq == eventSeqBase + i + 1 才成立。页为空时退回窗口水位：
	// 那时没有"首条事件"，但仍然要能说明窗口在整条日志里的位置。
	out.EventSeqBase = state.EventSeqBase
	if len(out.Events) > 0 {
		out.EventSeqBase = out.Events[0].Seq - 1
	}
	out.EventCount = s.cloudAgentRunEventCount(task.UserID, run, &state)
	if len(out.Events) > 0 {
		out.LatestSeq = out.Events[len(out.Events)-1].Seq
	}
	if view.SinceSeq == 0 && len(out.Events) < out.EventCount {
		out.EventsTruncated = true
	}
	out.Approval = state.Approval
	if cloudAgentRunTerminal(run.Status) {
		out.Approval = nil
	}
	out.Step = state.Step
	if stateErr == nil && state.ActiveTaskID != "" && (run.Status == "running" || run.Status == "queued") {
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
		// AmountMicrocredits is the reservation/quote, not necessarily the amount
		// finally charged. A run can reserve 100000 microcredits and settle at
		// 100000 microcredits (= 0.1 credits), or be refunded altogether. Never
		// expose the reservation as spend, otherwise the Agent claims a charge
		// that did not happen and budget/usage copy diverges from billing.
		if order.Status == model.BillingStatusSettled {
			out.SpentCredits += float64(order.ActualAmountMicrocredits) / float64(CreditScale)
		}
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

// wakeCloudAgentScheduler lets a completed model step resume its Agent without
// waiting for the periodic recovery scan. The ticker remains authoritative for
// other workers and missed in-process notifications.
func (s *Service) wakeCloudAgentScheduler() {
	if s == nil || s.agentSchedulerWake == nil {
		return
	}
	select {
	case s.agentSchedulerWake <- struct{}{}:
	default:
	}
}

func (s *Service) advanceCloudAgent(run *model.CloudAgentExecution) (err error) {
	defer func() {
		if errors.Is(err, errCloudAgentCheckpoint) {
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
	// 单步边界每次推进都重新解析：管理员改配置后，正在跑的这一轮下一步就用新值。
	state.StepLimits, err = s.cloudAgentStepLimits()
	if err != nil {
		return err
	}
	if state.ActiveTaskID != "" {
		task, err := s.repo.TaskForUser(run.UserID, state.ActiveTaskID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return s.terminateCloudAgent(run, "Agent 模型任务已不存在，本轮已停止")
		}
		if err != nil {
			return err // Transient database failures must not terminate a live task.
		}
		// 停在压缩上时，这个在跑的任务就是压缩调用：它的结果只用来生成检查点，
		// 不走"正文/工具调用"那套解析，也不会计入步数。
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
			// StopReason / StopReasonKind 是上游这一步的终止原因（原文 + 内部词表）。
			// 旧任务结果没有这两个键 → 空串 → 归一化成 unknown，不改变任何既有分支。
			StopReason     string `json:"stopReason"`
			StopReasonKind string `json:"stopReasonKind"`
			// 三个解析事实由 parser 记录（不从 kind 反推）：是否收到终态事件、原因原文是否非空、
			// 是否收到该协议的流结束标记。它们用于统计各 provider 的终结信号覆盖率。
			TerminalEventSeen bool `json:"terminalEventSeen"`
			StopReasonPresent bool `json:"stopReasonPresent"`
			StreamDoneSeen    bool `json:"streamDoneSeen"`
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
		// 升级开关的复位：只有"这一步仍在截断重试中"才保留开关，其余情况（重试被接受、
		// 或这一步撞上空输出/超时/参数截断等别的失败）先复位到升级前的值，再交给后面的阶梯——
		// 否则会把那些阶梯刚设的开关又覆盖回去。
		stepKindForRestore := strings.TrimSpace(result.StopReasonKind)
		if stepKindForRestore == "" {
			stepKindForRestore = normalizeCloudAgentStopReason(result.StopReason)
		}
		if cloudAgentStepStopDisposition(&state, stepKindForRestore) != cloudAgentStepDispositionRetry {
			cloudAgentRestoreEscalationSwitches(&state)
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
		// 单步墙钟到点同样是可恢复失败：关思考重试一次，而不是把整轮判死。
		if cloudAgentStepTimedOut(task) && state.StepTimeoutEscalated < cloudAgentMaxStepTimeoutEscalations {
			return s.correctCloudAgentStepTimeout(run, &state)
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
			// 终止原因先归一化再记账：这一步是"说完了""要调工具"还是"被输出上限截断"，
			// 从上游的权威字段读，不再靠匹配错误字符串倒推（见 cloud_agent_stop_reason.go）。
			stepStopKind := strings.TrimSpace(result.StopReasonKind)
			if stepStopKind == "" {
				stepStopKind = normalizeCloudAgentStopReason(result.StopReason)
			}
			// 处置先定，再决定要不要产生副作用：截断步的正文与工具调用都可能是半截的，
			// 因此它不写 assistant/canonical、不记观察，也不执行任何已解析出的工具调用。
			stepDisposition := cloudAgentStepStopDisposition(&state, stepStopKind)
			stepFacts := cloudAgentStepStopFacts{
				TerminalEventSeen: result.TerminalEventSeen,
				StopReasonPresent: result.StopReasonPresent,
				StreamDoneSeen:    result.StreamDoneSeen,
			}
			if run.ID != "" {
				state.event(run.ID, "model_step_stop", cloudAgentStopReasonPayload(
					state.Step, task.ID, result.StopReason, stepStopKind, stepDisposition, stepFacts,
					len(result.Text), len(result.Reasoning), len(calls)))
			}
			if stepDisposition == cloudAgentStepDispositionRetry {
				// 用空输出那条阶梯（关思考 + 放大输出预算）重发同一步一次，不追加催办消息。
				// 只有 length 会走到这里：pause / content_filter / refusal / incomplete_unknown
				// 都不是"重发就能变好"的失败，改请求形状也续不上挂起的回合。
				cloudAgentEscalateTruncatedStep(run.ID, &state)
				return cloudAgentSave(current, &state)
			}
			if stepDisposition == cloudAgentStepDispositionFail {
				// 这一步的结果不能采纳：不执行半截的工具调用、不发布半截正文，按 kind 如实终止本轮。
				message := cloudAgentStepStopFailureMessage(stepStopKind)
				current.Status = "failed"
				current.FailureMessage = truncateRunes(message, 1000)
				cloudAgentDropInterjections(run.ID, "本轮已结束："+truncateRunes(message, 120), &state)
				state.event(run.ID, "run_failed", map[string]any{
					"text": message, "reason": cloudAgentStepStopFailureReason(stepStopKind),
					"stopReason": result.StopReason, "stopReasonKind": stepStopKind,
				})
				return cloudAgentSave(current, &state)
			}
			if result.Reasoning != "" {
				state.event(run.ID, "reasoning_message", map[string]any{"messageId": task.ID + ":reasoning", "text": truncateRunes(result.Reasoning, 8000)})
			}
			// 无工具调用 = 候选收尾：**先过闸门再发布**（工作项 A，见 cloud_agent_completion.go）。
			// 正文在流式阶段已经到过前端（assistant_delta/assistant_snapshot），所以这里必须显式
			// 落一个 final 标记，把它收敛成"最终答复"或"过程说明"；否则用户会把中间稿当结论。
			// 带工具调用的正文只是过程说明（final=false）：它不是本轮答复，后面还有这一步的动作。
			completion := cloudAgentCompletionBlock{}
			if len(calls) == 0 {
				completion = cloudAgentEvaluateCompletion(&state)
				// 被截断的正文不是完整答复：不允许它作为本轮最终答复发布，
				// 否则用户会拿到一段半句话的结论（见 cloud_agent_completion.go）。
				completion = cloudAgentBlockTruncatedCompletion(completion, stepStopKind)
			}
			if result.Text != "" {
				state.event(run.ID, "assistant_message", map[string]any{"messageId": task.ID, "text": result.Text, "final": completion.Final})
				// 普通正文可能是读取失败或多图混合回答，不能自动归为某张图的视觉事实（上游口径）。
				if len(calls) == 0 {
					state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "assistant", "content": result.Text})
				}
			}
			// 上一批附图的观察就写在这一步的正文里：只有逐句点名 nodeId 的句子才入账，
			// 且只认"带工具调用"的正文（那是看图后的过程说明，不是候选收尾稿）。
			// 入账后这些节点在裁剪占位符里就能带上模型自己的观察，不必再重看一遍。
			recordedObservations := state.cloudAgentRecordImageObservations(result.Text, len(calls) > 0)
			// 观察入账后同步锚点：RequiresVisualInspection 只在观察真正落账后才翻 false。
			state.cloudAgentSyncVisualAnchor()
			if recorded := recordedObservations; recorded > 0 && run.ID != "" {
				state.event(run.ID, "context_transition", map[string]any{
					"kind": "image_observation", "reason": "vision_ledger",
					"text":         fmt.Sprintf("视觉账本：记下 %d 张图片的观察", recorded),
					"observations": recorded, "pendingImages": len(state.PendingImageObservations),
				})
			}
			state.ActiveTaskID = ""
			if len(calls) > 0 || strings.TrimSpace(result.Text) != "" {
				state.EmptyOutputNudged = 0
			}
			state.Canonical.ToolChoice = "auto"
			state.Calls = calls
			state.CallIndex = 0
			// 整批预检：在任何业务副作用之前判定每个调用的准入（工具表/权限/参数 schema），
			// 并把同批写入收敛成一个（见 cloud_agent_tool_preflight.go）。
			state.CallAdmissions = cloudAgentPreflightBatch(&state, calls)
			// 新的一批：清空上一批的画布版本记录，并固定本步读取时的画布版本。
			state.CanvasBatchHashes = nil
			state.StepSnapshotHash = cloudAgentCaptureStepSnapshotHash(calls)
			if len(calls) > 0 {
				state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "assistant", "content": result.Text, "tool_calls": calls})
			}
			if len(calls) == 0 {
				if completion.Final {
					current.Status = "completed"
					return cloudAgentSave(current, &state)
				}
				// 用户刚插话：下一步它就会进上下文，既不需要催办，也不该计入催办额度。
				if completion.Interjected && !cloudAgentStepBudgetExhausted(&state) {
					return cloudAgentSave(current, &state)
				}
				if !cloudAgentStepBudgetExhausted(&state) {
					attempt, exhausted := cloudAgentNoteCompletionBlocked(&state, completion.Fingerprint)
					if !exhausted {
						completion.Attempt = attempt
						cloudAgentCompletionBlockedNudge(run.ID, &state, completion)
						return cloudAgentSave(current, &state)
					}
					completion.Attempt = attempt
				}
				// 额度用尽或已经开不起下一步：如实终止，且不把中间稿当最终答复。
				return cloudAgentFailBlockedCompletion(current, &state, run.ID, completion)
			}
			return cloudAgentSave(current, &state)
		})
	}
	if state.CallIndex < len(state.Calls) {
		if handled, err := s.advanceCloudAgentReadBatch(run, &state); handled {
			return err
		}
		return s.advanceCloudAgentTool(run, &state)
	}
	// 兜底 flush：本批调用都执行完了（无论最后一个调用是不是看图、有没有被中断），
	// 缓冲里的图片必须在这里合并成一条 user 消息落到全部 tool 结果之后。少了这一步，
	// "看图不是最后一个调用"的批次会把图片永久丢掉，而对应的 tool 回执已经在历史里。
	// 幂等：正常路径（最后一个调用就是看图）已经在 cloudAgentToolResult 里 flush 过，这里是空操作。
	cloudAgentFlushPendingImages(&state)
	// 已经请求过压缩：这次推进只负责把压缩调用发出去（它不计入步数，见 enqueueCloudAgentTask）。
	if state.ContextCompaction != nil && state.ContextCompaction.Status == "requested" {
		return s.enqueueCloudAgentContextCompaction(run, &state)
	}
	contextBudget := s.cloudAgentContextBudgetForRequest(state.Request)
	if compactCloudAgentContext(&state.Canonical, contextBudget) {
		// Evicted read bodies must be obtainable again after compaction.
		state.ProfileReads = nil
	}
	if stepLimit := cloudAgentStepLimit(state.Request); stepLimit > 0 && state.Step >= stepLimit {
		return s.failCloudAgent(run, &state, fmt.Sprintf("达到 %d 次模型调用上限，本轮已停止", stepLimit))
	}
	cloudAgentDrainInterjections(run.ID, &state)
	// 上一步的模型调用已经回来，先用它的上游实测用量更新压力锚点，再发下一步：
	// 下一步的读数与后面的正文裁剪判定都要用到这份锚点。
	// 按 userId 限定查日志（上游 #601 的口径）：只认"本用户、成功、text 能力"的那条调用，
	// 否则别人的或失败的同 taskId 记录会把压力锚到错误的信封上。
	s.recordCloudAgentTokenAnchor(run.UserID, &state)
	// 轮内唯一裁剪 = 图片：超出保留轮次的看图结果换成文字回执（正文一律保留）。
	// 它必须在压缩判定之前跑：图片是最贵的一类内容，先移出再评估 token 压力才有意义。
	if changed, pruned := cloudAgentPruneInspectedImages(&state.Canonical, &state); changed {
		state.event(run.ID, "context_images_pruned", map[string]any{
			"prunedImages": pruned, "retentionRounds": cloudAgentImageRetentionRounds,
			"text": "已把超出保留轮次的看图结果移出模型上下文（保留文字回执与 nodeId）",
		})
		// 同一件事再落一条统一的过渡事件（设计 §4 的 context_transition）：前端趋势图与
		// "最近变化"都不必再为每种治理动作各写一套解析。
		afterRaw, _ := json.Marshal(state.Canonical.Messages)
		state.event(run.ID, "context_transition", map[string]any{
			"kind": "body_eviction", "reason": "image_prune",
			"after":        map[string]any{"sourceBytes": len(afterRaw), "estimatedTokens": estimateCloudAgentTokens(afterRaw), "historyMessages": len(state.Canonical.Messages)},
			"prunedImages": pruned, "retentionRounds": cloudAgentImageRetentionRounds,
			"text": "图片裁剪：移出超出保留轮次的看图结果",
		})
	}
	canonical, contextErr := s.cloudAgentModelContext(run, &state, contextBudget)
	if contextErr != nil {
		// 真实任务帧与用户要求有时在下一步建模时就超过输入预算；
		// 它会先于下方常规 token 判据失败，仍需给语义压缩一次机会。
		if errors.Is(contextErr, errCloudAgentContextOverBudget) {
			if requested, err := s.cloudAgentRequestCompaction(run, &state, contextBudget, contextBudget.CompactAtTokens); err != nil || requested {
				return err
			}
		}
		var appErr *AppError
		if errors.As(contextErr, &appErr) {
			return s.failCloudAgent(run, &state, appErr.Message)
		}
		return contextErr
	}
	s.attachCloudAgentLessons(&canonical, run.UserID, cloudAgentLessonTaskText(&state))
	// 看图结果按参考素材水合：上下文里只有 resource:ID，真实图片字节在这一步（请求期）才进
	// 到模型请求的 referenceImages 里，不进检查点，也不要求上游能访问部署地址。
	references, refErr := s.cloudAgentImageReferences(run.UserID, state.Request, &canonical)
	if refErr != nil {
		return s.failCloudAgent(run, &state, cloudAgentSafeToolError(refErr))
	}
	// 装配已经定下"这一次真正随请求发出去的是哪几张"：超出模型图片上限的旧图会被换成文字
	// 占位（丢的是最旧的）。账本只认这份实际送达集合 —— 否则被丢掉的图也会被当成"模型看过"，
	// 一次误记会跟着"命中账本就不再附图"固化下来（评审明确要求这条契约）。
	deliveredNodes := deliveredImageNodeIDs(canonical)
	state.cloudAgentSettleImageDelivery(deliveredNodes, len(deliveredNodes))
	// 空输出升级重试：关思考 + 放大输出预算，避免"思考吃满预算、正文为空"再次发生。
	stepThinking := cloudAgentReasoningEnabled(state.Policy.ReasoningMode) && !state.ForceThinkingOff
	stepOutputTokens := cloudAgentStepOutputBudget(state.StepLimits, state.BoostStepOutputBudget)
	input := map[string]any{"mode": "text", "prompt": state.Request.Prompt, "agentRequests": map[string]any{"canonical": canonical}, "config": map[string]any{"channelId": state.Request.ChannelID, "channelModelKey": state.Request.ChannelModelKey, "model": firstNonEmpty(state.Request.ChannelModelKey, state.Request.Model)}, "textOptions": map[string]any{"stream": true, "thinking": stepThinking, "maxOutputTokens": stepOutputTokens, "strictTools": s.cloudAgentStepStrictTools(&state)}}
	if len(references) > 0 {
		input["referenceImages"] = references
	}
	tokens, tokenErr := cloudAgentRequestEstimatedTokens(&canonical)
	if tokenErr != nil {
		return s.failCloudAgent(run, &state, "模型上下文估算失败，请稍后重试")
	}
	// 超预算不再直接判死：先暂停步进、把历史压成结构化检查点，压完用压缩后的上下文继续本轮。
	// 次数上限（cloudAgentMaxCompactionsPerRun）用完仍超预算时，才回到下面的判死路径。
	// 分工：这是轮内的**语义压缩**（触发者是 token 线，或没配窗口时的字节/条数兜底）；
	// compactCloudAgentContext 是轮内可重读正文的**就地卸载**，跨轮 textHistory 由
	// trimCloudAgentTextHistory 兜底——三者对象不同、不会互相打架。
	if requested, err := s.cloudAgentRequestCompaction(run, &state, contextBudget, tokens); err != nil || requested {
		return err
	}
	if tokens > contextBudget.InputBudgetTokens {
		return s.failCloudAgent(run, &state, cloudAgentContextBudgetMessage(contextBudget))
	}
	req := CreateTaskRequest{ProjectID: state.Request.CanvasID, Type: "canvas_text", Operation: cloudAgentStepOperation, Prompt: state.Request.Prompt, Model: state.Request.Model, LogicalModelID: state.Request.LogicalModelID, Input: input}
	return s.enqueueCloudAgentTask(run, &state, req, nil)
}

func compactCloudAgentContext(request *canonicalAgentRequest, budget cloudAgentContextBudget) bool {
	if request == nil {
		return false
	}
	tokens, err := cloudAgentRequestEstimatedTokens(request)
	if err != nil || tokens < budget.CompactAtTokens {
		return false
	}
	// Retain the latest complete tool turn. Never remove call/result envelopes,
	// user instructions, call arguments or write receipts to fabricate a summary.
	cut := len(request.Messages) - 1
	for cut > 0 && stringField(request.Messages[cut], "role") == "tool" {
		cut--
	}
	changed := false
	for _, message := range request.Messages[:max(0, cut)] {
		if stringField(message, "role") != "tool" {
			continue
		}
		var result map[string]any
		if json.Unmarshal([]byte(stringField(message, "content")), &result) != nil || result["contextCompacted"] == true {
			continue
		}
		// Only omit re-readable bodies. Preserve IDs, errors, generation status,
		// approvals and all other structured facts verbatim.
		omitted := false
		if nodes, ok := result["nodes"].([]any); ok {
			facts := make([]map[string]any, 0, len(nodes))
			for _, value := range nodes {
				if node, ok := value.(map[string]any); ok {
					fact := map[string]any{"nodeId": node["id"]}
					for _, key := range []string{"generation", "generationDraft", "outputReference"} {
						if value, exists := node[key]; exists {
							fact[key] = value
						}
					}
					if len(fact) > 1 {
						facts = append(facts, fact)
					}
				}
			}
			if len(facts) > 0 {
				result["observedFacts"] = facts
			}
		}
		for _, key := range []string{"content", "nodes"} {
			if _, exists := result[key]; exists {
				delete(result, key)
				omitted = true
			}
		}
		if !omitted {
			continue
		}
		result["contextCompacted"] = true
		result["guidance"] = "历史读取正文已移出模型上下文；需要时重新读取。保留的历史状态不是当前状态，也不是执行授权，不得据此重复提交生成。"
		body, err := json.Marshal(result)
		if err != nil || len(body) >= len(stringField(message, "content")) {
			continue
		}
		message["content"] = string(body)
		changed = true
	}
	return changed
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
	if diagnostic := taskExecutionDiagnostic(task); diagnostic != nil && diagnostic.Code == string(ReasonUpstreamDNSFailed) {
		return "模型服务域名解析失败，请检查渠道域名和后端 DNS 配置；本轮已停止。请在任务中心检查模型任务 " + task.ID, string(ReasonUpstreamDNSFailed)
	}
	raw := strings.ToLower(task.Error)
	switch {
	case strings.Contains(task.Error, "没有返回内容"):
		// 思考模型的典型失败：整个输出预算被推理吃掉，正文与工具调用皆空。
		detail, reason = "上游连续返回空内容（通常是思考占满输出预算）；已自动关思考并放大预算重试仍失败，建议换用非思考模型或调小上下文", "model_empty_output"
	case strings.Contains(task.Error, cloudAgentStepTimeoutError):
		detail, reason = "单步模型调用超过执行时限仍未返回（长思考或上下文过大时常见）；已自动关思考重试仍超时，可在管理端调大 Agent 单步超时", "model_step_timeout"
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
	var readLoopErr *cloudAgentReadLoopError
	if errors.As(err, &readLoopErr) {
		return readLoopErr.Error()
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

func cloudAgentToolResult(runID string, state *cloudAgentRuntime, call cloudAgentCall, result any, err error) bool {
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
		if !ok || detail == nil {
			detail = map[string]any{}
		}
		// 模型漏了必填字段（或参数不是对象）时，即便业务校验抛的是普通 AppError，也按
		// "参数契约错误"处理：把本轮实际暴露的 schema 回给模型，它才改得对，归类也随之变成
		// schema_error（handoff 工作项 B：缺必填字段必须能自纠，且不能只算普通工具失败）。
		// 只在"业务侧确实按参数问题拒绝了"（400/422）时才这么归类：上游 5xx 或网络错误
		// 即便模型同时漏了字段，也该算上游故障，别把锅扣到参数上。
		var appErr *AppError
		var existingArgumentErr *cloudAgentArgumentError
		if missing := cloudAgentMissingRequiredArguments(cloudAgentAdvertisedTools(state), call); len(missing) > 0 &&
			!errors.As(err, &existingArgumentErr) &&
			errors.As(err, &appErr) && appErr != nil && (appErr.Status == 400 || appErr.Status == 422) {
			err = &cloudAgentFieldArgumentError{
				error: &cloudAgentArgumentError{err},
				Field: strings.Join(missing, ","), Issue: "required",
			}
		}
		message := cloudAgentSafeToolError(err)
		detail["error"] = message
		// 预检结论：机器可读地说明"为什么这次没执行"。批次策略类结论不是模型的参数错，
		// 因此单独给 reason=call_skipped 与可重发的 requiredAction，并跳过通用归类。
		var preflightErr *cloudAgentCallAdmissionError
		admissionSkip := false
		if errors.As(err, &preflightErr) {
			detail["admission"] = preflightErr.Admission
			if preflightErr.Field != "" {
				detail["field"] = preflightErr.Field
			}
			if cloudAgentAdmissionIsSkip(preflightErr.Admission) {
				admissionSkip = true
				detail["reason"] = "call_skipped"
				detail["retryable"] = true
				detail["requiredAction"] = "resubmit_next_step"
				detail["errorClass"], detail["errorClassLabel"] = "call_skipped", cloudAgentToolErrorLabel("call_skipped")
				payload["errorClass"] = "call_skipped"
			}
		}
		var admissionErr *cloudAgentMediaAdmissionError
		if errors.As(err, &admissionErr) {
			detail["reason"], detail["nodeId"] = admissionErr.Reason, admissionErr.NodeID
		}
		var argumentErr *cloudAgentArgumentError
		if errors.As(err, &argumentErr) {
			detail["reason"] = "invalid_tool_arguments"
			var fieldErr *cloudAgentFieldArgumentError
			if errors.As(err, &fieldErr) {
				detail["field"], detail["issue"] = fieldErr.Field, fieldErr.Issue
			}
			// Use this run's advertised contract, including its permission scope.
			for _, tool := range cloudAgentAdvertisedTools(state) {
				function, _ := tool["function"].(map[string]any)
				if function["name"] == call.Function.Name {
					detail["parameters"] = function["parameters"]
					break
				}
			}
			detail["guidance"] = "本次调用未执行，请按 parameters 修正参数后重试，不要重复提交相同的错误参数"
			if call.Function.Name == "canvas_get_state" {
				detail["exampleArguments"] = map[string]any{}
			}
			if call.Function.Name == "canvas_apply_ops" {
				detail["exampleArguments"] = map[string]any{"snapshotHash": "<canvas_get_state.snapshotHash>", "ops": []any{map[string]any{"type": "add_node", "id": "<new-node-id>", "nodeType": "text", "content": "<content>"}}}
			}
		}
		// 稳定归类 + 可行动字段（handoff 工作项 B 第一步）：只加标注，不改任何放行/拒绝判定。
		// allowed 与执行器用的是同一份判定（cloudAgentToolAllowed 是纯函数，结果一致），
		// 只在失败路径上算一次，成功路径不付这份开销。call_skipped 已经在上面定过性，
		// 不要再被通用归类覆盖成"参数错误"。
		if class, retryable, requiredAction := cloudAgentToolErrorClass(state.Request, call, err, cloudAgentToolAllowed(state.Request, call.Function.Name)); class != "" && !admissionSkip {
			detail["errorClass"], detail["errorClassLabel"] = class, cloudAgentToolErrorLabel(class)
			detail["retryable"] = retryable
			if requiredAction != "" {
				detail["requiredAction"] = requiredAction
			}
			payload["errorClass"] = class
		}
		result = detail
		kind = "tool_failed"
		payload["text"] = message
	} else {
		payload["text"] = "工具执行成功"
	}
	if inspection, ok := result.(cloudAgentImageInspection); ok && err == nil {
		// 重复查看时只回执文字（ImageURL 为空），不再附图。
		staged := strings.TrimSpace(inspection.ImageURL) != ""
		if staged {
			cloudAgentStageImageInspection(state, inspection)
		}
		lastOfBatch := state.CallIndex+1 >= len(state.Calls)
		if lastOfBatch {
			// 本批最后一次看图：把"这一批到底附了哪几张"写进回执，模型不必再靠试探猜。
			// 必须赶在下面序列化回执之前补上，否则这条说明对模型不可见。
			cloudAgentDescribeImageBatch(state, inspection.Receipt)
		}
		receipt, _ := json.Marshal(inspection.Receipt)
		payload["result"] = inspection.Receipt
		state.event(runID, kind, payload)
		// tool 角色只接受字符串内容（四种上游图式都是纯文本），因此工具回执照常入历史，
		// 图片另起一条 user 消息携带，并显式标注为数据而非指令。
		state.Canonical.Messages = append(state.Canonical.Messages,
			map[string]any{"role": "tool", "tool_call_id": call.ID, "content": string(receipt)})
		// 一批里可能有多个调用（模型一次发起并行 tool calls），上游要求
		// assistant(tool_calls) 之后紧跟每一个 tool_call_id 的 tool 消息，所以图片
		// 不能插在 tool 结果之间。只有本批最后一个调用执行完，才把整批缓冲合并成
		// 一条 user 消息追加在全部 tool 结果之后。
		if lastOfBatch {
			cloudAgentFlushPendingImages(state)
		}
		state.CallIndex++
		state.Approval = nil
		// 看图是只读成功路径，不占自动纠错名额（那名额只给写/生成类工具的预执行参数错误）。
		return false
	}
	exhausted := cloudAgentTrackToolRepair(runID, state, call, result, err, payload)
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
	return exhausted
}

// cloudAgentModelToolResult 是"面向模型的那一份"工具结果。
//
// 审批卡明细是写给人看的，而且已经通过 approval_requested 事件进了 SSE/界面；
// 进入模型历史的工具结果只保留模型真正需要的东西：发生了什么、哪个节点、哪个快照。
// 这与 skill_read_file 的先例方向相反但同理（回执进 SSE，正文不进上下文）。
//
// 上游新增的 image_text_detect / image_annotation_render / task_get 等工具的返回里
// 不带 preview，因此原样穿过，不受影响。
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

// cloudAgentReadResultInContext reports whether the full cached result is still
// present in the canonical transcript. Context compaction deliberately removes old
// tool bodies, so a replay must restore the body once instead of returning a receipt
// that refers to history the model can no longer see.
func cloudAgentReadResultInContext(state *cloudAgentRuntime, result json.RawMessage) bool {
	if state == nil || len(result) == 0 {
		return false
	}
	// Unit-level callers may exercise the cache helper without constructing a
	// transcript. The real runtime always has the first tool message here.
	if len(state.Canonical.Messages) == 0 {
		return true
	}
	want := string(result)
	for _, message := range state.Canonical.Messages {
		if stringValue(message["role"]) == "tool" && stringValue(message["content"]) == want {
			return true
		}
	}
	return false
}

// cloudAgentBatchableReadTool identifies read calls that are independent of
// each other and safe to execute from one model tool-call response. Keeping
// this list limited to the cacheable read contract is important: mutations,
// approvals, user questions, media and vision calls remain ordered state
// transitions and must each retain their existing semantics.
func cloudAgentBatchableReadTool(name string) bool {
	return cloudAgentReadToolReadOnly(name)
}

func cloudAgentInvalidateReadCache(state *cloudAgentRuntime) {
	if state == nil {
		return
	}
	// Canvas writes invalidate only canvas projections. Skill documents,
	// profile preferences, and the model catalog do not change when a canvas is
	// edited, so retaining them avoids re-reading large unrelated tool results.
	for key := range state.ToolReadResults {
		if strings.HasPrefix(key, "canvas_get_state:") || strings.HasPrefix(key, "canvas_read_storyboard:") {
			delete(state.ToolReadResults, key)
			delete(state.ToolReadReplays, key)
		}
	}
}

// advanceCloudAgentReadBatch executes consecutive independent reads from the
// same model response before returning to the scheduler. The previous path
// checkpointed after every read, which turned one parallel tool-call response
// into N scheduler/database transitions. Results still get one tool message
// per call, in the original order, so every provider contract remains valid.
func (s *Service) advanceCloudAgentReadBatch(run *model.CloudAgentExecution, state *cloudAgentRuntime) (bool, error) {
	if run == nil || state == nil || (run.Status != "running" && run.Status != "queued") {
		return true, nil
	}
	if state.CallIndex < 0 || state.CallIndex >= len(state.Calls) {
		return false, nil
	}
	type outcome struct {
		call   cloudAgentCall
		result any
		err    error
	}
	outcomes := make([]outcome, 0, len(state.Calls)-state.CallIndex)
	// Reads are collected before one checkpoint write. During collection, their
	// successful receipts are not in Canonical.Messages yet, so the regular cache
	// layer cannot see that an earlier call in this same batch already returned
	// the full body. Track those keys here to avoid appending the same large result
	// repeatedly when a model emits duplicate parallel reads.
	batchReadResults := make(map[string]bool, len(state.Calls)-state.CallIndex)
	for index := state.CallIndex; index < len(state.Calls); index++ {
		call := state.Calls[index]
		if !cloudAgentBatchableReadTool(call.Function.Name) || !cloudAgentToolAllowed(state.Request, call.Function.Name) {
			break
		}
		call = s.cloudAgentRefreshStepSnapshotHash(run, state, call)
		state.Calls[index] = call
		if call.Function.Name == "skill_read_file" || call.Function.Name == "model_list" {
			state.RuntimeRunID = run.ID
		}
		result, err := cloudAgentReadToolCached(s.repo, run.UserID, state, call, s)
		if err == nil {
			cacheKey := cloudAgentReadCacheKeyForState(s.repo, run.UserID, state, call)
			if batchReadResults[cacheKey] {
				// The first receipt will be appended before this one in the same
				// checkpoint transaction, so this acknowledgement is truthful even
				// though the canonical transcript has not been updated yet.
				result = map[string]any{
					"cacheReplay": true,
					"replayCount": state.ToolReadReplays[cacheKey],
					"message":     "该只读结果已在本批工具调用的前序结果中，请直接使用已有结果，不要再次读取",
				}
			} else {
				batchReadResults[cacheKey] = true
			}
		}
		outcomes = append(outcomes, outcome{call: call, result: result, err: err})
		var loopErr *cloudAgentReadLoopError
		if errors.As(err, &loopErr) {
			break
		}
	}
	if len(outcomes) == 0 {
		return false, nil
	}

	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	err := s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		for _, item := range outcomes {
			var readLoopErr *cloudAgentReadLoopError
			if errors.As(item.err, &readLoopErr) {
				// A provider tool-call turn is atomic from the transcript's point of
				// view: every declared tool_call_id needs a tool message, even when a
				// read guard terminates the run. Record the triggering error and then
				// explicit skipped receipts before appending run_failed, otherwise a
				// later resume/replay produces an invalid provider transcript.
				cloudAgentToolResult(current.ID, state, item.call, item.result, item.err)
				for state.CallIndex < len(state.Calls) {
					pending := state.Calls[state.CallIndex]
					cloudAgentToolResult(current.ID, state, pending,
						map[string]any{"skipped": true, "reason": readLoopErr.reasonCode()},
						nil)
				}
				current.Status = "failed"
				current.FailureMessage = truncateRunes(readLoopErr.Error(), 1000)
				cloudAgentDropInterjections(run.ID, "本轮已结束："+truncateRunes(current.FailureMessage, 120), state)
				reason := readLoopErr.reasonCode()
				state.event(run.ID, "run_failed", map[string]any{
					"text": current.FailureMessage, "reason": reason,
					"toolName": item.call.Function.Name, "readCount": readLoopErr.Count,
				})
				break
			}
			cloudAgentRecordToolResult(current, state, item.call, item.result, item.err)
		}
		return cloudAgentSave(current, state)
	})
	return true, err
}

func (s *Service) advanceCloudAgentTool(run *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	if run == nil || state == nil || (run.Status != "running" && run.Status != "queued") {
		return nil
	}
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
	// 预检没过就不进任何业务分支：不申请审批、不读画布、不提交任务，只回一条结构化错误。
	// 这一步必须在写工具的审批分支之前——否则一个参数就不合法的写入会先弹出审批卡。
	if admission, ok := cloudAgentAdmissionFor(state, state.CallIndex); ok && !admission.Allowed {
		return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			cloudAgentRecordToolResult(current, state, call, nil, cloudAgentAdmissionError(admission))
			return cloudAgentSave(current, state)
		})
	}
	allowed := cloudAgentToolAllowed(state.Request, call.Function.Name)
	mediaTool := call.Function.Name == "generate_media" || call.Function.Name == "image_layer_split"
	if allowed && cloudAgentWrite(call.Function.Name) && (state.Request.PermissionMode == "request_approval" || mediaTool) && state.Approval == nil {
		var plan *cloudAgentMediaPlan
		var modelName string
		var mediaRequest CreateTaskRequest
		var preparedTask *model.Task
		var mediaPreparation *creationTaskPreparation
		policy, err := s.RuntimePolicy()
		if err != nil {
			return s.terminateCloudAgent(run, "Agent 运行策略不可用，本轮已停止")
		}
		if call.Function.Name == "generate_media" || call.Function.Name == "image_layer_split" {
			mediaCall := cloudAgentMediaCall(call)
			req, prepared, err := s.prepareCloudAgentMedia(run, state, mediaCall)
			if err != nil {
				return s.cloudAgentMediaError(run, state, "admission", false, false, err)
			}
			mediaRequest, plan = req, prepared
			// A durable auto checkpoint already contains the exact admitted input and
			// quote. Reusing it avoids a second dry admission after a worker restart.
			if plan.Prepared == nil {
				// Dry admission validates the selected model and prompt limits without a task or charge.
				mediaPreparation = &creationTaskPreparation{}
				req.creationPrepare = mediaPreparation
				preparedTask, err = s.CreateTask(run.UserID, req)
				if err != nil {
					return s.cloudAgentMediaError(run, state, "admission", false, false, err)
				}
				// Resolve model-owned defaults once during the dry admission. Both the
				// eventual task and the canvas draft must use this exact resolved input;
				// otherwise auto mode reports a false tool failure for omitted size or
				// duration and makes the model spend another turn repairing its own call.
				if err := applyCloudAgentResolvedMediaDefaults(&req, prepared, preparedTask); err != nil {
					return s.cloudAgentMediaError(run, state, "admission", false, false, err)
				}
				mediaRequest, plan = req, prepared
			}
			modelName, err = s.cloudAgentMediaModelName(plan.Args)
			if err != nil {
				return s.cloudAgentMediaError(run, state, "admission", false, false, err)
			}
		}
		if plan != nil && state.Request.PermissionMode == "auto" {
			if plan.Prepared != nil {
				// The draft and quote were already checkpointed. Reuse them after a
				// worker restart instead of dry-admitting and mutating the canvas again.
				latest, err := s.repo.CloudAgent(run.UserID, run.ID)
				if err != nil {
					return err
				}
				fresh, err := cloudAgentDecode(latest)
				if err != nil {
					return err
				}
				return s.enqueueCloudAgentTask(latest, &fresh, mediaRequest, plan)
			}
			// Auto media is a direct, server-admitted write. Prepare the draft and
			// its immutable quote in one checkpoint transaction, then release the
			// lock before enqueueCloudAgentTask performs the billed submission.
			s.storageMu.Lock()
			err := s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
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
				preparedMedia, err := prepareCloudAgentMediaApproval(repo, run.UserID, doc, plan, mediaRequest, preparedTask, mediaPreparation.Order)
				if err != nil {
					return err
				}
				plan.Prepared = preparedMedia
				state.AutoPreparedMedia = preparedMedia
				state.AutoPreparedCallHash = cloudAgentApprovalCallHash(call)
				return cloudAgentSave(current, state)
			})
			s.storageMu.Unlock()
			if err != nil {
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
			latest, err := s.repo.CloudAgent(run.UserID, run.ID)
			if err != nil {
				return err
			}
			fresh, err := cloudAgentDecode(latest)
			if err != nil {
				return err
			}
			return s.enqueueCloudAgentTask(latest, &fresh, mediaRequest, plan)
		}
		s.storageMu.Lock()
		defer s.storageMu.Unlock()
		return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
			var preview cloudAgentApprovalPreview
			var preparedMedia *cloudAgentPreparedMedia
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
				preparedMedia, err = prepareCloudAgentMediaApproval(repo, run.UserID, doc, plan, mediaRequest, preparedTask, mediaPreparation.Order)
				if err != nil {
					return err
				}
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
						cloudAgentRecordToolResult(current, state, call, nil, mutationErr)
						return cloudAgentSave(current, state)
					}
					// 过期快照是并发编辑的正常结果，不是准入失败：把它作为工具结果交回模型，
					// 让它在本轮内重新读取并重试，而不是丢掉整轮。真正的准入失败仍然终止运行。
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
			state.Approval = &cloudAgentApproval{ID: fmt.Sprintf("%s-%d-%d", run.ID, state.Step, state.CallIndex), Call: call, CallHash: cloudAgentApprovalCallHash(call), Preview: preview, ModelName: modelName, Prepared: preparedMedia}
			if preparedMedia != nil {
				if err := pinCloudAgentPreparedMedia(repo, run.UserID, run.ID, state.Approval.ID, preparedMedia); err != nil {
					return err
				}
			}
			current.Status = "waiting_approval"
			state.event(run.ID, "approval_requested", map[string]any{"approvalId": state.Approval.ID, "toolName": call.Function.Name, "modelName": modelName, "arguments": json.RawMessage(call.Function.Arguments), "preview": preview, "prepared": preparedMedia.publicView(), "text": preview.Description})
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
	if allowed && (call.Function.Name == "generate_media" || call.Function.Name == "image_layer_split") && state.Approval != nil && state.Approval.Decision == "approve" {
		return s.advanceCloudAgentMedia(run, state, cloudAgentMediaCall(call))
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		return s.terminateCloudAgent(run, "Agent 运行策略不可用，本轮已停止")
	}
	// 看图的资源与能力校验在写事务外完成；真实图片只在模型任务执行时读取。
	var inspectionResult any
	var inspectionErr error
	if allowed && call.Function.Name == "canvas_inspect_image" && state.Request.VisionEnabled {
		inspectionResult, inspectionErr = s.prepareCloudAgentImageInspection(run.UserID, state.Request.CanvasID, state, call)
		if errors.Is(inspectionErr, errCloudAgentImageInspectionBudget) {
			return s.failCloudAgent(run, state, cloudAgentImageInspectionBudgetMessage)
		}
	}
	// Skill reads use the domain repository and filesystem, not the checkpoint
	// transaction's connection. Read first to avoid nesting DB reads on SQLite.
	var skillResult any
	var skillErr error
	if allowed && (call.Function.Name == "skill_read_file" || call.Function.Name == "model_list" || call.Function.Name == "image_annotation_render") {
		state.RuntimeRunID = run.ID
		if call.Function.Name == "image_annotation_render" {
			skillResult, skillErr = cloudAgentReadTool(s.repo, run.UserID, state, call, s)
		} else {
			// 技能文件和模型目录都是稳定的只读结果。统一走检查点缓存，
			// 使重复调用不会再次访问文件系统/数据库，也不会把大结果重复
			// 写入后续模型上下文。
			skillResult, skillErr = cloudAgentReadToolCached(s.repo, run.UserID, state, call, s)
		}
	}
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		var result any
		var toolErr error
		switch {
		case !allowed:
			_, message := cloudAgentUnadvertisedToolAdmission(state, call.Function.Name)
			toolErr = BadAuthRequest(message)
		case call.Function.Name == "agent_tools_control", call.Function.Name == "agent_tools_memory", call.Function.Name == "agent_tools_skills", call.Function.Name == "agent_tools_canvas_read", call.Function.Name == "agent_tools_image", call.Function.Name == "agent_tools_canvas_edit", call.Function.Name == "agent_tools_generation":
			state.SelectedToolCategory = call.Function.Name
			state.ActivatedToolCategories = cloudAgentAppendActivatedCategory(state.ActivatedToolCategories, call.Function.Name)
			result = map[string]any{"category": call.Function.Name, "tools": cloudAgentCategoryChildren(state.Canonical.Tools, call.Function.Name)}
		case call.Function.Name == "canvas_apply_ops":
			result, toolErr = applyCloudAgentCanvas(repo, run.UserID, state.Request.CanvasID, call, policy, cloudAgentCanvasEventRecorder(run.ID, state))
		case call.Function.Name == "canvas_arrange_nodes":
			result, toolErr = applyCloudAgentArrangeNodes(repo, run.UserID, state.Request.CanvasID, call, policy, cloudAgentCanvasEventRecorder(run.ID, state))
		case call.Function.Name == "canvas_create_storyboard", call.Function.Name == "canvas_edit_storyboard":
			result, toolErr = applyCloudAgentStoryboardMutation(repo, run.UserID, state.Request.CanvasID, call, policy, cloudAgentCanvasEventRecorder(run.ID, state))
		case call.Function.Name == "canvas_edit_batch_table":
			result, toolErr = applyCloudAgentBatchTableMutation(repo, run.UserID, state.Request.CanvasID, call, policy, cloudAgentCanvasEventRecorder(run.ID, state))
		case call.Function.Name == "canvas_inspect_image":
			result, toolErr = inspectionResult, inspectionErr
			if toolErr == nil && inspectionResult != nil {
				if inspection, ok := inspectionResult.(cloudAgentImageInspection); ok {
					// 两种回执都算一次调用预算：attached=false 表示这次只回执文字、没有附图。
					attached := strings.TrimSpace(inspection.ImageURL) != ""
					state.markCanvasImageInspection(stringValue(inspection.Receipt["nodeId"]), attached, stringValue(inspection.Receipt["contentSignature"]))
					if inspection.CacheKey != "" {
						if state.ImageInspectionReads == nil {
							state.ImageInspectionReads = map[string]int{}
						}
						state.ImageInspectionReads[inspection.CacheKey]++
					}
				}
			}
		case call.Function.Name == "finish_run":
			// 显式完成：这里的闸门与"纯文本收尾"用的是同一份判据（cloud_agent_completion.go）。
			result, toolErr = cloudAgentFinishRun(run.ID, state, call)
		case call.Function.Name == "skill_read_file", call.Function.Name == "model_list", call.Function.Name == "image_annotation_render":
			result, toolErr = skillResult, skillErr
		default:
			result, toolErr = cloudAgentReadToolCached(repo, run.UserID, state, call)
		}
		if toolErr == nil && cloudAgentWrite(call.Function.Name) {
			// A successful canvas mutation changes the read model. Do not replay a
			// pre-mutation canvas snapshot later in the same Agent run.
			cloudAgentInvalidateReadCache(state)
		}
		var readLoopErr *cloudAgentReadLoopError
		if errors.As(toolErr, &readLoopErr) {
			cloudAgentRecordToolResult(current, state, call, result, toolErr)
			current.Status = "failed"
			current.FailureMessage = truncateRunes(readLoopErr.Error(), 1000)
			cloudAgentDropInterjections(run.ID, "本轮已结束："+truncateRunes(current.FailureMessage, 120), state)
			reason := readLoopErr.reasonCode()
			state.event(run.ID, "run_failed", map[string]any{
				"text": current.FailureMessage, "reason": reason,
				"toolName": call.Function.Name, "readCount": readLoopErr.Count,
			})
			return cloudAgentSave(current, state)
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
			cloudAgentRecordToolResult(current, state, call, result, nil)
			skipRemainingCloudAgentCalls(run.ID, state)
			current.Status = "completed"
			return cloudAgentSave(current, state)
		}
		if call.Function.Name == "finish_run" && toolErr == nil {
			// 闸门通过：summary 就是本轮唯一一次最终答复，本轮就此结束。
			if cloudAgentFinishRunAccepted(result) {
				return cloudAgentCompleteByFinishRun(current, state, run.ID, call, result)
			}
			// 申请收尾用的次数也已用尽：与"纯文本收尾"走同一条终止路径。
			if cloudAgentFinishRunExhausted(result) {
				block := cloudAgentEvaluateCompletion(state)
				block.Attempt = state.CompletionNudgeAttempt
				cloudAgentRecordToolResult(current, state, call, result, nil)
				return cloudAgentFailBlockedCompletion(current, state, run.ID, block)
			}
		}
		cloudAgentRecordToolResult(current, state, call, result, toolErr)
		return cloudAgentSave(current, state)
	})
}

// image_layer_split deliberately reuses the canonical media admission path.
// Keeping the alias at this boundary preserves one approval, billing and
// write-back implementation while exposing a task-specific Agent affordance.
func cloudAgentMediaCall(call cloudAgentCall) cloudAgentCall {
	if call.Function.Name != "image_layer_split" {
		return call
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err == nil {
		args["mode"] = "image"
		if raw, err := json.Marshal(args); err == nil {
			call.Function.Arguments = string(raw)
		}
	}
	return call
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
		return s.failCloudAgent(run, state, "Agent 累计预算已耗尽")
	}
	var prepared *cloudAgentPreparedMedia
	approvalID, generationID := "", ""
	if media != nil {
		prepared = media.Prepared
		if prepared == nil && state.Approval != nil {
			prepared = state.Approval.Prepared
			if state.Approval != nil {
				approvalID = state.Approval.ID
			}
		}
		if prepared == nil {
			return s.cloudAgentMediaError(run, state, "admission", false, false, creationConflict("缺少已批准的生成准备态，请重新申请审批；未提交任务"))
		}
		generationID = prepared.GenerationID
		refs, refErr := cloudAgentPreparedReferences(s.repo, run.UserID, prepared)
		if refErr != nil {
			return s.cloudAgentMediaError(run, state, "admission", false, false, refErr)
		}
		if req.Input == nil {
			req.Input = map[string]any{}
		}
		for _, field := range []string{"referenceImages", "referenceVideos", "referenceAudios"} {
			delete(req.Input, field)
		}
		for field, value := range refs {
			req.Input[field] = value
		}
	}
	prepare := &creationTaskPreparation{}
	req.admission = &taskAdmission{ID: cloudAgentID(run.UserID, fmt.Sprintf("%s:task:%d", run.ID, len(state.TaskIDs))), MaxCharge: remaining, AgentRunID: run.ID, GenerationID: generationID, ApprovalID: approvalID}
	req.creationPrepare = prepare
	task, err := s.CreateTask(run.UserID, req)
	if err != nil {
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
	// 压力读数只对"模型调用"这一步有意义：媒体任务发的是另一份请求（另一套信封），
	// 把读数挂到那份信封上会误导消费方；压缩任务同理由 enqueueCloudAgentContextCompaction
	// 自己记账。
	var contextPressure *cloudAgentContextPressure
	// requestCanonical 是"实际要发出去的那份信封"（上游 #601 新增）：定锚点、算口径指纹与
	// 占用分布都要对着它，而不是内存里的运行态副本。
	var requestCanonical canonicalAgentRequest
	if media == nil && req.Operation != cloudAgentContextCompactionOperation {
		canonical, ok := canonicalAgentRequestFromInput(input)
		if !ok {
			// 上游的硬失败：模型调用这一步必须有可计量的信封，缺了就如实报错，
			// 不要退回状态副本编一份读数出来。
			return fmt.Errorf("Agent 模型任务缺少可计量的请求信封")
		}
		requestCanonical = canonical
		value := s.cloudAgentContextPressure(canonical, state.Request.Prompt, state.Request)
		contextPressure = &value
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
			canvas, err := repo.CanvasProjectForUser(run.UserID, state.Request.CanvasID)
			if err != nil {
				return err
			}
			doc, err := creationDocument(canvas.PayloadJSON)
			if err != nil {
				return err
			}
			if err := validateCloudAgentPreparedAdmission(repo, run.UserID, prepared, task, prepare.Order, doc, media.Args); err != nil {
				return err
			}
			if err := createCloudAgentMediaNode(repo, run.UserID, state.Request.CanvasID, media, task, policy, cloudAgentCanvasEventRecorder(run.ID, state)); err != nil {
				return err
			}
		}
		if err := createTaskWithStorageQuotaRepository(repo, task, prepare.Order, policy); err != nil {
			return err
		}
		if media != nil && prepared != nil {
			if approvalID != "" {
				if err := repo.TransferCloudAgentResourceLeases(run.UserID, approvalID, "task:"+task.ID, task.ID, prepared.Quote.ExpiresAt); err != nil {
					return err
				}
			} else {
				// Auto execution has no approval owner to transfer from. Attach the
				// resource lease directly to the billed task before it can run.
				ids := make([]string, 0, len(prepared.ResourceSignatures))
				for id := range prepared.ResourceSignatures {
					ids = append(ids, id)
				}
				if err := repo.UpsertCloudAgentResourceLeases(run.UserID, run.ID, "task:"+task.ID, ids, prepared.Quote.ExpiresAt); err != nil {
					return err
				}
			}
		}
		if media != nil {
			// The prepared auto admission is single-use. Once the billed task and
			// draft node are committed, a later retry must observe the task state,
			// not reuse the old quote or create a second task.
			state.AutoPreparedMedia = nil
			state.AutoPreparedCallHash = ""
		}
		state.TaskIDs = append(state.TaskIDs, task.ID)
		if media != nil {
			state.MediaTaskID = task.ID
			state.Generations++
			state.VideoSeconds += media.Args.Duration
			toolName := "generate_media"
			if state.CallIndex >= 0 && state.CallIndex < len(state.Calls) && state.Calls[state.CallIndex].Function.Name != "" {
				toolName = state.Calls[state.CallIndex].Function.Name
			}
			state.event(run.ID, "generation_task_created", map[string]any{"toolName": toolName, "taskId": task.ID, "nodeId": media.Args.NodeID, "title": media.Args.Title, "mode": media.Args.Mode, "canvasId": state.Request.CanvasID, "referenceNodeIds": media.Args.ReferenceNodeIDs, "text": "媒体节点与引用连线已创建，生成任务已提交"})
		} else {
			state.ActiveTaskID = task.ID
			// 压缩调用不是本轮的一步：压完还要用压缩后的上下文继续步进，步数不该被它占掉。
			if state.ContextCompaction != nil && req.Operation == cloudAgentContextCompactionOperation {
				state.ContextCompaction.Status = "running"
			} else {
				if contextPressure != nil {
					state.AdvertisedToolNames = cloudAgentToolNames(requestCanonical.Tools)
					// 口径变化就地作废锚点（上游 #601）：这一步的模型/线路/窗口取自任务 input，
					// 与定锚时不一致的实测不再可比。我方 recordCloudAgentTokenAnchor 里还会兜一遍
					// （这一步拿不到新实测时也必须先把过期锚点作废）。
					config, _ := input["config"].(map[string]any)
					model, channelID := stringValue(config["model"]), stringValue(config["channelId"])
					signature := cloudAgentRequestSignature(state, requestCanonical, channelID, model)
					cloudAgentExpireTokenAnchorForRequest(run.ID, state, contextPressure.ContextWindowTokens, signature, model, channelID)
					// 个人记忆块是编译之后拼上去的，编译器录不到：每一步幂等补登记一次，
					// 占用分布的分段合计才能与 system 桶对齐。
					cloudAgentRecordMemorySegment(&state.Policy, requestCanonical.SystemPrompt)
					// requestId 把读数钉在"即将发出的这次请求"上（设计 §4）：没有它，前端只能猜
					// 哪个数字属于哪一次调用。占用分布对着实际信封（requestCanonical）算。
					payload := cloudAgentContextPressurePayload(*contextPressure, state, requestCanonical)
					payload["requestId"] = task.ID
					cloudAgentNoteContextWindowResolved(run.ID, state, *contextPressure)
					state.event(run.ID, "context_pressure", payload)
					state.LastStepEstimate = contextPressure.EstimatedInputTokens
					state.LastStepSourceBytes = contextPressure.SourceBytes
					// 上游 #601：把"发出去的那份信封"的口径与窗口一起记账，
					// 供后面的锚点作废比对（换模型/换线路/换窗口）。
					state.LastStepSignature = signature
					state.LastStepModel = model
					state.LastStepChannelID = channelID
					state.LastStepWindowTokens = contextPressure.ContextWindowTokens
				}
				// 记下发出去这份 canonical 的本地计价：任务回来时用它和上游实测配成锚点。
				state.LastStepTaskID = task.ID
				state.LastStepOperation = req.Operation
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
		cloudAgentRecordToolResult(current, state, state.Calls[state.CallIndex], map[string]any{"phase": phase, "taskSubmitted": submitted}, err)
		// Any admission failure advances the call into the repair path. Do not let
		// a prepared quote from the failed attempt leak into the corrected call.
		state.AutoPreparedMedia = nil
		state.AutoPreparedCallHash = ""
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
			writebackMessage, writebackReason := "", ""
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
				writebackMessage = truncateRunes(cloudAgentSafeToolError(writeErr), 60)
				writebackReason = "canvas_writeback_failed"
				var writeback *cloudAgentMediaWritebackError
				if errors.As(writeErr, &writeback) {
					writebackReason = writeback.reason
				}
				result["writebackError"], result["writebackReason"] = writebackMessage, writebackReason
				message := "媒体任务已成功，但画布回写未完成：" + writebackMessage
				if generationMessage != "" {
					message = "媒体任务未成功：" + generationMessage + "；任务状态也未回写画布：" + writebackMessage
				}
				toolErr = BadAuthRequest(message + "。请在任务中心查看详情，不会自动重试收费生成")
				result["summary"] = "画布回写未完成；任务记录保留在任务中心"
			}
			if task.Status == model.TaskStatusSucceeded && writeErr == nil {
				// Completion writes the generated asset into the canvas. Invalidate
				// the same-run snapshot cache just like ordinary canvas writes.
				cloudAgentInvalidateReadCache(state)
				result["summary"] = "生成结果已回写画布节点"
			}
			if writeErr != nil {
				recordTaskWritebackDiagnostic(task, target.NodeID, "failed", writebackReason, writeErr)
			} else {
				recordTaskWritebackDiagnostic(task, target.NodeID, "succeeded", "", nil)
			}
			if err := repo.UpdateTaskExecutionDiagnostic(run.UserID, task.ID, task.ExecutionDiagnosticJSON); err != nil {
				return err
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
	// Resource leases are a protection mechanism only. Expiry cleanup is safe to
	// run on the control-plane path and never changes the approval decision.
	if err := s.repo.ReleaseExpiredCloudAgentResourceLeases(time.Now().UTC()); err != nil {
		return err
	}
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
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	return s.repo.MutateCloudAgent(userID, id, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		if settings != nil {
			if err := s.updateCloudAgentMediaApproval(repo, run, &state, *settings); err != nil {
				return err
			}
		}
		state.Approval.Decision = decision
		state.Approval.Reason = reason
		state.Decisions[approvalID] = decision
		if settings != nil {
			if state.DecisionSettings == nil {
				state.DecisionSettings = map[string]string{}
			}
			state.DecisionSettings[approvalID] = creationHash(settings)
		}
		if decision == "approve" && state.Approval.Prepared != nil {
			if state.DecisionPreparedHashes == nil {
				state.DecisionPreparedHashes = map[string]string{}
			}
			state.DecisionPreparedHashes[approvalID] = state.Approval.Prepared.Hash
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
				"text":       "已拒绝本次生成，草稿节点仍保留在画布中；未提交任务、未产生扣费。你可以继续编辑后重新申请。",
			})
			if err := repo.ReleaseCloudAgentResourceLeases(userID, approvalID); err != nil {
				return err
			}
			return cloudAgentSave(current, &state)
		}
		current.Status = "running"
		payload := map[string]any{"approvalId": approvalID, "decision": decision, "arguments": json.RawMessage(state.Approval.Call.Function.Arguments), "preview": state.Approval.Preview, "modelName": state.Approval.ModelName}
		if state.Approval.Prepared != nil {
			payload["preparedHash"] = state.Approval.Prepared.Hash
			payload["generationId"] = state.Approval.Prepared.GenerationID
		}
		state.event(id, "approval_decided", payload)
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
			firstCancellation := current.Status != "cancelled"
			current.Status = "cancelled"
			current.CleanupPending = true
			if decodeErr == nil {
				current.CanvasID, current.ActiveTaskID, current.MediaTaskID = state.Request.CanvasID, state.ActiveTaskID, state.MediaTaskID
				if firstCancellation {
					state.event(id, "run_cancelled", map[string]any{"source": "user_request", "activeTaskId": state.ActiveTaskID, "mediaTaskId": state.MediaTaskID, "text": "用户取消接口已接收请求，正在取消关联任务"})
					if saveErr := cloudAgentSave(current, &state); saveErr != nil {
						// Cancellation must still work if the transcript is oversized.
						log.Printf("[cloud-agent] cancellation event unavailable for run %s: checkpoint rejected", id)
					}
				}
			} else {
				log.Printf("[cloud-agent] cancellation event unavailable for run %s: invalid transcript", id)
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
