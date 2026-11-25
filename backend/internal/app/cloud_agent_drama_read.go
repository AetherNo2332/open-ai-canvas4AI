package app

import (
	"encoding/json"
	"strconv"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

// cloudAgentDramaReadMaxRunes 与 Skill 读取共用同一分页上限，避免模型把一次读取
// 当成无限上下文使用。
const cloudAgentDramaReadMaxRunes = piNativeSkillReadMaxRunes

// cloudAgentDramaEpisodeReadArgs 只有对象 ID 和读取选项。用户与项目归属来自
// 已认证的 Run，不接受参数覆盖。
type cloudAgentDramaEpisodeReadArgs struct {
	EpisodeID string `json:"episodeId"`
	Offset    *int   `json:"offset"`
	Limit     *int   `json:"limit"`
}

// cloudAgentDramaEpisodeReadResult 是短剧分集读取的回执合同：分页游标可续读，
// adopted 恒为 false，因为读取不会采用任何提议。
type cloudAgentDramaEpisodeReadResult struct {
	ProjectID   string `json:"projectId"`
	EpisodeID   string `json:"episodeId"`
	Content     string `json:"content"`
	ContentType string `json:"contentType"`
	Visibility  string `json:"visibility"`
	Offset      int    `json:"offset"`
	NextOffset  int    `json:"nextOffset"`
	TotalRunes  int    `json:"totalRunes"`
	HasMore     bool   `json:"hasMore"`
	Adopted     bool   `json:"adopted"`
}

// cloudAgentDramaReadRange 解析分页选项：缺省 offset=0、limit=上限；显式 0、负数
// 和超上限都拒绝，不回落到默认值。
func cloudAgentDramaReadRange(offset, limit *int) (int, int, error) {
	start, size := 0, cloudAgentDramaReadMaxRunes
	if offset != nil {
		start = *offset
	}
	if limit != nil {
		size = *limit
	}
	if start < 0 || size < 1 || size > cloudAgentDramaReadMaxRunes {
		return 0, 0, &cloudAgentArgumentError{BadAuthRequest(
			"短剧分集读取分页无效：offset 必须是非负整数，limit 必须在 1 到 " + strconv.Itoa(cloudAgentDramaReadMaxRunes) + " 之间")}
	}
	return start, size, nil
}

// cloudAgentReadDramaEpisode 读取当前短剧项目内的一个分集及其镜头。项目和分集在
// 每次调用时按真实归属校验，跨项目、跨用户和撤权都在这里拒绝。
func (s *Service) cloudAgentReadDramaEpisode(userID string, state *cloudAgentRuntime, call cloudAgentCall) (cloudAgentDramaEpisodeReadResult, error) {
	var args cloudAgentDramaEpisodeReadArgs
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return cloudAgentDramaEpisodeReadResult{}, cloudAgentJSONArgumentError(err)
	}
	return s.cloudAgentDramaEpisodeReadPage(userID, state, args)
}

func (s *Service) cloudAgentDramaEpisodeReadPage(userID string, state *cloudAgentRuntime, args cloudAgentDramaEpisodeReadArgs) (cloudAgentDramaEpisodeReadResult, error) {
	var empty cloudAgentDramaEpisodeReadResult
	if state == nil || agentBusinessScope(state.Request).Type != "drama" {
		return empty, kernel.Forbidden("短剧分集读取只适用于短剧项目运行")
	}
	if err := validateCloudAgentID(args.EpisodeID, "短剧集 ID", 36); err != nil {
		return empty, &cloudAgentArgumentError{err}
	}
	offset, limit, err := cloudAgentDramaReadRange(args.Offset, args.Limit)
	if err != nil {
		return empty, err
	}
	projectID := agentBusinessScope(state.Request).ID
	if _, _, err := s.dramaForUser(userID, projectID); err != nil {
		return empty, err
	}
	episode, err := s.repo.DramaEpisode(projectID, args.EpisodeID)
	if err != nil {
		return empty, err
	}
	shots, err := s.repo.DramaShots(episode.ID)
	if err != nil {
		return empty, err
	}
	body, err := json.Marshal(struct {
		Episode *model.DramaEpisode `json:"episode"`
		Shots   []model.DramaShot   `json:"shots"`
	}{episode, shots})
	if err != nil {
		return empty, err
	}
	runes := []rune(string(body))
	// 超过正文末尾的 offset 返回末尾空页，与既有读取行为一致，不报错。
	offset = min(offset, len(runes))
	end := min(offset+limit, len(runes))
	return cloudAgentDramaEpisodeReadResult{
		ProjectID: projectID, EpisodeID: episode.ID, Content: string(runes[offset:end]),
		ContentType: "text/plain", Visibility: "authorized",
		Offset: offset, NextOffset: end, TotalRunes: len(runes), HasMore: end < len(runes), Adopted: false,
	}, nil
}
