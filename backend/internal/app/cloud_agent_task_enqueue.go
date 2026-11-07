package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// cloudAgentTaskEnqueue carries one task submission from admission through the
// checkpoint transaction. Admission (CreateTask, input protection, context
// pressure) runs outside storageMu; the billed insert, canvas registration,
// leases and runtime bookkeeping commit in one MutateCloudAgent.
type cloudAgentTaskEnqueue struct {
	run              *model.CloudAgentExecution
	state            *cloudAgentRuntime
	req              CreateTaskRequest
	media            *cloudAgentMediaPlan
	prepared         *cloudAgentPreparedMedia
	approvalID       string
	generationID     string
	prepare          *creationTaskPreparation
	task             *model.Task
	input            map[string]any
	contextPressure  *cloudAgentContextPressure
	requestCanonical canonicalAgentRequest
	policy           RuntimePolicySetting
	budget           *repository.BudgetAdmission
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
	if holdbacks, err := s.cloudAgentSubagentBudgetHoldbacks(state); err != nil {
		return err
	} else {
		remaining -= holdbacks
	}
	if remaining < 0 {
		return s.failCloudAgent(run, state, "Agent 累计预算已耗尽")
	}
	enqueue := &cloudAgentTaskEnqueue{run: run, state: state, req: req, media: media}
	// done=true means "stop and return err": err is nil when a failure was
	// already recorded as the tool result, exactly as the original early returns.
	if media != nil {
		if done, err := s.prepareCloudAgentTaskEnqueueMedia(enqueue); done {
			return err
		}
	}
	if done, err := s.admitCloudAgentTaskEnqueue(enqueue, remaining); done {
		return err
	}
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	err = s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		return enqueue.commit(current, repo)
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

// prepareCloudAgentTaskEnqueueMedia resolves the approved or auto-prepared
// generation and replaces request references with the prepared, leased ones.
func (s *Service) prepareCloudAgentTaskEnqueueMedia(enqueue *cloudAgentTaskEnqueue) (bool, error) {
	run, state, media := enqueue.run, enqueue.state, enqueue.media
	prepared := media.Prepared
	if prepared == nil && state.Approval != nil {
		prepared = state.Approval.Prepared
		if state.Approval != nil {
			enqueue.approvalID = state.Approval.ID
		}
	}
	if prepared == nil {
		return true, s.cloudAgentMediaError(run, state, "admission", false, false, creationConflict("缺少已批准的生成准备态，请重新申请审批；未提交任务"))
	}
	enqueue.prepared = prepared
	enqueue.generationID = prepared.GenerationID
	refs, refErr := cloudAgentPreparedReferences(s.repo, run.UserID, prepared)
	if refErr != nil {
		return true, s.cloudAgentMediaError(run, state, "admission", false, false, refErr)
	}
	if enqueue.req.Input == nil {
		enqueue.req.Input = map[string]any{}
	}
	for _, field := range []string{"referenceImages", "referenceVideos", "referenceAudios"} {
		delete(enqueue.req.Input, field)
	}
	for field, value := range refs {
		enqueue.req.Input[field] = value
	}
	return false, nil
}

// admitCloudAgentTaskEnqueue creates the task under the remaining budget,
// protects its secrets and computes the model step's context pressure.
func (s *Service) admitCloudAgentTaskEnqueue(enqueue *cloudAgentTaskEnqueue, remaining int64) (bool, error) {
	run, state, media := enqueue.run, enqueue.state, enqueue.media
	enqueue.prepare = &creationTaskPreparation{}
	enqueue.req.admission = &taskAdmission{ID: cloudAgentID(run.UserID, fmt.Sprintf("%s:task:%d", run.ID, len(state.TaskIDs))), MaxCharge: remaining, AgentRunID: run.ID, GenerationID: enqueue.generationID, ApprovalID: enqueue.approvalID}
	enqueue.req.creationPrepare = enqueue.prepare
	task, err := s.CreateTask(run.UserID, enqueue.req)
	if err != nil {
		return true, s.cloudAgentTaskCreateFailure(enqueue, err)
	}
	enqueue.task = task
	if done, err := s.protectCloudAgentTaskInput(enqueue); done {
		return true, err
	}
	// 压力读数只对"模型调用"这一步有意义：媒体任务发的是另一份请求（另一套信封），
	// 把读数挂到那份信封上会误导消费方；压缩任务同理由 enqueueCloudAgentContextCompaction
	// 自己记账。
	// requestCanonical 是"实际要发出去的那份信封"（上游 #601 新增）：定锚点、算口径指纹与
	// 占用分布都要对着它，而不是内存里的运行态副本。
	if media == nil && enqueue.req.Operation != cloudAgentContextCompactionOperation {
		canonical, ok := canonicalAgentRequestFromInput(enqueue.input)
		if !ok {
			// 上游的硬失败：模型调用这一步必须有可计量的信封，缺了就如实报错，
			// 不要退回状态副本编一份读数出来。
			return true, fmt.Errorf("Agent 模型任务缺少可计量的请求信封")
		}
		enqueue.requestCanonical = canonical
		value := s.cloudAgentContextPressure(canonical, state.Request.Prompt, state.Request)
		enqueue.contextPressure = &value
	}
	if enqueue.prepare.Order != nil {
		task.BillingOrderID = enqueue.prepare.Order.ID
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		if media != nil {
			return true, s.cloudAgentMediaError(run, state, "admission", false, false, err)
		}
		return true, err
	}
	enqueue.policy = policy
	if enqueue.state.Execution != nil && enqueue.state.Execution.Binding != nil {
		budget, budgetErr := s.budgetAdmission(run.UserID, enqueue.state.Execution.Binding.BudgetGrantID, enqueue.prepare.Order)
		if budgetErr != nil {
			return true, budgetErr
		}
		enqueue.budget = budget
	}
	return false, nil
}

func (s *Service) cloudAgentTaskCreateFailure(enqueue *cloudAgentTaskEnqueue, err error) error {
	run, state := enqueue.run, enqueue.state
	if enqueue.media != nil {
		return s.cloudAgentMediaError(run, state, "admission", false, false, err)
	}
	if cloudAgentAwaitingFirstStep(state) {
		// 首个模型步的准入失败发生在换单之前，占位预留还在账上：终态与退款必须同一个事务，
		// 否则用户看到"失败"时这一轮的钱还冻着，要等下一次收尾排空才释放。
		// 换单事务里的失败由 PiModelStep 的同一处理兜底。
		if failErr := s.failCloudAgentFirstStepAdmission(run, state, err); failErr != nil && !errors.Is(failErr, repository.ErrCreationConflict) {
			log.Printf("agent first-step admission refund: run=%s %v", run.ID, failErr)
		}
		return err
	}
	return s.failCloudAgent(run, state, cloudAgentSafeToolError(err))
}

// protectCloudAgentTaskInput decodes the created task input, checks resolved
// media options and re-encodes the input with task secrets protected.
func (s *Service) protectCloudAgentTaskInput(enqueue *cloudAgentTaskEnqueue) (bool, error) {
	run, state, media, task := enqueue.run, enqueue.state, enqueue.media, enqueue.task
	var input map[string]any
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		if media != nil {
			return true, s.cloudAgentMediaError(run, state, "admission", false, false, err)
		}
		return true, err
	}
	enqueue.input = input
	if media != nil {
		requested, _ := enqueue.req.Input["config"].(map[string]any)
		resolved, _ := input["config"].(map[string]any)
		if err := validateCloudAgentResolvedMediaOptions(requested, resolved); err != nil {
			return true, s.cloudAgentMediaError(run, state, "admission", false, false, err)
		}
	}
	if err := s.protectTaskSecrets(input); err != nil {
		if media != nil {
			return true, s.cloudAgentMediaError(run, state, "admission", false, false, err)
		}
		return true, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		if media != nil {
			return true, s.cloudAgentMediaError(run, state, "admission", false, false, err)
		}
		return true, err
	}
	task.InputJSON = string(raw)
	return false, nil
}

// commit runs inside the checkpoint transaction: billed insert (or first-step
// holding swap), canvas registration, resource leases and runtime state.
func (enqueue *cloudAgentTaskEnqueue) commit(current *model.CloudAgentExecution, repo *repository.Repository) error {
	run, state, media, task := enqueue.run, enqueue.state, enqueue.media, enqueue.task
	if media != nil {
		canvas, err := repo.CanvasProjectForUser(run.UserID, state.Request.CanvasID)
		if err != nil {
			return err
		}
		doc, err := creationDocument(canvas.PayloadJSON)
		if err != nil {
			return err
		}
		if err := validateCloudAgentPreparedAdmission(repo, run.UserID, enqueue.prepared, task, enqueue.prepare.Order, doc, media.Args); err != nil {
			return err
		}
	}
	if err := enqueue.insertTask(repo); err != nil {
		return err
	}
	if media != nil {
		if err := enqueue.registerMediaTask(repo); err != nil {
			return err
		}
	}
	state.TaskIDs = append(state.TaskIDs, task.ID)
	if media != nil {
		enqueue.recordMediaSubmission()
	} else {
		enqueue.recordModelTask()
	}
	return cloudAgentSave(current, state)
}

func (enqueue *cloudAgentTaskEnqueue) insertTask(repo *repository.Repository) error {
	run, state, task := enqueue.run, enqueue.state, enqueue.task
	// 只有真正的首个**模型步**才允许换掉占位预留并推进相位。压缩摘要等内部任务
	// 也走这条入队路径：若把它们当成首步，占位预留会被消耗、相位被清空，而
	// 首步合同（AssembledPromptHash）从未冻结——压缩后的续跑随后会以
	// "first-step contract snapshot is missing" 拒绝（真实 3000 部署验收复现）。
	if cloudAgentAwaitingFirstStep(state) && enqueue.req.Operation == cloudAgentStepOperation {
		// 首个模型步：把"建 run 时的占位预留"换成"真实首步报价"。
		//
		// 占位订单号必须从**本事务里**的占位行读出来，而不是用内存副本：
		// 副本可能落后于别人已经改过的账，按过期事实退款正是这一步要避免的事。
		placeholder, err := cloudAgentHoldingTaskFrom(repo, run.UserID, run.ID, state.PlaceholderTaskID)
		if err != nil {
			return err
		}
		if err := repo.SwapCloudAgentHoldingForFirstStep(task, enqueue.prepare.Order, placeholder.BillingOrderID, enqueue.policy.Task.ActiveTaskLimit); err != nil {
			return err
		}
		// 阶段推进与首步任务同事务提交：崩在中间会留下"任务已在队列、运行还在等首步"，
		// 下一次投递就会再建一遍 —— 那正是重复计费。
		state.Phase = ""
		return nil
	}
	if enqueue.budget != nil {
		return createTaskUnderBudgetRepository(repo, task, enqueue.prepare.Order, enqueue.policy, enqueue.budget)
	}
	return createTaskWithStorageQuotaRepository(repo, task, enqueue.prepare.Order, enqueue.policy)
}

// registerMediaTask binds the billed task to the canvas draft, emits the canvas
// change and moves (approval) or attaches (auto) the reference resource leases.
func (enqueue *cloudAgentTaskEnqueue) registerMediaTask(repo *repository.Repository) error {
	run, state, media, task, prepared := enqueue.run, enqueue.state, enqueue.media, enqueue.task, enqueue.prepared
	before, err := repo.CanvasProjectForUser(run.UserID, state.Request.CanvasID)
	if err != nil {
		return err
	}
	if err := registerCloudAgentStoryboardTask(repo, run, state.Request.CanvasID, media, task); err != nil {
		return err
	}
	if err := emitCloudAgentCanvasChange(repo, run.ID, state, cloudAgentMutationInput{UserID: run.UserID, CanvasID: state.Request.CanvasID, BeforeJSON: before.PayloadJSON, Operation: "generate_media_submit"}); err != nil {
		return err
	}
	if prepared != nil {
		if enqueue.approvalID != "" {
			if err := repo.TransferCloudAgentResourceLeases(run.UserID, enqueue.approvalID, "task:"+task.ID, task.ID, prepared.Quote.ExpiresAt); err != nil {
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
	// The prepared auto admission is single-use. Once the billed task and
	// draft node are committed, a later retry must observe the task state,
	// not reuse the old quote or create a second task.
	state.AutoPreparedMedia = nil
	state.AutoPreparedCallHash = ""
	return nil
}

func (enqueue *cloudAgentTaskEnqueue) recordMediaSubmission() {
	run, state, media, task := enqueue.run, enqueue.state, enqueue.media, enqueue.task
	// MediaTaskID 只记最近一次提交的生成，供诊断与取消事件引用；不再把运行钉在这次调用上。
	state.MediaTaskID = task.ID
	state.Generations++
	state.VideoSeconds += media.Args.Duration
	toolName := "generate_media"
	if state.CallIndex >= 0 && state.CallIndex < len(state.Calls) && state.Calls[state.CallIndex].Function.Name != "" {
		toolName = state.Calls[state.CallIndex].Function.Name
	}
	state.event(run.ID, "generation_task_created", map[string]any{"toolName": toolName, "taskId": task.ID, "nodeId": media.Args.NodeID, "title": media.Args.Title, "mode": media.Args.Mode, "canvasId": state.Request.CanvasID, "referenceNodeIds": media.Args.ReferenceNodeIDs, "text": "媒体节点与引用连线已创建，生成任务已提交"})
	// 提交即返回：生成在后台进行，结束时由 settleCloudAgentPendingGenerations 回写并通知。
	pending := cloudAgentPendingGeneration{TaskID: task.ID, NodeID: media.Args.NodeID, Mode: media.Args.Mode, Title: media.Args.Title, SubmittedAt: time.Now().UTC()}
	if state.CallIndex >= 0 && state.CallIndex < len(state.Calls) {
		pending.CallID = state.Calls[state.CallIndex].ID
	}
	state.PendingGenerations = append(state.PendingGenerations, pending)
	if state.CallIndex >= 0 && state.CallIndex < len(state.Calls) {
		cloudAgentToolResult(run.ID, state, state.Calls[state.CallIndex], cloudAgentGenerationSubmittedReceipt(pending, len(state.PendingGenerations)), nil)
	}
}

func (enqueue *cloudAgentTaskEnqueue) recordModelTask() {
	state, task := enqueue.state, enqueue.task
	state.ActiveTaskID = task.ID
	// A model task owns exactly one cumulative draft. Clear the prior
	// task before the first publisher CAS so snapshots cannot leak text
	// from the preceding model step.
	state.ActiveTextDraft = ""
	// 压缩调用不是本轮的一步：压完还要用压缩后的上下文继续步进，步数不该被它占掉。
	if state.ContextCompaction != nil && enqueue.req.Operation == cloudAgentContextCompactionOperation {
		state.ContextCompaction.Status = "running"
		if state.ContextCompaction.PiOperationID != "" {
			state.ContextCompaction.PiTaskID = task.ID
			if native := state.ContextCompaction.PiNative; native != nil && native.ActiveCall != "" {
				call := native.Calls[native.ActiveCall]
				call.TaskID = task.ID
				native.Calls[native.ActiveCall] = call
			}
		}
		return
	}
	if enqueue.contextPressure != nil {
		enqueue.recordContextPressure()
	}
	// 记下发出去这份 canonical 的本地计价：任务回来时用它和上游实测配成锚点。
	state.LastStepTaskID = task.ID
	state.LastStepOperation = enqueue.req.Operation
	state.Step++
}

func (enqueue *cloudAgentTaskEnqueue) recordContextPressure() {
	run, state, task, requestCanonical, contextPressure := enqueue.run, enqueue.state, enqueue.task, enqueue.requestCanonical, enqueue.contextPressure
	if len(state.AdvertisedToolNames) == 0 {
		state.AdvertisedToolNames = cloudAgentToolNames(requestCanonical.Tools)
	}
	// 口径变化就地作废锚点（上游 #601）：这一步的模型/线路/窗口取自任务 input，
	// 与定锚时不一致的实测不再可比。我方 recordCloudAgentTokenAnchor 里还会兜一遍
	// （这一步拿不到新实测时也必须先把过期锚点作废）。
	config, _ := enqueue.input["config"].(map[string]any)
	model, channelID := stringValue(config["model"]), stringValue(config["channelId"])
	signature := cloudAgentRequestSignature(state, requestCanonical, channelID, model)
	cloudAgentExpireTokenAnchorForRequest(run.ID, state, contextPressure.ContextWindowTokens, signature, model, channelID)
	// Node 拼在策略之后的工作区文件要单独登记，否则它们会被
	// 算成"其它"：那是排查时最想看到具体来源的一截。
	cloudAgentRecordHarnessSegments(&state.Policy, cloudAgentFrozenHarness(state.Snapshot), requestCanonical.SystemPrompt)
	// requestId 把读数钉在"即将发出的这次请求"上（设计 §4）：没有它，前端只能猜
	// 哪个数字属于哪一次调用。占用分布对着实际信封（requestCanonical）算。
	payload := cloudAgentContextPressurePayload(*contextPressure, state, requestCanonical)
	payload["requestId"] = task.ID
	// 冻结实际信封的占用分布：after_request 没有信封可传，
	// 只能复用这一步准入时算好的那一份。
	if breakdown, ok := payload["breakdown"].(map[string]any); ok {
		state.LastStepBreakdown = breakdown
	}
	cloudAgentNoteContextWindowResolved(run.ID, state, *contextPressure)
	state.event(run.ID, "context_pressure", payload)
	state.LastStepEstimate = contextPressure.EstimatedInputTokens
	state.LastStepPressure = contextPressure
	state.LastStepSourceBytes = contextPressure.SourceBytes
	// 上游 #601：把"发出去的那份信封"的口径与窗口一起记账，
	// 供后面的锚点作废比对（换模型/换线路/换窗口）。
	state.LastStepSignature = signature
	state.LastStepModel = model
	state.LastStepChannelID = channelID
	state.LastStepWindowTokens = contextPressure.ContextWindowTokens
	state.cloudAgentRecordImageDeliverySHA(requestCanonical)
}
