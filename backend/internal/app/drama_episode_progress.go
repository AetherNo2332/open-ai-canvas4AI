package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"yingce/backend/internal/model"
	"yingce/backend/internal/production"
)

func (r *dramaRun) manualMedia() bool { return r.settings.MediaMode == DramaMediaManual }

func (r *dramaRun) assetsReleased() bool { return !r.manualMedia() || r.production.AssetsReleased }

// mediaHeld 表示用户尚未放行本集的参考图和段视频。
func (r *dramaRun) mediaHeld(episode *model.DramaEpisode) bool {
	return dramaMediaHeld(r.settings, episode)
}

func dramaMediaHeld(settings DramaSettings, e *model.DramaEpisode) bool {
	return settings.MediaMode == DramaMediaManual && !e.MediaReleased && (e.Stage == model.DramaEpisodePrompts || e.Stage == model.DramaEpisodeReview || e.Stage == model.DramaEpisodeVideo)
}

func (r *dramaRun) sampleBlocked(episode *model.DramaEpisode) bool {
	return r.settings.ReviewMode == DramaReviewSample && !r.production.SampleApproved && episode.Number != 1
}

func (r *dramaRun) needsReview(e *model.DramaEpisode) bool {
	return r.settings.ReviewMode == DramaReviewEvery && !e.Approved
}

// advanceEpisode 推进单集：剧本 → 段提示词 → 审阅 → 段视频 → 成片。
func (r *dramaRun) advanceEpisode(e *model.DramaEpisode, loadedStage string) error {
	label := fmt.Sprintf("第 %d 集", e.Number)
	segments, err := r.s.repo.DramaSegments(e.ID)
	if err != nil {
		return err
	}
	// 从画布采用的视频可能只写入了视频（Agent 在事务内采用不做存储 IO），尾帧在后台补抽，不阻塞调度循环；
	// 补抽结果按段版本写回，完成后唤醒调度，下一段在下一轮拿到首帧。
	segments, err = r.backfillEpisodeTails(e, segments)
	if err != nil {
		return err
	}
	switch e.Stage {
	case model.DramaEpisodeScript:
		handled, err := r.advanceDramaScript(e, label)
		if err != nil || handled {
			return err
		}
	case model.DramaEpisodePrompts:
		if err := r.advanceDramaPrompts(e, segments); err != nil {
			return err
		}
	case model.DramaEpisodeReview:
		if err := r.advanceDramaReview(e, segments); err != nil {
			return err
		}
	case model.DramaEpisodeVideo:
		ready, err := r.enqueueSegmentVideos(e, segments, false)
		if err != nil {
			return err
		}
		if ready {
			e.Stage = model.DramaEpisodeRender
		}
	case model.DramaEpisodeRender:
		if e.Error == "" {
			r.enqueue(model.DramaJobRender, e.ID, "", "", episodePriority(dramaRankRender, e.Number), label+" 合成成片", nil)
		}
	case model.DramaEpisodeDone:
		if e.FinalStale {
			e.Stage = model.DramaEpisodeVideo
		}
	}
	if e.Stage != loadedStage {
		_, err = r.s.repo.UpdateDramaEpisodeIfRevision(e.ID, e.Revision, map[string]any{"stage": e.Stage})
	}
	return err
}

func (r *dramaRun) advanceDramaReview(e *model.DramaEpisode, segments []model.DramaSegment) error {
	if !r.mediaHeld(e) && !r.sampleBlocked(e) && !r.needsReview(e) {
		e.Stage = model.DramaEpisodeVideo
		return nil
	}
	_, err := r.enqueueSegmentVideos(e, segments, true)
	return err
}

// enqueueSegmentVideos 提交可生成的段视频。尾帧续接时按段序只提交下一段（第 2 段起要等上一段尾帧）；
// 并行出片时本集所有满足条件的段一起提交，由派发并发与渠道排队决定实际出片节奏。
// requestedOnly 时只提交用户明确请求（queued）的段，用于全集尚未进入视频阶段时。返回全部段视频是否就绪。
// 等待条件与 dramaVideoWaitReason 同源，详情页显示的等待原因就是这里卡住的条件。
func (r *dramaRun) enqueueSegmentVideos(e *model.DramaEpisode, segments []model.DramaSegment, requestedOnly bool) (bool, error) {
	if len(segments) == 0 {
		return false, nil
	}
	if r.settings.parallelVideo() {
		return r.enqueueParallelSegmentVideos(e, segments, requestedOnly)
	}
	var shots []model.DramaShot
	for i, seg := range segments {
		if dramaSegmentVideoDone(seg) {
			continue
		}
		// Edits mark media stale; only an explicit request may regenerate it.
		wanted := seg.VideoStatus == model.DramaMediaQueued || (!requestedOnly && seg.VideoStatus == "")
		if !wanted {
			return false, nil
		}
		if shots == nil {
			loaded, err := r.s.repo.DramaShots(e.ID)
			if err != nil {
				return false, err
			}
			shots = loaded
		}
		if dramaVideoWaitReason(r.settings, r.production, e, segments, i, shots, r.castLookup) == "" {
			r.enqueue(model.DramaJobSegmentVideo, e.ID, seg.ID, "", segmentPriority(dramaRankVideo, e.Number, seg.SegmentIndex), fmt.Sprintf("第 %d 集 第 %d 段视频", e.Number, seg.SegmentIndex+1), nil)
		}
		return false, nil
	}
	return true, nil
}

func (r *dramaRun) advanceDramaPrompts(e *model.DramaEpisode, segments []model.DramaSegment) error {
	ready := len(segments) > 0
	for _, seg := range segments {
		if !dramaPromptReady(seg) {
			ready = false
		}
	}
	if ready {
		e.Stage = model.DramaEpisodeReview
	} else if _, err := r.enqueueSegmentVideos(e, segments, true); err != nil {
		// 用户逐段点了“生成视频”的段不必等全集提示词写完。
		return err
	}
	return nil
}

func (r *dramaRun) advanceDramaScript(e *model.DramaEpisode, label string) (bool, error) {
	if e.Error != "" || r.active[dramaJobKey(model.DramaJobScript, e.ID, "", "")] {
		return false, nil
	}
	handled, err := r.applyDirectStoryboard(e)
	if err != nil {
		return false, err
	}
	if handled {
		// 分镜已直接写入（阶段与版本由 ApplyDramaScript 更新）或已记录错误，本轮不再回写阶段。
		return true, nil
	}
	r.enqueue(model.DramaJobScript, e.ID, "", "", episodePriority(dramaRankScript, e.Number), label+" 剧本与分镜", nil)
	return false, nil
}

func (r *dramaRun) backfillEpisodeTails(e *model.DramaEpisode, segments []model.DramaSegment) ([]model.DramaSegment, error) {
	var err error
	started := false
	for _, segment := range segments {
		if segment.VideoStatus != model.DramaMediaReady || segment.VideoResourceID == "" || segment.TailFrameResourceID != "" {
			continue
		}
		s, userID, segment := r.s, r.project.UserID, segment
		started = s.startDramaTail(segment.VideoResourceID, func() error {
			if err := s.backfillDramaSegmentTail(userID, segment); err != nil {
				slog.Warn("drama tail backfill failed", "project", segment.ProjectID, "segment", segment.ID, "error", err)
			}
			return nil
		}) || started
	}
	if started {
		// 后台执行器可能已同步跑完（测试），按最新状态推进本集。
		if segments, err = r.s.repo.DramaSegments(e.ID); err != nil {
			return nil, err
		}
	}
	return segments, nil
}

// enqueueParallelSegmentVideos 是并行出片的提交：段与段互不依赖，过期、失败或未请求的段只影响自己。
// r.enqueue 按作业键去重，已在队列或生成中的段不会重复提交。
func (r *dramaRun) enqueueParallelSegmentVideos(e *model.DramaEpisode, segments []model.DramaSegment, requestedOnly bool) (bool, error) {
	var shots []model.DramaShot
	ready := true
	for i, seg := range segments {
		if dramaSegmentVideoDone(seg) {
			continue
		}
		ready = false
		wanted := seg.VideoStatus == model.DramaMediaQueued || (!requestedOnly && seg.VideoStatus == "")
		if !wanted {
			continue
		}
		if shots == nil {
			loaded, err := r.s.repo.DramaShots(e.ID)
			if err != nil {
				return false, err
			}
			shots = loaded
		}
		if dramaVideoWaitReason(r.settings, r.production, e, segments, i, shots, r.castLookup) == "" {
			r.enqueue(model.DramaJobSegmentVideo, e.ID, seg.ID, "", segmentPriority(dramaRankVideo, e.Number, seg.SegmentIndex), fmt.Sprintf("第 %d 集 第 %d 段视频", e.Number, seg.SegmentIndex+1), nil)
		}
	}
	return ready, nil
}

func (r *dramaRun) castLookup(id string) (model.DramaCast, bool) {
	cast := r.castByID[id]
	if cast == nil {
		return model.DramaCast{}, false
	}
	return *cast, true
}

// dispatch 按优先级把排队工作提交为生成任务，受各并发池的同时生成数、账号并发与预算约束。
// 挡住提交的条件与 dramaDispatchWaits 同源，详情页和队列显示的等待原因就是这里的判断。
func (r *dramaRun) markRunning(job *model.DramaJob) error {
	var err error
	switch job.Kind {
	case model.DramaJobSegmentPrompt:
		var payload dramaRevisionPayload
		_ = json.Unmarshal([]byte(job.PayloadJSON), &payload)
		_, err = r.s.repo.UpdateDramaSegmentRevision(job.ShotID, payload.Revision, map[string]any{"prompt_status": model.DramaMediaRunning})
	case model.DramaJobCastImage:
		_, err = r.s.repo.UpdateDramaCastIf(job.CastID, []string{"", model.DramaMediaQueued}, map[string]any{"image_status": model.DramaMediaRunning})
	case model.DramaJobSegmentVideo:
		var payload dramaRevisionPayload
		_ = json.Unmarshal([]byte(job.PayloadJSON), &payload)
		_, err = r.s.repo.UpdateDramaSegmentRevision(job.ShotID, payload.Revision, map[string]any{"video_status": model.DramaMediaRunning})
	}
	return err
}

// ===== 结果回写 =====

func (r *dramaRun) applyJob(job *model.DramaJob, task *model.Task) error {
	switch job.Kind {
	case model.DramaJobBible:
		return r.applyBible(task)
	case model.DramaJobPlan:
		return r.applyPlan(job, task)
	case model.DramaJobScript:
		return r.applyScript(job, task)
	case model.DramaJobCastImage:
		return r.applyCastImage(job, task)
	case model.DramaJobSegmentVideo:
		return r.applySegmentVideo(job, task)
	case model.DramaJobRender:
		return r.applyRender(job, task)
	case model.DramaJobSegmentPrompt:
		return r.applySegmentPrompt(job, task)
	}
	return nil
}

func dramaTaskText(task *model.Task) (string, error) {
	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(task.ResultJSON), &result); err != nil || strings.TrimSpace(result.Text) == "" {
		return "", errors.New("模型没有返回文本结果")
	}
	return result.Text, nil
}

func (r *dramaRun) applyBible(task *model.Task) error {
	text, err := dramaTaskText(task)
	if err != nil {
		return err
	}
	bible, err := production.ParseBible(text)
	if err != nil {
		return fmt.Errorf("作品圣经解析失败：%w", err)
	}
	existing, err := r.s.repo.DramaCasts(r.project.ID)
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		now := time.Now()
		casts := make([]model.DramaCast, 0, len(bible.Characters)+len(bible.Props)+len(bible.Scenes))
		for index, character := range bible.Characters {
			instructions := strings.TrimSpace(strings.Join(nonEmpty(character.Gender, character.Age, character.Voice), "，"))
			characterID := newID()
			casts = append(casts, model.DramaCast{ID: characterID, ProjectID: r.project.ID, Kind: "character", Name: truncateRunes(character.Name, 60), AliasesJSON: encodeStringList(character.Aliases), Role: truncateRunes(character.Role, 60), Description: truncateRunes(character.Description, 1000), Appearance: truncateRunes(strings.Join(nonEmpty(character.Gender, character.Age, character.Appearance), "，"), 1000), VoiceInstructions: truncateRunes(instructions, 500), Importance: len(bible.Characters) - index, Position: index, CreatedAt: now, UpdatedAt: now})
			// 造型是角色的子行：承载这套造型的名称、外观与默认适用章节，名称/别名/声音沿用所属角色。
			for lookIndex, look := range character.Looks {
				casts = append(casts, model.DramaCast{ID: newID(), ProjectID: r.project.ID, Kind: "character", ParentID: characterID, Name: truncateRunes(character.Name, 60), LookName: truncateRunes(look.Name, 60), Appearance: truncateRunes(look.Appearance, 1000), LookFromChapter: int(look.FromChapter), LookToChapter: int(look.ToChapter), Position: index*100 + lookIndex + 1, CreatedAt: now, UpdatedAt: now})
			}
		}
		for index, prop := range bible.Props {
			casts = append(casts, model.DramaCast{ID: newID(), ProjectID: r.project.ID, Kind: "prop", Name: truncateRunes(prop.Name, 60), AliasesJSON: "[]", Description: truncateRunes(prop.Description, 1000), Appearance: truncateRunes(prop.Appearance, 1000), Position: index, CreatedAt: now, UpdatedAt: now})
		}
		for index, scene := range bible.Scenes {
			casts = append(casts, model.DramaCast{ID: newID(), ProjectID: r.project.ID, Kind: "scene", Name: truncateRunes(scene.Name, 60), AliasesJSON: "[]", Description: truncateRunes(scene.Description, 1000), Appearance: truncateRunes(scene.Appearance, 1000), Position: index, CreatedAt: now, UpdatedAt: now})
		}
		if err := r.s.repo.CreateDramaCasts(casts); err != nil {
			return err
		}
	}
	meta, _ := json.Marshal(dramaBibleMeta{Logline: bible.Logline, Tone: bible.Tone, VisualRules: bible.VisualRules})
	r.production.BibleJSON = string(meta)
	r.meta = parseDramaBibleMeta(string(meta))
	r.production.Stage = model.DramaStageProduce
	return nil
}

func nonEmpty(values ...string) []string {
	result := []string{}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, strings.TrimSpace(value))
		}
	}
	return result
}

func (r *dramaRun) applyPlan(job *model.DramaJob, task *model.Task) error {
	var payload dramaPlanPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		return err
	}
	if payload.Start != r.production.PlanCursor {
		// 游标已被其他回写推进，本次结果作废。
		return nil
	}
	text, err := dramaTaskText(task)
	if err != nil {
		return err
	}
	chapters, err := r.s.repo.DramaChapterSummaries(r.project.ID)
	if err != nil {
		return err
	}
	if payload.End > len(chapters) {
		payload.End = len(chapters)
	}
	batch := make([]production.Chapter, 0, payload.End-payload.Start)
	for index := payload.Start; index < payload.End; index++ {
		batch = append(batch, production.Chapter{ID: chapters[index].ID, Index: index + 1, Title: chapters[index].Title})
	}
	planned, err := production.ParsePlan(text, batch)
	if err != nil {
		return fmt.Errorf("分集规划解析失败：%w", err)
	}
	next, err := r.s.repo.MaxDramaEpisodeNumber(r.project.ID)
	if err != nil {
		return err
	}
	now := time.Now()
	episodes := make([]model.DramaEpisode, 0, len(planned))
	for _, item := range planned {
		next++
		ids := make([]string, 0, len(item.Chapters))
		for _, index := range item.Chapters {
			ids = append(ids, chapters[index-1].ID)
		}
		episodes = append(episodes, model.DramaEpisode{ID: newID(), ProjectID: r.project.ID, Number: next, Title: truncateRunes(item.Title, 60), Synopsis: truncateRunes(item.Synopsis, 1000), Hook: truncateRunes(item.Hook, 300), Cliffhanger: truncateRunes(item.Cliffhanger, 300), ChapterIDsJSON: encodeStringList(ids), SourceFormat: model.DramaSourceNovel, Stage: model.DramaEpisodeQueued, Revision: 1, CreatedAt: now, UpdatedAt: now})
	}
	expected := r.production.PlanCursor
	r.production.PlanCursor = payload.End
	r.production.PlanDone = payload.End >= len(chapters)
	return r.s.repo.AppendDramaEpisodes(r.production, episodes, expected)
}

// groupRemainingChapters 把尚未分集的章节按“每 N 章一集”直接建集，写入与模型规划同一游标 CAS。
func (r *dramaRun) groupRemainingChapters(chapters []model.ProjectUnit) error {
	next, err := r.s.repo.MaxDramaEpisodeNumber(r.project.ID)
	if err != nil {
		return err
	}
	episodes, err := groupDramaChapterEpisodes(r.project.ID, next+1, chapters[r.production.PlanCursor:], r.settings.ChaptersPerEpisode, model.DramaSourceNovel, time.Now())
	if err != nil {
		r.production.Status, r.production.PauseReason = model.DramaStatusPaused, "分集失败："+err.Error()
		return nil
	}
	expected := r.production.PlanCursor
	r.production.PlanCursor, r.production.PlanDone = len(chapters), true
	return r.s.repo.AppendDramaEpisodes(r.production, episodes, expected)
}

func (r *dramaRun) applyScript(job *model.DramaJob, task *model.Task) error {
	text, err := dramaTaskText(task)
	if err != nil {
		return err
	}
	scripted, err := production.ParseScript(text)
	if err != nil {
		return fmt.Errorf("分镜脚本解析失败：%w", err)
	}
	episode, err := r.s.repo.DramaEpisode(r.project.ID, job.EpisodeID)
	if err != nil {
		return err
	}
	var payload dramaRevisionPayload
	_ = json.Unmarshal([]byte(job.PayloadJSON), &payload)
	if episode.Stage != model.DramaEpisodeScript || episode.Revision != payload.Revision {
		return nil
	}
	return r.applyScriptShots(episode, scripted, production.ParseScriptCast(text), production.ParseScriptEpisode(text), false)
}

// applyDirectStoryboard 用户上传的分镜能直接解析时跳过文本模型，镜头与台词原样入库。返回是否已处理。
func (r *dramaRun) applyDirectStoryboard(e *model.DramaEpisode) (bool, error) {
	if production.NormalizeSourceFormat(e.SourceFormat) != production.SourceStoryboard {
		return false, nil
	}
	chapters, err := r.loadChapters(decodeStringList(e.ChapterIDsJSON))
	if err != nil || len(chapters) == 0 {
		return false, err
	}
	texts := make([]string, 0, len(chapters))
	for _, chapter := range chapters {
		texts = append(texts, chapter.Text)
	}
	scripted, ok := production.ParseStoryboardSource(strings.Join(texts, "\n"))
	if !ok {
		return false, nil
	}
	if err := r.applyScriptShots(e, scripted, nil, production.ScriptEpisode{}, true); err != nil {
		return true, r.s.repo.UpdateDramaEpisodeUser(e.ID, map[string]any{"error": "分镜导入失败：" + err.Error()})
	}
	return true, nil
}
