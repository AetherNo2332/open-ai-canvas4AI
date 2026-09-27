package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
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

type PiAgentSnapshot struct {
	RunID                    string                      `json:"runId"`
	PiSessionID              string                      `json:"piSessionId"`
	PiSessionRevision        int64                       `json:"piSessionRevision"`
	PiSessionLeaseEpoch      int64                       `json:"piSessionLeaseEpoch"`
	PiSessionHeader          json.RawMessage             `json:"piSessionHeader"`
	PiSessionEntries         []PiAgentSessionEntry       `json:"piSessionEntries"`
	PiActiveLeafID           string                      `json:"piActiveLeafId,omitempty"`
	UserID                   string                      `json:"userId"`
	Revision                 int64                       `json:"revision"`
	Status                   string                      `json:"status"`
	Request                  CloudAgentRequest           `json:"request"`
	ModelLimits              PiAgentModelLimits          `json:"modelLimits"`
	Canonical                canonicalAgentRequest       `json:"canonical"`
	ActiveTask               string                      `json:"activeTaskId,omitempty"`
	LastTaskID               string                      `json:"lastTaskId,omitempty"`
	NoToolTaskID             string                      `json:"noToolTaskId,omitempty"`
	NoToolNudge              string                      `json:"noToolNudge,omitempty"`
	PendingContextCompaction *PiPendingContextCompaction `json:"pendingContextCompaction,omitempty"`
	PreviousStepTemplate     string                      `json:"previousStepTemplate"`
	Tools                    []PiAgentToolSpec           `json:"tools"`
	PiMessages               []json.RawMessage           `json:"piMessages"`
	Opened                   []string                    `json:"openedCategories"`
	// Harness 是首步合同冻结的 Harness 正文（版本 2 运行在首步之后非空）。
	//
	// Node 恢复时优先用它而不是重读磁盘文件：运维改了 SYSTEM.md / AGENTS.md 再重启进程，
	// 在途运行的系统提示不能被静默换成新文件内容。
	Harness *cloudAgentHarnessSnapshot `json:"harness,omitempty"`
}

// PiAgentModelLimits mirrors the effective Go-side text capability into Pi's
// scheduler. Go remains authoritative for request admission and billing; these
// values keep Pi's automatic compaction threshold close to the route budget.
type PiAgentModelLimits struct {
	ContextWindowTokens int    `json:"contextWindowTokens"`
	MaxOutputTokens     int    `json:"maxOutputTokens"`
	Configured          bool   `json:"configured"`
	Source              string `json:"source"`
}

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
	TaskID    string          `json:"taskId"`
	Status    string          `json:"status"`
	TextDraft string          `json:"textDraft,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}

type PiToolBatchRequest struct {
	Calls []cloudAgentCall `json:"calls"`
}

type PiMessageCheckpoint struct {
	Sequence        int               `json:"sequence"`
	Message         json.RawMessage   `json:"message"`
	TaskID          string            `json:"taskId,omitempty"`
	SessionRevision int64             `json:"sessionRevision,omitempty"`
	ActiveLeafID    string            `json:"activeLeafId,omitempty"`
	SessionEntries  []json.RawMessage `json:"sessionEntries,omitempty"`
}

// PiCheckpointMessage commits an assistant message and its model-task acknowledgement
// in one database transaction. A retry with the same sequence and body is a no-op.
func (s *Service) PiCheckpointMessage(userID, runID, owner string, input PiMessageCheckpoint) error {
	_, err := s.PiCheckpointMessageResult(userID, runID, owner, input)
	return err
}

func (s *Service) PiCheckpointMessageResult(userID, runID, owner string, input PiMessageCheckpoint) (int64, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		return 0, err
	}
	if input.Sequence < 1 || input.Sequence > 1000 || len(input.Message) == 0 || len(input.Message) > 1<<20 || !json.Valid(input.Message) || len(input.SessionEntries) > 128 {
		return 0, BadAuthRequest("Pi 消息检查点无效")
	}
	var sessionEntryBytes int
	for _, entry := range input.SessionEntries {
		sessionEntryBytes += len(entry)
		if len(entry) == 0 || len(entry) > 1<<20 || !json.Valid(entry) || sessionEntryBytes > 512<<10 {
			return 0, BadAuthRequest("Pi 会话检查点无效")
		}
	}
	if input.SessionRevision < 0 || input.SessionRevision > 1<<62 {
		return 0, BadAuthRequest("Pi 会话版本无效")
	}
	var message struct {
		Role    string `json:"role"`
		Text    string `json:"text"`
		Content any    `json:"content"`
	}
	if json.Unmarshal(input.Message, &message) != nil || (message.Role != "user" && message.Role != "assistant" && message.Role != "toolResult" && message.Role != "system") {
		return 0, BadAuthRequest("Pi 消息类型无效")
	}
	var sessionEntries []model.CloudAgentPiEntry
	for _, raw := range input.SessionEntries {
		var identity struct {
			ID       string  `json:"id"`
			ParentID *string `json:"parentId"`
		}
		if err := json.Unmarshal(raw, &identity); err != nil || strings.TrimSpace(identity.ID) == "" {
			return 0, BadAuthRequest("Pi 会话条目缺少 ID")
		}
		parentID := ""
		if identity.ParentID != nil {
			parentID = *identity.ParentID
		}
		sessionEntries = append(sessionEntries, model.CloudAgentPiEntry{
			SessionID: run.ConversationID, EntryID: identity.ID, ParentID: parentID,
			UserID: userID, RunID: runID, EntryJSON: string(raw),
		})
	}
	// 旧 SSE 合同在模型每次返回正文时发 assistant_message（含"正文 + 工具调用"同时出现的
	// 过程说明，见 cloud_agent_runtime.go 的 result.Text != "" 分支）。Pi 路径原先只在
	// PiNoToolTurn（无工具调用的收尾步骤）发这条事件，模型"边做边说"时前端会丢掉过程消息。
	// 这里与检查点同事务补发，payload 与旧路径保持一致。
	assistantText := piAssistantText(message.Content)
	if message.Text != "" {
		assistantText = message.Text
	}
	// 推理正文同样有既有 SSE 事件（cloud_agent_runtime.go 的 reasoning_message）。
	reasoningText := piReasoningText(message.Content)
	var updatedSessionRevision int64
	err = s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		count := 0
		for _, record := range current.Transcript {
			if record.Kind != cloudAgentMessageKindPi {
				continue
			}
			count++
			if record.Sequence == input.Sequence {
				var oldValue, newValue any
				_ = json.Unmarshal([]byte(record.MessageJSON), &oldValue)
				_ = json.Unmarshal(input.Message, &newValue)
				if !reflect.DeepEqual(oldValue, newValue) {
					return kernel.Forbidden("Pi 消息检查点冲突")
				}
				if input.SessionRevision > 0 || len(input.SessionEntries) > 0 || input.ActiveLeafID != "" {
					updatedSessionRevision, err = repo.AppendCloudAgentPiSessionEntries(
						userID, current.ConversationID, runID, input.SessionRevision, input.ActiveLeafID, sessionEntries,
					)
					if err != nil {
						return err
					}
				}
				return nil
			}
		}
		if input.Sequence != count+1 {
			return kernel.Forbidden("Pi 消息检查点序号不连续")
		}
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		if input.TaskID != "" {
			if message.Role != "assistant" || state.ActiveTaskID != input.TaskID {
				return kernel.Forbidden("Pi 模型任务与消息不匹配")
			}
			task, err := repo.TaskForUser(userID, input.TaskID)
			if err != nil {
				return err
			}
			if task.Status != model.TaskStatusSucceeded {
				return BadAuthRequest("模型任务尚未成功")
			}
			// Context pressure for the next Pi step must use the upstream-reported
			// usage of this completed step when available. Read through this same
			// transaction so the usage anchor and assistant checkpoint commit together.
			s.recordCloudAgentTokenAnchorWithRepository(repo, userID, &state)
			state.ActiveTaskID = ""
		}
		if err := cloudAgentSave(current, &state); err != nil {
			return err
		}
		current.Transcript = append(current.Transcript, model.CloudAgentMessageRecord{RunID: runID, UserID: userID, Kind: cloudAgentMessageKindPi, Sequence: input.Sequence, MessageJSON: string(input.Message)})
		current.MessageCount = len(current.Transcript)
		// 过程说明（final=false）与消息检查点同事务提交：前端订阅事件时不会出现
		// "有消息行但没有事件"或反之的中间态。
		if message.Role == "assistant" {
			messageID := taskIDForPiMessage(input, runID)
			changed := false
			if strings.TrimSpace(reasoningText) != "" {
				state.event(runID, "reasoning_message", map[string]any{
					"messageId": messageID + ":reasoning", "text": truncateRunes(reasoningText, 8000),
				})
				changed = true
			}
			if strings.TrimSpace(assistantText) != "" {
				state.event(runID, "assistant_message", map[string]any{
					"messageId": messageID, "text": assistantText, "final": false,
				})
				changed = true
			}
			if changed {
				if err := cloudAgentSave(current, &state); err != nil {
					return err
				}
			}
		}
		if input.SessionRevision > 0 || len(input.SessionEntries) > 0 || input.ActiveLeafID != "" {
			updatedSessionRevision, err = repo.AppendCloudAgentPiSessionEntries(
				userID, current.ConversationID, runID, input.SessionRevision, input.ActiveLeafID, sessionEntries,
			)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return updatedSessionRevision, nil
}

type PiToolReceipt struct {
	CallID  string          `json:"callId"`
	Pending bool            `json:"pending"`
	Result  json.RawMessage `json:"result,omitempty"`
	IsError bool            `json:"isError,omitempty"`
	// Terminated 表示本轮已进入终态（拒绝/失败/取消/完成），该调用不会有回执。
	// Node 必须据此结束 Pi 循环，而不是继续轮询 pending —— 拒绝是控制面决策，
	// 按合同不产生工具结果，也不得产生后续模型请求。
	Terminated bool `json:"terminated,omitempty"`
}

type PiTurnDecision struct {
	Status string `json:"status"`
	Nudge  string `json:"nudge,omitempty"`
}

func (s *Service) PiNoToolTurn(userID, runID, owner, taskID string) (*PiTurnDecision, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		var terminal bool
		run, terminal = s.piTerminalRunAfterLeaseFailure(userID, runID, owner)
		if !terminal {
			return nil, err
		}
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return nil, err
	}
	if cloudAgentRunTerminal(run.Status) {
		return &PiTurnDecision{Status: run.Status}, nil
	}
	if state.PiNoToolTaskID == taskID {
		status := run.Status
		if status == "running" {
			status = "continue"
		}
		return &PiTurnDecision{Status: status, Nudge: state.PiNoToolNudge}, nil
	}
	if state.ActiveTaskID != "" || state.CallIndex < len(state.Calls) || state.LastStepTaskID != taskID {
		return nil, kernel.Forbidden("Pi 收尾步骤无效")
	}
	task, err := s.repo.TaskForUser(userID, taskID)
	if err != nil {
		return nil, err
	}
	if task.Status != model.TaskStatusSucceeded {
		return nil, BadAuthRequest("模型步骤尚未成功")
	}
	var result struct {
		Text           string           `json:"text"`
		ToolCalls      []cloudAgentCall `json:"toolCalls"`
		StopReasonKind string           `json:"stopReasonKind"`
	}
	if err := json.Unmarshal([]byte(task.ResultJSON), &result); err != nil {
		return nil, err
	}
	if len(result.ToolCalls) != 0 {
		return nil, kernel.Forbidden("Pi 收尾步骤包含工具调用")
	}
	decision := &PiTurnDecision{}
	err = s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "assistant", "content": result.Text})
		state.PiNoToolTaskID = taskID
		completion := cloudAgentEvaluateCompletion(&state)
		completion = cloudAgentBlockTruncatedCompletion(completion, result.StopReasonKind)
		state.event(runID, "assistant_message", map[string]any{"messageId": taskID, "text": result.Text, "final": completion.Final})
		state.cloudAgentRecordImageObservations(result.Text, false)
		state.cloudAgentSyncVisualAnchor()
		if completion.Final {
			current.Status = "completed"
			decision.Status = "completed"
			return cloudAgentSave(current, &state)
		}
		if cloudAgentStepBudgetExhausted(&state) {
			completion.Attempt = state.CompletionNudgeAttempt
			decision.Status = "failed"
			return cloudAgentFailBlockedCompletion(current, &state, runID, completion)
		}
		attempt, exhausted := cloudAgentNoteCompletionBlocked(&state, completion.Fingerprint)
		if exhausted {
			completion.Attempt = attempt
			decision.Status = "failed"
			return cloudAgentFailBlockedCompletion(current, &state, runID, completion)
		}
		completion.Attempt = attempt
		cloudAgentCompletionBlockedNudge(runID, &state, completion)
		last := state.Canonical.Messages[len(state.Canonical.Messages)-1]
		decision.Status = "continue"
		decision.Nudge = stringField(last, "content")
		state.PiNoToolNudge = decision.Nudge
		return cloudAgentSave(current, &state)
	})
	return decision, err
}

func (s *Service) ClaimPiAgent(owner string) (*PiAgentSnapshot, error) {
	if owner == "" {
		return nil, BadAuthRequest("Pi worker ID is required")
	}
	run, err := s.repo.ClaimPiAgent(owner, time.Now().Add(piAgentLeaseDuration))
	if err != nil || run == nil {
		return nil, err
	}
	return s.piAgentSnapshot(run)
}

func (s *Service) PiAgentSnapshot(userID, runID, owner string) (*PiAgentSnapshot, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		return nil, err
	}
	return s.piAgentSnapshot(run)
}

func (s *Service) RenewPiAgentLease(userID, runID, owner string) error {
	if _, err := s.piAgentLeasedRun(userID, runID, owner); err != nil {
		return err
	}
	workerID, epoch, _, err := parsePiAgentLeaseOwner(owner)
	if err != nil {
		return kernel.Forbidden("Pi Agent 运行租约无效")
	}
	ok, err := s.repo.RenewPiAgentLease(userID, runID, workerID, epoch, time.Now().Add(piAgentLeaseDuration))
	if err != nil {
		return err
	}
	if !ok {
		return kernel.Forbidden("Pi Agent 运行租约已失效")
	}
	return nil
}

func (s *Service) PiModelStep(userID, runID, owner string, request PiModelStepRequest) (*PiModelStepView, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		return nil, err
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return nil, err
	}
	state.StepLimits, err = s.cloudAgentStepLimits()
	if err != nil {
		return nil, err
	}
	fingerprint, err := piModelStepFingerprint(request)
	if err != nil {
		return nil, err
	}
	if state.ActiveTaskID != "" {
		if state.PiModelStepFingerprint != "" && state.PiModelStepFingerprint != fingerprint {
			return nil, kernel.Forbidden("Pi 模型步骤请求与当前在途任务不一致")
		}
		return s.PiModelStepView(userID, runID, owner, state.ActiveTaskID)
	}
	if state.PiModelStepFingerprint != "" && state.PiModelStepFingerprint == fingerprint && state.LastStepTaskID != "" {
		return s.PiModelStepView(userID, runID, owner, state.LastStepTaskID)
	}
	if run.Status != "running" && run.Status != "queued" {
		return nil, kernel.Forbidden("Agent 当前状态不允许继续请求模型")
	}
	if len(request.Canonical.Messages) == 0 || len(request.Canonical.Messages) > 500 || len(request.Canonical.SystemPrompt) > 128<<10 {
		return nil, BadAuthRequest("Pi 模型上下文无效")
	}
	state.PiModelStepFingerprint = fingerprint
	// 服务端策略是不可移除的强制层：Node 负责装配（策略前缀 + Harness 文件），
	// 但**不允许**把策略整段丢掉或改写。没有这条校验时，一个缺陷或篡改的 worker
	// 只要少发一段 system prompt，就能解除服务端对工具权限、能力边界与安全规则的约束 ——
	// 而 Go 此前只校验了长度。
	//
	// 判据用"包含"而不是"前缀"：Node 的装配结果以服务端策略开头（见 system-prompt.ts
	// 的 renderSystemPrompt），但先按包含校验可以容忍未来在策略前插入版本头等合法包装。
	if policy := strings.TrimSpace(state.Canonical.SystemPrompt); policy != "" {
		if !strings.Contains(request.Canonical.SystemPrompt, policy) {
			return nil, kernel.Forbidden("Pi 模型请求缺少服务端策略")
		}
	}
	allowed := map[string]bool{}
	for _, tool := range cloudAgentVisibleToolsForCategories(state.Canonical.Tools, cloudAgentActivatedCategories(&state), nil, state.ToolScope) {
		function, _ := tool["function"].(map[string]any)
		allowed[stringField(function, "name")] = true
	}
	for _, tool := range request.Canonical.Tools {
		function, _ := tool["function"].(map[string]any)
		name := stringField(function, "name")
		if !allowed[name] || !cloudAgentToolAllowed(state.Request, name) {
			return nil, kernel.Forbidden("Pi 模型请求包含未披露工具")
		}
	}
	// 名字一致不等于同一份工具：同名不同参数结构足以让一次调用被服务端按另一套语义解读
	// （例如只读工具被改写成带可写参数）。这里与 Go 目录逐字段比对参数结构。
	if err := cloudAgentToolSchemaDrift(state.Canonical.Tools, request.Canonical.Tools); err != nil {
		return nil, kernel.Forbidden(err.Error())
	}
	request.Canonical.PromptCacheKey = state.Canonical.PromptCacheKey
	state.DisclosureVersion = cloudAgentToolDisclosureVersion
	state.AdvertisedToolNames = cloudAgentToolNames(request.Canonical.Tools)
	// 看图结果按参考素材水合：上下文里只有 `resource:<id>`，真实图片字节在**请求期**才进到
	// referenceImages，不进检查点，也不要求上游能访问部署地址。
	//
	// 旧循环一直这么做（见 advanceCloudAgent 里的同一步骤），Pi 路径漏了它。后果是
	// provider.go 的 resolveAgentResourcePlaceholders 扫到 canonical 里的 `resource:`
	// 却在 referenceImages 里找不到白名单条目，直接 BadAuthRequest("模型协议引用了未获准的
	// 图片") —— 也就是"看过图之后的下一步必然失败"。
	//
	// 它同时会把超出模型图片上限的旧图换成文字占位，所以必须在装配 canonical 之后、
	// 发请求之前调用（与旧路径同一位置）。
	references, refErr := s.cloudAgentImageReferences(run.UserID, state.Request, &request.Canonical)
	if refErr != nil {
		// 与旧循环同一处理：水合失败按安全文案转成可见错误，由 worker 上报为运行失败，
		// 不把资源 ID 或上游地址写进失败原因。
		return nil, kernel.BadAuthRequest(cloudAgentSafeToolError(refErr))
	}
	remaining := int64(math.Floor(state.Request.Budget.MaxCredits * float64(CreditScale)))
	if remaining <= 0 {
		return nil, BadAuthRequest("Agent 累计预算已耗尽")
	}
	input := map[string]any{
		"mode": "text", "prompt": state.Request.Prompt,
		"agentRequests": map[string]any{"canonical": request.Canonical},
		"config":        map[string]any{"channelId": state.Request.ChannelID, "channelModelKey": state.Request.ChannelModelKey, "model": firstNonEmpty(state.Request.ChannelModelKey, state.Request.Model)},
		"textOptions":   map[string]any{"stream": true, "thinking": cloudAgentReasoningEnabled(state.Policy.ReasoningMode), "maxOutputTokens": cloudAgentStepOutputBudget(state.StepLimits, false)},
	}
	if len(references) > 0 {
		input["referenceImages"] = references
	}
	// 提示合同（Harness 身份）在首个模型步冻结，之后只比对。
	//
	// 为什么是"拒绝"而不是"用新的"：Node 只有当前磁盘上的 Harness，无法重建旧内容；
	// 静默换提示会让同一轮运行的系统提示中途改变，比明确失败更难排查。
	// 与工具 schema 漂移的既有处理一致（那一条也是 FatalWorkerError 明确停止）。
	//
	// 三条分支的区别只在合同版本：版本 2 的运行按冻结快照核验（首步连正文一起冻结），
	// 迁移前的运行保持原有"首个哈希胜出"的语义 —— 旧运行没有快照，不能因为升级被拒绝。
	switch {
	case cloudAgentAwaitingFirstStep(&state):
		if err := cloudAgentFreezeFirstStepContract(&state, request.HarnessHash, request.Harness, request.Canonical.SystemPrompt); err != nil {
			return nil, kernel.Forbidden(err.Error())
		}
	case state.ContractVersion >= cloudAgentContractVersionFirstStep:
		if err := cloudAgentVerifyFrozenContract(&state); err != nil {
			return nil, kernel.Forbidden(err.Error())
		}
		if err := cloudAgentHarnessMatchesSnapshot(state.Snapshot, request.HarnessHash, request.Harness); err != nil {
			return nil, kernel.Forbidden(err.Error())
		}
		if err := cloudAgentVerifyAssembledPrompt(&state, request.Canonical.SystemPrompt); err != nil {
			return nil, kernel.Forbidden(err.Error())
		}
	default:
		if hash := strings.TrimSpace(request.HarnessHash); hash != "" {
			if state.PromptContract == "" {
				state.PromptContract = hash
			} else if state.PromptContract != hash {
				return nil, kernel.Forbidden("本轮运行的提示合同与当前 agent 装配不一致（Harness 已变更），请重新发起对话")
			}
		}
	}
	req := CreateTaskRequest{ProjectID: state.Request.CanvasID, Type: "canvas_text", Operation: cloudAgentStepOperation, Prompt: state.Request.Prompt, Model: state.Request.Model, LogicalModelID: state.Request.LogicalModelID, Input: input}
	firstStep := cloudAgentAwaitingFirstStep(&state)
	if err := s.enqueueCloudAgentTask(run, &state, req, nil); err != nil {
		// 首步准入失败时运行仍停在 awaiting_first_step，本轮不会有任何模型调用：
		// 必须当场给出确定终态并退还占位预留，而不是等看门狗超时把它当成"卡住"。
		// CAS 冲突例外 —— 那说明另一方已经推进了这一行，不是准入失败。
		if firstStep && !errors.Is(err, repository.ErrCreationConflict) {
			if failErr := s.failCloudAgentFirstStepAdmission(run, &state, err); failErr != nil && !errors.Is(failErr, repository.ErrCreationConflict) {
				return nil, err
			}
		}
		return nil, err
	}
	latest, err := s.repo.CloudAgent(userID, runID)
	if err != nil {
		return nil, err
	}
	if latest.ActiveTaskID == "" {
		return nil, fmt.Errorf("Pi model task was not persisted")
	}
	return s.PiModelStepView(userID, runID, owner, latest.ActiveTaskID)
}

func (s *Service) PiModelStepView(userID, runID, owner, taskID string) (*PiModelStepView, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		return nil, err
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return nil, err
	}
	if state.ActiveTaskID != taskID && state.LastStepTaskID != taskID {
		return nil, kernel.Forbidden("模型任务不属于当前 Pi 步骤")
	}
	task, err := s.repo.TaskForUser(userID, taskID)
	if err != nil {
		return nil, err
	}
	view := &PiModelStepView{TaskID: task.ID, Status: string(task.Status), TextDraft: task.TextDraft}
	if cloudAgentTaskTerminal(task.Status) {
		view.Result = json.RawMessage(task.ResultJSON)
		view.Error = task.Error
	}
	return view, nil
}

func piModelStepFingerprint(request PiModelStepRequest) (string, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	return cloudAgentTextDigest(string(encoded)), nil
}

func (s *Service) PiFailModelStep(userID, runID, owner, taskID string) error {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		var terminal bool
		run, terminal = s.piTerminalRunAfterLeaseFailure(userID, runID, owner)
		if !terminal {
			return err
		}
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return err
	}
	// 任务查询提到最前面：下面两条守卫（"成功任务不得被报失败"与终态幂等）都要用它。
	task, err := s.repo.TaskForUser(userID, taskID)
	if err != nil {
		return err
	}
	// 协议不变量，与运行状态无关：已成功的任务永远不能被上报为失败。
	// 必须排在终态幂等守卫**之前**，否则幂等会把真正的协议违规一起吞掉。
	if task.Status == model.TaskStatusSucceeded {
		return kernel.Forbidden("模型任务与当前 Pi 步骤不匹配")
	}
	if run.Status == "failed" {
		// 重投：worker 可能在"已经记录失败、但没收到响应"之间崩溃。只有同一失败
		// 任务的重投才幂等成功；否则 400 会被 Node 当成协议错误并整轮退出。
		if state.ActiveTaskID == "" || (state.ActiveTaskID == taskID && samePiFailure(run.FailureMessage, task.Error)) {
			return nil
		}
		return kernel.Forbidden("模型任务与当前 Pi 步骤不匹配")
	}
	if cloudAgentRunTerminal(run.Status) {
		// 运行已经以 completed / cancelled / rejected 终结，而 worker 的 /fail 仍在途
		// （它先收到终态快照，这个请求已经在路上）。这是竞态的**正常结果**，不是错误。
		//
		// 必须返回 nil：bridge 会把确定性 4xx 归类为 FatalWorkerError 并整轮退出，
		// 对一次已经正确终结的运行回错误，等于把正常收尾变成 worker 报错。
		// 修复前这里没有守卫，会把 cancelled / rejected / completed 无条件改写成 failed。
		return nil
	}
	if state.ActiveTaskID != taskID {
		return kernel.Forbidden("模型任务与当前 Pi 步骤不匹配")
	}
	if task.Status == model.TaskStatusQueued || task.Status == model.TaskStatusRunning {
		return BadAuthRequest("模型任务没有失败")
	}
	return s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		current.Status = "failed"
		current.FailureMessage = truncateRunes(task.Error, 1000)
		state.event(runID, "run_failed", map[string]any{"text": "模型任务失败", "reason": "model_step_failed", "taskId": taskID})
		return cloudAgentSave(current, &state)
	})
}

func (s *Service) PiModelStepAck(userID, runID, owner, taskID string) error {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		return err
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return err
	}
	if state.ActiveTaskID == "" {
		return nil
	}
	if state.ActiveTaskID != taskID {
		return kernel.Forbidden("模型任务与当前 Pi 步骤不匹配")
	}
	task, err := s.repo.TaskForUser(userID, taskID)
	if err != nil {
		return err
	}
	if !cloudAgentTaskTerminal(task.Status) {
		return BadAuthRequest("模型任务尚未结束")
	}
	return s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		s.recordCloudAgentTokenAnchorWithRepository(repo, userID, &state)
		state.ActiveTaskID = ""
		return cloudAgentSave(current, &state)
	})
}

func (s *Service) PiToolBatch(userID, runID, owner string, batch PiToolBatchRequest) error {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		return err
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return err
	}
	if len(state.Calls) > 0 && piSameCalls(state.Calls, batch.Calls) {
		return nil
	}
	if state.ActiveTaskID != "" || state.CallIndex < len(state.Calls) || run.Status != "running" {
		return kernel.Forbidden("Agent 尚有未完成的模型或工具步骤")
	}
	if err := validateCloudAgentCalls(batch.Calls); err != nil || len(batch.Calls) == 0 || len(batch.Calls) > 16 {
		return BadAuthRequest("Pi 工具调用批次无效")
	}
	// The worker can only submit calls that the billed model step actually emitted.
	modelTask, err := s.repo.TaskForUser(userID, state.LastStepTaskID)
	if err != nil {
		return err
	}
	if modelTask.Status != model.TaskStatusSucceeded {
		return kernel.Forbidden("Pi 模型步骤未成功")
	}
	var modelOutput struct {
		ToolCalls []cloudAgentCall `json:"toolCalls"`
		Legacy    []cloudAgentCall `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(modelTask.ResultJSON), &modelOutput); err != nil {
		return err
	}
	expected := modelOutput.ToolCalls
	if len(expected) == 0 {
		expected = modelOutput.Legacy
	}
	if !piSameCalls(expected, batch.Calls) {
		return kernel.Forbidden("Pi 工具批次与模型结果不一致")
	}
	state.Calls = batch.Calls
	state.CallIndex = 0
	state.CallAdmissions = cloudAgentPreflightBatch(&state, batch.Calls)
	state.CanvasBatchHashes = nil
	state.StepSnapshotHash = cloudAgentCaptureStepSnapshotHash(batch.Calls)
	state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "assistant", "content": "", "tool_calls": batch.Calls})
	return s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	})
}

func piSameCalls(left, right []cloudAgentCall) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ID != right[index].ID || left[index].Function.Name != right[index].Function.Name {
			return false
		}
		var leftArgs, rightArgs any
		if json.Unmarshal([]byte(left[index].Function.Arguments), &leftArgs) != nil || json.Unmarshal([]byte(right[index].Function.Arguments), &rightArgs) != nil || !reflect.DeepEqual(leftArgs, rightArgs) {
			return false
		}
	}
	return true
}

func (s *Service) PiToolAdvance(userID, runID, owner, callID string) (*PiToolReceipt, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		// 终态转换会清除 session 租约。Pi worker 可能正好在终结后轮询
		// 工具回执；对已终结的本用户 Pi run 返回终止信号，避免旧租约错误
		// 让 Node 将整轮判为协议失败。非终态仍必须通过租约 fencing。
		if _, terminal := s.piTerminalRunAfterLeaseFailure(userID, runID, owner); terminal {
			return &PiToolReceipt{CallID: callID, Terminated: true}, nil
		}
		return nil, err
	}
	if cloudAgentRunTerminal(run.Status) {
		// 本轮已终结：不再有工具回执。返回明确的终止信号，避免 Node 无限轮询 pending。
		return &PiToolReceipt{CallID: callID, Terminated: true}, nil
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return nil, err
	}
	if receipt := piToolReceipt(&state, callID); receipt != nil {
		return receipt, nil
	}
	if state.CallIndex >= len(state.Calls) || state.Calls[state.CallIndex].ID != callID {
		return nil, kernel.Forbidden("Pi 工具调用顺序或标识无效")
	}
	if run.Status == "waiting_approval" && state.Approval != nil && state.Approval.Decision == "" {
		return &PiToolReceipt{CallID: callID, Pending: true}, nil
	}
	var advanceErr error
	if state.MediaTaskID != "" {
		advanceErr = s.executeCloudAgentMediaCall(run, &state, state.Calls[state.CallIndex])
	} else {
		advanceErr = s.executeCloudAgentToolCall(run, &state)
	}
	if advanceErr != nil {
		return nil, advanceErr
	}
	latest, err := s.repo.CloudAgent(userID, runID)
	if err != nil {
		return nil, err
	}
	updated, err := cloudAgentDecode(latest)
	if err != nil {
		return nil, err
	}
	if receipt := piToolReceipt(&updated, callID); receipt != nil {
		return receipt, nil
	}
	if cloudAgentRunTerminal(latest.Status) {
		return &PiToolReceipt{CallID: callID, Terminated: true}, nil
	}
	return &PiToolReceipt{CallID: callID, Pending: true}, nil
}

func piToolReceipt(state *cloudAgentRuntime, callID string) *PiToolReceipt {
	for index := len(state.Canonical.Messages) - 1; index >= 0; index-- {
		message := state.Canonical.Messages[index]
		if stringField(message, "role") != "tool" || stringField(message, "tool_call_id") != callID {
			continue
		}
		receipt := &PiToolReceipt{CallID: callID, Result: json.RawMessage(stringField(message, "content"))}
		for eventIndex := len(state.Events) - 1; eventIndex >= 0; eventIndex-- {
			event := state.Events[eventIndex]
			if stringField(event.Payload, "callId") == callID {
				receipt.IsError = event.Type == "tool_failed"
				break
			}
		}
		return receipt
	}
	return nil
}

func (s *Service) piAgentLeasedRun(userID, runID, owner string) (*model.CloudAgentExecution, error) {
	run, err := s.repo.CloudAgent(userID, runID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, kernel.NotFound("Agent 运行不存在")
	}
	if err != nil {
		return nil, err
	}
	workerID, expectedEpoch, hasEpoch, tokenErr := parsePiAgentLeaseOwner(owner)
	if tokenErr != nil || run.Engine != "pi" || run.LeaseOwner != workerID || run.LeaseExpiresAt == nil || !run.LeaseExpiresAt.After(time.Now()) {
		return nil, kernel.Forbidden("Pi Agent 运行租约无效")
	}
	session, _, err := s.repo.CloudAgentPiSession(userID, firstNonEmpty(run.ConversationID, run.ID))
	if err != nil || session.ActiveRunID != runID || session.LeaseOwner != workerID || session.LeaseExpiresAt == nil || !session.LeaseExpiresAt.After(time.Now()) || (hasEpoch && session.LeaseEpoch != expectedEpoch) {
		return nil, kernel.Forbidden("Pi Agent 会话租约已失效")
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
	state, err := cloudAgentDecode(run)
	if err != nil {
		return nil, err
	}
	budget := s.cloudAgentContextBudgetForRequest(state.Request)
	tools := make([]PiAgentToolSpec, 0, len(state.Canonical.Tools))
	for _, item := range state.Canonical.Tools {
		function, _ := item["function"].(map[string]any)
		name := stringField(function, "name")
		parameters, _ := function["parameters"].(map[string]any)
		if name == "" || parameters == nil {
			continue
		}
		tools = append(tools, PiAgentToolSpec{
			Name: name, Description: stringField(function, "description"), Parameters: parameters,
			Category: cloudAgentToolCategory(name), Allowed: cloudAgentToolAllowed(state.Request, name),
		})
	}
	session, sessionEntries, err := s.repo.CloudAgentPiSession(run.UserID, firstNonEmpty(run.ConversationID, run.ID))
	if err != nil {
		return nil, err
	}
	entryViews := make([]PiAgentSessionEntry, 0, len(sessionEntries))
	for _, entry := range sessionEntries {
		entryViews = append(entryViews, PiAgentSessionEntry{RunID: entry.RunID, Entry: json.RawMessage(entry.EntryJSON)})
	}
	var pendingCompaction *PiPendingContextCompaction
	if compaction := state.ContextCompaction; compaction != nil && compaction.PiOperationID != "" {
		pendingCompaction = &PiPendingContextCompaction{
			OperationID: compaction.PiOperationID, SessionRevision: compaction.PiSessionRevision,
			ActiveLeafID: compaction.PiSourceLeafID, Reason: compaction.PiReason,
			WillRetry: compaction.PiWillRetry, TokensBefore: compaction.PiTokensBefore,
		}
	}
	return &PiAgentSnapshot{
		RunID: run.ID, UserID: run.UserID, Revision: run.Revision, Status: run.Status,
		PiSessionID: session.ID, PiSessionRevision: session.Revision, PiSessionLeaseEpoch: session.LeaseEpoch, PiSessionHeader: json.RawMessage(session.HeaderJSON),
		PiSessionEntries: entryViews, PiActiveLeafID: session.ActiveLeafID,
		Request: state.Request, ModelLimits: PiAgentModelLimits{ContextWindowTokens: budget.ContextWindowTokens, MaxOutputTokens: budget.MaxOutputTokens, Configured: budget.Configured, Source: budget.Source}, Canonical: state.Canonical, ActiveTask: state.ActiveTaskID, LastTaskID: state.LastStepTaskID, NoToolTaskID: state.PiNoToolTaskID, NoToolNudge: state.PiNoToolNudge, PendingContextCompaction: pendingCompaction, PreviousStepTemplate: cloudAgentToolText("previous_step_calls"), Tools: tools,
		Opened: state.ActivatedToolCategories, PiMessages: piAgentMessages(run),
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

// samePiFailure 只用于失败上报的幂等重投判定：运行上记录的失败文案就是该步骤
// 任务错误截断后的结果，两者相同说明这次上报已经落库。
func samePiFailure(recorded, taskError string) bool {
	return truncateRunes(taskError, 1000) == recorded
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
	run, err := s.piAgentLeasedRun(userID, runID, owner)
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
	return s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		current.Status = "failed"
		current.FailureMessage = message
		state.event(runID, "run_failed", map[string]any{
			"text": "Agent 运行无法继续：" + message, "reason": "pi_worker_fatal",
		})
		return cloudAgentSave(current, &state)
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
	cutoff := time.Now().Add(-piStalledLeasePeriods * piAgentLeaseDuration)
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
