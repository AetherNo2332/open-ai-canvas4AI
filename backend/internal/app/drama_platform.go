package app

import (
	"encoding/json"
	"fmt"

	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

// submitDramaPlatformTask 是短剧生成任务的生产提交路径：经创作平台提交，归属短剧作品与当前作业。
// 幂等键取“作业 ID + 已记录的提交次数”：调度器在提交后、作业行保存前重启时，同一作业再次派发取回原任务；
// 失败重试会先记录上一次提交，因此得到新的键、新的任务与冻结。
func (r *dramaRun) submitDramaPlatformTask(req CreateTaskRequest) (*model.Task, error) {
	business := cp.BusinessRef{Application: "drama", ProjectID: r.project.ID}
	submission := adapterSubmission{UserID: r.project.UserID, Business: business, TaskReq: req}
	if job := r.submitting; job != nil {
		var attempts []DramaJobAttempt
		_ = json.Unmarshal([]byte(job.AttemptsJSON), &attempts)
		operation := fmt.Sprintf("%s:%d", job.ID, len(attempts))
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
