package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"net/http"
	"strings"
	"time"
	"yingce/backend/internal/model"
	"yingce/backend/internal/production"
)

func (s *Service) dramaForUser(userID string, projectID string) (*model.Project, *model.DramaProduction, error) {
	project, err := s.repo.ProjectForUser(userID, strings.TrimSpace(projectID))
	if err != nil {
		return nil, nil, err
	}
	productionRow, err := s.repo.DramaProduction(project.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, NewAppError(http.StatusNotFound, "该项目不是自动制作短剧")
		}
		return nil, nil, err
	}
	return project, productionRow, nil
}

// DramaListRequest 是作品列表的分页与筛选条件；status 为空表示全部。
type DramaListRequest struct {
	Page     int
	PageSize int
	Query    string
	Status   string
}

func (s *Service) ListDramas(userID string, req DramaListRequest) (map[string]any, error) {
	switch req.Status {
	case "", model.DramaStatusDraft, model.DramaStatusRunning, model.DramaStatusPaused, model.DramaStatusCompleted:
	default:
		return nil, BadAuthRequest("状态筛选无效")
	}
	projects, total, err := s.repo.DramaProjectsPage(userID, truncateRunes(req.Query, 60), req.Status, req.Page, req.PageSize)
	if err != nil {
		return nil, err
	}
	statusCounts, err := s.repo.DramaStatusCounts(userID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(projects))
	for _, project := range projects {
		ids = append(ids, project.ID)
	}
	productions, err := s.repo.DramaProductionsForUser(userID, ids)
	if err != nil {
		return nil, err
	}
	counts, err := s.repo.DramaEpisodeCounts(ids)
	if err != nil {
		return nil, err
	}
	covers, err := s.repo.DramaCoverResources(ids, 4)
	if err != nil {
		return nil, err
	}
	spends, err := s.repo.DramaBillingTotals(userID, ids)
	if err != nil {
		return nil, err
	}
	items := make([]DramaListItem, 0, len(projects))
	for _, project := range projects {
		productionRow, ok := productions[project.ID]
		if !ok {
			continue
		}
		count := counts[project.ID]
		settings := parseDramaSettings(productionRow.SettingsJSON)
		item := DramaListItem{
			Project: project, Status: productionRow.Status, Stage: productionRow.Stage, PauseReason: productionRow.PauseReason,
			EpisodeCount: count[0], DoneCount: count[1], CoverResourceIDs: covers[project.ID], AspectRatio: settings.AspectRatio,
			StyleName: settings.StyleName, SpentMicrocredits: spends[project.ID].ChargedMicrocredits(), HeldMicrocredits: spends[project.ID].HeldMicrocredits(),
			UpdatedAt: project.UpdatedAt.Format(time.RFC3339), HasDraftForm: productionRow.Status == model.DramaStatusDraft && productionRow.DraftJSON != "",
		}
		if len(item.CoverResourceIDs) > 0 {
			item.CoverResourceID = item.CoverResourceIDs[0]
		}
		items = append(items, item)
	}
	var all int64
	for _, count := range statusCounts {
		all += count
	}
	statusCounts["all"] = all
	return map[string]any{"items": items, "page": req.Page, "pageSize": req.PageSize, "total": total, "hasMore": int64(req.Page*req.PageSize) < total, "statusCounts": statusCounts}, nil
}

func (s *Service) DramaSummary(userID string, projectID string) (DramaSummary, error) {
	project, productionRow, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return DramaSummary{}, err
	}
	outline, err := s.repo.DramaChapterOutline(project.ID)
	if err != nil {
		return DramaSummary{}, err
	}
	episodes, err := s.repo.DramaEpisodes(project.ID)
	if err != nil {
		return DramaSummary{}, err
	}
	stats, err := s.repo.DramaSegmentStats(project.ID)
	if err != nil {
		return DramaSummary{}, err
	}
	jobCounts, err := s.repo.DramaJobCounts(project.ID)
	if err != nil {
		return DramaSummary{}, err
	}
	casts, err := s.repo.DramaCasts(project.ID)
	if err != nil {
		return DramaSummary{}, err
	}
	chapterCount, words, reservedCount, reservedWords := 0, 0, 0, 0
	for _, chapter := range outline {
		if chapter.Status == model.ProjectUnitStatusReserved {
			reservedCount++
			reservedWords += chapter.WordCount
			continue
		}
		chapterCount++
		words += chapter.WordCount
	}
	spend, err := s.dramaBillingTotals(productionRow)
	if err != nil {
		return DramaSummary{}, err
	}
	meta := parseDramaBibleMeta(productionRow.BibleJSON)
	summary := DramaSummary{
		Project: *project,
		Production: DramaProductionView{
			Status: productionRow.Status, Stage: productionRow.Stage, PauseReason: productionRow.PauseReason,
			Settings: parseDramaSettings(productionRow.SettingsJSON), BudgetMicrocredits: productionRow.BudgetMicrocredits,
			SpentMicrocredits: spend.ChargedMicrocredits(), CanvasSpentMicrocredits: spend.CanvasChargedMicrocredits, HeldMicrocredits: spend.HeldMicrocredits(),
			PlanDone: productionRow.PlanDone, PlanCursor: productionRow.PlanCursor, HasDraftForm: productionRow.Status == model.DramaStatusDraft && productionRow.DraftJSON != "",
			SampleApproved: productionRow.SampleApproved, AssetsReleased: productionRow.AssetsReleased, Logline: meta.Logline, Tone: meta.Tone,
			ChapterCount: chapterCount, WordCount: words, ReservedChapterCount: reservedCount, ReservedWordCount: reservedWords,
		},
		Episodes:  make([]DramaEpisodeRow, 0, len(episodes)),
		JobCounts: jobCounts,
		Inbox:     []DramaInboxItem{},
		CastCount: len(casts),
	}
	settings := summary.Production.Settings
	awaiting := 0
	for _, episode := range episodes {
		stat := stats[episode.ID]
		held := dramaMediaHeld(settings, &episode)
		if held {
			awaiting++
		}
		summary.Episodes = append(summary.Episodes, DramaEpisodeRow{
			ID: episode.ID, Number: episode.Number, Title: episode.Title, Synopsis: truncateRunes(episode.Synopsis, 120), Stage: episode.Stage, SourceFormat: production.NormalizeSourceFormat(episode.SourceFormat), Error: episode.Error,
			ShotCount: episode.ShotCount, DurationMs: episode.DurationMs, Approved: episode.Approved, MediaReleased: episode.MediaReleased, AwaitingMedia: held,
			FinalResourceID: episode.FinalResourceID, FinalStale: episode.FinalStale,
			Stats:    DramaEpisodeStats{Segments: stat.Total, PromptReady: stat.PromptReady, VideoReady: stat.VideoReady, VideoGenerated: stat.VideoGenerated, Failed: stat.Failed},
			Revision: episode.Revision, UpdatedAt: episode.UpdatedAt.Format(time.RFC3339),
		})
		if episode.Stage == model.DramaEpisodeReview {
			summary.Inbox = append(summary.Inbox, DramaInboxItem{ID: "review:" + episode.ID, Kind: "review", Severity: "info", Title: "第 " + itoa(episode.Number) + " 集视频提示词待审阅", Detail: "确认剧情段提示词后继续生成视频", EpisodeID: episode.ID})
		}
		if episode.Error != "" {
			summary.Inbox = append(summary.Inbox, DramaInboxItem{ID: "episode:" + episode.ID, Kind: "episode_error", Severity: "error", Title: "第 " + itoa(episode.Number) + " 集：" + episode.Title, Detail: episode.Error, EpisodeID: episode.ID})
		}
	}
	if awaiting > 0 {
		summary.Inbox = append([]DramaInboxItem{{ID: "media-ready", Kind: "media_ready", Severity: "info", Title: itoa(awaiting) + " 集剧本与分镜已就绪", Detail: "确认剧情、分镜与角色设定后再生成画面；可逐集生成，也可一次全部开始"}}, summary.Inbox...)
	}
	if productionRow.PauseReason != "" {
		summary.Inbox = append([]DramaInboxItem{{ID: "production", Kind: "paused", Severity: "warning", Title: "制作已暂停", Detail: productionRow.PauseReason}}, summary.Inbox...)
	}
	if err := s.fillDramaSummaryInbox(project, productionRow, &summary, casts, episodes, settings); err != nil {
		return DramaSummary{}, err
	}

	return summary, nil
}

func (s *Service) DramaEpisodeDetail(userID string, projectID string, episodeID string) (DramaEpisodeDetail, error) {
	project, productionRow, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return DramaEpisodeDetail{}, err
	}
	episode, err := s.repo.DramaEpisode(project.ID, episodeID)
	if err != nil {
		return DramaEpisodeDetail{}, err
	}
	shots, err := s.repo.DramaShots(episode.ID)
	if err != nil {
		return DramaEpisodeDetail{}, err
	}
	chapterIDs := decodeStringList(episode.ChapterIDsJSON)
	chapters, err := s.repo.DramaChaptersByIDs(project.ID, chapterIDs)
	if err != nil {
		return DramaEpisodeDetail{}, err
	}
	refs := make([]DramaChapterRef, 0, len(chapters))
	for _, chapter := range chapters {
		refs = append(refs, DramaChapterRef{ID: chapter.ID, Title: chapter.Title, Position: chapter.Position, WordCount: chapter.WordCount})
	}
	jobs, err := s.repo.DramaJobsByStatus(project.ID, model.DramaJobQueued, model.DramaJobRunning)
	if err != nil {
		return DramaEpisodeDetail{}, err
	}
	views := []DramaJobView{}
	for _, job := range jobs {
		if job.EpisodeID == episode.ID && len(views) < dramaEpisodeJobsLimit {
			views = append(views, dramaJobView(job))
		}
	}
	detail := DramaEpisodeDetail{Episode: *episode, Shots: shots, Chapters: refs, Jobs: views}
	if job, jobErr := s.repo.LatestDramaJob(project.ID, model.DramaJobScript, episode.ID); jobErr == nil {
		view := dramaJobView(*job)
		detail.ScriptJob = &view
	}
	if err := s.fillDramaSegmentViews(project.ID, productionRow, &detail); err != nil {
		return DramaEpisodeDetail{}, err
	}
	if detail.DialogueCheck, err = s.dramaDialogueCheck(project.ID, episode, chapters, shots); err != nil {
		return DramaEpisodeDetail{}, err
	}
	return detail, nil
}

func dramaJobView(job model.DramaJob) DramaJobView {
	view := DramaJobView{ID: job.ID, Kind: job.Kind, Status: job.Status, Label: job.Label, Error: job.Error, EpisodeID: job.EpisodeID, ShotID: job.ShotID, TaskID: job.TaskID, CostMicrocredits: job.CostMicrocredits, Attempt: job.Attempt, CreatedAt: job.CreatedAt.Format(time.RFC3339), UpdatedAt: job.UpdatedAt.Format(time.RFC3339)}
	if job.StartedAt != nil {
		view.StartedAt = job.StartedAt.Format(time.RFC3339)
	}
	if job.FinishedAt != nil {
		view.FinishedAt = job.FinishedAt.Format(time.RFC3339)
	}
	return view
}

// dramaJobTaskParams 是队列详情可展示的请求参数白名单；渠道地址、密钥等配置不回显。
var dramaJobTaskParams = []string{"size", "imageResolution", "quality", "count", "videoSeconds", "vquality", "videoGenerateAudio"}

// DramaJobDetail 返回一项调度工作的历次提交：时间、模型、参数、状态、花费、错误与产出。
func (s *Service) DramaJobDetail(userID string, projectID string, jobID string) (DramaJobDetail, error) {
	project, productionRow, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return DramaJobDetail{}, err
	}
	job, err := s.repo.DramaJob(project.ID, jobID)
	if err != nil {
		return DramaJobDetail{}, err
	}
	var attempts []DramaJobAttempt
	_ = json.Unmarshal([]byte(job.AttemptsJSON), &attempts)
	if job.TaskID != "" && (len(attempts) == 0 || attempts[len(attempts)-1].TaskID != job.TaskID) {
		attempts = append(attempts, DramaJobAttempt{TaskID: job.TaskID, Status: "running", CostMicrocredits: job.CostMicrocredits, At: dramaTime(job.StartedAt, job.UpdatedAt)})
	}
	labels := dramaModelLabels(parseDramaSettings(productionRow.SettingsJSON).Models)
	detail := DramaJobDetail{Job: dramaJobView(*job), Target: s.dramaJobTarget(project.ID, job), Attempts: make([]DramaJobAttemptView, 0, len(attempts))}
	// 最新一次排在最前，用户打开详情首先看到的是当前状态。
	for index := len(attempts) - 1; index >= 0; index-- {
		view := DramaJobAttemptView{DramaJobAttempt: attempts[index]}
		if view.TaskID != "" {
			if task, taskErr := s.repo.TaskForUser(userID, view.TaskID); taskErr == nil {
				view.Task = dramaJobTaskView(task, labels)
				view.Status = string(task.Status)
				if view.Error == "" {
					view.Error = task.Error
				}
			}
		}
		detail.Attempts = append(detail.Attempts, view)
	}
	return detail, nil
}

func dramaTime(primary *time.Time, fallback time.Time) string {
	if primary != nil {
		return primary.Format(time.RFC3339)
	}
	return fallback.Format(time.RFC3339)
}

func (s *Service) dramaJobTarget(projectID string, job *model.DramaJob) string {
	if job.CastID != "" {
		if cast, err := s.repo.DramaCast(projectID, job.CastID); err == nil {
			if cast.Kind == "scene" {
				return "场景 · " + cast.Name
			}
			return "角色 · " + cast.Name
		}
	}
	parts := []string{}
	if job.EpisodeID != "" {
		if episode, err := s.repo.DramaEpisode(projectID, job.EpisodeID); err == nil {
			parts = append(parts, "第 "+itoa(episode.Number)+" 集")
		}
	}
	if job.Kind == model.DramaJobSegmentPrompt || job.Kind == model.DramaJobSegmentVideo {
		if segment, err := s.repo.DramaSegment(projectID, job.ShotID); err == nil {
			parts = append(parts, fmt.Sprintf("第 %d 段", segment.SegmentIndex+1))
		}
	} else if job.ShotID != "" {
		if shot, err := s.repo.DramaShot(projectID, job.ShotID); err == nil {
			parts = append(parts, "镜头 "+itoa(shot.Position+1))
		}
	}
	return strings.Join(parts, " · ")
}

func dramaModelLabels(models DramaModels) map[string]string {
	labels := map[string]string{}
	for _, capability := range dramaCapabilities {
		choice := models.choice(capability)
		if choice.LogicalModelID != "" {
			labels["l:"+choice.LogicalModelID] = choice.Label
		}
		if choice.ModelKey != "" {
			labels["m:"+choice.ModelKey] = choice.Label
		}
	}
	return labels
}

func dramaJobTaskView(task *model.Task, labels map[string]string) *DramaJobTaskView {
	view := &DramaJobTaskView{
		ID: task.ID, Status: string(task.Status), Stage: task.Stage, Operation: task.Operation, Model: task.Model,
		ModelLabel: firstNonEmpty(labels["l:"+task.LogicalModelID], labels["m:"+task.Model], task.Model),
		Prompt:     truncateRunes(task.Prompt, 6000), Params: map[string]string{}, Error: task.Error, MediaStage: task.MediaStage,
		CreatedAt: task.CreatedAt.Format(time.RFC3339),
	}
	if task.StartedAt != nil {
		view.StartedAt = task.StartedAt.Format(time.RFC3339)
	}
	if task.CompletedAt != nil {
		view.CompletedAt = task.CompletedAt.Format(time.RFC3339)
	}
	var input struct {
		Config map[string]any `json:"config"`
	}
	if json.Unmarshal([]byte(task.InputJSON), &input) == nil {
		for _, key := range dramaJobTaskParams {
			if value := strings.TrimSpace(stringValue(input.Config[key])); value != "" {
				view.Params[key] = truncateRunes(value, 300)
			}
		}
	}
	var result map[string]any
	if json.Unmarshal([]byte(task.ResultJSON), &result) == nil {
		view.ResultMode, _ = result["mode"].(string)
		if text, ok := result["text"].(string); ok {
			view.ResultText = truncateRunes(text, 4000)
		}
		collect := func(value any) {
			if item, ok := value.(map[string]any); ok {
				if id, ok := item["resourceId"].(string); ok && id != "" {
					view.ResourceIDs = append(view.ResourceIDs, id)
				}
			}
		}
		for _, key := range []string{"images"} {
			if list, ok := result[key].([]any); ok {
				for _, item := range list {
					collect(item)
				}
			}
		}
		if len(view.ResourceIDs) == 0 {
			collect(result["video"])
		}
		if view.ResultMode == "" && task.Type == model.TaskTypeTimelineRender {
			view.ResultMode = "video"
			if id, ok := result["resourceId"].(string); ok && id != "" {
				view.ResourceIDs = append(view.ResourceIDs, id)
			}
		}
	}
	return view
}

func (s *Service) DramaJobs(userID string, projectID string, limit int) ([]DramaJobView, error) {
	project, productionRow, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 60
	}
	jobs, err := s.repo.RecentDramaJobs(project.ID, limit)
	if err != nil {
		return nil, err
	}
	views := make([]DramaJobView, 0, len(jobs))
	for _, job := range jobs {
		views = append(views, dramaJobView(job))
	}
	if err := s.attachDramaProviderStages(project.UserID, views); err != nil {
		return nil, err
	}
	waiting := make([]*DramaJobView, len(views))
	for i := range views {
		waiting[i] = &views[i]
	}
	if err := s.fillDramaJobWaits(productionRow, waiting...); err != nil {
		return nil, err
	}
	return views, nil
}

// attachDramaProviderStages 为生成中的作业补上上游进度文案；一次查询覆盖整批作业。
func (s *Service) attachDramaProviderStages(userID string, views []DramaJobView) error {
	ids := []string{}
	for _, view := range views {
		if view.Status == model.DramaJobRunning && view.TaskID != "" {
			ids = append(ids, view.TaskID)
		}
	}
	stages, err := s.repo.RunningTaskStages(userID, ids)
	if err != nil {
		return err
	}
	for i := range views {
		if stage := stages[views[i].TaskID]; views[i].Status == model.DramaJobRunning && stage != "" {
			views[i].ProviderStage = stage
		}
	}
	return nil
}

func (s *Service) DramaCasts(userID string, projectID string) ([]model.DramaCast, error) {
	project, _, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return nil, err
	}
	return s.repo.DramaCasts(project.ID)
}

func (s *Service) DramaChapter(userID string, projectID string, chapterID string) (model.ProjectUnit, error) {
	project, _, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return model.ProjectUnit{}, err
	}
	unit, err := s.repo.ProjectUnit(project.ID, chapterID)
	if err != nil {
		return model.ProjectUnit{}, err
	}
	return *unit, nil
}

func (s *Service) DeleteDrama(userID string, projectID string) error {
	if _, _, err := s.dramaForUser(userID, projectID); err != nil {
		return err
	}
	if err := s.DeleteProject(userID, projectID); err != nil {
		return err
	}
	return s.repo.DeleteDramaProductionData(projectID)
}

func decodeStringList(raw string) []string {
	var values []string
	_ = json.Unmarshal([]byte(raw), &values)
	return values
}

func encodeStringList(values []string) string {
	if values == nil {
		values = []string{}
	}
	encoded, _ := json.Marshal(values)
	return string(encoded)
}

func (s *Service) fillDramaSummaryInbox(project *model.Project, productionRow *model.DramaProduction, summary *DramaSummary, casts []model.DramaCast, episodes []model.DramaEpisode, settings DramaSettings) error {
	var err error
	var castErrors map[string]string
	for _, cast := range casts {
		if cast.ImageStatus != model.DramaMediaFailed {
			continue
		}
		if castErrors == nil {
			if castErrors, err = s.repo.FailedDramaCastErrors(project.ID); err != nil {
				return err
			}
		}
		// Title 只放名称，前端按原因把同类失败折叠成一组，不再逐条重复“参考图生成失败”。
		summary.Inbox = append(summary.Inbox, DramaInboxItem{ID: "cast:" + cast.ID, Kind: "cast_failed", Severity: "error", Title: cast.Name, Detail: firstNonEmpty(truncateRunes(castErrors[cast.ID], 240), "参考图生成失败"), CastID: cast.ID, CastKind: cast.Kind})
	}
	failedSegments, err := s.repo.FailedDramaSegments(project.ID, dramaInboxShotLimit)
	if err != nil {
		return err
	}
	numberByEpisode := map[string]int{}
	for _, episode := range episodes {
		numberByEpisode[episode.ID] = episode.Number
	}
	for _, segment := range failedSegments {
		media := "video"
		reason := segment.VideoError
		if segment.PromptStatus == model.DramaMediaFailed {
			media = "prompt"
			reason = segment.PromptError
		}
		summary.Inbox = append(summary.Inbox, DramaInboxItem{ID: "segment:" + segment.ID, Kind: "segment_failed", Severity: "error", Title: fmt.Sprintf("第 %d 集 · 第 %d 段", numberByEpisode[segment.EpisodeID], segment.SegmentIndex+1), Detail: reason, EpisodeID: segment.EpisodeID, EpisodeNumber: numberByEpisode[segment.EpisodeID], SegmentIndex: segment.SegmentIndex, Media: media})
	}
	if settings.ReviewMode == DramaReviewSample && !productionRow.SampleApproved {
		for _, episode := range episodes {
			if episode.Number == 1 && episode.Stage == model.DramaEpisodeDone && !episode.FinalStale {
				summary.Inbox = append(summary.Inbox, DramaInboxItem{ID: "sample:" + episode.ID, Kind: "review", Severity: "info", Title: "第 1 集样片待确认", Detail: "确认样片后继续其余集视频", EpisodeID: episode.ID})
			}
		}
	}
	return nil
}
