package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
	"yingce/backend/internal/model"
	"yingce/backend/internal/production"
)

// fillDramaSegmentViews 按当前设置预演每个镜头会发给模型的首帧与视频提示词、视频秒数和转场。
func (s *Service) fillDramaSegmentViews(projectID string, p *model.DramaProduction, detail *DramaEpisodeDetail) error {
	settings := parseDramaSettings(p.SettingsJSON)
	casts, err := s.repo.DramaCasts(projectID)
	if err != nil {
		return err
	}
	castByID := map[string]model.DramaCast{}
	for _, cast := range casts {
		castByID[cast.ID] = cast
	}
	rows, err := s.repo.DramaSegments(detail.Episode.ID)
	if err != nil {
		return err
	}
	detail.Segments = buildDramaSegmentViews(settings, parseDramaBibleMeta(p.BibleJSON), detail.Shots, castByID, rows)
	jobs, err := s.repo.RecentDramaSegmentJobs(projectID, detail.Episode.ID)
	if err != nil {
		return err
	}
	for i := range detail.Segments {
		view := &detail.Segments[i]
		for _, job := range jobs {
			if job.ShotID != view.ID {
				continue
			}
			j := dramaJobView(job)
			if job.Kind == model.DramaJobSegmentPrompt && view.PromptJob == nil {
				view.PromptJob = &j
			}
			if job.Kind == model.DramaJobSegmentVideo && view.VideoJob == nil {
				view.VideoJob = &j
				view.VideoReprocessable = view.VideoStatus == model.DramaMediaFailed && view.VideoResourceID != "" && dramaVideoJobReplayable(job)
			}
		}
	}
	if err := s.fillDramaSegmentProviderStages(p.UserID, detail); err != nil {
		return err
	}
	if err := s.fillDramaSegmentRequests(settings, p, detail, rows, castByID); err != nil {
		return err
	}
	return s.fillDramaSegmentWaits(settings, p, detail, rows, castByID)
}

// dramaSegmentDraft 是一个剧情段的镜头分组与撰写素材；预览与撰写任务共用。
type dramaSegmentDraft struct {
	view  DramaSegmentView
	input production.SegmentPromptInput
	shots []model.DramaShot
}

// buildDramaSegmentDrafts 按镜头的 SegmentIndex 分段。参考图编号依次为角色、道具、场景，各自按首次出场顺序排列；
// 用户为某段手动指定过参考图时（rows 里 reference_cast_ids_json 非空），按用户给的顺序与集合编号。
func buildDramaSegmentDrafts(settings DramaSettings, meta dramaBibleMeta, shots []model.DramaShot, castByID map[string]model.DramaCast, rows []model.DramaSegment) []dramaSegmentDraft {
	overrides := map[int][]string{}
	images := map[int][]model.DramaReferenceImage{}
	for _, row := range rows {
		if strings.TrimSpace(row.ReferenceCastIDsJSON) != "" {
			overrides[row.SegmentIndex] = decodeStringList(row.ReferenceCastIDsJSON)
		}
		var extra []model.DramaReferenceImage
		_ = json.Unmarshal([]byte(row.ReferenceImagesJSON), &extra)
		images[row.SegmentIndex] = extra
	}
	groups := [][]model.DramaShot{}
	for _, shot := range shots {
		if len(groups) > 0 && groups[len(groups)-1][0].SegmentIndex == shot.SegmentIndex {
			groups[len(groups)-1] = append(groups[len(groups)-1], shot)
			continue
		}
		groups = append(groups, []model.DramaShot{shot})
	}
	drafts := make([]dramaSegmentDraft, 0, len(groups))
	for position, group := range groups {
		view := DramaSegmentView{Index: group[0].SegmentIndex, Title: firstNonEmpty(group[0].SegmentTitle, group[0].Scene, "剧情段"), ShotIDs: []string{}, Request: DramaSegmentRequest{References: []DramaSegmentReference{}}}
		characterIDs, propIDs, sceneIDs := []string{}, []string{}, []string{}
		lookForCharacter := dramaSegmentCharacterLooks(group, castByID)
		input := production.SegmentPromptInput{Index: position, Count: len(groups), Title: view.Title, AspectRatio: settings.AspectRatio, Style: settings.StylePrompt, VisualRules: meta.VisualRules, Language: settings.Language, PauseMs: settings.Storyboard.DialoguePauseMs(), CharsPerSecond: settings.Storyboard.CharsPerSecond(), Independent: settings.parallelVideo(), Performance: settings.Storyboard.Performance, CameraMotion: settings.Storyboard.CameraMotion}
		for _, shot := range group {
			view.ShotIDs = append(view.ShotIDs, shot.ID)
			view.DurationMs += shot.DurationMs
			for _, id := range decodeStringList(shot.CastIDsJSON) {
				// 角色选了造型时，参考图与外观用造型行；否则用角色本体。
				if lookID, ok := lookForCharacter[id]; ok {
					id = lookID
				}
				if _, ok := castByID[id]; ok && !containsString(characterIDs, id) {
					characterIDs = append(characterIDs, id)
				}
			}
			for _, id := range decodeStringList(shot.PropIDsJSON) {
				if _, ok := castByID[id]; ok && !containsString(propIDs, id) {
					propIDs = append(propIDs, id)
				}
			}
			if _, ok := castByID[shot.SceneCastID]; ok && !containsString(sceneIDs, shot.SceneCastID) {
				sceneIDs = append(sceneIDs, shot.SceneCastID)
			}
			input.Shots = append(input.Shots, production.SegmentShot{Scene: shot.Scene, ShotSize: shot.ShotSize, Camera: shot.Camera, ImagePrompt: shot.ImagePrompt, Action: shot.Action, VideoPrompt: shot.VideoPrompt, Speaker: shot.Speaker, Dialogue: shot.Dialogue, DurationMs: shot.DurationMs, Transition: settings.Storyboard.EffectiveTransition(shot.Transition, shot.Position == 0)})
		}
		referenceIDs := dramaSegmentReferenceSelection(&view, characterIDs, propIDs, sceneIDs, overrides, castByID)
		appendDramaSegmentCastReferences(&view, &input, referenceIDs, castByID)
		for _, extra := range images[view.Index] {
			label := fmt.Sprintf("参考图%d", len(view.Request.References)+1)
			view.Request.References = append(view.Request.References, DramaSegmentReference{ResourceID: extra.ResourceID, Label: label, Kind: "image", Name: extra.Name})
			input.References = append(input.References, production.SegmentReference{Label: label, Kind: "image", Name: extra.Name, Detail: "用户指定的参考图片"})
		}
		drafts = append(drafts, dramaSegmentDraft{view: view, input: input, shots: group})
	}
	return drafts
}

// buildDramaSegmentViews 合并分段与已保存的提示词：改写优先，其次是模型撰写的版本。
func buildDramaSegmentViews(settings DramaSettings, meta dramaBibleMeta, shots []model.DramaShot, castByID map[string]model.DramaCast, rows []model.DramaSegment) []DramaSegmentView {
	rowByIndex := map[int]model.DramaSegment{}
	for _, row := range rows {
		rowByIndex[row.SegmentIndex] = row
	}
	drafts := buildDramaSegmentDrafts(settings, meta, shots, castByID, rows)
	views := make([]DramaSegmentView, 0, len(drafts))
	for _, draft := range drafts {
		view := draft.view
		view.AudioReferenceMode = "auto"
		view.PromptStatus = "none"
		if row, ok := rowByIndex[view.Index]; ok {
			if strings.TrimSpace(row.AudioCastIDsJSON) != "" {
				view.AudioReferenceMode = "custom"
			}
			view.ID, view.Revision, view.UpdatedAt = row.ID, row.Revision, row.UpdatedAt.Format(time.RFC3339)
			view.Generated, view.PromptError = strings.TrimSpace(row.VideoPrompt), row.PromptError
			view.VideoStatus, view.VideoResourceID, view.TailFrameResourceID, view.VideoError = row.VideoStatus, row.VideoResourceID, row.TailFrameResourceID, row.VideoError
			view.VideoSeconds = row.VideoSeconds
			view.VideoModel = segmentVideoChoice(row, settings.Models.Video)
			view.PromptStatus = firstNonEmpty(row.PromptStatus, "queued")
			if override := strings.TrimSpace(row.VideoPromptOverride); override != "" {
				view.VideoPrompt, view.Custom = override, true
			} else {
				view.VideoPrompt = view.Generated
			}
		}
		views = append(views, view)
	}
	return views
}

// RequestDramaSegmentPrompts 让文本模型（重新）撰写指定剧情段的完整视频提示词；不影响已有的手动改写。
func (s *Service) RequestDramaSegmentPrompts(userID string, projectID string, episodeID string, req GenerateDramaSegmentsRequest) (map[string]any, error) {
	project, productionRow, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return nil, err
	}
	if len(req.Segments) == 0 || len(req.Segments) > 200 {
		return nil, BadAuthRequest("请选择要撰写的剧情段")
	}
	episode, err := s.repo.DramaEpisode(project.ID, episodeID)
	if err != nil {
		return nil, err
	}
	shots, err := s.repo.DramaShots(episode.ID)
	if err != nil {
		return nil, err
	}
	known := map[int]bool{}
	for _, shot := range shots {
		known[shot.SegmentIndex] = true
	}
	indexes := []int{}
	for _, index := range req.Segments {
		if !known[index] {
			return nil, BadAuthRequest("剧情段不存在")
		}
		indexes = append(indexes, index)
	}
	queued, err := s.repo.RequestDramaSegmentPrompts(project.ID, episode.ID, indexes)
	if err != nil {
		return nil, err
	}
	return map[string]any{"queued": queued}, s.requestDramaWork(productionRow)
}

// UpdateDramaSegment 保存或清除一个剧情段的整段视频提示词改写。
func (s *Service) UpdateDramaSegment(userID string, projectID string, episodeID string, index int, req UpdateDramaSegmentRequest) error {
	project, _, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return err
	}
	episode, err := s.repo.DramaEpisode(project.ID, episodeID)
	if err != nil {
		return err
	}
	if !req.hasChanges() {
		return BadAuthRequest("没有要保存的修改")
	}
	shots, err := s.repo.DramaShots(episode.ID)
	if err != nil {
		return err
	}
	found := false
	for _, shot := range shots {
		if shot.SegmentIndex == index {
			found = true
			break
		}
	}
	if !found {
		return BadAuthRequest("剧情段不存在")
	}
	fields, err := s.dramaSegmentUpdateReferenceFields(userID, project.ID, episode.ID, index, req)
	if err != nil {
		return err
	}
	// 先完成全部校验，再写参考选择，避免一个无效字段使本次修改只保存一半。
	if req.VideoPromptOverride != nil && utf8.RuneCountInString(strings.TrimSpace(*req.VideoPromptOverride)) > 12000 {
		return BadAuthRequest("段提示词过长（最多 12000 字）")
	}
	if len(fields) > 0 {
		if err := s.repo.SaveDramaSegmentReferenceFields(episode.ID, index, fields); err != nil {
			return err
		}
	}
	if req.VideoPromptOverride != nil {
		prompt := production.NormalizeReferenceTokens(strings.TrimSpace(*req.VideoPromptOverride))
		if utf8.RuneCountInString(prompt) > 12000 {
			return BadAuthRequest("段提示词过长（最多 12000 字）")
		}
		if err := s.repo.SaveDramaSegmentOverride(model.DramaSegment{ProjectID: project.ID, EpisodeID: episode.ID, SegmentIndex: index, VideoPromptOverride: prompt}); err != nil {
			return err
		}
	}
	s.wakeDramaScheduler()
	return nil
}

// dramaSegmentReferenceIDs 校验用户指定的参考图：必须是本作品的设定，去重并保持顺序。
func (s *Service) dramaSegmentReferenceIDs(projectID string, ids []string) ([]string, error) {
	if len(ids) > dramaMaxReferenceLimit {
		return nil, BadAuthRequest(fmt.Sprintf("每段最多指定 %d 个参考设定", dramaMaxReferenceLimit))
	}
	casts, err := s.repo.DramaCasts(projectID)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, cast := range casts {
		known[cast.ID] = true
	}
	result := []string{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if !known[id] {
			return nil, BadAuthRequest("参考设定不存在")
		}
		if !containsString(result, id) {
			result = append(result, id)
		}
	}
	return result, nil
}

// dramaShotCastIDs 返回镜头引用的全部设定：出场角色、关键道具与场景。
func dramaShotCastIDs(shot model.DramaShot) []string {
	ids := append(decodeStringList(shot.CastIDsJSON), decodeStringList(shot.PropIDsJSON)...)
	if shot.SceneCastID != "" {
		ids = append(ids, shot.SceneCastID)
	}
	return ids
}

func (s *Service) fillDramaSegmentProviderStages(userID string, detail *DramaEpisodeDetail) error {
	videoJobs := []DramaJobView{}
	for _, view := range detail.Segments {
		if view.VideoJob != nil {
			videoJobs = append(videoJobs, *view.VideoJob)
		}
	}
	if err := s.attachDramaProviderStages(userID, videoJobs); err != nil {
		return err
	}
	for i := range detail.Segments {
		for _, job := range videoJobs {
			if view := &detail.Segments[i]; view.VideoJob != nil && view.VideoJob.ID == job.ID {
				view.VideoJob.ProviderStage = job.ProviderStage
			}
		}
	}
	return nil
}

func (s *Service) fillDramaSegmentRequests(settings DramaSettings, p *model.DramaProduction, detail *DramaEpisodeDetail, rows []model.DramaSegment, castByID map[string]model.DramaCast) error {
	drafts := buildDramaSegmentDrafts(settings, parseDramaBibleMeta(p.BibleJSON), detail.Shots, castByID, rows)
	for i, draft := range drafts {
		if i >= len(rows) || rows[i].SegmentIndex != draft.view.Index {
			return errors.New("剧情段记录与镜头分组不一致")
		}
		var previous *model.DramaSegment
		if i > 0 {
			previous = &rows[i-1]
		}
		request, _, _, prepareErr := s.prepareDramaSegment(settings, draft, rows[i], previous, castByID)
		if prepareErr != nil {
			request.Warning = prepareErr.Error()
			// 视频模型下线、时长超限等会让预演在列参考图前返回。镜头里的设定引用仍在，照常列出并标为未发送；
			// 否则制作台看起来像资产引用丢了，在参考面板勾选候选还会把本段存成只含该项的手动列表。
			if len(request.References) < len(draft.view.Request.References) {
				request.References = make([]DramaSegmentReference, 0, len(draft.view.Request.References))
				for _, reference := range draft.view.Request.References {
					if reference.CastID != "" {
						reference.ResourceID = castByID[reference.CastID].ImageResourceID
					}
					reference.Sent = false
					request.References = append(request.References, reference)
				}
			}
		}
		detail.Segments[i].Request = request
	}
	return nil
}

func dramaSegmentCharacterLooks(group []model.DramaShot, castByID map[string]model.DramaCast) map[string]string {
	// lookForCharacter 把本段某角色映射到本段镜头选用的造型行；一段里同一角色取第一个出现的造型。
	lookForCharacter := map[string]string{}
	for _, shot := range group {
		for _, lookID := range decodeStringList(shot.LookCastIDsJSON) {
			look, ok := castByID[lookID]
			if ok && look.ParentID != "" {
				if _, set := lookForCharacter[look.ParentID]; !set {
					lookForCharacter[look.ParentID] = lookID
				}
			}
		}
	}
	return lookForCharacter
}

func appendDramaSegmentCastReferences(view *DramaSegmentView, input *production.SegmentPromptInput, referenceIDs []string, castByID map[string]model.DramaCast) {
	for _, id := range referenceIDs {
		cast := castByID[id]
		label := fmt.Sprintf("参考图%d", len(view.Request.References)+1)
		// 造型行：名称写“角色·造型”，声线沿用所属角色本体。
		name, voice := cast.Name, cast.VoiceInstructions
		if cast.ParentID != "" {
			if base, ok := castByID[cast.ParentID]; ok {
				voice = base.VoiceInstructions
			}
			if cast.LookName != "" {
				name = cast.Name + "·" + cast.LookName
			}
		}
		view.Request.References = append(view.Request.References, DramaSegmentReference{CastID: cast.ID, Label: label, Kind: cast.Kind, Name: name})
		input.References = append(input.References, production.SegmentReference{Label: label, Kind: cast.Kind, Name: name, Detail: firstNonEmpty(cast.Appearance, cast.Description), Voice: voice})
	}
}

func (s *Service) dramaSegmentUpdateReferenceFields(userID, projectID, episodeID string, index int, req UpdateDramaSegmentRequest) (map[string]any, error) {
	fields := map[string]any{}
	if req.AutoReferences || req.ReferenceCastIDs != nil {
		// 参考图编号写在提示词里，换参考图后模型撰写的提示词标记过期；手写的提示词保留，由用户自行核对编号。
		encoded := ""
		if !req.AutoReferences {
			ids, err := s.dramaSegmentReferenceIDs(projectID, *req.ReferenceCastIDs)
			if err != nil {
				return nil, err
			}
			encoded = encodeStringList(ids)
		}
		fields["reference_cast_ids_json"] = encoded
	}
	if req.ReferenceImages != nil {
		images, err := s.dramaReferenceImages(userID, *req.ReferenceImages)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(images)
		if err != nil {
			return nil, err
		}
		fields["reference_images_json"] = string(encoded)
	}
	if req.AutoAudioReferences {
		fields["audio_cast_ids_json"] = ""
	} else if req.AudioCastIDs != nil {
		ids, err := s.dramaSegmentAudioIDs(projectID, episodeID, index, *req.AudioCastIDs)
		if err != nil {
			return nil, err
		}
		fields["audio_cast_ids_json"] = encodeStringList(ids)
	}
	return fields, nil
}

func (s *Service) fillDramaSegmentWaits(settings DramaSettings, p *model.DramaProduction, detail *DramaEpisodeDetail, rows []model.DramaSegment, castByID map[string]model.DramaCast) error {
	lookup := func(id string) (model.DramaCast, bool) {
		cast, ok := castByID[id]
		return cast, ok
	}
	// 排队作业的等待原因一次算好：本集作业、剧本作业与各段的提示词、视频作业。
	waiting := []*DramaJobView{detail.ScriptJob}
	for i := range detail.Jobs {
		waiting = append(waiting, &detail.Jobs[i])
	}
	for i := range detail.Segments {
		waiting = append(waiting, detail.Segments[i].PromptJob, detail.Segments[i].VideoJob)
	}
	if err := s.fillDramaJobWaits(p, waiting...); err != nil {
		return err
	}
	for i := range detail.Segments {
		view := &detail.Segments[i]
		if view.VideoStatus != model.DramaMediaQueued || i >= len(rows) {
			continue
		}
		if job := view.VideoJob; job != nil && job.Status == model.DramaJobRunning {
			continue
		}
		// 作业已排队、还没提交：原因是作品暂停、账号或本池同时生成数已满；下一轮就会提交时为空，显示“排队提交中”。
		if job := view.VideoJob; job != nil && job.Status == model.DramaJobQueued {
			view.VideoWait = job.Wait
			continue
		}
		view.VideoWait = firstNonEmpty(dramaVideoWaitReason(settings, p, &detail.Episode, rows, i, detail.Shots, lookup), "排队中，等待调度提交")
	}
	return nil
}

func dramaSegmentReferenceSelection(view *DramaSegmentView, characterIDs, propIDs, sceneIDs []string, overrides map[int][]string, castByID map[string]model.DramaCast) []string {
	referenceIDs := append(append(characterIDs, propIDs...), sceneIDs...)
	view.ReferenceMode = "auto"
	if custom, ok := overrides[view.Index]; ok {
		view.ReferenceMode = "custom"
		referenceIDs = []string{}
		for _, id := range custom {
			if _, exists := castByID[id]; exists && !containsString(referenceIDs, id) {
				referenceIDs = append(referenceIDs, id)
			}
		}
	}
	return referenceIDs
}

func (req UpdateDramaSegmentRequest) hasChanges() bool {
	return req.VideoPromptOverride != nil || req.ReferenceCastIDs != nil || req.AutoReferences || req.ReferenceImages != nil || req.AudioCastIDs != nil || req.AutoAudioReferences
}
