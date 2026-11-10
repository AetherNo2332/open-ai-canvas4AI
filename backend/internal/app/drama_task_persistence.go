package app

import (
	"errors"
	"fmt"
	"time"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func (s *Service) createDramaRenderTask(project *model.Project, timeline renderProject, job *model.DramaJob) (*model.Task, bool, error) {
	if s.dramaTaskHook != nil {
		task, err := s.dramaTaskHook(project.UserID, CreateTaskRequest{Type: model.TaskTypeTimelineRender}, &timeline)
		return task, false, err
	}
	task, err := s.CreateTimelineRenderTask(project.UserID, TimelineRenderCreateRequest{ProjectID: project.ID, Timeline: timeline, prepareOnly: true})
	if err != nil {
		return nil, false, err
	}
	task, err = s.persistDramaTask(job, task, nil)
	return task, false, err
}

func (s *Service) persistDramaTask(job *model.DramaJob, task *model.Task, order *model.BillingOrder, submission ...*model.PlatformSubmission) (*model.Task, error) {
	policy, err := s.RuntimePolicy()
	if err != nil {
		return nil, err
	}
	if order != nil {
		task.BillingOrderID = order.ID
	}
	bound := *job
	bound.Status, bound.TaskID, bound.CostMicrocredits = model.DramaJobRunning, task.ID, 0
	if bound.StartedAt == nil {
		// 精简续任务沿用作业原来的开始时间，队列里的耗时不被截断。
		now := time.Now()
		bound.StartedAt = &now
	}
	// 任务与计费预留、作业绑定在同一事务中提交，提交前不会唤醒 dispatcher。绑定只在作业仍是派发前
	// 读到的状态时生效；期间被取消或重试则整笔回滚，不留下已扣费却没人认领的任务。
	s.storageMu.Lock()
	err = s.repo.Transaction(func(repo *repository.Repository) error {
		if err := createTaskWithStorageQuotaRepository(repo, task, order, policy, submission...); err != nil {
			return err
		}
		return repo.BindDramaJobTask(&bound, job.Status, job.TaskID)
	})
	s.storageMu.Unlock()
	if errors.Is(err, repository.ErrActiveTaskLimit) {
		return nil, BadAuthRequest(fmt.Sprintf("同时排队或运行的任务最多 %d 个，请等待已有任务完成", policy.Task.ActiveTaskLimit))
	}
	if errors.Is(err, repository.ErrInsufficientCredits) {
		return nil, BadAuthRequest("积分不足，请先使用兑换码充值")
	}
	if errors.Is(err, repository.ErrLogicalModelUnavailable) {
		return nil, BadAuthRequest("所选模型已停用、归档或配置已更新，请重新选择")
	}
	if err != nil {
		return nil, err
	}
	*job = bound
	s.recordActivity(task.UserID, "task", 1)
	_ = s.log(task.UserID, task.ID, "info", "短剧任务已进入队列并绑定作业", "")
	s.wakeTaskDispatcher()
	return taskForOutput(*task), nil
}
