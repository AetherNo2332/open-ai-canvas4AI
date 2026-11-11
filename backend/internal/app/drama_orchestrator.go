package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"log/slog"
	"strings"
	"sync"
	"time"
	"yingce/backend/internal/model"
)

// 短剧调度循环：每个运行中的项目按“同步任务结果 → 规划新工作 → 按优先级派发”推进。
// 所有状态都在数据库里，进程重启或多副本部署时由租约保证同一项目只被一个循环推进。

const (
	dramaTickInterval      = 3 * time.Second
	dramaLeaseTTL          = 2 * time.Minute
	dramaProjectsPerTick   = 20
	dramaEpisodeWindow     = 6
	dramaPlanBatchRunes    = 30000
	dramaPlanBatchChapters = 12
	dramaBibleSampleRunes  = 40000
	dramaBudgetPauseReason = "已达到预算上限，提高预算后可继续"
	dramaCreditsReason     = "积分不足，充值后点击继续制作"
)

var dramaWake = struct {
	once sync.Once
	ch   chan struct{}
}{}

func dramaWakeChannel() chan struct{} {
	dramaWake.once.Do(func() { dramaWake.ch = make(chan struct{}, 1) })
	return dramaWake.ch
}

func (s *Service) wakeDramaScheduler() {
	select {
	case dramaWakeChannel() <- struct{}{}:
	default:
	}
}

// wakeDramaAfterTask 在短剧任务进入终态后立即唤醒调度，回写结果和派发下一步不用等下一个轮询周期。
// 只看短剧提交的任务；分离式回查中的任务每 15 秒执行一次但仍未结束，不唤醒。
func (s *Service) wakeDramaAfterTask(taskID string, inputJSON string) {
	if !strings.Contains(inputJSON, `"drama_production"`) {
		return
	}
	task, err := s.repo.Task(taskID)
	if err != nil {
		return
	}
	switch task.Status {
	case model.TaskStatusSucceeded, model.TaskStatusFailed, model.TaskStatusCancelled:
		s.wakeDramaScheduler()
	}
}

func (s *Service) startDramaScheduler() {
	s.runWorkerLoop(func(ctx context.Context) {
		ticker := time.NewTicker(dramaTickInterval)
		defer ticker.Stop()
		for {
			s.tickDramaProductions(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-dramaWakeChannel():
			}
		}
	})
}

func (s *Service) tickDramaProductions(ctx context.Context) {
	if s.IsDraining() {
		return
	}
	productions, err := s.repo.RunningDramaProductions(dramaProjectsPerTick)
	if err != nil {
		slog.Warn("drama scheduler load failed", "error", err)
		return
	}
	for i := range productions {
		if ctx.Err() != nil {
			return
		}
		release, acquired, err := s.coordinator.Acquire(ctx, "drama:"+productions[i].ProjectID, 1, dramaLeaseTTL)
		if err != nil || !acquired {
			continue
		}
		if err := s.tickDrama(&productions[i]); err != nil {
			slog.Warn("drama tick failed", "project", productions[i].ProjectID, "error", err)
		}
		release()
	}
}

// dramaRun 是一次 tick 内的工作集，避免同一 tick 反复读库。
type dramaRun struct {
	s          *Service
	production *model.DramaProduction
	project    *model.Project
	settings   DramaSettings
	meta       dramaBibleMeta
	casts      []model.DramaCast
	castByID   map[string]*model.DramaCast
	active     map[string]bool
	// running 是各并发池（见 dramaDispatchPool）正在运行的作业数。
	running map[string]int
	newJobs []model.DramaJob
	// imageSizes 缓存本轮调度中“期望画幅 → 模型尺寸参数”的换算，避免每张参考图重复读取能力合同。
	imageSizes map[string]dramaImageRequest
	submitting *model.DramaJob // 正在派发的作业：平台提交据此生成幂等键与业务归属
}

func dramaJobKey(kind, episodeID, shotID, castID string) string {
	return kind + "|" + episodeID + "|" + shotID + "|" + castID
}

func (s *Service) tickDrama(productionRow *model.DramaProduction) error {
	fresh, err := s.repo.DramaProduction(productionRow.ProjectID)
	if err != nil {
		return err
	}
	productionRow = fresh
	project, err := s.repo.ProjectForUser(productionRow.UserID, productionRow.ProjectID)
	if err != nil {
		return err
	}
	run := &dramaRun{s: s, production: productionRow, project: project, settings: parseDramaSettings(productionRow.SettingsJSON), meta: parseDramaBibleMeta(productionRow.BibleJSON)}
	if productionRow.Status != model.DramaStatusRunning {
		// 暂停后已派发的任务仍会完成：照常回写结果（视频入库、抽尾帧），否则段会一直停在“生成中”，下一段也拿不到首帧。
		status := productionRow.Status
		if err := run.syncJobs(); err != nil {
			return err
		}
		return s.repo.SaveDramaProductionProgress(run.production, status, run.production.Status != status)
	}
	if err := run.syncJobs(); err != nil {
		return err
	}
	if run.production.Status != model.DramaStatusRunning {
		return s.repo.SaveDramaProductionProgress(run.production, model.DramaStatusRunning, true)
	}
	if err := run.loadCasts(); err != nil {
		return err
	}
	if err := run.plan(); err != nil {
		return err
	}
	if err := s.repo.CreateDramaJobs(run.newJobs); err != nil {
		return err
	}
	if err := run.dispatch(); err != nil {
		return err
	}
	now := time.Now()
	run.production.LastTickAt = &now
	return s.repo.SaveDramaProductionProgress(run.production, model.DramaStatusRunning, run.production.Status != model.DramaStatusRunning)
}

func (r *dramaRun) loadCasts() error {
	casts, err := r.s.repo.DramaCasts(r.project.ID)
	if err != nil {
		return err
	}
	r.casts = casts
	r.castByID = make(map[string]*model.DramaCast, len(casts))
	for i := range r.casts {
		r.castByID[r.casts[i].ID] = &r.casts[i]
	}
	return nil
}

// syncJobs 读取已派发任务的终态并写回业务对象。
func (r *dramaRun) syncJobs() error {
	jobs, err := r.s.repo.DramaJobsByStatus(r.project.ID, model.DramaJobQueued, model.DramaJobRunning)
	if err != nil {
		return err
	}
	r.active = map[string]bool{}
	r.running = map[string]int{}
	for i := range jobs {
		job := &jobs[i]
		if job.Status == model.DramaJobRunning {
			if err := r.syncRunningJob(job); err != nil {
				var deferred *dramaSyncDeferred
				if !errors.As(err, &deferred) {
					return err
				}
				// 任务或账单暂时读不到、或订单还没结算：本作业保持运行、下一轮再读，不按失败或 0 花费落账。
				// 不中断本轮，前面已回写的作业花费随本轮进度一起保存。等待结算与后台抽尾帧是正常过程，不记告警。
				var pending *dramaPendingBilling
				var tail *dramaPendingTail
				if !errors.As(deferred.err, &pending) && !errors.As(deferred.err, &tail) {
					slog.Warn("drama job sync deferred", "project", r.project.ID, "job", job.ID, "error", deferred.err)
				}
			}
		}
		if job.Status == model.DramaJobQueued || job.Status == model.DramaJobRunning {
			r.active[dramaJobKey(job.Kind, job.EpisodeID, job.ShotID, job.CastID)] = true
			if job.Status == model.DramaJobRunning {
				r.running[dramaDispatchPool(job.Kind)]++
			}
		}
	}
	return nil
}

// dramaSyncDeferred 表示作业的任务或账单暂时读不到（如数据库繁忙），本轮跳过、下一轮重读。
type dramaSyncDeferred struct{ err error }

func (e *dramaSyncDeferred) Error() string { return e.err.Error() }

func (r *dramaRun) syncRunningJob(job *model.DramaJob) error {
	task, err := r.taskFor(job)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r.failJob(job, "任务记录丢失")
	}
	if err != nil {
		// 读库出错不等于任务丢失：已付费的生成结果可能还在，不能直接判失败。
		return &dramaSyncDeferred{err: err}
	}
	switch task.Status {
	case model.TaskStatusSucceeded:
		if err := r.settleTaskAttempt(job, task, ""); err != nil {
			return err
		}
		if applyErr := r.applyJob(job, task); applyErr != nil {
			var deferred *dramaSyncDeferred
			if errors.As(applyErr, &deferred) {
				return applyErr
			}
			var next *dramaJobContinued
			if errors.As(applyErr, &next) {
				// 本任务的花费已记入历次提交；作业换成新任务继续运行，下一轮读取新任务的结果。
				job.TaskID, job.CostMicrocredits = next.taskID, 0
				return r.s.repo.SaveDramaJob(job)
			}
			var issue *dramaResultIssue
			if errors.As(applyErr, &issue) {
				return r.failJob(job, issue.message)
			}
			return r.failJob(job, "结果处理失败（保留原任务，重试不重复生成）："+applyErr.Error())
		}
		now := time.Now()
		job.Status, job.FinishedAt, job.Error = model.DramaJobSucceeded, &now, ""
		return r.s.repo.SaveDramaJob(job)
	case model.TaskStatusFailed, model.TaskStatusCancelled:
		if err := r.settleTaskAttempt(job, task, firstNonEmpty(task.Error, "生成失败")); err != nil {
			return err
		}
		return r.failJob(job, firstNonEmpty(task.Error, "生成失败"))
	}
	return nil
}

// settleTaskAttempt 在应用结果前先落库本次任务的去重记录与作业花费，中断恢复时不会重复记账。
// 写入冲突（作业已被取消或重试）或数据库错误时本轮跳过该作业，下一轮重新读取，不中断其他作业。
func (r *dramaRun) settleTaskAttempt(job *model.DramaJob, task *model.Task, reason string) error {
	if dramaAttemptRecorded(job, task.ID) {
		return nil
	}
	cost, err := r.taskCost(task)
	if err != nil {
		return &dramaSyncDeferred{err: err}
	}
	previousAttempts := job.AttemptsJSON
	job.CostMicrocredits = cost
	recordDramaAttempt(job, task.ID, string(task.Status), reason, cost)
	// 先原子保存去重记录与作品花费，再处理结果；中断恢复只重做结果应用。
	if err := r.s.repo.RecordDramaJobCost(job, previousAttempts); err != nil {
		job.AttemptsJSON = previousAttempts
		return &dramaSyncDeferred{err: err}
	}
	r.production.SpentMicrocredits += cost
	return nil
}

// dramaResultIssue 是生成结果本身不合格（例如参考图画幅不符）：任务已成功结算，失败原因原样展示给用户。
type dramaResultIssue struct{ message string }

func (e *dramaResultIssue) Error() string { return e.message }

// dramaJobContinued 表示结果需要同一作业再跑一轮（例如段提示词超长时自动精简），taskID 是已提交的新任务。
type dramaJobContinued struct{ taskID string }

func (e *dramaJobContinued) Error() string { return "作业继续运行：" + e.taskID }

// taskCost 是任务实际扣费：已结算记实收（不含差额退回），已退回记 0。
// 订单还没结算时返回 dramaPendingBilling，由调用方推迟到下一轮；待人工核对（uncertain）或任务结束超过
// dramaBillingSettleGrace 仍未结算时按冻结额记，避免作业无限等待。读账单出错同样推迟，不按 0 记账。
func (r *dramaRun) taskCost(task *model.Task) (int64, error) {
	if task.BillingOrderID == "" {
		return 0, nil
	}
	order, err := r.s.repo.BillingOrder(task.BillingOrderID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	charged, held, settled := dramaOrderSpend(*order)
	if settled {
		return charged, nil
	}
	if order.Status != model.BillingStatusUncertain && time.Since(dramaTaskFinishedAt(task)) < dramaBillingSettleGrace {
		return 0, &dramaPendingBilling{orderID: order.ID}
	}
	return held, nil
}

// failJob 让失败落到具体镜头或设定上，进入“待处理”而不是卡住整条流水线。
func (r *dramaRun) failJob(job *model.DramaJob, reason string) error {
	now := time.Now()
	job.Status, job.FinishedAt, job.Error = model.DramaJobFailed, &now, truncateRunes(reason, 500)
	if err := r.s.repo.SaveDramaJob(job); err != nil {
		return err
	}
	switch job.Kind {
	case model.DramaJobBible, model.DramaJobPlan:
		r.production.Status = model.DramaStatusPaused
		r.production.PauseReason = "作品改编失败：" + truncateRunes(reason, 200)
	case model.DramaJobCastImage:
		var payload dramaRevisionPayload
		if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
			return err
		}
		_, err := r.s.repo.UpdateDramaCastRevision(job.CastID, payload.Revision, map[string]any{"image_status": model.DramaMediaFailed})
		return err
	case model.DramaJobSegmentPrompt:
		var payload dramaRevisionPayload
		_ = json.Unmarshal([]byte(job.PayloadJSON), &payload)
		_, err := r.s.repo.UpdateDramaSegmentRevision(job.ShotID, payload.Revision, map[string]any{"prompt_status": model.DramaMediaFailed, "prompt_error": truncateRunes(reason, 300)})
		return err
	case model.DramaJobScript, model.DramaJobRender:
		if episode, err := r.s.repo.DramaEpisode(r.project.ID, job.EpisodeID); err == nil {
			var payload dramaRevisionPayload
			_ = json.Unmarshal([]byte(job.PayloadJSON), &payload)
			_, err = r.s.repo.UpdateDramaEpisodeIfRevision(episode.ID, payload.Revision, map[string]any{"error": truncateRunes(reason, 300)})
			return err
		}
	case model.DramaJobSegmentVideo:
		var payload dramaRevisionPayload
		_ = json.Unmarshal([]byte(job.PayloadJSON), &payload)
		_, err := r.s.repo.UpdateDramaSegmentRevision(job.ShotID, payload.Revision, map[string]any{"video_status": model.DramaMediaFailed, "video_error": truncateRunes(reason, 500)})
		return err
	}
	return nil
}

// recordDramaAttempt 追加一次提交记录；最多保留最近 20 次，供队列详情回看历次请求。
func recordDramaAttempt(job *model.DramaJob, taskID, status, reason string, cost int64) {
	var attempts []DramaJobAttempt
	_ = json.Unmarshal([]byte(job.AttemptsJSON), &attempts)
	if taskID != "" {
		for _, attempt := range attempts {
			if attempt.TaskID == taskID {
				return
			}
		}
	}
	attempts = append(attempts, DramaJobAttempt{TaskID: taskID, Status: status, Error: truncateRunes(reason, 500), CostMicrocredits: cost, At: time.Now().Format(time.RFC3339)})
	if len(attempts) > 20 {
		attempts = attempts[len(attempts)-20:]
	}
	encoded, _ := json.Marshal(attempts)
	job.AttemptsJSON = string(encoded)
}

func dramaAttemptRecorded(job *model.DramaJob, taskID string) bool {
	var attempts []DramaJobAttempt
	_ = json.Unmarshal([]byte(job.AttemptsJSON), &attempts)
	for _, a := range attempts {
		if a.TaskID == taskID {
			return true
		}
	}
	return false
}

func (r *dramaRun) enqueue(kind, episodeID, shotID, castID string, priority int, label string, payload any) bool {
	key := dramaJobKey(kind, episodeID, shotID, castID)
	if r.active[key] {
		return false
	}
	r.active[key] = true
	now := time.Now()
	job := model.DramaJob{ID: newID(), ProjectID: r.project.ID, Status: model.DramaJobQueued, Kind: kind, EpisodeID: episodeID, ShotID: shotID, CastID: castID, Priority: priority, Label: truncateRunes(label, 200), CreatedAt: now, UpdatedAt: now}
	if payload != nil {
		encoded, _ := json.Marshal(payload)
		job.PayloadJSON = string(encoded)
	}
	r.newJobs = append(r.newJobs, job)
	return true
}

type dramaPlanPayload struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// plan 根据业务对象的当前状态推导需要的工作；它是幂等的，重复执行只会跳过已排队项。
func (r *dramaRun) plan() error {
	p := r.production
	if p.Stage == model.DramaStageBible {
		r.enqueue(model.DramaJobBible, "", "", "", dramaPriorityBible, "建立作品圣经：角色、场景与风格", nil)
		return nil
	}
	chapters, err := r.s.repo.DramaChapterSummaries(r.project.ID)
	if err != nil {
		return err
	}
	if !p.PlanDone {
		if p.PlanCursor >= len(chapters) {
			p.PlanDone = true
		} else if r.settings.EpisodeMode == DramaEpisodeModeChapter {
			// 规划中途改成按章分集：剩余章节直接分组建集，在途的规划结果因游标前移作废。
			if err := r.groupRemainingChapters(chapters); err != nil {
				return err
			}
		} else if !r.active[dramaJobKey(model.DramaJobPlan, "", "", "")] {
			end, runes := p.PlanCursor, 0
			for end < len(chapters) && end-p.PlanCursor < dramaPlanBatchChapters && (runes == 0 || runes+chapters[end].WordCount <= dramaPlanBatchRunes) {
				runes += chapters[end].WordCount
				end++
			}
			r.enqueue(model.DramaJobPlan, "", "", "", dramaPriorityPlan, fmt.Sprintf("分集规划：第 %d-%d 章", p.PlanCursor+1, end), dramaPlanPayload{Start: p.PlanCursor, End: end})
		}
	}
	if err := r.planCastImages(); err != nil {
		return err
	}
	episodes, err := r.s.repo.DramaEpisodes(r.project.ID)
	if err != nil {
		return err
	}
	inFlight := 0
	for i := range episodes {
		episode := &episodes[i]
		// 等待放行生图的集不占制作窗口，剧本可以全部先写出来供审阅。
		if episode.Stage != model.DramaEpisodeQueued && episode.Stage != model.DramaEpisodeDone && episode.Stage != model.DramaEpisodeReview && !r.mediaHeld(episode) {
			inFlight++
		}
	}
	allDone := p.PlanDone && len(episodes) > 0
	for i := range episodes {
		episode := &episodes[i]
		if episode.Stage != model.DramaEpisodeDone || episode.FinalStale {
			allDone = false
		}
		loadedStage := episode.Stage
		if episode.Stage == model.DramaEpisodeQueued {
			if inFlight >= dramaEpisodeWindow {
				continue
			}
			episode.Stage = model.DramaEpisodeScript
			inFlight++
		}
		if err := r.advanceEpisode(episode, loadedStage); err != nil {
			return err
		}
	}
	// 完整视频提示词只花文本费用，剧本一写好就撰写，不受手动生图放行限制。
	segments, err := r.s.repo.PendingDramaSegments(r.project.ID)
	if err != nil {
		return err
	}
	numberByEpisode := map[string]int{}
	for _, episode := range episodes {
		numberByEpisode[episode.ID] = episode.Number
	}
	for _, segment := range segments {
		number := numberByEpisode[segment.EpisodeID]
		r.enqueue(model.DramaJobSegmentPrompt, segment.EpisodeID, segment.ID, "", segmentPriority(dramaRankPrompt, number, segment.SegmentIndex), fmt.Sprintf("第 %d 集第 %d 段 视频提示词", number, segment.SegmentIndex+1), nil)
		allDone = false
	}
	if allDone {
		p.Status, p.Stage = model.DramaStatusCompleted, model.DramaStageDone
	}
	return nil
}
