package app

import (
	"context"
	"errors"
	"log"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// DrainPendingPiAgentCleanups 补做"已进入终态、但收尾还没跑"的运行清理。
//
// 为什么需要它：HTTP 取消会自己调 finishCloudAgentCleanup，而看门狗（租约停滞、从未领取）
// 只写终态并置 CleanupPending。没有这个排空入口，被判停的运行就永远不清：子任务不取消、
// 资源租约不释放、占位预留不退 —— 账上会留着一笔谁也不会再动的冻结额度。
func (s *Service) DrainPendingPiAgentCleanups() (int, error) {
	runs, err := s.repo.PendingCloudAgentCleanups(20)
	if err != nil {
		return 0, err
	}
	done := 0
	for index := range runs {
		run := runs[index]
		if err := s.finishCloudAgentCleanup(context.Background(), &run); err != nil {
			// CAS 冲突说明另一方正在收尾这一行，让给它；其余错误记录后继续 ——
			// 一条坏记录不该挡住其他运行的收尾（它们下周还会被再次排空）。
			if errors.Is(err, repository.ErrCreationConflict) {
				continue
			}
			log.Printf("agent cleanup drain: run=%s %v", run.ID, err)
			continue
		}
		done++
	}
	return done, nil
}

// CleanupPending is the durable hand-off between orchestration and task/canvas
// cleanup. HTTP cancellation and the background scheduler use the same path.
func (s *Service) finishCloudAgentCleanup(ctx context.Context, run *model.CloudAgentExecution) error {
	if !run.CleanupPending || !cloudAgentRunTerminal(run.Status) {
		return nil
	}
	state, decodeErr := cloudAgentDecode(run)
	activeID, mediaID, canvasID := run.ActiveTaskID, run.MediaTaskID, run.CanvasID
	if decodeErr == nil {
		activeID, mediaID, canvasID = state.ActiveTaskID, state.MediaTaskID, state.Request.CanvasID
	}
	// 本轮若正停在压缩上（被取消/失败收尾），要在取消子任务之前把保底检查点落盘并清掉压缩态：
	// 终态轮次不会再被调度器推进，否则 ContextCompaction 会永远挂在"正在压缩"、检查点永久丢失。
	// keepTerminal=true：收尾只落检查点与事件，绝不把 failed/cancelled 复活成 completed。
	if decodeErr == nil && state.ContextCompaction != nil {
		if err := s.finalizeCloudAgentInterruptedCompaction(run, &state, "本轮在压缩期间结束，已使用服务端保底检查点"); err != nil && !errors.Is(err, errCloudAgentCheckpoint) {
			return err
		}
		// 收尾那一步要再写一次控制面，必须先重新读：上面的检查点写入推进了 revision。
		refreshed, readErr := s.repo.CloudAgent(run.UserID, run.ID)
		if readErr != nil {
			return readErr
		}
		run = refreshed
	}
	seen := map[string]bool{}
	for _, id := range []string{run.ID, activeID, mediaID} {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if err := ctx.Err(); err != nil {
			return err
		}
		task, err := s.repo.TaskForUser(run.UserID, id)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if task.Status == model.TaskStatusQueued || task.Status == model.TaskStatusRunning {
			source := model.TaskCancellationParentFailed
			if run.Status == "cancelled" {
				source = model.TaskCancellationParentCancelled
			}
			if _, err = s.taskLifecycle().cancelTaskWithIntent(ctx, run.UserID, id, model.TaskCancellationIntent{Source: source}); err != nil {
				// Completion may win the cancellation race. Re-read rather than
				// treating a truthful terminal result as a permanent cleanup error.
				latest, readErr := s.repo.TaskForUser(run.UserID, id)
				if readErr != nil || !cloudAgentTaskTerminal(latest.Status) {
					return err
				}
			}
		}
	}
	if mediaID != "" && decodeErr == nil && state.CallIndex < len(state.Calls) {
		err := s.executeCloudAgentMediaCall(run, &state, state.Calls[state.CallIndex])
		if err != nil && !errors.Is(err, errCloudAgentCheckpoint) {
			return err
		}
		if err == nil {
			var readErr error
			run, readErr = s.repo.CloudAgent(run.UserID, run.ID)
			if readErr != nil {
				return readErr
			}
			mediaID = ""
		}
	}
	// A damaged/oversized transcript cannot record another tool event. The
	// control-plane CAS and canvas terminal write must still be able to commit.
	var mediaTask *model.Task
	var policy RuntimePolicySetting
	if mediaID != "" && canvasID != "" {
		var err error
		mediaTask, err = s.repo.TaskForUser(run.UserID, mediaID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			mediaTask = nil
		}
		policy, err = s.RuntimePolicy()
		if err != nil {
			return err
		}
	}
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		if mediaTask != nil {
			targetNodeID := ""
			if taskContext := taskClientContext(mediaTask.InputJSON); taskContext != nil {
				targetNodeID = taskContext.NodeID
			}
			if _, err := completeCloudAgentMediaNode(repo, run.UserID, canvasID, targetNodeID, mediaTask, policy); err != nil {
				var appErr *AppError
				if !errors.Is(err, gorm.ErrRecordNotFound) && !(errors.As(err, &appErr) && (appErr.Status == 400 || appErr.Status == 409)) {
					return err
				}
				current.FailureMessage = cloudAgentSafeToolError(err) + "；任务记录保留在任务中心"
			}
		}
		current.CleanupPending = false
		current.ActiveTaskID, current.MediaTaskID = "", ""
		// 占位预留与"清理完成"必须同一个事务提交：先清 CleanupPending 再退款，崩在中间就再也没有
		// 任何一方会回来退这笔钱。这里刻意不依赖解码后的状态 —— 收尾可能拿不到可解码的
		// StateJSON，而"解码失败"绝不能等价于"不用退钱"（见 cloudAgentRefundHoldingReservation）。
		if err := cloudAgentRefundHoldingReservation(repo, run, "Agent 本轮结束，首步占位预留退还"); err != nil {
			return err
		}
		if err := repo.ReleaseCloudAgentResourceLeasesByRun(run.UserID, run.ID); err != nil {
			return err
		}
		return nil
	})
}
