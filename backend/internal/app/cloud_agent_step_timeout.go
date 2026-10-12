package app

import (
	"strings"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// cloudAgentStepTimeoutError 是一次画布 Agent 模型调用被单步墙钟掐断时写进任务错误的标记。
// 秒级超时与文本任务超时使用同一标记，运行期据此终止整轮 Agent。
const cloudAgentStepTimeoutError = "画布 Agent 单步模型调用超时"

// cloudAgentModelOperation 判断任务是不是"画布 Agent 的一次模型调用"：根任务（第一步）、
// 后续每一步、以及上下文压缩调用都属于同一类，都要按单步口径计时（压缩调用同样可能
// 卡在长时间无输出上，不能没有墙钟）。
func cloudAgentModelOperation(task *model.Task) bool {
	if task == nil {
		return false
	}
	switch task.Operation {
	case cloudAgentOperation, cloudAgentStepOperation, cloudAgentContextCompactionOperation:
		return true
	default:
		return false
	}
}

// cloudAgentStepTimedOut 判断这一步是不是被单步墙钟中止的。
func cloudAgentStepTimedOut(task *model.Task) bool {
	if task == nil || task.Status == model.TaskStatusSucceeded {
		return false
	}
	return strings.Contains(task.Error, cloudAgentStepTimeoutError)
}

// 摘要与普通模型步共用终止路径，保留清理标记以收尾子任务与预留额度。
func (s *Service) failCloudAgentStepTimeout(run *model.CloudAgentExecution, state *cloudAgentRuntime, task *model.Task) error {
	text, reason := cloudAgentModelFailure(task)
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		current.Status = "failed"
		current.FailureMessage = truncateRunes(text, 1000)
		current.CleanupPending = true
		state.PiModelFailureTaskID, state.PiModelFailureNudge = task.ID, ""
		state.Approval = nil
		cloudAgentDropInterjections(run.ID, "本轮已结束："+truncateRunes(text, 120), state)
		state.event(run.ID, "run_failed", map[string]any{
			"reason": reason,
			"text":   text,
			"taskId": task.ID,
		})
		return cloudAgentSave(current, state)
	})
}
