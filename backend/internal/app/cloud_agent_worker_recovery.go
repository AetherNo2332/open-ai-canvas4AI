package app

import (
	"encoding/json"
	"fmt"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"math/rand/v2"
	"strings"
	"time"
)

type PiWorkerRecoveryRequest struct {
	Revision     int64  `json:"revision"`
	Class        string `json:"class"`
	Operation    string `json:"operation"`
	TaskID       string `json:"taskId,omitempty"`
	CallID       string `json:"callId,omitempty"`
	HTTPStatus   int    `json:"httpStatus,omitempty"`
	RetryAfterMS int64  `json:"retryAfterMs,omitempty"`
}
type PiWorkerRecoveryView struct {
	Status         string     `json:"status"`
	Attempts       int        `json:"attempts"`
	NextRecoveryAt *time.Time `json:"nextRecoveryAt,omitempty"`
}

func workerRecoveryView(run *model.CloudAgentExecution) *PiWorkerRecoveryView {
	return &PiWorkerRecoveryView{run.RecoveryStatus, run.RecoveryAttempts, run.NextRecoveryAt}
}

func recoveryBusinessIdentity(taskID, callID string) string {
	if callID != "" {
		return "call:" + callID
	}
	if taskID != "" {
		return "task:" + taskID
	}
	return "run"
}

// Upgrade valid legacy checkpoints through the existing transcript writer.
// Damaged checkpoints still use the control-only terminal path.
func (s *Service) mutatePiControl(userID, runID string, revision int64, fn func(*model.CloudAgentExecution, *repository.Repository) error) error {
	row, err := s.repo.CloudAgentControlRow(userID, runID)
	if err != nil {
		return err
	}
	_, legacyDecodeErr := cloudAgentDecode(row)
	if row.CheckpointVersion < model.CloudAgentCheckpointVersion && legacyDecodeErr == nil {
		return s.repo.MutateCloudAgent(userID, runID, revision, func(run *model.CloudAgentExecution, repo *repository.Repository) error {
			state, err := cloudAgentDecode(run)
			if err != nil {
				return err
			}
			if err = cloudAgentSave(run, &state); err != nil {
				return err
			}
			return fn(run, repo)
		})
	}
	return s.repo.MutateCloudAgentControl(userID, runID, revision, fn)
}
func appendWorkerEvent(run *model.CloudAgentExecution, kind string, payload map[string]any) error {
	sequence := run.EventCount + 1
	event := CloudAgentEvent{EventID: fmt.Sprintf("%s:%d", run.ID, sequence), RunID: run.ID, Seq: sequence, Type: kind, Payload: payload, CreatedAt: time.Now().UTC()}
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	run.Journal = append(run.Journal, model.CloudAgentEventRecord{RunID: run.ID, UserID: run.UserID, Sequence: sequence, EventJSON: string(body), CreatedAt: event.CreatedAt})
	run.EventCount = sequence
	return nil
}
func failWorkerRecovery(run *model.CloudAgentExecution, reason, message string) error {
	run.Status = "failed"
	run.CleanupPending = true
	run.FailureMessage = message
	run.RecoveryStatus = "exhausted"
	run.NextRecoveryAt = nil
	if reason == "pi_worker_result_unknown" {
		run.RecoveryStatus = "needs_review"
	}
	if err := appendWorkerEvent(run, "worker_recovery_exhausted", map[string]any{"reason": reason, "attempts": run.RecoveryAttempts}); err != nil {
		return err
	}
	return appendWorkerEvent(run, "run_failed", map[string]any{"text": message, "reason": reason})
}

func (s *Service) PiWorkerRecovery(userID, runID, owner string, input PiWorkerRecoveryRequest) (*PiWorkerRecoveryView, error) {
	switch input.Class {
	case "network", "http_5xx", "timeout", "rate_limited", "conflict", "result_unknown":
	default:
		return nil, BadAuthRequest("未知 worker 恢复分类")
	}
	switch input.Operation {
	case "snapshot", "control", "checkpoint", "model", "tool", "phase", "renew", "compaction":
	default:
		return nil, BadAuthRequest("未知 worker 恢复操作")
	}
	if input.Revision < 1 || input.RetryAfterMS < 0 || input.RetryAfterMS > 24*60*60*1000 || len(input.TaskID) > 80 || len(input.CallID) > 160 {
		return nil, BadAuthRequest("无效 worker 恢复参数")
	}
	worker, epoch, hasEpoch, err := parsePiAgentLeaseOwner(owner)
	if err != nil || !hasEpoch {
		return nil, kernel.AgentLeaseLost("恢复上报必须携带租约 epoch")
	}
	run, err := s.repo.CloudAgentControlRow(userID, runID)
	if err != nil {
		return nil, err
	}
	session, err := s.repo.CloudAgentPiLease(userID, firstNonEmpty(run.ConversationID, run.ID))
	if err != nil || session.LeaseEpoch != epoch || run.LeaseOwner != worker || run.Engine != "pi" {
		return nil, kernel.AgentLeaseLost("恢复上报租约已失效")
	}
	identity := strings.Join([]string{input.Operation, input.TaskID, input.CallID}, ":")
	if run.RecoveryLastEpoch == epoch && run.RecoveryOperationID == identity && run.RecoveryClass == input.Class {
		return workerRecoveryView(run), nil
	}
	if cloudAgentRunTerminal(run.Status) {
		return workerRecoveryView(run), nil
	}
	if _, err = s.piAgentLeaseRow(userID, runID, owner, false); err != nil {
		return nil, err
	}
	if run.Revision != input.Revision {
		return nil, kernel.NewAppError(409, "恢复上报 revision 已变化，请重新读取")
	}
	if input.TaskID != "" && input.TaskID != run.ActiveTaskID {
		hydrated, readErr := s.repo.CloudAgent(userID, runID)
		if readErr != nil {
			return nil, readErr
		}
		state, decodeErr := cloudAgentDecode(hydrated)
		if decodeErr != nil || input.TaskID != state.LastStepTaskID {
			return nil, BadAuthRequest("恢复任务与本轮步骤不匹配")
		}
	}
	if input.CallID != "" {
		hydrated, readErr := s.repo.CloudAgent(userID, runID)
		if readErr != nil {
			return nil, readErr
		}
		state, decodeErr := cloudAgentDecode(hydrated)
		if decodeErr != nil || state.CallIndex >= len(state.Calls) || state.Calls[state.CallIndex].ID != input.CallID {
			return nil, BadAuthRequest("恢复调用与本轮步骤不匹配")
		}
	}
	now := time.Now().UTC()
	err = s.mutatePiControl(userID, runID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		if current.LeaseOwner != worker {
			return kernel.AgentLeaseLost("恢复上报租约已失效")
		}
		budgets := map[string]int{}
		if current.RecoveryOperationBudgets != "" {
			if err := json.Unmarshal([]byte(current.RecoveryOperationBudgets), &budgets); err != nil {
				return err
			}
		}
		if budgets == nil {
			budgets = map[string]int{}
		}
		business := recoveryBusinessIdentity(input.TaskID, input.CallID)
		// Preserve counters recorded before the per-operation ledger was added.
		if previous := recoveryBusinessIdentity(current.RecoveryTaskID, current.RecoveryCallID); current.RecoveryOperationID != "" && budgets[previous] < current.RecoveryOperationAttempts {
			budgets[previous] = current.RecoveryOperationAttempts
		}
		budgets[business]++
		body, err := json.Marshal(budgets)
		if err != nil {
			return err
		}
		current.RecoveryOperationBudgets = string(body)
		current.RecoveryAttempts++
		current.RecoveryOperationAttempts = budgets[business]
		current.RecoveryLastEpoch = epoch
		current.RecoveryOperationID = identity
		current.RecoveryTaskID = input.TaskID
		current.RecoveryCallID = input.CallID
		current.RecoveryClass = input.Class
		current.LastErrorReason = input.Class
		if current.RecoveryStartedAt == nil {
			current.RecoveryStartedAt = &now
		}
		if input.Class == "result_unknown" {
			return failWorkerRecovery(current, "pi_worker_result_unknown", "本轮操作结果无法确认，已停止，请核对原任务结果后重试")
		}
		if current.WorkerRecoveryExhausted(now) {
			return failWorkerRecovery(current, "pi_worker_recovery_exhausted", "Pi worker 恢复次数或时间已耗尽，本轮已停止，请重试")
		}
		delay := time.Second * time.Duration(1<<min(current.RecoveryAttempts-1, 4))
		delay += time.Duration(rand.Int64N(int64(delay)/4 + 1))
		delay = min(delay, 30*time.Second)
		delay = max(delay, time.Duration(input.RetryAfterMS)*time.Millisecond)
		next := now.Add(delay)
		if !next.Before(current.RecoveryStartedAt.Add(model.AgentWorkerRecoveryWindow)) {
			return failWorkerRecovery(current, "pi_worker_recovery_exhausted", "等待恢复超过本轮恢复期限，已停止，请重试")
		}
		if err := repo.SetPiRecoveryLease(current, worker, epoch, next); err != nil {
			return err
		}
		current.NextRecoveryAt = &next
		current.LeaseExpiresAt = &next
		current.RecoveryStatus = "scheduled"
		current.RuntimePhase = "waiting_resource"
		current.WaitKind = "worker_recovery"
		current.WaitReason = "worker 连接异常，等待恢复"
		return appendWorkerEvent(current, "worker_recovery_scheduled", map[string]any{"text": current.WaitReason, "class": input.Class, "operation": input.Operation, "attempts": current.RecoveryAttempts, "nextRecoveryAt": next})
	})
	if err != nil {
		return nil, err
	}
	run, err = s.repo.CloudAgentControlRow(userID, runID)
	if err != nil {
		return nil, err
	}
	return workerRecoveryView(run), nil
}

func (s *Service) SweepWorkerRecoveries() (int, error) {
	count := 0
	now := time.Now()
	after := ""
	for {
		runs, err := s.repo.WorkerRecoveryRuns(after, 100)
		if err != nil {
			return count, err
		}
		for _, run := range runs {
			after = run.ID
			if !run.WorkerRecoveryExhausted(now) {
				continue
			}
			protected, err := s.repo.WorkerRecoveryProtectedWait(run)
			if err != nil {
				return count, err
			}
			if protected && run.RecoveryAttempts < model.AgentWorkerRecoveryLimit && run.RecoveryOperationAttempts < model.AgentWorkerRecoveryLimit {
				continue
			}
			err = s.mutatePiControl(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
				if cloudAgentRunTerminal(current.Status) || !current.WorkerRecoveryExhausted(time.Now()) {
					return nil
				}
				return failWorkerRecovery(current, "pi_worker_recovery_exhausted", "Pi worker 长时间未能恢复，本轮已停止，请重试")
			})
			if err == repository.ErrCreationConflict {
				continue
			}
			if err != nil {
				return count, err
			}
			count++
		}
		if len(runs) < 100 {
			break
		}
	}
	return count, nil
}
