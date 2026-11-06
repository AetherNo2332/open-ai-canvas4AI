package app

import (
	"fmt"

	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

// submitDramaPlatformTask 是短剧生成任务的生产提交路径：经创作平台提交，归属短剧作品与当前作业。
// 作业的提交序号 = 该作业已到终态的平台提交数：调度器在提交后、作业行保存前重启时，在途任务不计数，
// 同一作业再次提交取回原任务；任务失败或成功后的重试、自动精简续作得到新序号。序号不依赖只保留 20 条的展示历史。
func (r *dramaRun) submitDramaPlatformTask(req CreateTaskRequest) (*model.Task, error) {
	business := cp.BusinessRef{Application: "drama", ProjectID: r.project.ID}
	// Agent 工作授权绑定时，任务按该 grant 准入并归属工作树。
	submission := adapterSubmission{UserID: r.project.UserID, Business: business, TaskReq: req, BudgetGrantID: r.production.AgentGrantID}
	if job := r.submitting; job != nil {
		settled, err := r.s.repo.PlatformSettledSubmissionCount(r.project.UserID, "drama", job.ID+":")
		if err != nil {
			return nil, err
		}
		operation := fmt.Sprintf("%s:%d", job.ID, settled)
		submission.Key = "drama:" + operation
		submission.Business.ObjectType, submission.Business.ObjectID, submission.Business.OperationID = job.Kind, firstNonEmpty(job.ShotID, job.CastID, job.EpisodeID), operation
	}
	task, _, err := (&creationPlatform{s: r.s}).submitResolved(submission)
	if err != nil {
		return nil, err
	}
	r.s.wakeTaskDispatcher()
	return task, nil
}

// dispatchBudgetCommitted 按订单口径保守汇总预算占用：流水线与画布精修的实收
// 加上所有未结算订单的冻结额，并回写 spent_microcredits 缓存。
func (r *dramaRun) dispatchBudgetCommitted(queued int) (committed int64, budgeted bool, err error) {
	budgeted = r.production.BudgetMicrocredits > 0 && queued > 0
	if !budgeted {
		return 0, false, nil
	}
	totals, err := r.s.dramaBillingTotals(r.production)
	if err != nil {
		return 0, true, err
	}
	r.production.SpentMicrocredits = totals.PipelineChargedMicrocredits
	return totals.ChargedMicrocredits() + totals.HeldMicrocredits(), true, nil
}

// pauseWhenAgentAuthorizationStopped 暂停被停止或关闭授权的 Agent 驱动制作，
// 返回是否已暂停；未绑定的制作不在此拦截。
func (r *dramaRun) pauseWhenAgentAuthorizationStopped() (bool, error) {
	if r.production.AgentGrantID == "" {
		return false, nil
	}
	live, err := r.s.agentWorkAuthorizationLive(r.production.UserID, r.production.AgentGrantID)
	if err != nil {
		return false, err
	}
	if live {
		return false, nil
	}
	r.production.Status, r.production.PauseReason = model.DramaStatusPaused, "Agent 工作授权已停止"
	return true, r.s.repo.UpdateDramaProduction(r.production.ProjectID, map[string]any{"status": r.production.Status, "pause_reason": r.production.PauseReason})
}

// taskFor 读取作业当前任务，并把作业设为提交身份：同步阶段触发的续作（自动精简）也带作业身份提交。
func (r *dramaRun) taskFor(job *model.DramaJob) (*model.Task, error) {
	r.submitting = job
	return r.s.repo.Task(job.TaskID)
}
