package app

import (
	"encoding/json"
	"errors"
	"strings"
	"yingce/backend/internal/model"
	"yingce/backend/internal/production"
)

// textInput 让上游以流式返回：剧本与分镜这类长输出在非流式下要等全部写完才回第一个字节，
// 中转站前的网关（如 Cloudflare 约 100 秒无响应）会先断开并报 524；流式边写边回，连接一直有数据。
// 结果仍在流结束后整段写入任务结果，模型声明不支持流式时由执行层改走非流式。
func (r *dramaRun) textInput(job *model.DramaJob, systemPrompt string, step string) map[string]any {
	return map[string]any{
		"config":      map[string]any{"systemPrompt": systemPrompt},
		"textOptions": map[string]any{"stream": true, "thinkingLevel": r.settings.TextThinking.levelFor(step)},
		"metadata":    r.metadata(job),
	}
}

func (r *dramaRun) loadChapters(ids []string) ([]production.Chapter, error) {
	var units []model.ProjectUnit
	var err error
	if ids == nil {
		units, err = r.s.repo.DramaChapters(r.project.ID)
	} else {
		units, err = r.s.repo.DramaChaptersByIDs(r.project.ID, ids)
	}
	if err != nil {
		return nil, err
	}
	chapters := make([]production.Chapter, 0, len(units))
	for _, unit := range units {
		chapters = append(chapters, production.Chapter{ID: unit.ID, Index: unit.Position + 1, Title: unit.Title, Text: unit.SourceText})
	}
	return chapters, nil
}

func (r *dramaRun) submitBible(job *model.DramaJob) (*model.Task, bool, error) {
	chapters, err := r.loadChapters(nil)
	if err != nil {
		return nil, false, err
	}
	skill, err := r.s.dramaWritingSkill(r.project.UserID, r.settings.BibleSkill, "资产提取")
	if err != nil {
		return nil, false, err
	}
	prompt := production.BiblePrompt(r.project.Name, r.settings.Settings, chapters, dramaBibleSampleRunes, skill)
	input := map[string]any{
		"config":      map[string]any{"systemPrompt": production.BibleSystemPrompt()},
		"textOptions": map[string]any{"stream": true, "thinkingLevel": r.settings.TextThinking.levelFor(DramaStepBible)},
		"metadata":    map[string]any{"source": "drama_production", "domainProjectId": r.project.ID},
	}
	task, err := r.createTaskWith(job, r.settings.Models.textFor(DramaStepBible), "text", "text", prompt, input)
	return task, false, err
}

func (r *dramaRun) bibleSummary() string {
	var b strings.Builder
	if r.meta.Logline != "" {
		b.WriteString("卖点：" + r.meta.Logline + "\n")
	}
	if r.meta.Tone != "" {
		b.WriteString("基调：" + r.meta.Tone + "\n")
	}
	b.WriteString(r.castSummary("character", 24))
	return b.String()
}

func (r *dramaRun) castSummary(kind string, limit int) string {
	var b strings.Builder
	count := 0
	for _, cast := range r.casts {
		if cast.Kind != kind || count >= limit {
			continue
		}
		count++
		aliases := decodeStringList(cast.AliasesJSON)
		line := "- " + cast.Name
		if len(aliases) > 0 {
			line += "（又称 " + strings.Join(aliases, "、") + "）"
		}
		if detail := firstNonEmpty(cast.Role, cast.Description); detail != "" {
			line += "：" + truncateRunes(detail, 60)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (r *dramaRun) submitPlan(job *model.DramaJob) (*model.Task, bool, error) {
	var payload dramaPlanPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		return nil, false, err
	}
	if payload.Start != r.production.PlanCursor {
		return nil, true, nil
	}
	chapters, err := r.loadChapters(nil)
	if err != nil {
		return nil, false, err
	}
	if payload.End > len(chapters) {
		payload.End = len(chapters)
	}
	next, err := r.s.repo.MaxDramaEpisodeNumber(r.project.ID)
	if err != nil {
		return nil, false, err
	}
	previous := ""
	if last, err := r.s.repo.LastDramaEpisode(r.project.ID); err == nil && last != nil {
		previous = last.Cliffhanger
	}
	prompt := production.PlanPrompt(r.project.Name, r.settings.Settings, r.bibleSummary(), chapters[payload.Start:payload.End], next+1, previous)
	task, err := r.createTask(job, "text", "text", prompt, r.textInput(job, production.PlanSystemPrompt(), DramaStepPlan))
	return task, false, err
}

func (r *dramaRun) submitScript(job *model.DramaJob) (*model.Task, bool, error) {
	episode, err := r.s.repo.DramaEpisode(r.project.ID, job.EpisodeID)
	if err != nil {
		return nil, true, nil
	}
	if episode.Stage != model.DramaEpisodeScript {
		return nil, true, nil
	}
	chapters, err := r.loadChapters(decodeStringList(episode.ChapterIDsJSON))
	if err != nil {
		return nil, false, err
	}
	profile, err := r.s.dramaVideoProfile(r.settings.Models.Video)
	if err != nil {
		return nil, false, err
	}
	skill, err := r.s.dramaWritingSkill(r.project.UserID, r.settings.ScriptSkill, "分镜拆分")
	if err != nil {
		return nil, false, err
	}
	prompt := production.ScriptPrompt(production.ScriptContext{
		Segment: r.settings.Storyboard.SegmentLimits(profile.Durations),
		Title:   r.project.Name, Settings: r.settings.Settings, VisualRules: r.meta.VisualRules,
		EpisodeNumber: episode.Number, EpisodeTitle: episode.Title, Synopsis: episode.Synopsis, Hook: episode.Hook, Cliffhanger: episode.Cliffhanger,
		CastSummary: r.castSummary("character", 30), PropSummary: r.castSummary("prop", 20), SceneSummary: r.castSummary("scene", 20), SourceChapters: chapters,
		SourceFormat: episode.SourceFormat, Skill: skill,
	})
	task, err := r.createTaskWith(job, r.settings.Models.textFor(DramaStepScript), "text", "text", prompt, r.textInput(job, production.ScriptSystemPrompt(), DramaStepScript))
	return task, false, err
}

func (r *dramaRun) submitCastImage(job *model.DramaJob) (*model.Task, bool, error) {
	cast, err := r.s.repo.DramaCast(r.project.ID, job.CastID)
	// 槽位处于 running 可能是本工作的上一轮尝试，只有已就绪、已失败（终态）或锁定时才不再需要。
	if err != nil || cast.Locked || cast.ImageStatus == model.DramaMediaReady || cast.ImageStatus == model.DramaMediaDraft || cast.ImageStatus == model.DramaMediaFailed {
		return nil, true, nil
	}
	appearance := firstNonEmpty(cast.Appearance, cast.Description, cast.Role)
	assets := r.settings.Assets
	// 造型行：以所属角色的参考图为底图做图生图，保持五官、脸型一致，只换服装、发型与妆容。
	var baseImage *model.DramaCast
	operation := "text_to_image"
	if cast.ParentID != "" {
		base, baseErr := r.s.repo.DramaCast(r.project.ID, cast.ParentID)
		if baseErr != nil {
			return nil, false, baseErr
		}
		// 底图还没生成：等角色本体参考图就绪后再排造型，保证同一张脸。
		if base.ImageStatus != model.DramaMediaReady || base.ImageResourceID == "" {
			return nil, true, nil
		}
		baseImage = base
		operation = "image_to_image"
	}
	// 画风与画面规则写的是成片画幅，设定图有自己的画幅：带入前去掉其中的画幅与横竖构图要求，
	// 设定和方案模板的文字不动。
	stylePrompt := production.StripFrameDirectives(r.settings.StylePrompt)
	visualRules := production.StripFrameDirectives(r.meta.VisualRules)
	prompt := production.CastImagePrompt(assets, cast.Kind, cast.Name, appearance, stylePrompt, visualRules)
	if baseImage != nil {
		prompt = "参考图1是角色" + cast.Name + "的既有形象。保持五官、脸型、体态与参考图1完全一致。" + firstNonEmpty(strings.TrimSpace(cast.ImagePrompt), "只把造型改为："+appearance+"。"+stylePrompt)
	}
	// 资产参考图统一横版 16:9，与作品画幅无关：场景要容纳完整空间，道具要留足展示余量。
	// 角色按参考图方案的画幅（内置方案均为 16:9，自定义方案可改）。画幅通过尺寸参数约束，
	// 不只靠提示词；模型给不出接近的尺寸时直接失败并说明原因，不退回模型默认的方图。
	ratio := r.castImageRatio(cast.Kind)
	prompt = production.WithAssetRatio(prompt, ratio)
	request := r.imageRequest(cast.Kind)
	if request.Issue != "" {
		return nil, false, errors.New(request.Issue)
	}
	metadata := r.metadata(job)
	metadata["assetMode"], metadata["assetRatio"] = assets.Mode, ratio
	if assets.SourceSkillID != "" {
		metadata["assetSourceSkillId"], metadata["assetSourcePath"] = assets.SourceSkillID, assets.SourcePath
	}
	config := map[string]any{"count": "1"}
	request.apply(config)
	input := map[string]any{"config": config, "metadata": metadata}
	if baseImage != nil {
		ref, refErr := r.s.dramaResourceReference(baseImage.ImageResourceID, "参考图1", "reference_image")
		if refErr != nil {
			return nil, false, refErr
		}
		input["referenceImages"] = []any{ref}
	}
	task, err := r.createTaskWith(job, r.settings.Models.imageFor(cast.Kind), "image", operation, prompt, input)
	return task, false, err
}

// assignDramaSegments 按分镜规则（智能 / 固定 / 范围）把一集镜头切成剧情段。
func assignDramaSegments(shots []model.DramaShot, limits production.SegmentLimits) {
	scenes := make([]string, len(shots))
	durations := make([]int64, len(shots))
	for i := range shots {
		scenes[i] = shots[i].Scene
		durations[i] = shots[i].DurationMs
	}
	for index, seg := range production.SegmentShotsForVideo(scenes, durations, limits) {
		for i := seg.Start; i < seg.End; i++ {
			shots[i].SegmentIndex = index
			shots[i].SegmentTitle = truncateRunes(seg.Title, 120)
		}
	}
}

func dramaRenderSize(aspect string) (int, int) {
	switch aspect {
	case "16:9":
		return 1920, 1080
	case "1:1":
		return 1080, 1080
	case "3:4":
		return 1080, 1440
	case "4:3":
		return 1440, 1080
	}
	return 1080, 1920
}

// buildDramaTimeline 把一集镜头装配为时间线：视频轨静音，配音轨按镜头起点对齐，字幕单独输出。
func buildDramaTimeline(segments []model.DramaSegment, aspect string) (renderProject, error) {
	width, height := dramaRenderSize(aspect)
	visible := true
	timeline := renderProject{Version: 2, Width: width, Height: height, Tracks: []renderTrack{{ID: "video", Kind: "video", Visible: &visible}}}
	if len(segments) == 0 {
		return timeline, errors.New("没有可合成的剧情段")
	}
	for _, seg := range segments {
		if seg.VideoStatus != model.DramaMediaReady || seg.VideoResourceID == "" || seg.VideoSeconds <= 0 {
			return timeline, errors.New("所有剧情段视频就绪后才能合成")
		}
		duration := int64(seg.VideoSeconds) * 1000
		clip := renderClip{ID: "v-" + seg.ID, Kind: "video", TrackID: "video", StartMs: timeline.DurationMs, DurationMs: duration, SourceDurationMs: duration, Volume: 1}
		clip.DirectMedia = &struct {
			ID         string `json:"id"`
			Kind       string `json:"kind"`
			StorageKey string `json:"storageKey"`
		}{seg.VideoResourceID, "video", "resource:" + seg.VideoResourceID}
		timeline.Clips = append(timeline.Clips, clip)
		timeline.DurationMs += duration
	}
	return timeline, nil
}

func (r *dramaRun) submitRender(job *model.DramaJob) (*model.Task, bool, error) {
	episode, err := r.s.repo.DramaEpisode(r.project.ID, job.EpisodeID)
	if err != nil || (episode.Stage != model.DramaEpisodeRender) {
		return nil, true, nil
	}
	// 先记下版本：时间线装配失败时 failJob 要按这个版本把原因写到本集上，否则本集没有错误、每轮重新排队合成。
	job.PayloadJSON = encodeDramaRevision(episode.Revision)
	segments, err := r.s.repo.DramaSegments(episode.ID)
	if err != nil {
		return nil, false, err
	}
	timeline, err := buildDramaTimeline(segments, r.settings.AspectRatio)
	if err != nil {
		return nil, false, err
	}
	return r.s.createDramaRenderTask(r.project, timeline, job)
}
