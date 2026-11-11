package app

import (
	"errors"
	"log/slog"
	"strings"
	"time"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func (r *dramaRun) dispatch() error {
	queued, err := r.s.repo.DramaJobsByStatus(r.project.ID, model.DramaJobQueued)
	if err != nil {
		return err
	}
	sortDramaJobsForDispatch(queued)
	// 预算按订单口径保守判断：流水线与画布精修的实收加上所有未结算订单的冻结额。每轮只汇总一次，
	// 本轮新提交任务的冻结额逐项累加；同时把流水线实收回写到 spent_microcredits 缓存。
	var committed int64
	budgeted := r.production.BudgetMicrocredits > 0 && len(queued) > 0
	if budgeted {
		totals, err := r.s.dramaBillingTotals(r.production)
		if err != nil {
			return err
		}
		committed = totals.ChargedMicrocredits() + totals.HeldMicrocredits()
		r.production.SpentMicrocredits = totals.PipelineChargedMicrocredits
	}
	for i := range queued {
		job := &queued[i]
		pool := dramaDispatchPool(job.Kind)
		if r.running[pool] >= r.settings.Concurrency {
			continue
		}
		if budgeted && committed >= r.production.BudgetMicrocredits {
			r.production.Status, r.production.PauseReason = model.DramaStatusPaused, dramaBudgetPauseReason
			return nil
		}
		r.submitting = job
		task, skip, err := r.submit(job)
		if errors.Is(err, repository.ErrDramaJobChanged) {
			// 派发期间作业被取消或重试：任务与计费预留已随绑定一起回滚，按最新状态在下一轮处理。
			continue
		}
		if err != nil {
			stop, failErr := r.handleDramaDispatchError(job, err)
			if stop {
				return failErr
			}
			continue
		}
		if skip {
			now := time.Now()
			job.Status, job.FinishedAt = model.DramaJobCanceled, &now
			if err := r.s.repo.SaveDramaJob(job); err != nil {
				return err
			}
			continue
		}
		if r.s.dramaTaskHook != nil {
			now := time.Now()
			job.Status, job.TaskID, job.StartedAt = model.DramaJobRunning, task.ID, &now
			if err := r.s.repo.SaveDramaJob(job); err != nil {
				return err
			}
		}
		if err := r.markRunning(job); err != nil {
			return err
		}
		r.running[pool]++
		if budgeted && task.BillingOrderID != "" {
			order, err := r.s.repo.BillingOrder(task.BillingOrderID)
			if err != nil {
				// 读不到刚提交任务的冻结额时本轮不再派发，下一轮按订单重新汇总。
				slog.Warn("drama dispatch billing read failed", "project", r.project.ID, "task", task.ID, "error", err)
				return nil
			}
			charged, held, _ := dramaOrderSpend(*order)
			committed += charged + held
		}
	}
	return nil
}

func (r *dramaRun) handleDramaDispatchError(job *model.DramaJob, err error) (bool, error) {
	message := err.Error()
	switch {
	case strings.Contains(message, "同时排队或运行的任务最多"):
		return true, nil
	case strings.Contains(message, "积分不足"):
		r.production.Status, r.production.PauseReason = model.DramaStatusPaused, dramaCreditsReason
		return true, nil
	case strings.Contains(message, "维护"):
		return true, nil
	}
	recordDramaAttempt(job, "", "rejected", message, 0)
	if failErr := r.failJob(job, message); failErr != nil {
		return true, failErr
	}
	return false, nil
}
