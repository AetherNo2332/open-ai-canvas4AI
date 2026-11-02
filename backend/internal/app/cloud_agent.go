package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

const cloudAgentOperation = "cloud_agent"

// A run is one immutable, durable model turn backed by a normal task. Subsequent
// turns reference the previous run, not a mutable in-memory conversation. This
// reuses transactional billing, worker leases, cancellation and text replay.
type CloudAgentRequest struct {
	subagent      *SubagentRuntime
	ReasoningMode string `json:"reasoningMode,omitempty"`
	CanvasID      string `json:"canvasId"`
	Prompt        string `json:"prompt"`
	Workflow      string `json:"workflow,omitempty"`
	// SubagentEnabled is the frozen per-canvas consent for this turn.
	SubagentEnabled bool `json:"subagentEnabled,omitempty"`
	// SubagentWriteEnabled 冻结本画布"写型子代理"授权；仍需 spawn 显式申请。
	SubagentWriteEnabled bool `json:"subagentWriteEnabled,omitempty"`
	// StoryboardDisabled 冻结 Agent 设置里的分镜表工具开关：关闭时 storyboard
	// 工具不披露、调用被拒；分镜行仍可经 canvas_get_state / canvas_read_content 读取。
	StoryboardDisabled bool `json:"storyboardDisabled,omitempty"`
	Model               string `json:"model,omitempty"`
	LogicalModelID       string `json:"logicalModelId,omitempty"`
	ChannelID            string `json:"channelId,omitempty"`
	ChannelModelKey      string `json:"channelModelKey,omitempty"`
	// VisionEnabled is server-derived from the selected model input contract and persisted for the run.
	VisionEnabled bool `json:"visionEnabled,omitempty"`
	// MemoryMode 是建 run 时从用户记忆文件冻结的模式（auto / review / off），服务端推导，
	// 客户端传入值一律被覆盖；为空或 off 时不暴露 memory_update，也不注入记忆快照。
	MemoryMode string `json:"memoryMode,omitempty"`
	// WebSearchEnabled is server-derived; no provider credentials enter the run.
	WebSearchEnabled bool     `json:"webSearchEnabled,omitempty"`
	PermissionMode   string   `json:"permissionMode"`
	SkillIDs         []string `json:"skillIds,omitempty"`
	ContextScope     []string `json:"contextScope"`
	FocusNodeIDs     []string `json:"focusNodeIds,omitempty"`
	Budget           struct {
		MaxCredits         float64 `json:"maxCredits"`
		MaxGenerationTasks int     `json:"maxGenerationTasks,omitempty"`
		MaxVideoSeconds    int     `json:"maxVideoSeconds,omitempty"`
		// 0 = 请求不另设模型步数限制；仍受后台执行策略约束。正数夹到参数上界。
		MaxSteps int `json:"maxSteps,omitempty"`
	} `json:"budget"`
	IdempotencyKey string `json:"idempotencyKey"`
	// VisionEnabled 决定是否给模型暴露看图工具，由服务端在创建 run 时按渠道模型合同
	// （text.references.maxImages > 0）重新推导并覆盖，客户端传入值一律被忽略；
	// 必须持久化：工具授权校验每步都从落库状态重建，丢掉这个标记会让模型看得见工具
	// 却被判为"未获本轮权限授权"。
}

type cloudAgentState struct {
	Subagent         *SubagentRuntime         `json:"subagent,omitempty"`
	Workspace        *WorkspaceSnapshot       `json:"workspace,omitempty"`
	Version          int                      `json:"version"`
	SkillRuntimeMode string                   `json:"skillRuntimeMode,omitempty"`
	Request          CloudAgentRequest        `json:"request"`
	ParentID         string                   `json:"parentId"`
	Fingerprint      string                   `json:"fingerprint"`
	CreativeAnchor   cloudAgentCreativeAnchor `json:"creativeAnchor,omitempty"`
	Plan             []cloudAgentPlanItem     `json:"plan,omitempty"`
	Skills           []cloudAgentSkill        `json:"skills,omitempty"`
	Memory           cloudAgentMemorySnapshot `json:"memory,omitempty"`
	Policy           cloudAgentPolicySnapshot `json:"policy"`
}

type CloudAgentRun struct {
	SkillRuntimeMode string            `json:"skillRuntimeMode,omitempty"`
	ID               string            `json:"id"`
	ConversationID   string            `json:"conversationId"`
	UserPrompt       string            `json:"userPrompt,omitempty"`
	IdempotencyKey   string            `json:"idempotencyKey,omitempty"`
	CanvasID         string            `json:"canvasId"`
	ParentID         string            `json:"parentId,omitempty"`
	Status           string            `json:"status"`
	Revision         int64             `json:"revision"`
	RuntimePhase     string            `json:"runtimePhase"`
	WaitReason       string            `json:"waitReason,omitempty"`
	CleanupPending   bool              `json:"cleanupPending,omitempty"`
	FailureMessage   string            `json:"failureMessage,omitempty"`
	PermissionMode   string            `json:"permissionMode"`
	Model            string            `json:"model"`
	ChannelID        string            `json:"channelId,omitempty"`
	ChannelModelKey  string            `json:"channelModelKey,omitempty"`
	LogicalModelID   string            `json:"logicalModelId,omitempty"`
	CreatedAt        time.Time         `json:"createdAt"`
	UpdatedAt        time.Time         `json:"updatedAt"`
	Events           []CloudAgentEvent `json:"events,omitempty"`
	// 事件已全量落库，运行详情只返回一页，因此必须把"这一页在整条日志里的位置"说清楚：
	// EventSeqBase 是本次返回的首条事件之前已入库的条数（不变量 events[i].seq ==
	// eventSeqBase + i + 1），EventCount 是该运行累计事件条数，LatestSeq 可直接当作下次
	// 增量读取的 sinceSeq，EventsTruncated 表示还有更早的记录没随本次返回。
	// 这四个字段**不用 omitempty**：零值本身是有信息的位置读数（例如首条之前无记录、
	// 没有更早的记录），省略会让客户端无法区分"为 0"与"服务端没给"。
	EventSeqBase    int                 `json:"eventSeqBase"`
	EventCount      int                 `json:"eventCount"`
	LatestSeq       int                 `json:"latestSeq"`
	EventsTruncated bool                `json:"eventsTruncated"`
	Skills          []cloudAgentSkill   `json:"skills,omitempty"`
	Approval        *cloudAgentApproval `json:"approval,omitempty"`
	SpentCredits    float64             `json:"spentCredits"`
	Step            int                 `json:"step"`
	ActiveMessage   map[string]string   `json:"activeMessage,omitempty"`
}

// CloudAgentRunViewOptions 是运行详情的读取选项：SinceSeq 只取该序号之后的增量，
// EventLimit 覆盖默认页大小。零值即默认视图（尾部一窗）。
type CloudAgentRunViewOptions struct {
	SinceSeq   int
	EventLimit int
	FromStart  bool
}

func validateCloudAgentPrompt(value string, maxRunes int) error {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || utf8.RuneCountInString(strings.TrimSpace(value)) > maxRunes {
		return BadAuthRequest("需要有效画布和 1–16000 个字符的提示词")
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return BadAuthRequest("提示词不能包含控制字符")
		}
	}
	return nil
}

func cloudAgentID(userID, key string) string {
	sum := sha256.Sum256([]byte(userID + "\x00" + key))
	return "ag" + hex.EncodeToString(sum[:16])
}

func cloudAgentFingerprint(req CloudAgentRequest, parent string) string {
	data, _ := json.Marshal(struct {
		Request CloudAgentRequest
		Parent  string
	}{req, parent})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// cloudAgentRunIdentity 是渲染运行视图所需的最小身份字段。
//
// 旧 run 的身份来自根任务行；阶段 2 之后的新 run 由 Go 直接创建（run + 预授权 + 不可变快照），
// **不再有根任务**。视图层不该知道身份来自哪里，所以这里显式收敛成一份身份，
// 而不是继续传 *model.Task —— 新 run 根本没有任务行可传。
type cloudAgentRunIdentity struct {
	ID       string
	CanvasID string
	// Status 已经是 run 口径的终态字符串（completed / failed / cancelled / rejected / queued / running）。
	Status    string
	Model     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// cloudAgentTaskIdentity 把旧的根任务行映射成同一份身份，保证旧 run 的视图逐字段不变。
func cloudAgentTaskIdentity(task *model.Task) cloudAgentRunIdentity {
	status := string(task.Status)
	if task.Status == model.TaskStatusSucceeded {
		status = "completed"
	}
	return cloudAgentRunIdentity{ID: task.ID, CanvasID: task.ProjectID, Status: status,
		Model: task.Model, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt}
}

func agentRunOutput(identity cloudAgentRunIdentity, state cloudAgentState) *CloudAgentRun {
	return &CloudAgentRun{ID: identity.ID, CanvasID: identity.CanvasID, ParentID: state.ParentID, Status: identity.Status, PermissionMode: state.Request.PermissionMode, Model: identity.Model,
		UserPrompt: state.Request.Prompt, IdempotencyKey: state.Request.IdempotencyKey,
		ChannelID: state.Request.ChannelID, ChannelModelKey: state.Request.ChannelModelKey, LogicalModelID: state.Request.LogicalModelID,
		CreatedAt: identity.CreatedAt, UpdatedAt: identity.UpdatedAt, Skills: state.Skills}
}

func (s *Service) cloudAgentTask(userID, id string) (*model.Task, cloudAgentState, error) {
	var input struct {
		Agent cloudAgentState `json:"cloudAgent"`
	}
	task, err := s.repo.TaskForUser(userID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = kernel.NotFound("Agent 运行不存在")
		}
		return nil, input.Agent, err
	}
	if task.Operation != cloudAgentOperation {
		return nil, input.Agent, kernel.NotFound("Agent 运行不存在")
	}
	if json.Unmarshal([]byte(task.InputJSON), &input) == nil && input.Agent.Version == 1 && task.ID == cloudAgentID(userID, input.Agent.Request.IdempotencyKey) {
		return task, input.Agent, nil
	}
	// 成功、失败和取消任务的 InputJSON 都可能按存储配额策略压缩；执行
	// 记录才是完成后轮次的持久来源。运行中如果无法解析执行记录，不能
	// 返回一个可操作的审批/活动任务状态。
	run, err := s.repo.CloudAgent(userID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = kernel.NotFound("Agent 运行不存在")
		}
		return nil, input.Agent, err
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		if cloudAgentTaskTerminal(task.Status) || cloudAgentRunTerminal(run.Status) {
			// 终态运行仍应可查询，但绝不从损坏的 runtime 伪造审批、权限、
			// 活动任务或技能内容。TaskForUser 已完成归属校验，画布 ID 仅
			// 使用任务自身的归属字段作为安全展示降级。
			return task, cloudAgentState{Version: 1, Request: CloudAgentRequest{CanvasID: task.ProjectID}}, nil
		}
		return nil, input.Agent, kernel.NotFound("Agent 运行不存在")
	}
	if task.ID != cloudAgentID(userID, state.Request.IdempotencyKey) || task.ProjectID != state.Request.CanvasID {
		return nil, input.Agent, kernel.NotFound("Agent 运行不存在")
	}
	input.Agent = cloudAgentState{Version: 1, SkillRuntimeMode: state.SkillRuntimeMode, Request: state.Request, ParentID: state.ParentID, Fingerprint: state.Fingerprint, CreativeAnchor: state.CreativeAnchor, Skills: state.Skills, Policy: state.Policy}
	return task, input.Agent, nil
}

// cloudAgentRunRef 是"读一次运行"的统一结果。
//
// LegacyTask 只在"执行记录尚不存在、需要从旧根任务引导"时有值；
// 阶段 2 之后的新 run（Go 只建 run + 预授权 + 不可变快照，不建根任务）它为 nil。
type cloudAgentRunRef struct {
	UserID     string
	Identity   cloudAgentRunIdentity
	LegacyTask *model.Task
	State      cloudAgentState
}

// cloudAgentRunRefFor 解析一次运行，同时兼容两种形态。
//
// 顺序刻意是**旧路径优先**：只要根任务还在，行为与迁移前逐字段一致
// （含终态损坏 runtime 的只读降级、幂等键校验与 ProjectID 校验）。
// 只有当根任务路径失败时，才回退到"以执行记录为权威"的读取 ——
// 那正是阶段 2 之后的新 run 形态。
//
// 因此这一步是**严格增量**的：既有可读运行的行为一个都不变，
// 而此前必然 404 的"没有根任务的 run"开始可读。删除根任务（阶段 2 第 3 步）
// 只需删掉这里的旧路径分支，不必再动调用方。
func (s *Service) cloudAgentRunRefFor(userID, id string) (*cloudAgentRunRef, error) {
	if task, state, err := s.cloudAgentTask(userID, id); err == nil {
		return &cloudAgentRunRef{UserID: userID, Identity: cloudAgentTaskIdentity(task), LegacyTask: task, State: state}, nil
	}
	run, err := s.repo.CloudAgent(userID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, kernel.NotFound("Agent 运行不存在")
		}
		return nil, err
	}
	state, decodeErr := cloudAgentDecode(run)
	if decodeErr != nil {
		if cloudAgentRunTerminal(run.Status) {
			// 与旧路径同一合同：终态仍应可查询，但绝不从损坏的 runtime 伪造
			// 审批、权限、活动任务或技能内容。
			return &cloudAgentRunRef{UserID: userID,
				Identity: cloudAgentRunIdentity{ID: run.ID, CanvasID: run.CanvasID, Status: run.Status,
					CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt},
				State: cloudAgentState{Version: 1, Request: CloudAgentRequest{CanvasID: run.CanvasID}}}, nil
		}
		return nil, kernel.NotFound("Agent 运行不存在")
	}
	// 身份自校验与旧路径同源：运行 ID 由 (userID, 幂等键) 决定，防止越权或错挂。
	if run.ID != cloudAgentID(userID, state.Request.IdempotencyKey) {
		return nil, kernel.NotFound("Agent 运行不存在")
	}
	return &cloudAgentRunRef{UserID: userID,
		Identity: cloudAgentRunIdentity{ID: run.ID, CanvasID: run.CanvasID, Status: run.Status,
			Model: state.Request.Model, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt},
		State: cloudAgentState{Version: 1, SkillRuntimeMode: state.SkillRuntimeMode, Request: state.Request, ParentID: state.ParentID,
			Fingerprint: state.Fingerprint, CreativeAnchor: state.CreativeAnchor, Skills: state.Skills,
			Policy: state.Policy}}, nil
}

func cloudAgentTaskTerminal(status model.TaskStatus) bool {
	return status == model.TaskStatusSucceeded || status == model.TaskStatusFailed || status == model.TaskStatusCancelled
}

func cloudAgentRunTerminal(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled" || status == "rejected"
}

func (s *Service) CloudAgentRun(userID, id string, options ...CloudAgentRunViewOptions) (*CloudAgentRun, error) {
	ref, err := s.cloudAgentRunRefFor(userID, id)
	if err != nil {
		return nil, err
	}
	// A durable execution is authoritative once it exists. In particular, do
	// not try to reconstruct/overwrite a terminal run whose task input or
	// runtime blob was compacted or damaged; the output path has a deliberately
	// read-only, minimal fallback for that case. The ensure path is only for a
	// legacy root task whose execution row has not been created yet.
	if _, lookupErr := s.repo.CloudAgent(userID, id); errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		if ref.LegacyTask == nil {
			// 没有根任务又没有执行记录：无法引导，也不能凭空造一个运行。
			return nil, kernel.NotFound("Agent 运行不存在")
		}
		if err := s.ensureCloudAgentExecution(ref.LegacyTask, ref.State); err != nil {
			return nil, err
		}
	} else if lookupErr != nil {
		return nil, lookupErr
	}
	return s.cloudAgentExecutionOutput(userID, ref.Identity, ref.State, options...)
}

// CloudAgentRunIfChanged keeps idle event streams on a small indexed read.
// The persisted revision, not a process-local notification, is authoritative
// across instances and after missed/disconnected notifications.
func (s *Service) CloudAgentRunIfChanged(userID, id string, revision int64, options ...CloudAgentRunViewOptions) (*CloudAgentRun, error) {
	current, err := s.repo.CloudAgentRevision(userID, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, kernel.NotFound("Agent 运行不存在")
	}
	if err != nil {
		return nil, err
	}
	if current == revision {
		return nil, nil
	}
	return s.CloudAgentRun(userID, id, options...)
}

// CreateCloudAgentRun validates every capability before admission. The task PK
// is deterministic per user/key. Competing requests may race, but task creation
// and credit reservation share a transaction: only one can commit.
func (s *Service) CreateCloudAgentRun(userID string, req CloudAgentRequest, parentID string) (*CloudAgentRun, error) {
	return s.createCloudAgentRunScoped(userID, req, parentID, nil)
}

type cloudAgentRunScope struct {
	Workspace      *WorkspaceSnapshot
	Skills         []cloudAgentSkill
	ConversationID string
	ParentID       string
	Prepared       *repository.CloudAgentAdmission
	Subagent       *SubagentRuntime
}

// newCloudAgentExecution 组装新运行的执行记录。
//
// 会话归属必须在插入前定好：EnsureCloudAgent 只会在 ConversationID 为空时去查父轮，
// 而父轮可能是一轮旧合同运行（执行记录由 Go worker 建），任何一次查不到都会让建 run 失败。
// 这里按同一语义先把会话与标题从父轮继承下来（父轮在本函数之前已被读过一次）。
func (s *Service) newCloudAgentExecution(userID, id, canvasID, parentID, conversationID string, state *cloudAgentRuntime) (*model.CloudAgentExecution, error) {
	now := time.Now()
	run := &model.CloudAgentExecution{
		ID: id, UserID: userID, Status: "queued", Engine: "pi", Revision: 1,
		CanvasID: canvasID, ParentID: parentID, ConversationID: conversationID, CreatedAt: now, UpdatedAt: now,
	}
	if parentID != "" {
		// 标题仍从父轮继承；会话归属已在建 run 前由 cloudAgentConversationIDFor 解析，
		// 这里不再重复查询，避免两处口径漂移。
		if parent, err := s.repo.CloudAgent(userID, parentID); err == nil {
			run.Title = parent.Title
		}
	}
	if state.SkillRuntimeMode == cloudAgentSkillRuntimeNative {
		for _, skill := range state.Skills {
			state.event(id, "native_skill_enabled", map[string]any{"skillId": skill.ID, "nativeName": skill.NativeName,
				"skillName": skill.Name, "version": firstNonEmpty(skill.VersionLabel, skill.Version)})
		}
	}
	if err := cloudAgentSave(run, state); err != nil {
		return nil, err
	}
	return run, nil
}

// 旧运行记录没有单独保存历史；只从模型请求中当前用户消息之前的严格交替前缀恢复。
func cloudAgentLegacyHistory(messages []map[string]interface{}, currentPrompt string) []providerTextMessage {
	currentIndex := -1
	for index, message := range messages {
		role, _ := message["role"].(string)
		content, _ := message["content"].(string)
		if role != "user" && role != "assistant" {
			break
		}
		if role == "user" && content == currentPrompt {
			currentIndex = index
		}
	}
	if currentIndex < 0 || currentIndex%2 != 0 {
		return nil
	}
	history := make([]providerTextMessage, 0, currentIndex)
	for index := 0; index < currentIndex; index++ {
		role, _ := messages[index]["role"].(string)
		content, ok := messages[index]["content"].(string)
		if !ok || role != []string{"user", "assistant"}[index%2] {
			return nil
		}
		// 来源标记必须一起搬：丢了它，旧会话里的运行时交接消息会被当成真人轮次参与裁剪。
		message := providerTextMessage{Role: role, Content: content}
		if source, ok := messages[index][cloudAgentContextSourceKey].(string); ok && source != "" && source != "runtime" {
			message.AgentContextSource = source
		}
		history = append(history, message)
	}
	return history
}

type cloudAgentDigestNode struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Title    string         `json:"title"`
	Metadata map[string]any `json:"metadata"`
}

// 轮首摘要每次运行都会重建、每一步都会重发，所以它的体积必须由构造保证有界，并且
// 绝不能因为画布太大而判死整轮（上游原来的 64KB 硬上限就是直接拒绝整轮）。
// 8KB 是我们自己的软预算：摘要只承诺存在性与规模，正文按需分页读取。
func cloudAgentContinuationHistory(history []providerTextMessage, state cloudAgentRuntime, prompt, reply string) []providerTextMessage {
	if !state.HistoryIncludesCurrent {
		// The user's goal survives a failed first model call too. Tool facts are
		// context, not authorization to replay a write or charge a second time.
		history = append(history, providerTextMessage{Role: "user", Content: prompt})
		for _, message := range state.Canonical.Messages {
			if cloudAgentIsDeliveredInterjection(message, &state) {
				history = append(history, providerTextMessage{Role: "user", Content: stringField(message, "content")})
			}
		}
		return append(history, providerTextMessage{Role: "assistant", Content: reply})
	}
	// TextHistory 是压缩瞬间的 canonical 快照；之后只有 canonical 会追加模型回复。
	// 终态取消/失败可能没有新回复，不能拿压缩前的最后一条 assistant 伪装成新回复。
	after := state.Canonical.Messages[len(state.TextHistory):]
	for _, message := range after {
		if cloudAgentIsDeliveredInterjection(message, &state) {
			history = append(history, providerTextMessage{Role: "user", Content: stringField(message, "content")})
		}
	}
	if strings.TrimSpace(reply) != "" {
		for i := len(after) - 1; i >= 0; i-- {
			message := after[i]
			if stringField(message, "role") == "assistant" && stringField(message, "content") == reply {
				if _, hasCalls := message["tool_calls"]; !hasCalls {
					history = append(history, providerTextMessage{Role: "assistant", Content: reply})
				}
				break
			}
		}
	}
	return history
}

const (
	// 目录只回答“画布上有哪些节点”。正文、提示词和结构化表留给 canvas_get_state 按页读取，
	// 否则几千个节点的 JSON 会在每一次模型调用里重复出现。
	cloudAgentCanvasSummaryMaxNodes = 120
	// 单条目录约百字节量级；预算只兜住异常长的 id / 标题，不再为正文预留 60KB。
	cloudAgentCanvasSummaryBudgetBytes = 24 << 10
)

func cloudAgentCanvasSummary(canvas *model.CanvasProject, focusNodeIDs ...string) (string, error) {
	var payload struct {
		Nodes []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Title    string `json:"title"`
			Metadata struct {
				WorkflowKind     string `json:"workflowKind"`
				CharacterAssetID string `json:"characterAssetId"`
			} `json:"metadata"`
		} `json:"nodes"`
		Connections []struct {
			FromNodeID string `json:"fromNodeId"`
			ToNodeID   string `json:"toNodeId"`
		} `json:"connections"`
	}
	if err := json.Unmarshal([]byte(canvas.PayloadJSON), &payload); err != nil {
		return "", BadAuthRequest("服务端画布内容无法解析，请先重新同步")
	}
	focus := make(map[string]bool, len(focusNodeIDs))
	for _, id := range focusNodeIDs {
		focus[id] = true
	}
	known := make(map[string]bool, len(payload.Nodes))
	for _, node := range payload.Nodes {
		known[node.ID] = true
	}
	selectedFocus := make(map[string]bool, len(focus))
	for id := range focus {
		selectedFocus[id] = true
	}
	if len(focus) > 0 {
		for id := range focus {
			if !known[id] {
				return "", BadAuthRequest("画布焦点节点已不存在，请重新选择后发送")
			}
		}
		// The initial catalog follows the actual canvas selection instead of
		// injecting an unrelated global node list. Include one graph hop so the
		// model can identify the local working set before requesting node bodies.
		adjacent := make(map[string]bool, len(focus))
		for _, edge := range payload.Connections {
			if focus[edge.FromNodeID] && known[edge.ToNodeID] {
				adjacent[edge.ToNodeID] = true
			}
			if focus[edge.ToNodeID] && known[edge.FromNodeID] {
				adjacent[edge.FromNodeID] = true
			}
		}
		for id := range adjacent {
			focus[id] = true
		}
	}
	candidates := make([]struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Title    string `json:"title"`
		Metadata struct {
			WorkflowKind     string `json:"workflowKind"`
			CharacterAssetID string `json:"characterAssetId"`
		} `json:"metadata"`
	}, 0, len(payload.Nodes))
	if len(focus) > 0 {
		// Always retain explicitly selected nodes before neighbors when a highly
		// connected node would otherwise exceed the bounded catalog.
		for _, node := range payload.Nodes {
			if selectedFocus[node.ID] {
				candidates = append(candidates, node)
			}
		}
		for _, node := range payload.Nodes {
			if focus[node.ID] && !selectedFocus[node.ID] {
				candidates = append(candidates, node)
			}
		}
	} else {
		candidates = payload.Nodes
	}
	nodes := make([]map[string]any, 0)
	for index, node := range candidates {
		if index >= cloudAgentCanvasSummaryMaxNodes {
			break
		}
		item := map[string]any{"id": truncateRunes(node.ID, 100), "type": truncateRunes(node.Type, 40), "title": truncateRunes(node.Title, 80)}
		if node.Type == "text" && node.Metadata.WorkflowKind == "character" {
			item["kind"] = "character"
		}
		if _, known := canvasCapabilityRegistry.ResolveNode(node.Type, node.Metadata.WorkflowKind); !known {
			item["agentSupported"] = false
		}
		nodes = append(nodes, item)
		encoded, err := json.Marshal(nodes)
		if err != nil {
			return "", err
		}
		if len(encoded) > cloudAgentCanvasSummaryBudgetBytes {
			nodes = nodes[:len(nodes)-1]
			break
		}
	}
	summary := map[string]any{
		"kind": "node_catalog", "title": truncateRunes(canvas.Title, 240),
		"totalNodes": len(payload.Nodes), "includedNodes": len(nodes), "nodes": nodes,
		"read": "目录不含正文。用 canvas_get_state 按页读取；nodeIds 精读单节点，focusNodeIds+depth 读取关联子图，结构化节点用对应 read 工具。",
	}
	if len(focus) > 0 {
		selected := append([]string(nil), focusNodeIDs...)
		neighborCount := 0
		for _, node := range nodes {
			if !selectedFocus[stringValue(node["id"])] {
				neighborCount++
			}
		}
		summary["selection"] = map[string]any{"focusNodeIds": selected, "includedNeighbors": neighborCount, "nextReadDepth": 1}
	} else if omitted := len(payload.Nodes) - len(nodes); omitted > 0 {
		summary["omittedNodes"] = omitted
	}
	data, err := json.Marshal(summary)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
