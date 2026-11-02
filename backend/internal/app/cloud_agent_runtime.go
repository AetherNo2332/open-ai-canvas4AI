package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"yingce/backend/internal/agentcontext"
	"yingce/backend/internal/model"
	"yingce/backend/internal/observability"
	"yingce/backend/internal/repository"
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
	ID string `json:"id"`
	// Rebuilt from durable run/step context before approval and execution;
	// never accepted from the model or serialized on the tool-call wire.
	storyboardMutationID string
	// Type 是 OpenAI 形态工具调用的 "function" 标记。
	//
	// /internal-agent 的请求解码使用 DisallowUnknownFields，而 Node 侧
	// `PiToolCall` 由 `callsFromAssistant` 构造时**始终**带上 type:"function"。
	// 结构体缺这个字段会让整个工具批次以空 400 被拒（实测 ~50µs，即在任何业务
	// 逻辑之前），worker 于是无限重试并持续续租 —— 租约既不过期、看门狗也不会
	// 触发，运行永远停在 running。这里的字段集必须与 agent/src/bridge.ts 的
	// PiToolCall 保持一致。
	Type string `json:"type,omitempty"`
	// ThoughtSignature 可选：部分上游（Gemini 系）在工具调用上带回推理签名，
	// Node 会原样转发。
	ThoughtSignature string `json:"thoughtSignature,omitempty"`
	Function         struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type cloudAgentCachedToolResult struct {
	Result          json.RawMessage `json:"result,omitempty"`
	Error           string          `json:"error,omitempty"`
	ArgumentError   bool            `json:"argumentError,omitempty"`
	Field           string          `json:"field,omitempty"`
	Issue           string          `json:"issue,omitempty"`
	InvalidRowIDs   []string        `json:"invalidRowIds,omitempty"`
	DuplicateRowIDs []string        `json:"duplicateRowIds,omitempty"`
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
		return fmt.Sprintf("Agent 本轮只读工具调用已达到安全上限（%d 次），本轮已停止；请基于已读到的内容答复，或请用户确认后开新一轮", e.Count)
	}
	return fmt.Sprintf("Agent 连续多次读取同一份%s且结果未变，本轮已停止；请基于已读到的内容继续", e.ToolName)
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
	QuoteHash string                    `json:"quoteHash,omitempty"`
}
type cloudAgentRuntime struct {
	ToolSchemaVersion string           `json:"toolSchemaVersion,omitempty"`
	ActiveSubagents   int              `json:"activeSubagents,omitempty"`
	Subagent          *SubagentRuntime `json:"subagent,omitempty"`
	// SubagentBudgetCarves 记录父运行给仍在运行的子代理切分的积分份额，
	// 计算父剩余预算时扣减；子代理终态后释放（键为 linkId，单位积分）。
	SubagentBudgetCarves map[string]float64 `json:"subagentBudgetCarves,omitempty"`
	// SubagentGenerationCarves 记录切分给仍在运行子代理的生成次数份额（终态释放）。
	SubagentGenerationCarves map[string]int `json:"subagentGenerationCarves,omitempty"`
	// SubagentBudgetSpent 记录已终态子代理实际花费的积分（不归还，永久 holdback）。
	SubagentBudgetSpent map[string]float64       `json:"subagentBudgetSpent,omitempty"`
	Workspace           *WorkspaceSnapshot       `json:"workspace,omitempty"`
	RuntimeRunID        string                   `json:"-"`
	SkillRuntimeMode    string                   `json:"skillRuntimeMode,omitempty"`
	Request             CloudAgentRequest        `json:"request"`
	Policy              cloudAgentPolicySnapshot `json:"policy"`
	ParentID            string                   `json:"parentId,omitempty"`
	Fingerprint         string                   `json:"fingerprint,omitempty"`
	CreativeAnchor      cloudAgentCreativeAnchor `json:"creativeAnchor,omitempty"`
	TextHistory         []providerTextMessage    `json:"textHistory,omitempty"`
	Skills              []cloudAgentSkill        `json:"skills"`
	// Memory 是建 run 时冻结的长期记忆快照（只读参考数据）；本轮内的修改只体现在工具回执里。
	Memory            cloudAgentMemorySnapshot `json:"memory,omitempty"`
	SkillReads        map[string]bool          `json:"skillReads,omitempty"`
	Canonical         canonicalAgentRequest    `json:"canonical"`
	DisclosureVersion int                      `json:"disclosureVersion,omitempty"`
	// PromptContract 是"运行创建时装配提示所用的 Harness 内容身份"（Node 侧 sha256）。
	//
	// 为什么必须固化：Node 在 worker 启动时读一次 Harness，然后把同一个对象用于它领取的
	// **每一条**运行（`server.ts` 的 `harness` 在 while 循环外）。运维改了
	// SYSTEM.md/AGENTS.md 再重启，在途运行就会**静默换提示** —— 既违反"首步与后续步同一份
	// 快照"，也违反"恢复不得重读新版磁盘文件"。运行恢复继续核对冻结的 Harness 内容身份。
	PromptContract          string   `json:"promptContract,omitempty"`
	SelectedToolCategory    string   `json:"selectedToolCategory,omitempty"`
	ActivatedToolCategories []string `json:"activatedToolCategories,omitempty"`
	AdvertisedToolNames     []string `json:"advertisedToolNames,omitempty"`
	// 首步合同（P1.5）：合同版本、阶段、不可变快照与占位任务的持久化位置。
	// 旧运行的 ContractVersion 为 0，这些字段全部留空，行为与迁移前一致。
	ContractVersion int `json:"contractVersion,omitempty"`
	// Phase 只取值 awaiting_first_step（见 cloudAgentPhaseAwaitingFirstStep）。
	Phase string `json:"phase,omitempty"`
	// Snapshot 是本轮不可变的提示合同；首个模型步补齐 Harness 正文，之后只比对不重写。
	Snapshot *cloudAgentContractSnapshot `json:"contractSnapshot,omitempty"`
	// PlaceholderTaskID 是承载本轮报价预留的占位任务（当前实现里等于运行 ID）。
	PlaceholderTaskID    string `json:"placeholderTaskId,omitempty"`
	ActiveTaskID         string `json:"activeTaskId"`
	PiNoToolTaskID       string `json:"piNoToolTaskId,omitempty"`
	PiNoToolNudge        string `json:"piNoToolNudge,omitempty"`
	PiModelFailureTaskID string `json:"piModelFailureTaskId,omitempty"`
	PiModelFailureNudge  string `json:"piModelFailureNudge,omitempty"`
	ActiveTextDraft      string `json:"activeTextDraft,omitempty"`
	MediaTaskID          string `json:"mediaTaskId,omitempty"`
	// PendingGenerations 是已提交、尚未回写的媒体生成：工具提交后立即返回，
	// 完成时由 settleCloudAgentPendingGenerations 回写画布并以系统通知告知 Agent。
	PendingGenerations []cloudAgentPendingGeneration `json:"pendingGenerations,omitempty"`
	TaskIDs            []string                      `json:"taskIds"`
	Step               int                           `json:"step"`
	Generations        int                           `json:"generations"`
	VideoSeconds       int                           `json:"videoSeconds"`
	Calls              []cloudAgentCall              `json:"calls"`
	CallIndex          int                           `json:"callIndex"`
	// DirectorPreview 是 director preview 等待浏览器视口回传的持久挂起点。
	DirectorPreview *cloudAgentDirectorPreview `json:"directorPreview,omitempty"`
	// ToolRepairs is counted per tool so reads do not reset write-argument repairs.
	ToolRepairs map[string]cloudAgentToolRepair `json:"toolRepairs,omitempty"`
	// EnvironmentConflicts 按工具名记录画布并发冲突（过期快照 / 节点被用户占用），与 ToolRepairs 分开计数。
	EnvironmentConflicts   map[string]cloudAgentEnvironmentConflict `json:"environmentConflicts,omitempty"`
	Approval               *cloudAgentApproval                      `json:"approval,omitempty"`
	AutoPreparedMedia      *cloudAgentPreparedMedia                 `json:"autoPreparedMedia,omitempty"`
	AutoPreparedCallHash   string                                   `json:"autoPreparedCallHash,omitempty"`
	Decisions              map[string]string                        `json:"decisions"`
	DecisionSettings       map[string]string                        `json:"decisionSettings,omitempty"`
	DecisionPreparedHashes map[string]string                        `json:"decisionPreparedHashes,omitempty"`
	ActionNudged           bool                                     `json:"actionNudged,omitempty"`
	EmptyOutputNudged      int                                      `json:"emptyOutputNudged,omitempty"`
	// EmptyOutputEscalated 记录"空输出已经升级重试过几次"（关思考 + 放大输出预算）。
	EmptyOutputEscalated int `json:"emptyOutputEscalated,omitempty"`
	// StreamStallRetried 记录"中转在流中途断开后已经重试过几次"。
	StreamStallRetried int `json:"streamStallRetried,omitempty"`
	// TruncatedStepEscalated 记录"输出被输出上限截断后已经升级重试过几次"。
	// 空输出恢复会关思考并放大输出预算，重发同一步。
	TruncatedStepEscalated int `json:"truncatedStepEscalated,omitempty"`
	// EscalationRestore 保存"升级之前"的 ForceThinkingOff / BoostStepOutputBudget，
	// 重试被接受（或撞上别的失败阶梯）后复位，避免一次截断让本轮余下所有步骤都受开关影响。
	EscalationRestore *cloudAgentEscalationRestore `json:"escalationRestore,omitempty"`
	// ForceThinkingOff 让本步请求强制关闭上游思考：思考模型偶发把整个输出预算花在推理上，
	// 结果正文与工具调用皆空（实测 output_tokens 正好等于 maxOutputTokens）。
	ForceThinkingOff bool `json:"forceThinkingOff,omitempty"`
	// BoostStepOutputBudget 让本步请求使用放大后的输出预算（配合关思考重试）。
	BoostStepOutputBudget bool   `json:"boostStepOutputBudget,omitempty"`
	StepSnapshotHash      string `json:"stepSnapshotHash,omitempty"`
	// StepLimits 是本步实际生效的执行边界（管理员策略解析结果），只用于构造请求与展示压力读数，
	// 因此不进状态 JSON：每次推进都按当时的策略重新解析，改配置无需重发本轮。
	StepLimits cloudAgentStepLimits `json:"-"`
	// Refreshed at new model/tool admission; persisted so an admitted batch can resume.
	CallLimits cloudAgentCallLimits `json:"callLimits"`
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
	// Only admitted provider envelopes prove delivery; durable history can contain
	// images excluded by Pi's model-facing projection.
	DeliveredImageSHAs map[string]string `json:"deliveredImageSHAs,omitempty"`
	// Comparisons may resend an observed SHA once per exact inspection call.
	DeliveredImageComparisons map[string]string `json:"deliveredImageComparisons,omitempty"`
	// ImageInspectCalls 记录本轮所有图片识别工具调用次数（包括只回执文字的重复调用）。
	// 它与 ImageInspectCounts 一起进检查点，防止模型通过 refresh 或切换节点绕过总预算。
	ImageInspectCalls int `json:"imageInspectCalls,omitempty"`
	// ReadToolCalls 记录本轮会读取运行时只读快照的工具调用次数。除了同参缓存护栏，
	// 还需要一个跨参数的总上限，防止模型通过不断变化 offset/nodeIds 绕过重复读取保护。
	ReadToolCalls int `json:"readToolCalls,omitempty"`
	// MemoryWriteCalls 是本轮 memory_update 的调用次数，上限 agentMemoryCallsPerRun。
	MemoryWriteCalls int `json:"memoryWriteCalls,omitempty"`
	// Native Skill pages share the read budget; retries of the same step/page are free.
	NativeReadBudgetKeys map[string]bool `json:"nativeReadBudgetKeys,omitempty"`
	// PendingImageInspections 暂存"本批还有工具结果没入历史"的看图结果，等整批 tool
	// 结果都入历史后合并成一条 user 图片消息（见 cloudAgentFlushPendingImages）。
	//
	// 它必须进检查点，不能标 `json:"-"`：PiToolAdvance 一次只执行一个工具调用，
	// 每次调用都从 StateJSON 重新解码（cloudAgentDecode）。进程内字段在下一个调用到来时必然为空，
	// 缓冲就白缓冲了。载荷只有回执与签名链接，几十字节级。
	PendingImageInspections []cloudAgentImageInspection `json:"pendingImageInspections,omitempty"`
	VisionBatchBudget       cloudAgentVisionBatchBudget `json:"visionBatchBudget,omitempty"`
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
	// StartPermissionMode 是首次运行中切换权限前的开局权限（为空表示从未切换）：
	// 系统提示与 prompt cache 身份按它冻结，执行判定读 Request.PermissionMode。
	StartPermissionMode string                       `json:"startPermissionMode,omitempty"`
	PermissionChanges   []cloudAgentPermissionChange `json:"permissionChanges,omitempty"`
	// PiModelStepFingerprint 绑定最近一次模型任务的完整请求，确保丢响应重投复用原任务；
	// 同一在途任务收到不同请求正文时拒绝，而不是静默复用。
	PiModelStepFingerprint string `json:"piModelStepFingerprint,omitempty"`
	// 最近一次已发出的步骤请求（模型调用）的本地计价，与上游实测用量配成锚点用。
	// 估算与实测指向同一个 canonical：估算取自任务 input 里实际发出的那份，
	// 因此"信封一致"是构造保证，不需要额外比对。
	LastStepTaskID string `json:"lastStepTaskId,omitempty"`
	// PiToolBatchTaskID binds the admitted call IDs and receipts to the model
	// step that emitted them. Model providers may reuse call IDs across turns.
	PiToolBatchTaskID            string                     `json:"piToolBatchTaskId,omitempty"`
	PiAssistantCheckpointTaskID  string                     `json:"piAssistantCheckpointTaskId,omitempty"`
	PiAssistantCheckpointEntryID string                     `json:"piAssistantCheckpointEntryId,omitempty"`
	PiToolBatchAssistantEntryID  string                     `json:"piToolBatchAssistantEntryId,omitempty"`
	LastStepOperation            string                     `json:"lastStepOperation,omitempty"`
	LastStepEstimate             int                        `json:"lastStepEstimate,omitempty"`
	LastStepPressure             *cloudAgentContextPressure `json:"lastStepPressure,omitempty"`
	LastStepSourceBytes          int                        `json:"lastStepSourceBytes,omitempty"`
	// LastStepBreakdown 是这一步"实际发出去的信封"的占用构成（准入时算好并冻结）。
	// 运行态 canonical 与真实请求不是同一份：它含全量工具目录，也不含 Node 拼在策略后的
	// Harness 块。after_request 若从它重算，同一步前后两条读数的 system/tools 桶会对不上。
	LastStepBreakdown map[string]any `json:"lastStepBreakdown,omitempty"`
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
		AgentEngine string                `json:"agentEngine"`
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
	canonical.Tools = compileCloudAgentToolsForRuntime(initial.Request, firstNonEmpty(initial.SkillRuntimeMode, cloudAgentSkillRuntimeLegacy))
	stepLimits, err := s.cloudAgentStepLimits()
	if err != nil {
		return err
	}
	state := cloudAgentRuntime{Request: initial.Request, SkillRuntimeMode: initial.SkillRuntimeMode, Policy: initial.Policy, ParentID: initial.ParentID, Fingerprint: initial.Fingerprint, CreativeAnchor: initial.CreativeAnchor, TextHistory: input.TextHistory, Skills: initial.Skills, Canonical: canonical, ActiveTaskID: task.ID, TaskIDs: []string{task.ID}, Step: 1, Decisions: map[string]string{}, Plan: initial.Plan, Events: []CloudAgentEvent{}, StepLimits: stepLimits}
	state.CallLimits = stepLimits.CallLimits
	s.RecordObservability(observability.Event{TaskID: task.ID, RunID: task.ID, TraceID: task.ID, Kind: observability.KindTask, Status: observability.StatusRunning, StartedAt: time.Now().UTC(), Model: stringValue(input.Config["model"]), PromptVersion: state.Fingerprint})
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
	state.LastStepPressure = &pressure
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
	// 冻结这一步实际信封的分布：发完之后补报 after_request 时运行态 canonical
	// 已不是同一份（工具目录、Harness 块都可能不同），不能从它重算。
	if breakdown, ok := firstPressure["breakdown"].(map[string]any); ok {
		state.LastStepBreakdown = breakdown
	}
	state.event(task.ID, "context_pressure", firstPressure)
	run := &model.CloudAgentExecution{ID: task.ID, UserID: task.UserID, Status: "running", Engine: input.AgentEngine, Revision: 1, CreatedAt: task.CreatedAt, UpdatedAt: time.Now()}
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
	state.Request.subagent = state.Subagent
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

// cloudAgentMessageKindCanonical / cloudAgentMessageKindHistory 是消息表的两种 kind。
const (
	cloudAgentMessageKindCanonical  = "canonical"
	cloudAgentMessageKindHistory    = "history"
	cloudAgentMessageKindPi         = "pi"
	cloudAgentMessageKindCompaction = "pi_compaction"
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
	s.RecordObservability(observability.Event{TaskID: run.ActiveTaskID, RunID: run.ID, TraceID: run.ID, Kind: observability.KindTask, Status: observability.StatusFailed, EndedAt: time.Now().UTC(), Reason: message})
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		current.Status = "failed"
		current.FailureMessage = truncateRunes(message, 1000)
		// 终态必须携带收尾标记：首步之前失败时，本轮还压着一笔占位预留，
		// 而退它是收尾路径的职责（见 cloudAgentRefundHoldingReservation）。
		// 只写 failed 会让那笔钱一直冻在账上 —— 没有任何一方会回来退它。
		// 换单之后的失败再走一次收尾是幂等的：占位订单已退，退款直接 no-op。
		current.CleanupPending = true
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
