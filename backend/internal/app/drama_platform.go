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
	submission := adapterSubmission{UserID: r.project.UserID, Business: business, TaskReq: req}
	if job := r.submitting; job != nil {
		submission.DramaJob = job
		settled, err := r.s.repo.PlatformSettledSubmissionCount(r.project.UserID, "drama", job.ID+":")
		if err != nil {
			return nil, err
		}
		operation := fmt.Sprintf("%s:%d", job.ID, settled)
		submission.Key = "drama:" + operation
		submission.Business.ObjectType, submission.Business.ObjectID, submission.Business.OperationID = job.Kind, firstNonEmpty(job.ShotID, job.CastID, job.EpisodeID), operation
	}
	task, replayed, err := (&creationPlatform{s: r.s}).submitResolved(submission)
	if err != nil {
		return nil, err
	}
	if replayed && r.submitting != nil {
		bound, err := r.s.repo.DramaJob(r.project.ID, r.submitting.ID)
		if err != nil {
			return nil, err
		}
		*r.submitting = *bound
	}
	r.s.wakeTaskDispatcher()
	return task, nil
}

// taskFor 读取作业当前任务，并把作业设为提交身份：同步阶段触发的续作（自动精简）也带作业身份提交。
func (r *dramaRun) taskFor(job *model.DramaJob) (*model.Task, error) {
	r.submitting = job
	return r.s.repo.Task(job.TaskID)
}
