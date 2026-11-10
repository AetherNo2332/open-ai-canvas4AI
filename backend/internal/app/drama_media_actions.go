package app

import (
	"encoding/json"
	"fmt"
	"yingce/backend/internal/model"
)

type GenerateDramaSegmentsRequest struct {
	Segments   []int             `json:"segments"`
	VideoModel *DramaModelChoice `json:"videoModel"`
}

// GenerateDramaSegments 显式请求剧情段视频重生成，所有段先校验再统一写入。
func (s *Service) GenerateDramaSegments(userID, projectID, episodeID string, req GenerateDramaSegmentsRequest) (map[string]any, error) {
	project, p, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return nil, err
	}
	if len(req.Segments) == 0 || len(req.Segments) > 200 {
		return nil, BadAuthRequest("请选择剧情段")
	}
	episode, err := s.repo.DramaEpisode(project.ID, episodeID)
	if err != nil {
		return nil, err
	}
	if parseDramaSettings(p.SettingsJSON).ReviewMode == DramaReviewSample && episode.Number != 1 && !p.SampleApproved {
		return nil, BadAuthRequest("请先确认第 1 集样片")
	}
	var modelJSON *string
	if req.VideoModel != nil {
		encoded := ""
		if !req.VideoModel.Empty() {
			if err := s.requireDramaModelOption(userID, "video", *req.VideoModel); err != nil {
				return nil, err
			}
			b, _ := json.Marshal(req.VideoModel)
			encoded = string(b)
		}
		modelJSON = &encoded
	}
	rows, err := s.repo.DramaSegments(episode.ID)
	if err != nil {
		return nil, err
	}
	indexes, err := s.dramaSegmentGenerationIndexes(p, rows, req)
	if err != nil {
		return nil, err
	}
	if err := s.repo.RequestDramaSegmentVideos(project.ID, episode.ID, indexes, modelJSON); err != nil {
		return nil, err
	}
	if err := s.releaseDramaEpisodes(project.ID, []string{episode.ID}); err != nil {
		return nil, err
	}
	return map[string]any{"queued": len(indexes)}, s.requestDramaWork(p)
}

func mediaBusyStatus(status string) bool {
	return status == model.DramaMediaQueued || status == model.DramaMediaRunning
}

// requireDramaModelOption 只接受当前目录中可由服务端执行的模型，防止提交任意渠道标识。
func (s *Service) requireDramaModelOption(userID string, capability string, choice DramaModelChoice) error {
	options, unavailable, err := s.dramaModelOptions(userID, capability)
	if err != nil {
		return err
	}
	for _, item := range unavailable {
		if dramaSameChoice(item.Choice, choice) {
			return BadAuthRequest("所选模型不能用于短剧：" + item.Reason)
		}
	}
	for _, option := range options {
		if option.Choice.LogicalModelID != "" && option.Choice.LogicalModelID == choice.LogicalModelID {
			return nil
		}
		if option.Choice.ChannelID != "" && option.Choice.ChannelID == choice.ChannelID && option.Choice.ModelKey == choice.ModelKey {
			return nil
		}
	}
	return BadAuthRequest("所选" + map[string]string{"text": "文本", "image": "图片", "video": "视频"}[capability] + "模型不可用，请重新选择")
}

func dramaSameChoice(a, b DramaModelChoice) bool {
	if a.LogicalModelID != "" || b.LogicalModelID != "" {
		return a.LogicalModelID == b.LogicalModelID
	}
	return a.ChannelID == b.ChannelID && a.ModelKey == b.ModelKey
}

// releaseDramaEpisodes 放行指定集的视频生成，并把这些集引用、尚未请求的参考图标记为待生成。
func (s *Service) releaseDramaEpisodes(projectID string, episodeIDs []string) error {
	if len(episodeIDs) == 0 {
		return nil
	}
	casts := map[string]bool{}
	for _, id := range episodeIDs {
		episode, err := s.repo.DramaEpisode(projectID, id)
		if err != nil {
			return err
		}
		if !episode.MediaReleased {
			if err := s.repo.UpdateDramaEpisodeUser(episode.ID, map[string]any{"media_released": true}); err != nil {
				return err
			}
		}
		shots, err := s.repo.DramaShots(episode.ID)
		if err != nil {
			return err
		}
		for _, shot := range shots {
			for _, castID := range dramaShotCastIDs(shot) {
				if castID != "" {
					casts[castID] = true
				}
			}
		}
	}
	for castID := range casts {
		// 只把从未请求过的参考图排队；已生成、失败或锁定的保持原状。
		if _, err := s.repo.UpdateDramaCastIf(castID, []string{""}, map[string]any{"image_status": model.DramaMediaQueued}); err != nil {
			return err
		}
	}
	return nil
}

// ReleaseDramaMedia 是手动生图模式的“开始生图”：先确认剧情、分镜和资产，再按需放行。
func (s *Service) ReleaseDramaMedia(userID string, projectID string, req ReleaseDramaMediaRequest) (DramaSummary, error) {
	project, productionRow, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return DramaSummary{}, err
	}
	switch req.Target {
	case "assets":
		if err := s.repo.UpdateDramaProduction(project.ID, map[string]any{"assets_released": true}); err != nil {
			return DramaSummary{}, err
		}
	case "episodes":
		if len(req.EpisodeIDs) == 0 || len(req.EpisodeIDs) > 500 {
			return DramaSummary{}, BadAuthRequest("请选择要生成画面的集")
		}
		if err := s.releaseDramaEpisodes(project.ID, req.EpisodeIDs); err != nil {
			return DramaSummary{}, err
		}
	case "all":
		episodes, err := s.repo.DramaEpisodes(project.ID)
		if err != nil {
			return DramaSummary{}, err
		}
		ids := make([]string, 0, len(episodes))
		for _, episode := range episodes {
			ids = append(ids, episode.ID)
		}
		if err := s.repo.UpdateDramaProduction(project.ID, map[string]any{"assets_released": true}); err != nil {
			return DramaSummary{}, err
		}
		if err := s.releaseDramaEpisodes(project.ID, ids); err != nil {
			return DramaSummary{}, err
		}
	default:
		return DramaSummary{}, BadAuthRequest("请选择要放行的范围")
	}
	if err := s.requestDramaWork(productionRow); err != nil {
		return DramaSummary{}, err
	}
	return s.DramaSummary(userID, projectID)
}

func (s *Service) checkDramaSegmentDuration(projectID string, p *model.DramaProduction, shot model.DramaShot, nextMs int64) error {
	shots, err := s.repo.DramaShots(shot.EpisodeID)
	if err != nil {
		return err
	}
	total := nextMs
	for _, item := range shots {
		if item.SegmentIndex == shot.SegmentIndex && item.ID != shot.ID {
			total += item.DurationMs
		}
	}
	segments, err := s.repo.DramaSegments(shot.EpisodeID)
	if err != nil {
		return err
	}
	choice := parseDramaSettings(p.SettingsJSON).Models.Video
	for _, segment := range segments {
		if segment.SegmentIndex == shot.SegmentIndex {
			choice = segmentVideoChoice(segment, choice)
		}
	}
	profile, err := s.dramaVideoProfile(choice)
	if err != nil {
		return err
	}
	if limit := int64(profile.maxSeconds()) * 1000; total > limit {
		return BadAuthRequest(fmt.Sprintf("本段合计 %.1f 秒，超过视频模型单次最长 %d 秒；请缩短镜头或重新改编本集", float64(total)/1000, profile.maxSeconds()))
	}
	return nil
}

func (s *Service) dramaSegmentGenerationIndexes(p *model.DramaProduction, rows []model.DramaSegment, req GenerateDramaSegmentsRequest) ([]int, error) {
	known := map[int]model.DramaSegment{}
	for _, row := range rows {
		known[row.SegmentIndex] = row
	}
	indexes := []int{}
	seen := map[int]bool{}
	for _, index := range req.Segments {
		seg, ok := known[index]
		if !ok {
			return nil, BadAuthRequest("剧情段不存在")
		}
		if !dramaPromptReady(seg) {
			return nil, BadAuthRequest("请先重新撰写过期或缺失的视频提示词")
		}
		if mediaBusyStatus(seg.VideoStatus) {
			return nil, BadAuthRequest("剧情段视频正在生成或排队")
		}
		settings := parseDramaSettings(p.SettingsJSON)
		choice := segmentVideoChoice(seg, settings.Models.Video)
		if req.VideoModel != nil && !req.VideoModel.Empty() {
			choice = *req.VideoModel
		}
		profile, err := s.dramaVideoProfile(choice)
		if err != nil {
			return nil, err
		}
		if issue := profile.screen(settings.AspectRatio, settings.VideoResolution).Issue; issue != "" {
			return nil, BadAuthRequest(issue)
		}
		// 第 1 段总用参考图或文生视频；尾帧续接的后续段才需要图生视频。
		if issue := profile.continuityIssue(settings); issue != "" && index > 0 {
			return nil, BadAuthRequest(issue)
		}
		if !seen[index] {
			seen[index] = true
			indexes = append(indexes, index)
		}
	}
	return indexes, nil
}
