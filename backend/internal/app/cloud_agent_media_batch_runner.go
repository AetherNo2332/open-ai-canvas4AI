package app

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"log"
	"time"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func cloudAgentMediaBatchSummary(batch *cloudAgentMediaBatch, includeAll bool) map[string]any {
	counts := batch.counts()
	items := []map[string]any{}
	for index, item := range batch.Items {
		if !includeAll && item.Status == cloudAgentMediaBatchItemPending && len(batch.Items) > 12 {
			continue
		}
		entry := map[string]any{"index": index, "nodeId": item.NodeID, "title": truncateRunes(item.Title, 60), "status": item.Status}
		if taskID := item.taskID(); taskID != "" {
			entry["taskId"] = taskID
		}
		if item.Error != "" {
			entry["error"] = truncateRunes(item.Error, 90)
		}
		if item.Attempts > 0 {
			entry["attempts"] = item.Attempts
		}
		items = append(items, entry)
	}
	return map[string]any{
		"batchId": batch.ID, "mode": batch.Mode, "phase": batch.Phase, "concurrency": batch.Concurrency,
		"total": counts["total"], "pending": counts[cloudAgentMediaBatchItemPending], "submitted": counts[cloudAgentMediaBatchItemSubmitted],
		"succeeded": counts[cloudAgentMediaBatchItemSucceeded], "failed": counts[cloudAgentMediaBatchItemFailed], "cancelled": counts[cloudAgentMediaBatchItemCancelled],
		"items": items,
	}
}

func (s *Service) emitCloudAgentMediaBatchProgress(runID string, state *cloudAgentRuntime, changed ...int) {
	batch := state.MediaBatch
	if batch == nil {
		return
	}
	summary := cloudAgentMediaBatchSummary(batch, true)
	if len(changed) > 0 {
		items := []map[string]any{}
		all, _ := summary["items"].([]map[string]any)
		for _, index := range changed {
			if index >= 0 && index < len(all) {
				items = append(items, all[index])
			}
		}
		summary["items"] = items
		summary["delta"] = true
	}
	counts := batch.counts()
	summary["text"] = fmt.Sprintf("批量生成：完成 %d / %d，运行 %d，失败 %d", counts[cloudAgentMediaBatchItemSucceeded], counts["total"], counts[cloudAgentMediaBatchItemSubmitted], counts[cloudAgentMediaBatchItemFailed])
	summary["callId"] = batch.CallID
	state.event(runID, "media_batch_progress", summary)
}

// startCloudAgentMediaBatch 在用户批准后打开批次；真正的提交由后台执行器完成。
func (s *Service) startCloudAgentMediaBatch(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) error {
	batch := state.MediaBatch
	if batch == nil || batch.ID != state.Approval.ID || batch.CallID != call.ID {
		return s.terminateCloudAgent(run, "批量生成审批与待执行批次不一致，本轮已停止")
	}
	if batch.active() {
		return nil
	}
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		batch.Phase = cloudAgentMediaBatchPhaseRunning
		if batch.Canary {
			batch.Phase = cloudAgentMediaBatchPhaseCanary
		}
		s.emitCloudAgentMediaBatchProgress(run.ID, state)
		return cloudAgentSave(current, state)
	})
}

func (s *Service) startCloudAgentMediaBatchRunner(userID, runID string) {
	if s == nil || runID == "" {
		return
	}
	key := "batch:" + runID
	ctx, cancel := context.WithCancel(context.Background())
	s.mediaBatchMu.Lock()
	if s.mediaBatchClosed {
		s.mediaBatchMu.Unlock()
		cancel()
		return
	}
	if s.mediaBatchWaiters == nil {
		s.mediaBatchWaiters = make(map[string]context.CancelFunc)
	}
	if _, exists := s.mediaBatchWaiters[key]; exists {
		s.mediaBatchMu.Unlock()
		cancel()
		return
	}
	s.mediaBatchWaiters[key] = cancel
	s.mediaBatchWg.Add(1)
	s.mediaBatchMu.Unlock()
	go func() {
		defer func() {
			cancel()
			s.mediaBatchMu.Lock()
			delete(s.mediaBatchWaiters, key)
			s.mediaBatchMu.Unlock()
			s.mediaBatchWg.Done()
		}()
		if err := s.runCloudAgentMediaBatch(ctx, userID, runID); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("[Agent] media batch runner failed run=%s: %v", runID, err)
			if run, readErr := s.repo.CloudAgent(userID, runID); readErr == nil {
				_ = s.terminateCloudAgent(run, cloudAgentUserFailureMessage(runID, err))
			}
		}
	}()
}

func (s *Service) runCloudAgentMediaBatch(ctx context.Context, userID, runID string) error {
	conflicts := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		done, err := s.stepCloudAgentMediaBatch(ctx, userID, runID)
		switch {
		case errors.Is(err, repository.ErrCreationConflict):
			conflicts++
			if conflicts > 50 {
				return err
			}
			time.Sleep(time.Duration(min(conflicts, 10)) * 20 * time.Millisecond)
			continue
		case err != nil:
			return err
		case done:
			return nil
		}
		conflicts = 0
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(cloudAgentMediaBatchPollInterval):
		}
	}
}

// stepCloudAgentMediaBatch 推进一轮：先回写已结束的条目，再判断阶段，最后按空余并发提交。
// 返回 true 表示执行器可以退出（阶段已结束或批次已收尾）。
func (s *Service) stepCloudAgentMediaBatch(ctx context.Context, userID, runID string) (bool, error) {
	run, err := s.repo.CloudAgent(userID, runID)
	if err != nil {
		return false, err
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return false, err
	}
	batch := state.MediaBatch
	if batch == nil {
		return true, nil
	}
	if settled, err := s.settleFinishedCloudAgentBatchItem(run, &state); settled {
		return false, err
	}
	if cloudAgentRunTerminal(run.Status) {
		return s.closeCloudAgentMediaBatchAfterStop(ctx, run, &state)
	}
	if !batch.active() {
		return batch.inFlight() == 0, nil
	}
	if batch.Phase == cloudAgentMediaBatchPhaseCanary && batch.Items[0].Status != cloudAgentMediaBatchItemPending && batch.Items[0].Status != cloudAgentMediaBatchItemSubmitted {
		return s.finishCloudAgentMediaBatchPhase(run, &state, cloudAgentMediaBatchPhaseCanaryDone)
	}
	next := batch.nextPending()
	if next < 0 && batch.inFlight() == 0 {
		return s.finishCloudAgentMediaBatchPhase(run, &state, cloudAgentMediaBatchPhaseDone)
	}
	if next < 0 || batch.inFlight() >= batch.Concurrency {
		return false, nil
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		return false, err
	}
	if active, err := s.repo.ActiveTaskCountForUser(userID); err != nil {
		return false, err
	} else if active >= int64(policy.Task.ActiveTaskLimit) {
		return false, nil
	}
	err = s.submitCloudAgentMediaBatchItem(run, &state, next, policy)
	if errors.Is(err, errCloudAgentMediaBatchWait) {
		return false, nil
	}
	return false, err
}

func (s *Service) settleFinishedCloudAgentBatchItem(run *model.CloudAgentExecution, state *cloudAgentRuntime) (bool, error) {
	batch := state.MediaBatch
	userID := run.UserID
	for index, item := range batch.Items {
		if item.Status != cloudAgentMediaBatchItemSubmitted {
			continue
		}
		task, err := s.repo.TaskForUser(userID, item.taskID())
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return true, s.settleCloudAgentMediaBatchItem(run, state, index, nil)
		}
		if err != nil {
			return true, err
		}
		if cloudAgentTaskTerminal(task.Status) {
			return true, s.settleCloudAgentMediaBatchItem(run, state, index, task)
		}
	}

	return false, nil
}
