package app

import (
	"context"
	"fmt"
	"strings"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func cloudAgentMediaBatchResult(batch *cloudAgentMediaBatch) map[string]any {
	result := cloudAgentMediaBatchSummary(batch, false)
	failed := []string{}
	for index, item := range batch.Items {
		if item.Status == cloudAgentMediaBatchItemFailed {
			label := fmt.Sprintf("#%d《%s》", index+1, truncateRunes(item.Title, 30))
			if item.Error != "" {
				label += "：" + truncateRunes(item.Error, 40)
			}
			failed = append(failed, label)
		}
	}
	counts := batch.counts()
	switch batch.Phase {
	case cloudAgentMediaBatchPhaseCanaryDone:
		canary := batch.Items[0]
		result["summary"] = fmt.Sprintf("试跑完成：第 1 条%s，其余 %d 条等待放行", map[string]string{cloudAgentMediaBatchItemSucceeded: "成功", cloudAgentMediaBatchItemFailed: "失败"}[canary.Status], counts[cloudAgentMediaBatchItemPending])
		result["next"] = "成功时先用 canvas_inspect_image 查看试跑结果，再调用 media_batch_control（action=continue）放行其余条目；失败时按原因调整后用 action=retry 重试第 1 条，或 action=cancel 结束本批"
	default:
		summary := fmt.Sprintf("批次完成：成功 %d / %d", counts[cloudAgentMediaBatchItemSucceeded], counts["total"])
		if len(failed) > 0 {
			summary += "，失败 " + strings.Join(failed, "；")
		}
		if counts[cloudAgentMediaBatchItemCancelled] > 0 {
			summary += fmt.Sprintf("，取消 %d", counts[cloudAgentMediaBatchItemCancelled])
		}
		result["summary"] = truncateRunes(summary, 600)
		result["next"] = "结果已逐条回写画布节点。可用 canvas_inspect_image 抽查画面；失败或不满意的条目可在重试额度内调用 media_batch_control（action=retry）重跑，不会再次审批"
	}
	return result
}

// finishCloudAgentMediaBatchPhase 记录当前调用的工具结果并把运行交回 Agent。
func (s *Service) finishCloudAgentMediaBatchPhase(run *model.CloudAgentExecution, state *cloudAgentRuntime, phase string) (bool, error) {
	batch := state.MediaBatch
	if state.CallIndex < 0 || state.CallIndex >= len(state.Calls) || state.Calls[state.CallIndex].ID != batch.CallID {
		return true, s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			batch.Phase = phase
			return cloudAgentSave(current, state)
		})
	}
	call := state.Calls[state.CallIndex]
	err := s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		batch.Phase = phase
		if phase == cloudAgentMediaBatchPhaseDone && !cloudAgentMediaBatchRetryable(batch) {
			if err := repo.ReleaseCloudAgentResourceLeases(run.UserID, batch.ID); err != nil {
				return err
			}
		}
		result := cloudAgentMediaBatchResult(batch)
		s.emitCloudAgentMediaBatchProgress(run.ID, state)
		cloudAgentRecordToolResult(current, state, call, result, nil)
		current.RuntimePhase, current.WaitKind, current.WaitID, current.WaitReason = "running", "", "", ""
		return cloudAgentSave(current, state)
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

func cloudAgentMediaBatchRetryable(batch *cloudAgentMediaBatch) bool {
	for _, item := range batch.Items {
		if item.Attempts < batch.MaxRetries && (item.Status == cloudAgentMediaBatchItemFailed || item.Status == cloudAgentMediaBatchItemSucceeded) {
			return true
		}
	}
	return false
}

// closeCloudAgentMediaBatchAfterStop 处理已停止或失败的运行：未提交的条目取消，
// 在途任务由收尾流程决定继续或取消，全部落定后释放批次租约并完成收尾。
func (s *Service) closeCloudAgentMediaBatchAfterStop(ctx context.Context, run *model.CloudAgentExecution, state *cloudAgentRuntime) (bool, error) {
	batch := state.MediaBatch
	changed := []int{}
	for index, item := range batch.Items {
		if item.Status == cloudAgentMediaBatchItemPending {
			changed = append(changed, index)
		}
	}
	if len(changed) > 0 || batch.active() {
		err := s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			for _, index := range changed {
				state.MediaBatch.Items[index].Status = cloudAgentMediaBatchItemCancelled
			}
			if batch.inFlight() == 0 {
				batch.Phase = cloudAgentMediaBatchPhaseDone
			}
			s.emitCloudAgentMediaBatchProgress(run.ID, state, changed...)
			return cloudAgentSave(current, state)
		})
		if err != nil {
			return false, err
		}
		return false, nil
	}
	if batch.inFlight() > 0 {
		return false, nil
	}
	if err := s.repo.ReleaseCloudAgentResourceLeases(run.UserID, batch.ID); err != nil {
		return false, err
	}
	latest, err := s.repo.CloudAgent(run.UserID, run.ID)
	if err != nil {
		return false, err
	}
	return true, s.finishCloudAgentCleanup(ctx, latest)
}
