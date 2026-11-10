package app

import (
	"errors"
	"unicode/utf8"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// WithdrawCloudAgentInterjection 撤回一条尚未送达模型的插话。只动待送队列，
// 不停止本轮、不影响正在进行的模型调用和已提交的媒体任务。
// 已送达的插话已经进入模型上下文，撤回会造成"界面说撤了、模型却看到了"，因此拒绝。
func (s *Service) WithdrawCloudAgentInterjection(userID, id, messageID string) error {
	if messageID == "" || !utf8.ValidString(messageID) || utf8.RuneCountInString(messageID) > 160 {
		return BadAuthRequest("插话标识无效")
	}
	if _, _, err := s.cloudAgentTask(userID, id); err != nil {
		return err
	}
	for attempt := 0; attempt < cloudAgentInterjectionAttempts; attempt++ {
		run, err := s.repo.CloudAgent(userID, id)
		if err != nil {
			return err
		}
		state, err := cloudAgentDecode(run)
		if err != nil {
			return err
		}
		index := -1
		for i, item := range state.PendingInterjections {
			if item.ID == messageID {
				index = i
				break
			}
		}
		if index < 0 {
			for _, seen := range state.InterjectionIDs {
				if seen == messageID {
					return cloudAgentInterjectionConflict(ReasonAgentInterjectionDelivered, "插话已送达或已结束，无法撤回")
				}
			}
			return kernel.NotFound("插话不存在")
		}
		err = s.repo.MutateCloudAgent(userID, id, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			item := state.PendingInterjections[index]
			state.PendingInterjections = append(state.PendingInterjections[:index:index], state.PendingInterjections[index+1:]...)
			cloudAgentRememberInterjectionID(&state, item.ID)
			state.event(id, "user_interjection_withdrawn", map[string]any{"messageId": item.ID, "text": item.Text})
			return cloudAgentSave(current, &state)
		})
		if errors.Is(err, repository.ErrCreationConflict) {
			continue
		}
		return err
	}
	return cloudAgentInterjectionConflict(ReasonAgentRunStateBusy, "Agent 正在写入运行状态，请重试")
}
