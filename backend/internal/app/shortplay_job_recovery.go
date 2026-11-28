package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/shortplay"
)

// 短剧 job 的派发守卫与孤儿回收：新提交在任务创建事务内同时写入 Task 与 job.taskId；
// 旧版本或事务提交前崩溃才可能留下 running + 空 taskId 的行，这种行不会被
// RunningShortplayJob 读到，还占着 dedupe_key。这里负责关停时停止派发，并用任务行已有的
// metadata.shortplayJobId 把这种行找回或按合同明确失败。不自动重发、不退款。

// shortplayOrphanGrace 是判定「提交后没记下 taskId」的静默期。它只用于兼容旧版本或
// 任务事务提交前崩溃留下的行，不能单独证明提交者已死亡；新提交在任务创建事务内绑定
// taskId，跨进程恢复只能看到已经提交的绑定或仍未提交的空行。
const shortplayOrphanGrace = time.Minute

var errShortplayTaskCreatedWithoutBinding = errors.New("短剧任务创建未在同一事务内绑定 job")

// submitShortplayTaskForJob 把任务创建与 job.taskId 绑定放在同一个数据库事务里。
// 事务外的恢复路径只能看到任务与绑定同时提交，或两者都不存在，不需要依赖进程内登记
// 或静默期来判断某个慢提交是否还活着。测试注入的任务提交没有事务，故只在回调未执行时
// 走一次条件补绑，仍不会覆盖已经被恢复路径收口的行。
func (s *Service) submitShortplayTaskForJob(job *model.ShortplayJob, userID string, req CreateTaskRequest, payloadJSON string) (*model.Task, error) {
	linkedInCreate := false
	req.afterTaskCreate = func(repo *repository.Repository, task *model.Task) error {
		applied, err := repo.LinkShortplayJobTaskIfEmpty(job.ID, task.ID, payloadJSON)
		if err != nil {
			return err
		}
		if !applied {
			return repository.ErrShortplayJobNotSubmitting
		}
		if err := projectShortplayJobMediaStatus(repo, job, payloadJSON, shortplay.ImageRunning); err != nil {
			return err
		}
		linkedInCreate = true
		return nil
	}
	task, usedTestHook, err := s.submitShortplayTask(userID, req)
	if err != nil {
		return nil, err
	}
	if !linkedInCreate {
		if !usedTestHook {
			return nil, errShortplayTaskCreatedWithoutBinding
		}
		if linkErr := s.repo.Transaction(func(repo *repository.Repository) error { return req.afterTaskCreate(repo, task) }); linkErr != nil {
			return nil, linkErr
		}
	}
	job.TaskID = task.ID
	job.Status = shortplay.JobRunning
	job.UpdatedAt = time.Now()
	if payloadJSON != "" {
		job.PayloadJSON = payloadJSON
	}
	return task, nil
}

// shortplayCanDispatch 报告本轮是否还能领取/推进短剧 job。drain 已开始或 worker ctx 已取消
// 时必须停止，否则关停宽限内仍会派发新任务；关停合同只要求停止新领取与等在跑协程返回。
func (s *Service) shortplayCanDispatch(ctx context.Context) bool {
	if s.IsDraining() {
		return false
	}
	if ctx == nil {
		return true
	}
	select {
	case <-ctx.Done():
		return false
	default:
		return true
	}
}

// shortplayJobTaskType 返回某类 job 提交出来的任务类型，用于校验任务行与 job 的可信关联。
func shortplayJobTaskType(kind string) string {
	switch kind {
	case shortplay.JobExtract:
		return "canvas_text"
	case shortplay.JobAssetImage:
		return "canvas_image"
	case shortplay.JobAssetAudio:
		return "canvas_audio"
	default:
		return ""
	}
}

// recoverShortplayJobTask 找回「已提交但没记下 taskId」的 job 的任务，返回是否已拿到 taskId。
// 关联先结构化匹配 metadata.shortplayJobId，再校验任务的项目/用户/类型与 job 所属作品一致。
// 0 条或多条候选都按合同明确失败并写明结果待核对，不挑一条当结果、不退回 queued 重发。
func (s *Service) recoverShortplayJobTask(job *model.ShortplayJob) (bool, error) {
	owner, err := s.repo.ShortplayByID(job.ShortplayID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, s.failOrphanedShortplayJob(job, "短剧不存在或已删除，无法核对提交结果；未自动重发")
	}
	if err != nil {
		return false, err
	}
	taskType := shortplayJobTaskType(job.Kind)
	if taskType == "" {
		return false, s.failOrphanedShortplayJob(job, "未知短剧 job 类型，无法核对提交结果；未自动重发")
	}
	tasks, err := s.repo.ShortplayJobTasks(job.ID, owner.UserID, owner.ProjectID, taskType)
	if err != nil {
		return false, err
	}
	switch len(tasks) {
	case 0:
		return false, s.failOrphanedShortplayJob(job,
			"提交后未记录任务号，按 metadata.shortplayJobId 未找到可信任务；结果待核对，未自动重发")
	case 1:
		applied := false
		linkErr := s.repo.Transaction(func(repo *repository.Repository) error {
			var err error
			applied, err = repo.LinkShortplayJobTask(job.ID, tasks[0].ID, time.Now().Add(-shortplayOrphanGrace))
			if err != nil || !applied {
				return err
			}
			return repo.RestoreShortplayJobMediaRunning(job)
		})
		if linkErr != nil {
			return false, linkErr
		}
		if !applied {
			// 条件更新没命中：活跃提交已经先写回，这一轮由它完成。
			return false, nil
		}
		job.TaskID = tasks[0].ID
		return true, nil
	default:
		ids := make([]string, 0, len(tasks))
		for _, task := range tasks {
			ids = append(ids, task.ID)
		}
		return false, s.failOrphanedShortplayJob(job,
			"命中多条可信任务（"+strings.Join(ids, ",")+"）；结果待核对，未自动重发")
	}
}

// failOrphanedShortplayJob 把孤儿 job 明确收口为 failed；条件更新没命中说明行已被推进，忽略。
func (s *Service) failOrphanedShortplayJob(job *model.ShortplayJob, lastError string) error {
	applied := false
	err := s.repo.Transaction(func(repo *repository.Repository) error {
		var err error
		applied, err = repo.FailOrphanedShortplayJob(job.ID, lastError, job.PayloadJSON, time.Now().Add(-shortplayOrphanGrace))
		if err != nil || !applied {
			return err
		}
		return projectShortplayJobMediaStatus(repo, job, job.PayloadJSON, shortplay.ImageFailed)
	})
	if err != nil {
		return err
	}
	if applied {
		job.Status = shortplay.JobFailed
		job.LastError = lastError
	}
	return nil
}

// finishShortplayJob 让未绑定任务的提交失败与资产投影原子收口。
// 恢复已先收口或新任务已绑定时，迟到提交者不再写 job 或资产。
func (s *Service) finishShortplayJob(job *model.ShortplayJob, status, lastError, payload string) error {
	if strings.TrimSpace(job.TaskID) != "" {
		return s.repo.FinishShortplayJob(job.ID, status, lastError, payload)
	}
	return s.repo.Transaction(func(repo *repository.Repository) error {
		applied, err := repo.FinishUnboundShortplayJob(job.ID, status, lastError, payload)
		if err != nil || !applied || status != shortplay.JobFailed {
			return err
		}
		return projectShortplayJobMediaStatus(repo, job, job.PayloadJSON, shortplay.ImageFailed)
	})
}

// 仅在同一事务内赢得 job 条件更新后调用；行锁和在途去重键保护资产投影。
func projectShortplayJobMediaStatus(repo *repository.Repository, job *model.ShortplayJob, payload, status string) error {
	switch job.Kind {
	case shortplay.JobAssetImage:
		return repo.UpdateShortplayAssetImageStatus(job.ShortplayID, shortplay.ParseAssetImageJob(payload).AssetID, status)
	case shortplay.JobAssetAudio:
		return repo.UpdateShortplayAssetAudioStatus(job.ShortplayID, shortplay.ParseAssetAudioJob(payload).AssetID, status)
	default:
		return nil
	}
}

// takeOrphanedShortplayJob 取一条静默超期的孤儿 job 并尝试找回；返回已带 taskId、可以走正常
// 读结果路径的 job。没有孤儿、孤儿被明确失败，或条件竞争失败时返回 (nil,nil)。
func (s *Service) takeOrphanedShortplayJob(ctx context.Context, kind string) (*model.ShortplayJob, error) {
	if !s.shortplayCanDispatch(ctx) {
		return nil, nil
	}
	job, err := s.repo.OrphanedShortplayJob(kind, time.Now().Add(-shortplayOrphanGrace))
	if err != nil || job == nil {
		return nil, err
	}
	recovered, err := s.recoverShortplayJobTask(job)
	if err != nil || !recovered {
		return nil, err
	}
	return job, nil
}
