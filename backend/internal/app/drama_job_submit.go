package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"strings"
	"unicode/utf8"
	"yingce/backend/internal/model"
	"yingce/backend/internal/production"
	"yingce/backend/internal/repository"
)

// submit 在派发时构建任务请求；skip=true 表示目标已不再需要该工作。
func (r *dramaRun) submit(job *model.DramaJob) (*model.Task, bool, error) {
	switch job.Kind {
	case model.DramaJobBible:
		return r.submitBible(job)
	case model.DramaJobPlan:
		return r.submitPlan(job)
	case model.DramaJobScript:
		episode, err := r.s.repo.DramaEpisode(r.project.ID, job.EpisodeID)
		if err != nil {
			return nil, false, err
		}
		job.PayloadJSON = encodeDramaRevision(episode.Revision)
		return r.submitScript(job)
	case model.DramaJobCastImage:
		cast, err := r.s.repo.DramaCast(r.project.ID, job.CastID)
		if err != nil {
			return nil, false, err
		}
		job.PayloadJSON = encodeDramaRevision(cast.Revision)
		return r.submitCastImage(job)
	case model.DramaJobSegmentVideo:
		return r.submitSegmentVideo(job)
	case model.DramaJobRender:
		return r.submitRender(job)
	case model.DramaJobSegmentPrompt:
		return r.submitSegmentPrompt(job)
	}
	return nil, true, nil
}

// submitSegmentPrompt 让文本模型按镜头表把一个剧情段写成完整视频提示词。
func (r *dramaRun) submitSegmentPrompt(job *model.DramaJob) (*model.Task, bool, error) {
	segment, err := r.s.repo.DramaSegment(r.project.ID, job.ShotID)
	if err != nil {
		return nil, false, err
	}
	job.PayloadJSON = encodeDramaRevision(segment.Revision)
	if segment.PromptStatus != "" {
		return nil, true, nil
	}
	shots, err := r.s.repo.DramaShots(segment.EpisodeID)
	if err != nil {
		return nil, false, err
	}
	castByID := make(map[string]model.DramaCast, len(r.casts))
	for _, cast := range r.casts {
		castByID[cast.ID] = cast
	}
	rows, err := r.s.repo.DramaSegments(segment.EpisodeID)
	if err != nil {
		return nil, false, err
	}
	drafts := buildDramaSegmentDrafts(r.settings, r.meta, shots, castByID, rows)
	for position, draft := range drafts {
		if draft.view.Index != segment.SegmentIndex {
			continue
		}
		previous := ""
		if position > 0 {
			last := drafts[position-1].shots[len(drafts[position-1].shots)-1]
			previous = strings.TrimSpace(strings.Join([]string{last.Action, last.VideoPrompt, last.Dialogue}, "；"))
		}
		profile, err := r.s.dramaVideoProfile(segmentVideoChoice(*segment, r.settings.Models.Video))
		if err != nil {
			return nil, false, err
		}
		if draft.view.DurationMs > int64(profile.maxSeconds())*1000 {
			return nil, false, errors.New("剧情段时长超过所选模型上限")
		}
		seconds := production.SnapVideoSeconds(draft.view.DurationMs, profile.Durations)
		// 并行出片不带上一段尾帧，提示词不能写“首帧承接上一段”。
		draft.input.FirstFrame = position > 0 && !r.settings.parallelVideo()
		if len(draft.input.Shots) > 0 {
			draft.input.Shots[len(draft.input.Shots)-1].DurationMs += int64(seconds)*1000 - draft.view.DurationMs
		}
		maxChars, err := r.segmentPromptBudget(profile, draft, segment, rows, castByID)
		if err != nil {
			return nil, false, err
		}
		// 文本模型数字数常常超出要求，写作目标比硬上限再留一成；硬上限在结果回写时校验。
		draft.input.MaxChars = maxChars * 9 / 10
		payload, _ := json.Marshal(dramaRevisionPayload{Revision: segment.Revision, Seconds: seconds, MaxChars: maxChars})
		job.PayloadJSON = string(payload)
		skill, err := r.s.dramaWritingSkill(r.project.UserID, r.settings.PromptSkill, "视频提示词")
		if err != nil {
			return nil, false, err
		}
		prompt := production.SegmentWriterPrompt(draft.input, production.SegmentVideoPrompt(draft.input), previous, skill)
		task, err := r.createTaskWith(job, r.settings.Models.textFor(DramaStepPrompt), "text", "text", prompt, r.textInput(job, production.SegmentWriterSystemPrompt(), DramaStepPrompt))
		return task, false, err
	}
	return nil, true, nil
}

// dramaPromptCondenseAttempts 是段提示词超出视频模型上限时自动精简的次数上限。
const dramaPromptCondenseAttempts = 2

// dramaMinPromptBudget 是段提示词正文至少要有的字符数；参考图说明占掉太多时直接报错，不写一段注定被拒的提示词。
const dramaMinPromptBudget = 300

// segmentPromptBudget 是本段提示词正文的字符上限：视频模型提示词上限减去提交时加在开头的参考图说明。
// 参考图说明按当前参考图状态预演；之后参考图状态变化导致超长时，提交视频前会退回重写（见 submitSegmentVideo）。返回 0 表示不限。
func (r *dramaRun) segmentPromptBudget(profile dramaVideoProfile, draft dramaSegmentDraft, segment *model.DramaSegment, rows []model.DramaSegment, casts map[string]model.DramaCast) (int, error) {
	if profile.PromptMaxChars <= 0 {
		return 0, nil
	}
	var previous *model.DramaSegment
	for i := range rows {
		if rows[i].ID == segment.ID && i > 0 && !r.settings.parallelVideo() {
			previous = &rows[i-1]
		}
	}
	// 预演失败（如画幅不符）不阻止撰写：提交视频时会报出同一原因，届时也会再核对一次字数。
	request, _, _, _ := r.s.prepareDramaSegment(r.settings, draft, *segment, previous, casts)
	header := utf8.RuneCountInString(request.Header)
	budget := profile.PromptMaxChars - header - 2
	if budget < dramaMinPromptBudget {
		return 0, fmt.Errorf("开头的参考图说明占用 %d 个字符，所选视频模型的提示词上限只有 %d 个字符，放不下本段正文；请减少本段参考图，或换用提示词上限更大的视频模型", header, profile.PromptMaxChars)
	}
	return budget, nil
}

func (r *dramaRun) applySegmentPrompt(job *model.DramaJob, task *model.Task) error {
	text, err := dramaTaskText(task)
	if err != nil {
		return err
	}
	prompt := production.CleanWrittenPrompt(text)
	if utf8.RuneCountInString(prompt) < 50 {
		return errors.New("模型返回的视频提示词过短")
	}
	var payload dramaRevisionPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		return err
	}
	if chars := utf8.RuneCountInString(prompt); payload.MaxChars > 0 && chars > payload.MaxChars {
		if payload.Condensed < dramaPromptCondenseAttempts {
			return r.condenseSegmentPrompt(job, payload, prompt, chars)
		}
		// 自动精简后仍超长：保存这一版供手动精简，本段标记失败；不截断，也不提交注定被拒的视频。
		if _, err := r.s.repo.UpdateDramaSegmentRevision(job.ShotID, payload.Revision, map[string]any{"video_prompt": truncateRunes(prompt, 12000), "prompt_seconds": payload.Seconds}); err != nil {
			return err
		}
		return &dramaResultIssue{message: fmt.Sprintf("视频提示词 %d 个字符，自动精简 %d 次后仍超过本段上限 %d 个字符（视频模型上限减去开头的参考图说明）；请手动精简后保存，或重新撰写", chars, payload.Condensed, payload.MaxChars)}
	}
	_, err = r.s.repo.UpdateDramaSegmentRevision(job.ShotID, payload.Revision, map[string]any{"video_prompt": truncateRunes(prompt, 12000), "prompt_status": model.DramaMediaReady, "prompt_error": "", "prompt_seconds": payload.Seconds})
	return err
}

// condenseSegmentPrompt 让文本模型把超长的段提示词精简到上限内。同一作业换成精简任务继续运行，
// 撰写状态保持“撰写中”，精简结果回写时按同一上限再校验一次。
func (r *dramaRun) condenseSegmentPrompt(job *model.DramaJob, payload dramaRevisionPayload, prompt string, chars int) error {
	segment, err := r.s.repo.DramaSegment(r.project.ID, job.ShotID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if segment.Revision != payload.Revision {
		// 撰写期间镜头或参考图已改，这一版作废，调度会按新素材重写。
		return nil
	}
	// 计数随绑定一起落库；提交失败时恢复原值，不占用用户重试时的精简次数。
	previousPayload := job.PayloadJSON
	payload.Condensed++
	encoded, _ := json.Marshal(payload)
	job.PayloadJSON = string(encoded)
	task, err := r.createTaskWith(job, r.settings.Models.textFor(DramaStepPrompt), "text", "text", production.SegmentCondensePrompt(prompt, payload.MaxChars), r.textInput(job, production.SegmentCondenseSystemPrompt(), DramaStepPrompt))
	if err != nil {
		job.PayloadJSON = previousPayload
		if errors.Is(err, repository.ErrDramaJobChanged) {
			return &dramaSyncDeferred{err: err}
		}
		return &dramaResultIssue{message: fmt.Sprintf("视频提示词 %d 个字符，超过本段上限 %d 个字符，自动精简未能提交：%s", chars, payload.MaxChars, err.Error())}
	}
	return &dramaJobContinued{taskID: task.ID}
}

func (r *dramaRun) metadata(job *model.DramaJob) map[string]any {
	return map[string]any{"source": "drama_production", "dramaJobId": job.ID, "domainProjectId": r.project.ID, "episodeId": job.EpisodeID, "shotId": job.ShotID}
}

func (r *dramaRun) createTask(job *model.DramaJob, capability string, operation string, prompt string, input map[string]any) (*model.Task, error) {
	return r.createTaskWith(job, r.settings.Models.choice(capability), capability, operation, prompt, input)
}

func (r *dramaRun) createTaskWith(job *model.DramaJob, choice DramaModelChoice, capability string, operation string, prompt string, input map[string]any) (*model.Task, error) {
	if choice.Empty() {
		return nil, fmt.Errorf("没有可用的%s模型", map[string]string{"text": "文本", "image": "图片", "video": "视频"}[capability])
	}
	config, _ := input["config"].(map[string]any)
	if config == nil {
		config = map[string]any{}
	}
	if choice.ChannelID != "" {
		config["channelId"], config["model"], config["channelModelKey"] = choice.ChannelID, choice.ModelKey, choice.ModelKey
	}
	input["config"], input["mode"], input["prompt"] = config, capability, prompt
	req := CreateTaskRequest{ProjectID: r.project.ID, Type: "canvas_" + capability, Operation: operation, Prompt: prompt, LogicalModelID: choice.LogicalModelID, Model: choice.ModelKey, Input: input}
	if r.s.dramaTaskHook != nil {
		return r.s.dramaTaskHook(r.project.UserID, req, nil)
	}
	r.submitting = job
	task, err := r.submitDramaPlatformTask(req)
	// 准入只说“指定的模型不存在”；作品里可能选了多个模型，点名是哪一个、去哪里改。
	var modelErr *ModelError
	if errors.As(err, &modelErr) && modelErr.ErrorCode == ErrCodeInvalidModelSelection {
		return nil, dramaModelUnavailable(capability, choice, "不可用（"+modelErr.Error()+"）")
	}
	return task, err
}

// imageRequest 把某类参考图的期望画幅与分辨率换算到该类所用图片模型真正支持的尺寸；一轮调度内按模型、画幅与档位缓存。
func (r *dramaRun) imageRequest(kind string) dramaImageRequest {
	if r.imageSizes == nil {
		r.imageSizes = map[string]dramaImageRequest{}
	}
	choice, ratio, tier := r.settings.Models.imageFor(kind), r.castImageRatio(kind), r.settings.Assets.ResolutionFor(kind)
	key := dramaChoiceCacheKey(choice) + "|" + ratio + "|" + tier
	request, cached := r.imageSizes[key]
	if !cached {
		request = r.s.dramaImageRequestFor(choice, ratio, tier)
		r.imageSizes[key] = request
	}
	return request
}

func dramaChoiceCacheKey(choice DramaModelChoice) string {
	if choice.LogicalModelID != "" {
		return "l:" + choice.LogicalModelID
	}
	return "c:" + choice.ChannelID + ":" + choice.ModelKey
}

// castImageRatio 是设定参考图的期望画幅：场景与道具统一横版 16:9，角色按参考图方案。
func (r *dramaRun) castImageRatio(kind string) string {
	if kind == "character" {
		return r.settings.Assets.CharacterRatio
	}
	return production.AssetImageRatio
}
