package app

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"yingce/backend/internal/model"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

// ShortplayExtractFunc 执行一次提取模型调用：给定系统提示词与正文提示词，返回模型原始回复。
// W04 只接调度与落库，真实模型渠道在执行器实现里注入，调度层不做渠道判断。
type ShortplayExtractFunc func(ctx context.Context, systemPrompt, userPrompt string) (string, error)

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

// RunShortplayExtractOnce 领取并执行一条提取 job；没有可领取的返回空 jobID 与 nil。
// 成功时把游标推进到下一批，失败时写 failed 与错误摘要，供收件箱定位。
func (s *Service) RunShortplayExtractOnce(ctx context.Context, extract ShortplayExtractFunc) (string, error) {
	job, err := s.repo.ClaimShortplayJob(shortplay.JobExtract)
	if err != nil || job == nil {
		return "", err
	}
	if extract == nil {
		failure := errors.New("提取执行器未配置")
		_ = s.repo.FinishShortplayJob(job.ID, shortplay.JobFailed, failure.Error(), "")
		return job.ID, failure
	}
	next, err := s.runShortplayExtractJob(ctx, job, extract)
	if err != nil {
		_ = s.repo.FinishShortplayJob(job.ID, shortplay.JobFailed, err.Error(), shortplay.ExtractCursorPayload(shortplay.ParseExtractCursor(job.PayloadJSON)))
		return job.ID, err
	}
	return job.ID, s.repo.FinishShortplayJob(job.ID, shortplay.JobSucceeded, "", shortplay.ExtractCursorPayload(next))
}

func (s *Service) runShortplayExtractJob(ctx context.Context, job *model.ShortplayJob, extract ShortplayExtractFunc) (int, error) {
	owner, err := s.repo.ShortplayByID(job.ShortplayID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, errors.New("短剧不存在或已删除")
	}
	if err != nil {
		return 0, err
	}
	episodes, err := s.repo.ShortplayEpisodes(owner.ID)
	if err != nil {
		return 0, err
	}
	batch, next := shortplay.NextEpisodeBatch(episodes, shortplay.ParseExtractCursor(job.PayloadJSON), 0)
	if len(batch) == 0 {
		return 0, nil
	}
	assets, err := s.repo.ShortplayAssets(owner.ID)
	if err != nil {
		return 0, err
	}
	settings := parseShortplaySettings(owner.SettingsJSON)
	title := owner.ProjectID
	if project, lookupErr := s.repo.ProjectForUser(owner.UserID, owner.ProjectID); lookupErr == nil && strings.TrimSpace(project.Name) != "" {
		title = project.Name
	}
	prompt := shortplay.ExtractPrompt(title, settings.Settings, batch, len(episodes), shortplayAssetHints(assets), nil)
	raw, err := extract(ctx, shortplay.ExtractSystemPrompt, prompt)
	if err != nil {
		return 0, err
	}
	parsed, err := shortplay.ParseExtractAssets(raw)
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
	return next, nil
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
