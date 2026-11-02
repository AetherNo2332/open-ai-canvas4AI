package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

type PiRuntimePhaseRequest struct {
	Phase  string `json:"phase"`
	Kind   string `json:"kind"`
	WaitID string `json:"waitId"`
	Reason string `json:"reason"`
}

// cloudAgentToolOperationTimeout 是单次工具操作（不含审批与 wait_generations 挂起）
// 的护栏超时：超过就补失败回执并结束 op，不制造"永远 Pending"的黑洞。初值放宽到
// 5 分钟，配合 advance 耗时日志观察后再收紧。
const cloudAgentToolOperationTimeout = 5 * time.Minute

// cloudAgentAppendOperationEvent 追加一条 op 生命周期事件（tool_pending 等）：
// 独立于工具回执，不推进 CallIndex、不写 canonical 消息；best-effort，两次 CAS 失败即放弃。
func (s *Service) cloudAgentAppendOperationEvent(userID, runID, kind string, payload map[string]any) {
	for attempt := 0; attempt < 2; attempt++ {
		run, err := s.repo.CloudAgent(userID, runID)
		if err != nil || cloudAgentRunTerminal(run.Status) {
			return
		}
		if err := s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			state, err := cloudAgentDecode(current)
			if err != nil {
				return err
			}
			state.event(runID, kind, payload)
			return cloudAgentSave(current, &state)
		}); err == nil {
			return
		}
	}
}

// recordCloudAgentOperationTimeoutReceipt 在 op 失败收尾时补一条 tool_failed 回执：
// 仅当该调用仍在本批待执行队列（callIndex 命中且无回执）时才写，返回是否补写了。
// 这是"op 标 succeeded 当且仅当回执已持久化"不变量在取消/超时路径的兜底。
// 完成的运行不补写（模型已离场，回执只落后于事件流）；取消/失败/仍存活才补。
func (s *Service) recordCloudAgentOperationTimeoutReceipt(op *model.AgentToolOperation) bool {
	run, err := s.repo.CloudAgent(op.UserID, op.RunID)
	if err != nil {
		return false
	}
	switch {
	case cloudAgentRunTerminal(run.Status) && run.Status != "cancelled" && run.Status != "failed":
		return false
	case run.Status == "waiting_approval":
		return false
	}
	written := false
	_ = s.repo.MutateCloudAgent(op.UserID, op.RunID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		if state.PiToolBatchTaskID != op.TaskID || state.CallIndex >= len(state.Calls) || state.Calls[state.CallIndex].ID != op.CallID {
			// 批次已推进或有别的执行者落了回执：不重复写。
			return &cloudAgentReceiptExistsError{}
		}
		if receipt := piToolReceipt(&state, op.TaskID, op.CallID); receipt != nil {
			return &cloudAgentReceiptExistsError{}
		}
		cloudAgentRecordToolResult(current, &state, state.Calls[state.CallIndex], nil, BadAuthRequest("工具执行超时：本次调用未完成，如需继续请重新提交"))
		if err := cloudAgentSave(current, &state); err != nil {
			return err
		}
		written = true
		return nil
	})
	return written
}

// cloudAgentReceiptExistsError 表示目标调用已经有回执，不重复写失败收尾。
type cloudAgentReceiptExistsError struct{}

func (e *cloudAgentReceiptExistsError) Error() string { return "tool receipt already exists" }

func (s *Service) PiRuntimePhase(userID, runID, owner string, input PiRuntimePhaseRequest) error {
	run, err := s.piAgentLeaseRow(userID, runID, owner, false)
	if err != nil {
		return err
	}
	if run.Status == "waiting_approval" && input.Phase == "waiting_tool" {
		input.Phase = "waiting_approval"
		input.Kind = "approval"
		input.Reason = "等待用户审批"
		var state struct {
			Approval *struct {
				ID string `json:"id"`
			} `json:"approval"`
		}
		if json.Unmarshal([]byte(run.StateJSON), &state) == nil && state.Approval != nil {
			input.WaitID = state.Approval.ID
		}
	}
	switch input.Phase {
	case "ready", "advancing", "waiting_model", "waiting_tool", "waiting_approval", "waiting_compaction", "waiting_resource", "retry_delay":
	default:
		return BadAuthRequest("Invalid Agent runtime phase")
	}
	if len(input.Kind) > 32 || len(input.WaitID) > 240 || len(input.Reason) > 160 {
		return BadAuthRequest("Invalid Agent wait reference")
	}
	workerID, epoch, _, err := parsePiAgentLeaseOwner(owner)
	if err != nil {
		return err
	}
	if err := s.repo.SetAgentPhase(userID, runID, workerID, epoch, input.Phase, input.Kind, input.WaitID, input.Reason); err != nil {
		if errors.Is(err, repository.ErrCreationConflict) {
			return kernel.AgentLeaseLost("Agent runtime phase lease conflict")
		}
		return err
	}
	return nil
}

func (s *Service) PiWakeEvents(after int64) ([]model.AgentWakeEvent, error) {
	return s.repo.AgentWakeEvents(after, 128)
}

func (s *Service) PiWakeEventsForWorker(after int64, userScope string) ([]model.AgentWakeEvent, error) {
	return s.repo.AgentWakeEvents(after, 128, userScope)
}

func (s *Service) PiCapacity(owner string, active, capacity int) error {
	if owner == "" || len(owner) > 80 || active < 0 || capacity < 1 || capacity > 64 || active > capacity {
		return BadAuthRequest("Invalid Agent capacity")
	}
	return s.repo.AgentCapacity(owner, active, capacity)
}

func (s *Service) PiControl(userID, runID, owner string) (map[string]any, error) {
	run, err := s.piAgentLeaseRow(userID, runID, owner, false)
	if err != nil {
		if terminalRun, terminal := s.piTerminalRunAfterLeaseFailure(userID, runID, owner); terminal {
			return map[string]any{"status": terminalRun.Status}, nil
		}
		return nil, err
	}
	var control struct {
		PendingInterjections []cloudAgentInterjection     `json:"pendingInterjections"`
		ContextCompaction    *cloudAgentContextCompaction `json:"contextCompaction"`
	}
	if err := json.Unmarshal([]byte(run.StateJSON), &control); err != nil {
		return nil, err
	}
	pending := []PiPendingInterjection{}
	for _, item := range control.PendingInterjections {
		pending = append(pending, PiPendingInterjection{ID: item.ID, Text: item.Text, Source: item.Source, CreatedAt: item.CreatedAt})
	}
	var compaction *PiPendingContextCompaction
	if c := control.ContextCompaction; c != nil && c.PiOperationID != "" {
		compaction = &PiPendingContextCompaction{OperationID: c.PiOperationID, SessionRevision: c.PiSessionRevision, ActiveLeafID: c.PiSourceLeafID, Reason: c.PiReason, WillRetry: c.PiWillRetry, TokensBefore: c.PiTokensBefore}
	}
	return map[string]any{"status": run.Status, "pendingInterjections": pending, "pendingContextCompaction": compaction}, nil
}

func (s *Service) PiRelease(userID, runID, owner string) error {
	if _, err := s.piAgentLeasedRun(userID, runID, owner); err != nil {
		return err
	}
	workerID, epoch, _, err := parsePiAgentLeaseOwner(owner)
	if err != nil {
		return err
	}
	return s.repo.ReleasePiAgentLease(userID, runID, workerID, epoch)
}

func (s *Service) PiToolAdvanceAsync(userID, runID, owner, taskID, callID string) (*PiToolReceipt, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		if _, terminal := s.piTerminalRunAfterLeaseFailure(userID, runID, owner); terminal {
			return &PiToolReceipt{CallID: callID, Terminated: true}, nil
		}
		return nil, err
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return nil, err
	}
	if taskID == "" || state.PiToolBatchTaskID != taskID || !piToolBatchContainsCall(state.Calls, callID) {
		return nil, kernel.Forbidden("Invalid Agent tool operation identity")
	}
	if receipt := piToolReceipt(&state, taskID, callID); receipt != nil {
		return receipt, nil
	}
	if cloudAgentRunTerminal(run.Status) {
		return &PiToolReceipt{CallID: callID, Terminated: true}, nil
	}
	if state.CallIndex >= len(state.Calls) || state.Calls[state.CallIndex].ID != callID {
		return nil, kernel.Forbidden("Invalid Agent tool operation order")
	}
	if (run.WaitKind == "subagents" || run.WaitKind == cloudAgentGenerationsWaitKind) && run.WaitID == callID {
		return &PiToolReceipt{CallID: callID, Pending: true, Suspended: true}, nil
	}
	operationID := fmt.Sprintf("ato_%x", sha256.Sum256([]byte(runID+"\x00"+taskID+"\x00"+callID)))
	operation := model.AgentToolOperation{ID: operationID, UserID: userID, RunID: runID, TaskID: taskID, CallID: callID, Status: "queued"}
	if err := s.repo.PrepareAgentToolOperation(&operation); err != nil {
		return nil, err
	}
	if operation.Status == "failed" {
		return nil, kernel.BadAuthRequest("Agent 工具执行失败：" + operation.Error)
	}
	// 入队即给前端一条可见事件；回执仍由真正的执行产生，这里只是"已接单"。
	pendingCall := state.Calls[state.CallIndex]
	s.cloudAgentAppendOperationEvent(userID, runID, "tool_pending", map[string]any{
		"toolName": pendingCall.Function.Name, "callId": callID, "operationId": operationID,
		"queuedAt": time.Now().UTC().Format(time.RFC3339), "text": "工具已接单，正在后台执行",
	})
	s.wakeAgentTools()
	return &PiToolReceipt{CallID: callID, Pending: true, OperationID: operationID}, nil
}

// finishAgentToolOperation 记录 op 终态；只允许每个 op 调用一次。
func (s *Service) finishAgentToolOperation(op *model.AgentToolOperation, owner, status, message string) {
	log.Printf("agent tool operation finish: op=%s run=%s call=%s status=%s queued=%s message=%q", op.ID, op.RunID, op.CallID, status, time.Since(op.CreatedAt), message)
	if finishErr := s.repo.FinishAgentToolOperation(*op, owner, status, message); finishErr != nil {
		log.Printf("agent tool completion: %v", finishErr)
	}
}

// runAgentToolOperationTask 执行一次已 claim 的工具 op：预检运行/租约/超时，
// 通过后推进工具调用并按不变量收尾（标 succeeded 当且仅当回执已持久化）。
func (s *Service) runAgentToolOperationTask(op *model.AgentToolOperation, owner string) {
	// markFailed 先补写失败回执（已有回执时不重复写）再收尾。
	markFailed := func(message string) {
		if s.recordCloudAgentOperationTimeoutReceipt(op) {
			message = message + "；已补写失败回执"
		}
		s.finishAgentToolOperation(op, owner, "failed", message)
	}
	current, err := s.repo.CloudAgent(op.UserID, op.RunID)
	if err != nil {
		// 读取失败不能记成"执行过"：保持待执行，允许接管重试。
		s.finishAgentToolOperation(op, owner, "pending", "工具执行暂时失败，等待恢复")
		return
	}
	if cloudAgentRunTerminal(current.Status) {
		// 运行已取消/失败：入队前已有回执的调用会被 PiToolAdvanceAsync 直接回放，
		// 能走到这里的 op 还没有回执，必须失败收尾而不是记成功。
		markFailed("运行已结束，本次调用没有产生回执")
		return
	}
	session, _, loadErr := s.repo.CloudAgentPiSession(op.UserID, firstNonEmpty(current.ConversationID, current.ID))
	if loadErr != nil {
		s.finishAgentToolOperation(op, owner, "pending", "工具执行暂时失败，等待恢复")
		return
	}
	if current.LeaseOwner == "" || session.LeaseExpiresAt == nil || session.LeaseExpiresAt.Before(time.Now()) {
		s.finishAgentToolOperation(op, owner, "pending", "租约暂不可用，等待恢复")
		return
	}
	// 等待审批/挂起等待的操作允许长时间驻留；其他调用超过护栏超时
	// 就补一条失败回执并结束 op，避免"永远 Pending"的执行黑洞。
	wait := current.WaitKind == "subagents" || current.WaitKind == cloudAgentGenerationsWaitKind
	if !wait && time.Since(op.CreatedAt) > cloudAgentToolOperationTimeout {
		markFailed("工具执行超时")
		return
	}
	receipt, advanceErr := s.advanceAgentToolOperation(op, owner, current, session.LeaseEpoch)
	status, message := agentToolOperationOutcome(receipt, advanceErr)
	if status == "failed" {
		markFailed(message)
		return
	}
	s.finishAgentToolOperation(op, owner, status, message)
}

// agentToolOperationOutcome 把 advance 结果归类为 op 终态：
// pending 等待下轮、failed 无真实回执或确定性错误、其余 succeeded。
func agentToolOperationOutcome(receipt *PiToolReceipt, advanceErr error) (string, string) {
	switch {
	case receipt != nil && receipt.Pending:
		return "pending", ""
	case receipt != nil && receipt.Terminated && receipt.Result == nil:
		// 终态且没有真实回执：同"运行已结束"的处理，不能当成功。
		return "failed", "运行已结束，本次调用没有产生回执"
	case advanceErr != nil:
		// 租约/版本冲突可恢复；确定性错误写 op 失败，回执仍由下次读取可见。
		var appError *kernel.AppError
		if errors.As(advanceErr, &appError) && appError.Status >= 400 && appError.Status < 500 && appError.Status != 409 && appError.Status != 429 && appError.Reason != kernel.ReasonAgentLeaseLost {
			return "failed", appError.Message
		}
		return "pending", "工具执行暂时失败，等待恢复"
	}
	return "succeeded", ""
}

// advanceAgentToolOperation 续租并推进一次工具调用，返回回执与错误供终态判定。
func (s *Service) advanceAgentToolOperation(op *model.AgentToolOperation, owner string, run *model.CloudAgentExecution, epoch int64) (*PiToolReceipt, error) {
	done := make(chan struct{})
	go func() {
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				_ = s.repo.RenewAgentToolOperation(op.ID, owner)
			}
		}
	}()
	started := time.Now()
	receipt, advanceErr := s.PiToolAdvance(run.UserID, run.ID, fmt.Sprintf("%s@%d", run.LeaseOwner, epoch), op.TaskID, op.CallID)
	close(done)
	log.Printf("agent tool operation advance: op=%s run=%s call=%s tool=%s duration=%s err=%v pending=%t terminated=%t",
		op.ID, run.ID, op.CallID, op.CallID, time.Since(started), advanceErr, receipt != nil && receipt.Pending, receipt != nil && receipt.Terminated)
	return receipt, advanceErr
}

func (s *Service) wakeAgentTools() {
	if s == nil || s.agentToolWake == nil {
		return
	}
	select {
	case s.agentToolWake <- struct{}{}:
	default:
	}
}

func (s *Service) startAgentToolOperations() {
	s.runWorkerLoop(func(ctx context.Context) {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		slots := make(chan struct{}, 16)
		for {
			if ctx.Err() != nil {
				return
			}
			if !s.IsDraining() {
				operations, err := s.repo.AgentToolOperations(64)
				if err != nil {
					log.Printf("agent tool recovery: %v", err)
				}
				for _, operation := range operations {
					if len(slots) >= cap(slots) {
						break
					}
					// Approval decisions wake this operation through durable recovery without
					// holding a tool worker while a human is deciding.
					run, err := s.repo.CloudAgent(operation.UserID, operation.RunID)
					if err != nil {
						continue
					}
					if run.Status == "waiting_approval" {
						continue
					}
					owner := s.workerID + ":" + newID()
					claimed, err := s.repo.ClaimAgentToolOperation(operation.ID, owner)
					if err != nil || !claimed {
						continue
					}
					slots <- struct{}{}
					op := operation
					if !s.runWorkerTask(func() {
						defer func() { <-slots }()
						s.runAgentToolOperationTask(&op, owner)
					}) {
						<-slots
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-s.agentToolWake:
			}
		}
	})
}
