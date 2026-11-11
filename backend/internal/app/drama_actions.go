package app

import (
	"gorm.io/gorm"
	"strings"
	"unicode/utf8"
	"yingce/backend/internal/model"
	"yingce/backend/internal/production"
)

// 用户操作只写自己负责的列：业务意图（文本、锁定、审阅）和把槽位重置为待生成。
// 调度循环的结果写入带状态条件，因此两边并发时以用户最新意图为准。

func (s *Service) UpdateDramaSettings(userID string, projectID string, req UpdateDramaSettingsRequest) (DramaSummary, error) {
	_, productionRow, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return DramaSummary{}, err
	}
	fields := map[string]any{}
	if req.Settings != nil {
		current := parseDramaSettings(productionRow.SettingsJSON)
		next := req.Settings.normalized()
		if err := next.TextThinking.validate(); err != nil {
			return DramaSummary{}, err
		}
		if !production.ValidAspectRatio(next.AspectRatio) {
			return DramaSummary{}, BadAuthRequest("不支持的画幅：" + next.AspectRatio)
		}
		if problem := production.ValidateAssetPrompts(next.Assets); problem != "" {
			return DramaSummary{}, BadAuthRequest(problem)
		}
		if err := s.validateDramaSkills(userID, next); err != nil {
			return DramaSummary{}, err
		}
		if err := s.validateDramaModelOverrides(userID, next.Models); err != nil {
			return DramaSummary{}, err
		}
		if err := validateDramaSettingsTransition(productionRow, current, &next); err != nil {
			return DramaSummary{}, err
		}
		models, err := s.resolveDramaModels(userID, next)
		if err != nil {
			return DramaSummary{}, err
		}
		if err := s.validateDramaImageSettings(models, next.Assets); err != nil {
			return DramaSummary{}, err
		}
		next.Models = models
		if err := s.validateDramaVideoSettings(next); err != nil {
			return DramaSummary{}, err
		}
		fields["settings_json"] = encodeDramaSettings(next)
	}
	if req.BudgetMicrocredits != nil {
		budget := *req.BudgetMicrocredits
		if budget < 0 {
			return DramaSummary{}, BadAuthRequest("预算不能为负数")
		}
		fields["budget_microcredits"] = budget
		if productionRow.PauseReason == dramaBudgetPauseReason {
			spent, err := s.dramaTotalSpent(productionRow)
			if err != nil {
				return DramaSummary{}, err
			}
			if budget == 0 || spent < budget {
				fields["pause_reason"] = ""
			}
		}
	}
	if len(fields) > 0 {
		if err := s.repo.UpdateDramaProduction(productionRow.ProjectID, fields); err != nil {
			return DramaSummary{}, err
		}
	}
	return s.DramaSummary(userID, projectID)
}

func (s *Service) SetDramaRunning(userID string, projectID string, running bool) (DramaSummary, error) {
	_, productionRow, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return DramaSummary{}, err
	}
	if running {
		if err := s.resumeDrama(productionRow); err != nil {
			return DramaSummary{}, err
		}
	} else if productionRow.Status == model.DramaStatusRunning {
		if err := s.repo.UpdateDramaProduction(productionRow.ProjectID, map[string]any{"status": model.DramaStatusPaused}); err != nil {
			return DramaSummary{}, err
		}
	}
	s.wakeDramaScheduler()
	return s.DramaSummary(userID, projectID)
}

// resumeDrama 校验模型与预算后把制作切回运行；“继续制作”和所有显式生成请求共用这一入口。
func (s *Service) resumeDrama(productionRow *model.DramaProduction) error {
	settings := parseDramaSettings(productionRow.SettingsJSON)
	models, err := s.resolveDramaModels(productionRow.UserID, settings)
	if err != nil {
		return err
	}
	if missing := missingDramaCapabilities(models); len(missing) > 0 {
		return BadAuthRequest("还没有可用的" + strings.Join(missing, "、") + "模型")
	}
	if exhausted, err := s.dramaBudgetExhausted(productionRow); err != nil {
		return err
	} else if exhausted {
		return BadAuthRequest("已达到预算上限（含画布精修花费），请先提高预算")
	}
	settings.Models = models
	// 开工后草稿表单不再使用，清掉避免长期保存第二份原文。
	fields := map[string]any{"status": model.DramaStatusRunning, "pause_reason": "", "settings_json": encodeDramaSettings(settings), "draft_json": ""}
	if productionRow.Status == model.DramaStatusCompleted {
		fields["stage"] = model.DramaStageProduce
	}
	if err := s.repo.UpdateDramaProduction(productionRow.ProjectID, fields); err != nil {
		return err
	}
	productionRow.Status, productionRow.PauseReason = model.DramaStatusRunning, ""
	return nil
}

// reopenDrama 在用户修改已完成内容后让调度循环重新接手；只改内容不代表要开始花钱，暂停中的制作保持暂停。
func (s *Service) reopenDrama(productionRow *model.DramaProduction) error {
	if productionRow.Status == model.DramaStatusCompleted {
		if err := s.repo.UpdateDramaProduction(productionRow.ProjectID, map[string]any{"status": model.DramaStatusRunning, "stage": model.DramaStageProduce}); err != nil {
			return err
		}
	}
	s.wakeDramaScheduler()
	return nil
}

// requestDramaWork 用于用户显式要求产出的操作（重新改编、生成、重试、放行、确认）：
// 暂停或已完成的制作直接恢复运行，否则请求只会写进状态而没有调度循环接手。
func (s *Service) requestDramaWork(productionRow *model.DramaProduction) error {
	if productionRow.Status == model.DramaStatusPaused || productionRow.Status == model.DramaStatusCompleted {
		if err := s.resumeDrama(productionRow); err != nil {
			return err
		}
	}
	s.wakeDramaScheduler()
	return nil
}

var dramaStageOrder = map[string]int{model.DramaEpisodeQueued: 0, model.DramaEpisodeScript: 1, model.DramaEpisodePrompts: 2, model.DramaEpisodeReview: 3, model.DramaEpisodeVideo: 4, model.DramaEpisodeRender: 5, model.DramaEpisodeDone: 6}

// episodeRewind 累积一次用户操作对多集的影响，最后统一写回并递增各集版本。
type episodeRewind struct {
	s        *Service
	project  string
	episodes map[string]*model.DramaEpisode
	targets  map[string]string
}

func (s *Service) newEpisodeRewind(projectID string) *episodeRewind {
	return &episodeRewind{s: s, project: projectID, episodes: map[string]*model.DramaEpisode{}, targets: map[string]string{}}
}

func (e *episodeRewind) to(episodeID string, stage string) error {
	if _, ok := e.episodes[episodeID]; !ok {
		episode, err := e.s.repo.DramaEpisode(e.project, episodeID)
		if err != nil {
			return err
		}
		e.episodes[episodeID] = episode
	}
	if current, ok := e.targets[episodeID]; !ok || dramaStageOrder[stage] < dramaStageOrder[current] {
		e.targets[episodeID] = stage
	}
	return nil
}

// commit 把集阶段回退到受影响的最早阶段；已有成片保留但标记过期。
func (e *episodeRewind) commit() error {
	for id, episode := range e.episodes {
		target := e.targets[id]
		fields := map[string]any{"error": ""}
		if dramaStageOrder[episode.Stage] > dramaStageOrder[target] {
			fields["stage"] = target
		}
		if episode.FinalResourceID != "" {
			fields["final_stale"] = true
		}
		if err := e.s.repo.UpdateDramaEpisodeUser(id, fields); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ApproveDramaEpisode(userID, projectID, episodeID string) (DramaEpisodeDetail, error) {
	project, p, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return DramaEpisodeDetail{}, err
	}
	episode, err := s.repo.DramaEpisode(project.ID, episodeID)
	if err != nil {
		return DramaEpisodeDetail{}, err
	}
	settings := parseDramaSettings(p.SettingsJSON)
	if settings.ReviewMode == DramaReviewSample && episode.Number == 1 {
		if episode.Stage != model.DramaEpisodeDone || episode.FinalResourceID == "" || episode.FinalStale {
			return DramaEpisodeDetail{}, BadAuthRequest("样片完成后才能确认样片")
		}
		if err := s.repo.UpdateDramaProduction(project.ID, map[string]any{"sample_approved": true}); err != nil {
			return DramaEpisodeDetail{}, err
		}
	} else {
		segments, err := s.repo.DramaSegments(episode.ID)
		if err != nil {
			return DramaEpisodeDetail{}, err
		}
		if len(segments) == 0 {
			return DramaEpisodeDetail{}, BadAuthRequest("尚无视频提示词")
		}
		for _, seg := range segments {
			if !dramaPromptReady(seg) {
				return DramaEpisodeDetail{}, BadAuthRequest("所有剧情段视频提示词就绪后才能确认")
			}
		}
		if err := s.repo.UpdateDramaEpisodeUser(episode.ID, map[string]any{"approved": true, "stage": model.DramaEpisodeVideo}); err != nil {
			return DramaEpisodeDetail{}, err
		}
	}
	if err := s.requestDramaWork(p); err != nil {
		return DramaEpisodeDetail{}, err
	}
	return s.DramaEpisodeDetail(userID, projectID, episodeID)
}

// RescriptDramaEpisode 重新改编一集；旧镜头的产物版本保留在版本库中。
func (s *Service) RescriptDramaEpisode(userID string, projectID string, episodeID string) (DramaEpisodeDetail, error) {
	project, productionRow, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return DramaEpisodeDetail{}, err
	}
	episode, err := s.repo.DramaEpisode(project.ID, episodeID)
	if err != nil {
		return DramaEpisodeDetail{}, err
	}
	if err := s.repo.CancelQueuedDramaJobs(project.ID, func(db *gorm.DB) *gorm.DB { return db.Where("episode_id = ?", episode.ID) }); err != nil {
		return DramaEpisodeDetail{}, err
	}
	fields := map[string]any{"stage": model.DramaEpisodeScript, "shot_count": 0, "approved": false, "error": ""}
	if episode.FinalResourceID != "" {
		fields["final_stale"] = true
	}
	if _, err := s.repo.ApplyDramaScript(episode.ID, nil, fields, nil); err != nil {
		return DramaEpisodeDetail{}, err
	}
	if err := s.requestDramaWork(productionRow); err != nil {
		return DramaEpisodeDetail{}, err
	}
	return s.DramaEpisodeDetail(userID, projectID, episodeID)
}

// UpdateDramaShot 修改镜头内容，并只把受影响的下游槽位标为过期。
func (s *Service) UpdateDramaShot(userID, projectID, shotID string, req UpdateDramaShotRequest) (model.DramaShot, error) {
	project, p, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return model.DramaShot{}, err
	}
	shot, err := s.repo.DramaShot(project.ID, shotID)
	if err != nil {
		return model.DramaShot{}, err
	}
	fields := map[string]any{}
	for _, f := range []struct {
		column string
		target *string
		value  *string
		limit  int
	}{
		{"speaker", &shot.Speaker, req.Speaker, 60}, {"dialogue", &shot.Dialogue, req.Dialogue, dramaMaxDialogueRunes}, {"action", &shot.Action, req.Action, 1000},
		{"shot_size", &shot.ShotSize, req.ShotSize, 30}, {"image_prompt", &shot.ImagePrompt, req.ImagePrompt, 2000}, {"camera", &shot.Camera, req.Camera, 60}, {"video_prompt", &shot.VideoPrompt, req.VideoPrompt, 2000},
	} {
		if f.value == nil {
			continue
		}
		value := strings.TrimSpace(*f.value)
		if utf8.RuneCountInString(value) > f.limit {
			return model.DramaShot{}, BadAuthRequest("镜头内容过长")
		}
		if value != *f.target {
			*f.target = value
			fields[f.column] = value
		}
	}
	if req.Transition != nil {
		value := production.NormalizeTransition(*req.Transition)
		if value != shot.Transition {
			shot.Transition = value
			fields["transition"] = value
		}
	}
	if req.DurationSeconds != nil {
		value := *req.DurationSeconds
		if value < 1 || value > production.MaxStoryboardShotSeconds {
			return model.DramaShot{}, BadAuthRequest("镜头时长超出范围")
		}
		duration := int64(value * 1000)
		if duration != shot.DurationMs {
			// 一个剧情段一次生成，段总时长不能超过该段视频模型的单次上限。
			if err := s.checkDramaSegmentDuration(project.ID, p, *shot, duration); err != nil {
				return model.DramaShot{}, err
			}
			shot.DurationMs = duration
			fields["duration_ms"] = duration
		}
	}
	if len(fields) == 0 {
		return *shot, nil
	}
	if err := s.repo.UpdateDramaShotScript(*shot, fields); err != nil {
		return model.DramaShot{}, err
	}
	return *shot, s.reopenDrama(p)
}

func validateDramaSettingsTransition(productionRow *model.DramaProduction, current DramaSettings, next *DramaSettings) error {
	// 画幅决定已生成画面的构图，制作开始后不允许修改。
	if productionRow.Stage != model.DramaStageBible && next.AspectRatio != current.AspectRatio {
		return BadAuthRequest("制作开始后不能修改画幅")
	}
	// 分集方式只影响尚未分集的章节：分集完成后已有的集里可能写好了剧本，不重新分组。
	if next.EpisodeMode != current.EpisodeMode || next.ChaptersPerEpisode != current.ChaptersPerEpisode {
		if productionRow.PlanDone {
			return BadAuthRequest("已完成分集，不能再修改分集方式；之后可在制作台逐集追加新的一集")
		}
		if next.EpisodeMode == "" {
			next.EpisodeMode, next.ChaptersPerEpisode = current.EpisodeMode, current.ChaptersPerEpisode
		}
	}
	return nil
}
