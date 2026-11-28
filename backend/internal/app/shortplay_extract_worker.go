package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

// 短剧资产提取是 4AI 里的普通文本任务：提交走 CreateTask（模型选择、准入、计费与出站
// 控制都在那里），结果从任务行读回。调度只负责「领 job → 提交一次 → 读结果 → 落库」，
// 不自己发模型 HTTP 请求，避免绕开 4AI 的渠道、计费与出站合同。

// shortplayExtractTickInterval 是常驻提取调度的轮询间隔；与 drama 调度同量级。
const (
	shortplayExtractTickInterval = 5 * time.Second
	shortplayExtractPerTick      = 4
	// shortplayExtractMaxOutputTokens 是资产提取任务的输出预算：一次要读完整个批次并
	// 输出结构化资产清单，给足余量但不超过平台单任务上限。
	shortplayExtractMaxOutputTokens = 8192
)

// startShortplayExtractWorker 是短剧后台调度：提取与出图共用一条循环。
func (s *Service) startShortplayExtractWorker() {
	s.runWorkerLoop(func(ctx context.Context) {
		ticker := time.NewTicker(shortplayExtractTickInterval)
		defer ticker.Stop()
		for {
			s.tickShortplayExtract(ctx)
			s.tickShortplayAssetImages(ctx)
			s.tickShortplayAssetAudio(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}

// tickShortplayExtract 领取并推进至多 shortplayExtractPerTick 条提取 job；
// 单条失败只记录日志，循环继续（失败已由 RunShortplayExtractOnce 写入 job）。
func (s *Service) tickShortplayExtract(ctx context.Context) {
	if !s.shortplayCanDispatch(ctx) {
		return
	}
	for i := 0; i < shortplayExtractPerTick; i++ {
		if !s.shortplayCanDispatch(ctx) {
			return
		}
		jobID, err := s.RunShortplayExtractOnce(ctx)
		if err != nil {
			slog.Warn("shortplay extract failed", "jobId", jobID, "error", err)
			return
		}
		if jobID == "" {
			return
		}
	}
}

// QueueShortplayExtract 入队一次提取；已有在途提取时返回那一条（queued=false），
// 游标取最近一次成功 job 的 payload，保证「中断后继续」而不是从头重跑。
func (s *Service) QueueShortplayExtract(userID, idOrProject string) (shortplay.ExtractQueueResult, error) {
	owner, err := s.shortplayRowForUser(userID, idOrProject)
	if err != nil {
		return shortplay.ExtractQueueResult{}, err
	}
	if active, err := s.repo.ActiveShortplayJob(owner.ID, shortplay.JobExtract); err != nil {
		return shortplay.ExtractQueueResult{}, err
	} else if active != nil {
		return shortplay.ExtractQueueResult{JobID: active.ID, Cursor: shortplay.ParseExtractCursor(active.PayloadJSON)}, nil
	}
	cursor := 0
	if last, err := s.repo.LatestShortplayJob(owner.ID, shortplay.JobExtract, shortplay.JobSucceeded); err != nil {
		return shortplay.ExtractQueueResult{}, err
	} else if last != nil {
		cursor = shortplay.ParseExtractCursor(last.PayloadJSON)
	}
	job := &model.ShortplayJob{
		ID:          newID(),
		ShortplayID: owner.ID,
		Kind:        shortplay.JobExtract,
		Status:      shortplay.JobQueued,
		DedupeKey:   shortplay.JobDedupeKey(shortplay.JobExtract, owner.ID),
		PayloadJSON: shortplay.ExtractCursorPayload(cursor),
	}
	if err := s.repo.CreateShortplayJob(job); err != nil {
		if isShortplayUniqueViolation(err) {
			if active, findErr := s.repo.ActiveShortplayJob(owner.ID, shortplay.JobExtract); findErr == nil && active != nil {
				return shortplay.ExtractQueueResult{JobID: active.ID, Cursor: shortplay.ParseExtractCursor(active.PayloadJSON)}, nil
			}
		}
		return shortplay.ExtractQueueResult{}, err
	}
	return shortplay.ExtractQueueResult{JobID: job.ID, Queued: true, Cursor: cursor}, nil
}

// shortplayExtractRun 是一轮执行结果；Status 为空表示仍在途（下一 tick 继续读任务）。
type shortplayExtractRun struct {
	Status    string
	LastError string
	Payload   string
}

// RunShortplayExtractOnce 推进一条提取 job：优先把已提交任务读结果，没有在途任务时
// 领取一条 queued job 并提交；两头都没有返回空 jobID 与 nil。
func (s *Service) RunShortplayExtractOnce(ctx context.Context) (string, error) {
	if !s.shortplayCanDispatch(ctx) {
		return "", nil
	}
	orphan, err := s.takeOrphanedShortplayJob(ctx, shortplay.JobExtract)
	if err != nil {
		return "", err
	}
	if orphan != nil {
		return s.advanceShortplayExtract(orphan)
	}
	if active, err := s.repo.RunningShortplayJob(shortplay.JobExtract); err != nil {
		return "", err
	} else if active != nil {
		return s.advanceShortplayExtract(active)
	}
	if !s.shortplayCanDispatch(ctx) {
		return "", nil
	}
	job, err := s.repo.ClaimShortplayJob(shortplay.JobExtract)
	if err != nil || job == nil {
		return "", err
	}
	return s.advanceShortplayExtract(job)
}

// advanceShortplayExtract 跑一轮并收口；在途任务已在创建事务内绑定。
func (s *Service) advanceShortplayExtract(job *model.ShortplayJob) (string, error) {
	run := s.runShortplayExtractJob(job)
	if run.Status == "" {
		return job.ID, nil
	}
	return job.ID, s.finishShortplayJob(job, run.Status, run.LastError, run.Payload)
}

// runShortplayExtractJob 按是否已提交分派：TaskID 为空 → 提交；非空 → 读结果落库。
func (s *Service) runShortplayExtractJob(job *model.ShortplayJob) shortplayExtractRun {
	owner, err := s.repo.ShortplayByID(job.ShortplayID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return shortplayExtractRun{Status: shortplay.JobFailed, LastError: "短剧不存在或已删除"}
	}
	if err != nil {
		return shortplayExtractRun{}
	}
	project, err := s.repo.ProjectForUser(owner.UserID, owner.ProjectID)
	if err != nil {
		return shortplayExtractRun{Status: shortplay.JobFailed, LastError: "作品容器行不存在，无法提交提取任务"}
	}
	if strings.TrimSpace(job.TaskID) == "" {
		return s.submitShortplayExtract(owner, project, job)
	}
	return s.applyShortplayExtract(owner, job)
}

// submitShortplayExtract 提交一次文本任务，把 taskId 与当前游标挂到 job 上。
func (s *Service) submitShortplayExtract(owner *model.Shortplay, project *model.Project, job *model.ShortplayJob) shortplayExtractRun {
	episodes, err := s.repo.ShortplayEpisodes(owner.ID)
	if err != nil {
		return shortplayExtractRun{}
	}
	cursor := shortplay.ParseExtractCursor(job.PayloadJSON)
	batch, next := shortplay.NextEpisodeBatch(episodes, cursor, 0)
	if len(batch) == 0 {
		// 游标已越过最后一集（例如续跑时集被删）：没有可提交的工作，直接收尾。
		return shortplayExtractRun{Status: shortplay.JobSucceeded, Payload: shortplay.ExtractCursorPayload(next)}
	}
	assets, err := s.repo.ShortplayAssets(owner.ID)
	if err != nil {
		return shortplayExtractRun{}
	}
	settings := parseShortplaySettings(owner.SettingsJSON)
	title := project.Name
	if strings.TrimSpace(title) == "" {
		title = owner.ProjectID
	}
	prompt := shortplay.ExtractPrompt(title, settings.Settings, batch, len(episodes), shortplayAssetHints(assets), nil)
	choice, err := s.shortplayTextModel(owner)
	if err != nil {
		return shortplayExtractRun{Status: shortplay.JobFailed, LastError: err.Error()}
	}
	// config 同时写 systemPrompt 与渠道选择：渠道模型只有 channelId + modelKey，缺了
	// 准入会判「必须指定系统渠道和模型」。
	config := map[string]any{"systemPrompt": shortplay.ExtractSystemPrompt}
	applyShortplayChannelSelection(config, choice)
	input := map[string]any{
		"config": config,
		"mode":   "text",
		// 资产提取要一次读完整部正文，固定高档位思考。
		"textOptions": map[string]any{"stream": true, "thinkingLevel": ThinkingLevelHigh, "maxOutputTokens": shortplayExtractMaxOutputTokens},
		"metadata":    map[string]any{"source": "shortplay_extract", "domainProjectId": project.ID, "shortplayJobId": job.ID},
	}
	_, err = s.submitShortplayTaskForJob(job, project.UserID, CreateTaskRequest{
		ProjectID:      project.ID,
		Type:           "canvas_text",
		Operation:      "text",
		Prompt:         prompt,
		LogicalModelID: choice.LogicalModelID,
		Model:          choice.ModelKey,
		Input:          input,
	}, shortplay.ExtractCursorPayload(cursor))
	if err != nil {
		return shortplayExtractRun{Status: shortplay.JobFailed, LastError: "提交提取任务失败：" + err.Error()}
	}
	return shortplayExtractRun{}
}

// applyShortplayExtract 读任务结果：成功时解析并落库，失败时记录上游原因。
// 读库失败不算任务丢失，保持 pending 下一轮重读（结果可能已经计费）。
func (s *Service) applyShortplayExtract(owner *model.Shortplay, job *model.ShortplayJob) shortplayExtractRun {
	task, err := s.repo.Task(strings.TrimSpace(job.TaskID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return shortplayExtractRun{Status: shortplay.JobFailed, LastError: "任务记录丢失"}
	}
	if err != nil {
		return shortplayExtractRun{}
	}
	switch task.Status {
	case model.TaskStatusSucceeded:
	case model.TaskStatusFailed, model.TaskStatusCancelled:
		return shortplayExtractRun{Status: shortplay.JobFailed, LastError: firstNonEmpty(task.Error, "生成失败")}
	case model.TaskStatusSubmissionUncertain:
		return shortplayExtractRun{Status: shortplay.JobFailed, LastError: "上游提交结果待核对，未自动重发"}
	default:
		return shortplayExtractRun{}
	}
	text := taskResultText(task.ResultJSON)
	if strings.TrimSpace(text) == "" {
		return shortplayExtractRun{Status: shortplay.JobFailed, LastError: "模型没有返回文本结果"}
	}
	parsed, err := shortplay.ParseExtractAssets(text)
	if err != nil {
		return shortplayExtractRun{Status: shortplay.JobFailed, LastError: "资产提取结果解析失败：" + err.Error()}
	}
	next, err := s.applyShortplayExtractAssets(owner, job, parsed)
	if err != nil {
		return shortplayExtractRun{Status: shortplay.JobFailed, LastError: err.Error()}
	}
	return shortplayExtractRun{Status: shortplay.JobSucceeded, Payload: shortplay.ExtractCursorPayload(next)}
}

// applyShortplayExtractAssets 把一批解析结果落成资产行并回写每集的 assetIdsJSON，
// 返回推进后的游标。锁定行只并别名与分集绑定，不覆盖设定（由 PlanExtractApply 保证）。
func (s *Service) applyShortplayExtractAssets(owner *model.Shortplay, job *model.ShortplayJob, parsed []shortplay.ExtractedAsset) (int, error) {
	episodes, err := s.repo.ShortplayEpisodes(owner.ID)
	if err != nil {
		return 0, err
	}
	cursor := shortplay.ParseExtractCursor(job.PayloadJSON)
	batch, next := shortplay.NextEpisodeBatch(episodes, cursor, 0)
	if len(batch) == 0 {
		return next, nil
	}
	assets, err := s.repo.ShortplayAssets(owner.ID)
	if err != nil {
		return 0, err
	}
	nextID, err := s.repo.NextShortplayAssetID(owner.ID)
	if err != nil {
		return 0, err
	}
	plan := shortplay.PlanExtractApply(owner.ID, assets, parsed, batch, nextID)
	existing := make(map[int]model.ShortplayAsset, len(assets))
	for _, row := range assets {
		existing[row.ID] = row
	}
	persisted := make([]model.ShortplayAsset, 0, len(plan.Rows))
	for _, row := range plan.Rows {
		if prior, ok := existing[row.ID]; ok {
			row.Revision = prior.Revision
			updated, updateErr := s.repo.UpdateShortplayAsset(&row, prior.Revision)
			if updateErr != nil {
				return 0, updateErr
			}
			if !updated {
				return 0, errors.New("资产在提取过程中被其他操作修改，请稍后重试")
			}
			persisted = append(persisted, row)
			continue
		}
		if createErr := s.repo.CreateShortplayAsset(&row); createErr != nil {
			return 0, createErr
		}
		persisted = append(persisted, row)
	}
	for _, episode := range batch {
		binding := mergeShortplayInts(shortplayIntList(episode.AssetIDsJSON), episodeAssetIDs(persisted, episode.Number))
		if err := s.repo.UpdateShortplayEpisodeAssetIDs(episode.ID, encodeShortplayInts(binding)); err != nil {
			return 0, err
		}
	}
	// 落库后把本集资产投成画布卡：幂等、无差异不写画布；失败只记录，下一次提取再对齐。
	s.injectShortplayAssetCards(owner.ID)
	return next, nil
}

// shortplayTextModel 取提取实际使用的文本模型：设置里的 bibleText 优先、其次 text，
// 都没选时按平台目录第一个可用项兜底。模型选择与目录解析沿用 4AI 既有合同。
func (s *Service) shortplayTextModel(owner *model.Shortplay) (shortplayModelChoice, error) {
	models := parseShortplaySettings(owner.SettingsJSON).Models
	if choice := models.extractText(); !choice.empty() {
		return choice, nil
	}
	options, _, err := s.dramaModelOptions(owner.UserID, "text")
	if err != nil {
		return shortplayModelChoice{}, errors.New("读取文本模型失败：" + err.Error())
	}
	if len(options) == 0 {
		return shortplayModelChoice{}, errors.New("没有可用的文本模型，无法提取资产")
	}
	return shortplayModelChoice{
		LogicalModelID: options[0].Choice.LogicalModelID,
		ChannelID:      options[0].Choice.ChannelID,
		ModelKey:       options[0].Choice.ModelKey,
	}, nil
}

// shortplayAssetHints 把现有资产压成提示词清单身份行（id/kind/name/aliases）。
func shortplayAssetHints(assets []model.ShortplayAsset) []shortplay.AssetHint {
	hints := make([]shortplay.AssetHint, 0, len(assets))
	for _, row := range assets {
		hints = append(hints, shortplay.AssetHint{
			ID:      row.ID,
			Kind:    row.Kind,
			Name:    row.Name,
			Aliases: shortplay.SplitAliasesJSON(row.AliasesJSON),
		})
	}
	return hints
}

// episodeAssetIDs 取出「这一集绑定了」的资产序号：按计划行里的分集绑定判断。
func episodeAssetIDs(rows []model.ShortplayAsset, episodeNumber int) []int {
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		for _, number := range shortplay.SplitEpisodeNumbersJSON(row.EpisodeNumbersJSON) {
			if number == episodeNumber {
				ids = append(ids, row.ID)
				break
			}
		}
	}
	sort.Ints(ids)
	return ids
}

func mergeShortplayInts(existing, incoming []int) []int {
	seen := make(map[int]struct{}, len(existing)+len(incoming))
	result := make([]int, 0, len(existing)+len(incoming))
	for _, group := range [][]int{existing, incoming} {
		for _, value := range group {
			if value <= 0 {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	sort.Ints(result)
	return result
}

func encodeShortplayInts(values []int) string {
	raw, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(raw)
}
