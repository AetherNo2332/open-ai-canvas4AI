package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func (s *Service) PiModelStep(userID, runID, owner string, request PiModelStepRequest) (*PiModelStepView, error) {
	return s.piModelStep(userID, runID, owner, request, false)
}

// Preflight validates the same envelope as admission, without creating a task or order.
func (s *Service) PiModelPreflight(userID, runID, owner string, request PiModelStepRequest) (*PiModelStepView, error) {
	return s.piModelStep(userID, runID, owner, request, true)
}

func (s *Service) piModelStep(userID, runID, owner string, request PiModelStepRequest, preflight bool) (*PiModelStepView, error) {
	run, view, err := s.piModelStepLeasedRun(userID, runID, owner)
	if run == nil || err != nil {
		return view, err
	}
	state, err := s.piModelStepState(run)
	if err != nil {
		return nil, err
	}
	fingerprint, err := piModelStepFingerprint(request)
	if err != nil {
		return nil, err
	}
	if view, resumed, err := s.piModelStepResume(userID, runID, owner, &state, fingerprint); resumed {
		return view, err
	}
	if err := s.piModelStepGate(run, &state, request); err != nil {
		return nil, err
	}
	state.PiModelStepFingerprint = fingerprint
	if err := piModelStepCheckDisclosure(&state, request); err != nil {
		return nil, err
	}
	input, modelBudget, err := s.piModelStepTaskInput(run, &state, &request)
	if err != nil {
		return nil, err
	}
	if err := piModelStepVerifyContract(&state, request, preflight); err != nil {
		return nil, err
	}
	pressure := s.cloudAgentContextPressure(request.Canonical, state.Request.Prompt, state.Request)
	cloudAgentExpireTokenAnchorForSelection(run.ID, &state)
	projected, _ := cloudAgentProjectedInputTokens(pressure, &state)
	view = &PiModelStepView{Status: "ready", Decision: "model", ModelLimits: modelBudget, ProjectedTokens: projected}
	// Pi owns the trigger. Go only blocks ordinary requests while an existing
	// persisted compaction operation is being recovered or committed.
	if state.ContextCompaction != nil {
		view.Status, view.Decision = "waiting_compaction", "compact"
		return view, nil
	}
	if preflight {
		return view, nil
	}
	return s.piModelStepEnqueue(userID, runID, owner, run, &state, input)
}

// piModelStepLeasedRun returns the leased run, or a terminal view (with a nil
// run) when the run already ended or the lease lost to a terminal transition.
func (s *Service) piModelStepLeasedRun(userID, runID, owner string) (*model.CloudAgentExecution, *PiModelStepView, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		var terminal bool
		run, terminal = s.piTerminalRunAfterLeaseFailure(userID, runID, owner)
		if !terminal {
			return nil, nil, err
		}
		return nil, &PiModelStepView{Status: run.Status, Error: run.FailureMessage}, nil
	}
	if cloudAgentRunTerminal(run.Status) {
		return nil, &PiModelStepView{Status: run.Status, Error: run.FailureMessage}, nil
	}
	return run, nil, nil
}

// piModelStepState decodes the run and loads the step limits. A run on a retired
// policy contract is archived instead of continuing.
func (s *Service) piModelStepState(run *model.CloudAgentExecution) (cloudAgentRuntime, error) {
	state, err := cloudAgentDecode(run)
	if err != nil {
		return state, err
	}
	if err := validateCloudAgentPolicySnapshot(state.Policy); err != nil {
		message := "本轮使用旧版执行合同，已归档；请在新一轮继续对话"
		if cloudAgentAwaitingFirstStep(&state) {
			if failErr := s.failCloudAgentFirstStepAdmission(run, &state, kernel.Forbidden(message)); failErr != nil {
				return state, failErr
			}
		} else if failErr := s.failCloudAgent(run, &state, message); failErr != nil {
			return state, failErr
		}
		return state, kernel.Forbidden(message)
	}
	state.StepLimits, err = s.cloudAgentStepLimits()
	if err != nil {
		return state, err
	}
	state.CallLimits = state.StepLimits.CallLimits
	return state, nil
}

// piModelStepResume answers a repeated request for an in-flight or just finished
// step without admitting a new task. resumed reports whether it answered.
func (s *Service) piModelStepResume(userID, runID, owner string, state *cloudAgentRuntime, fingerprint string) (*PiModelStepView, bool, error) {
	if state.ActiveTaskID != "" {
		if state.ContextCompaction != nil {
			return &PiModelStepView{Status: "waiting_compaction", Decision: "compact", ModelLimits: s.cloudAgentContextBudgetForRequest(state.Request)}, true, nil
		}
		if state.PiModelStepFingerprint != "" && state.PiModelStepFingerprint != fingerprint {
			return nil, true, kernel.Forbidden("Pi 模型步骤请求与当前在途任务不一致")
		}
		view, err := s.PiModelStepView(userID, runID, owner, state.ActiveTaskID)
		return view, true, err
	}
	if state.PiModelStepFingerprint != "" && state.PiModelStepFingerprint == fingerprint && state.LastStepTaskID != "" {
		view, err := s.PiModelStepView(userID, runID, owner, state.LastStepTaskID)
		return view, true, err
	}
	return nil, false, nil
}

// piModelStepGate rejects a new step when the run cannot continue or the request
// envelope is out of bounds.
func (s *Service) piModelStepGate(run *model.CloudAgentExecution, state *cloudAgentRuntime, request PiModelStepRequest) error {
	if run.Status != "running" && run.Status != "queued" {
		return kernel.Forbidden("Agent 当前状态不允许继续请求模型")
	}
	if cloudAgentStepBudgetExhausted(state) {
		message := fmt.Sprintf("达到 %d 次模型调用上限，本轮已停止", state.callLimits().stepLimit(state.Request))
		if err := s.failCloudAgent(run, state, message); err != nil {
			return err
		}
		return kernel.Forbidden(message)
	}
	if len(request.Canonical.Messages) == 0 || len(request.Canonical.Messages) > 500 || len(request.Canonical.SystemPrompt) > 128<<10 {
		return BadAuthRequest("Pi 模型上下文无效")
	}
	return nil
}

// piModelStepCheckDisclosure verifies the server policy and the tool loadout the
// worker assembled against what Go disclosed for this run.
func piModelStepCheckDisclosure(state *cloudAgentRuntime, request PiModelStepRequest) error {
	// 服务端策略是不可移除的强制层：Node 负责装配（策略前缀 + Harness 文件），
	// 但**不允许**把策略整段丢掉或改写。没有这条校验时，一个缺陷或篡改的 worker
	// 只要少发一段 system prompt，就能解除服务端对工具权限、能力边界与安全规则的约束 ——
	// 而 Go 此前只校验了长度。
	//
	// 判据用"包含"而不是"前缀"：Node 的装配结果以服务端策略开头（见 system-prompt.ts
	// 的 renderSystemPrompt），但先按包含校验可以容忍未来在策略前插入版本头等合法包装。
	if policy := strings.TrimSpace(state.Canonical.SystemPrompt); policy != "" {
		if !strings.Contains(request.Canonical.SystemPrompt, policy) {
			return kernel.Forbidden("Pi 模型请求缺少服务端策略")
		}
	}
	if err := piModelStepRequestedToolsDisclosed(state, request); err != nil {
		return err
	}
	requestedNames := cloudAgentToolNames(request.Canonical.Tools)
	for _, previous := range state.AdvertisedToolNames {
		// A recovered batch may finish before the worker reads a fresh snapshot.
		// Retired tools can leave the next model loadout only after that batch
		// settles; current tools and all new calls retain normal disclosure checks.
		nativeRead := state.SkillRuntimeMode == cloudAgentSkillRuntimeNative && previous == "read"
		if state.CallIndex >= len(state.Calls) && !nativeRead && !cloudAgentToolAllowed(state.Request, previous) {
			continue
		}
		if !cloudAgentContainsString(requestedNames, previous) {
			return kernel.Forbidden("Pi 模型请求不能移除已披露工具")
		}
	}
	// 名字一致不等于同一份工具：同名不同参数结构足以让一次调用被服务端按另一套语义解读
	// （例如只读工具被改写成带可写参数）。这里与 Go 目录逐字段比对参数结构。
	if err := cloudAgentToolSchemaDrift(state.Canonical.Tools, request.Canonical.Tools); err != nil {
		return kernel.Forbidden(err.Error())
	}
	return nil
}

func piModelStepRequestedToolsDisclosed(state *cloudAgentRuntime, request PiModelStepRequest) error {
	allowed := map[string]bool{}
	nextNames := state.AdvertisedToolNames
	if len(nextNames) == 0 {
		nextNames = cloudAgentToolNames(state.Canonical.Tools)
	}
	// Only concrete tools offered at this model boundary may enter the request.
	// Repair scope remains a separate restriction on execution.
	for _, tool := range cloudAgentToolsNamed(state.Canonical.Tools, nextNames) {
		function, _ := tool["function"].(map[string]any)
		allowed[stringField(function, "name")] = true
	}
	for _, tool := range request.Canonical.Tools {
		function, _ := tool["function"].(map[string]any)
		name := stringField(function, "name")
		nativeRead := state.SkillRuntimeMode == cloudAgentSkillRuntimeNative && name == "read"
		if !allowed[name] || (!nativeRead && !cloudAgentToolAllowed(state.Request, name)) {
			return kernel.Forbidden("Pi 模型请求包含未披露工具")
		}
	}
	return nil
}

// piModelStepTaskInput projects the request for this provider envelope and builds
// the model task input. It records the disclosed loadout and delivered images and
// returns the model budget the step was sized against.
func (s *Service) piModelStepTaskInput(run *model.CloudAgentExecution, state *cloudAgentRuntime, request *PiModelStepRequest) (map[string]any, cloudAgentContextBudget, error) {
	request.Canonical.PromptCacheKey = cloudAgentPromptCacheKeyForRequest(state.Request.CanvasID, cloudAgentPromptCacheIdentity(cloudAgentCacheRequest(state), state.Policy), state.Canonical.SystemPrompt, request.Canonical.Tools)
	request.Canonical.Messages = append(request.Canonical.Messages, state.callLimitFacts())
	state.DisclosureVersion = cloudAgentToolDisclosureVersion
	state.AdvertisedToolNames = cloudAgentToolNames(request.Canonical.Tools)
	// 看图结果按参考素材水合：上下文里只有 `resource:<id>`，真实图片字节在**请求期**才进到
	// referenceImages，不进检查点，也不要求上游能访问部署地址。
	//
	// 迁移前 Go 组装器会先水合请求里的资源引用；Pi 路径曾漏掉它。后果是
	// provider.go 的 resolveAgentResourcePlaceholders 扫到 canonical 里的 `resource:`
	// 却在 referenceImages 里找不到白名单条目，直接 BadAuthRequest("模型协议引用了未获准的
	// 图片") —— 也就是"看过图之后的下一步必然失败"。
	//
	// 容量不足必须拒绝，不能为了满足上限把任何历史图片换成文字。
	visionBudget := state.VisionBatchBudget
	if visionBudget.MaxImages == 0 && visionBudget.MaxCost == 0 {
		if configured, budgetErr := s.cloudAgentVisionBatchBudget(state.Request); budgetErr == nil {
			visionBudget = configured
		}
	}
	request.Canonical, _ = projectCloudAgentVisionRequest(request.Canonical, visionBudget, state.DeliveredImageSHAs, state.DeliveredImageComparisons)
	references, refErr := s.cloudAgentImageReferences(run.UserID, state.Request, &request.Canonical)
	if refErr != nil {
		// 水合失败按安全文案转成可见错误，由 worker 上报为运行失败，
		// 不把资源 ID 或上游地址写进失败原因。
		return nil, cloudAgentContextBudget{}, kernel.BadAuthRequest(cloudAgentSafeToolError(refErr))
	}
	// The Go vision ledger tracks images that reached this specific provider envelope,
	// not merely images present in Pi history. Check the model's image limit before
	// settling pending observations; all visual inputs remain intact.
	deliveredNodes := deliveredImageNodeIDs(request.Canonical)
	state.cloudAgentSettleImageDelivery(deliveredNodes, len(deliveredNodes))
	remaining := int64(math.Floor(state.Request.Budget.MaxCredits * float64(CreditScale)))
	if holdbacks, err := s.cloudAgentSubagentBudgetHoldbacks(state); err != nil {
		return nil, cloudAgentContextBudget{}, err
	} else {
		remaining -= holdbacks
	}
	if remaining <= 0 {
		return nil, cloudAgentContextBudget{}, BadAuthRequest("Agent 累计预算已耗尽")
	}
	stepThinking := cloudAgentThinkingLevel(state.Policy.ReasoningMode, state.ForceThinkingOff)
	stepOutputTokens := cloudAgentStepOutputBudget(state.StepLimits, state.BoostStepOutputBudget)
	modelBudget := s.cloudAgentContextBudgetForRequest(state.Request)
	if stepOutputTokens <= 0 || stepOutputTokens > modelBudget.MaxOutputTokens {
		stepOutputTokens = modelBudget.MaxOutputTokens
	}
	input := map[string]any{
		"mode": "text", "prompt": state.Request.Prompt,
		"agentRequests": map[string]any{"canonical": request.Canonical},
		"config":        map[string]any{"channelId": state.Request.ChannelID, "channelModelKey": state.Request.ChannelModelKey, "model": firstNonEmpty(state.Request.ChannelModelKey, state.Request.Model)},
		"textOptions":   map[string]any{"stream": true, "thinkingLevel": stepThinking, "maxOutputTokens": stepOutputTokens},
	}
	if len(references) > 0 {
		input["referenceImages"] = references
	}
	return input, modelBudget, nil
}

// piModelStepVerifyContract 提示合同（Harness 身份）在首个模型步冻结，之后只比对。
//
// 为什么是"拒绝"而不是"用新的"：Node 只有当前磁盘上的 Harness，无法重建旧内容；
// 静默换提示会让同一轮运行的系统提示中途改变，比明确失败更难排查。
// 与工具 schema 漂移的既有处理一致（那一条也是 FatalWorkerError 明确停止）。
//
// 三条分支的区别只在合同版本：版本 2 的运行按冻结快照核验（首步连正文一起冻结），
// 迁移前的运行保持原有"首个哈希胜出"的语义 —— 旧运行没有快照，不能因为升级被拒绝。
func piModelStepVerifyContract(state *cloudAgentRuntime, request PiModelStepRequest, preflight bool) error {
	originalSnapshot, originalPromptContract := state.Snapshot, state.PromptContract
	if preflight && state.Snapshot != nil {
		copy := *state.Snapshot
		state.Snapshot = &copy
	}
	switch {
	case cloudAgentAwaitingFirstStep(state):
		if err := cloudAgentFreezeFirstStepContract(state, request.HarnessHash, request.Harness, request.Canonical.SystemPrompt); err != nil {
			return kernel.Forbidden(err.Error())
		}
	case state.ContractVersion >= cloudAgentContractVersionFirstStep:
		if err := cloudAgentVerifyFrozenContract(state); err != nil {
			return kernel.Forbidden(err.Error())
		}
		if err := cloudAgentHarnessMatchesSnapshot(state.Snapshot, request.HarnessHash, request.Harness); err != nil {
			return kernel.Forbidden(err.Error())
		}
		if err := cloudAgentVerifyAssembledPrompt(state, request.Canonical.SystemPrompt); err != nil {
			return kernel.Forbidden(err.Error())
		}
	default:
		if hash := strings.TrimSpace(request.HarnessHash); hash != "" {
			if state.PromptContract == "" {
				state.PromptContract = hash
			} else if state.PromptContract != hash {
				return kernel.Forbidden("本轮运行的提示合同与当前 agent 装配不一致（Harness 已变更），请重新发起对话")
			}
		}
	}
	if preflight {
		state.Snapshot, state.PromptContract = originalSnapshot, originalPromptContract
	}
	return nil
}

// piModelStepEnqueue admits the model task and returns the view of the persisted task.
func (s *Service) piModelStepEnqueue(userID, runID, owner string, run *model.CloudAgentExecution, state *cloudAgentRuntime, input map[string]any) (*PiModelStepView, error) {
	req := CreateTaskRequest{ProjectID: state.Request.CanvasID, Type: "canvas_text", Operation: cloudAgentStepOperation, Prompt: state.Request.Prompt, Model: state.Request.Model, LogicalModelID: state.Request.LogicalModelID, Input: input}
	firstStep := cloudAgentAwaitingFirstStep(state)
	// The recovery user entry is already durable in the Pi message tree when a
	// retry is admitted. Clear one-shot nudges so later steps and restarts do not
	// replay the same timeout/empty-output instruction.
	state.PiNoToolTaskID, state.PiNoToolNudge = "", ""
	state.PiModelFailureNudge = ""
	if err := s.enqueueCloudAgentTask(run, state, req, nil); err != nil {
		// 首步准入失败时运行仍停在 awaiting_first_step，本轮不会有任何模型调用：
		// 必须当场给出确定终态并退还占位预留，而不是等看门狗超时把它当成"卡住"。
		// CAS 冲突例外 —— 那说明另一方已经推进了这一行，不是准入失败。
		if firstStep && !errors.Is(err, repository.ErrCreationConflict) {
			if failErr := s.failCloudAgentFirstStepAdmission(run, state, err); failErr != nil && !errors.Is(failErr, repository.ErrCreationConflict) {
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
		if terminalRun, terminal := s.piTerminalRunAfterLeaseFailure(userID, runID, owner); terminal {
			return &PiModelStepView{TaskID: taskID, Status: terminalRun.Status, Error: terminalRun.FailureMessage}, nil
		}
		return nil, err
	}
	if cloudAgentRunTerminal(run.Status) {
		return &PiModelStepView{TaskID: taskID, Status: run.Status, Error: run.FailureMessage}, nil
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
	// Worker 将 failed 查询结果作为终止信号，不一定再调用失败上报。
	// 因此超时必须在返回任务状态前持久化整轮失败。
	if cloudAgentTaskTerminal(task.Status) && cloudAgentStepTimedOut(task) {
		if err := s.failCloudAgentStepTimeout(run, &state, task); err != nil {
			return nil, err
		}
		text, _ := cloudAgentModelFailure(task)
		return &PiModelStepView{TaskID: task.ID, Status: "failed", Error: text}, nil
	}
	if task.Status == model.TaskStatusSucceeded && state.PiAssistantCheckpointTaskID != taskID && state.PiToolBatchTaskID != taskID {
		var output struct {
			Calls  []cloudAgentCall `json:"toolCalls"`
			Legacy []cloudAgentCall `json:"tool_calls"`
		}
		if err := json.Unmarshal([]byte(task.ResultJSON), &output); err != nil {
			return nil, err
		}
		// Count native reads before Worker filters them out of canvas batches.
		if message := cloudAgentOutputViolation("", max(len(output.Calls), len(output.Legacy)), state.callLimits()); message != "" {
			if err := s.failCloudAgent(run, &state, message); err != nil {
				return nil, err
			}
			return &PiModelStepView{TaskID: task.ID, Status: "failed", Error: message}, nil
		}
	}
	draft := state.ActiveTextDraft
	if draft == "" {
		// Legacy runs persisted the draft on the Task row before the runtime
		// source became authoritative.
		draft = task.TextDraft
	}
	view := &PiModelStepView{TaskID: task.ID, Status: string(task.Status), TextDraft: draft}
	if cloudAgentTaskTerminal(task.Status) {
		view.Result = json.RawMessage(task.ResultJSON)
		view.Error = task.Error
	}
	return view, nil
}
