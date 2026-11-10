package repository

import (
	"errors"
	"gorm.io/gorm"
	"strings"
	"time"
	"yingce/backend/internal/model"
)

// DramaChapters 返回已加入制作的章节（不含预留章节），供资产提取与分集规划读取原文。
func (r *Repository) DramaChapters(projectID string) ([]model.ProjectUnit, error) {
	var units []model.ProjectUnit
	err := r.db.Where("project_id = ? AND kind = ? AND status <> ?", projectID, model.ProjectUnitKindChapter, model.ProjectUnitStatusReserved).Order("position asc").Find(&units).Error
	return units, err
}

// DramaChapterSummaries 与 DramaChapters 同一范围但不读原文；分集规划游标按这个列表计数。
func (r *Repository) DramaChapterSummaries(projectID string) ([]model.ProjectUnit, error) {
	var units []model.ProjectUnit
	err := dramaChapterSummaryQuery(r.db, projectID).Where("status <> ?", model.ProjectUnitStatusReserved).Find(&units).Error
	return units, err
}

// DramaChapterOutline 返回全部原著章节（含预留章节）的目录，不读原文。
func (r *Repository) DramaChapterOutline(projectID string) ([]model.ProjectUnit, error) {
	var units []model.ProjectUnit
	err := dramaChapterSummaryQuery(r.db, projectID).Find(&units).Error
	return units, err
}

func dramaChapterSummaryQuery(db *gorm.DB, projectID string) *gorm.DB {
	return db.Select("id", "project_id", "kind", "title", "word_count", "position", "status", "created_at", "updated_at").
		Where("project_id = ? AND kind = ?", projectID, model.ProjectUnitKindChapter).Order("position asc")
}

// ErrDramaChaptersChanged 表示要加入制作的章节已不是预留状态（已被加入或不属于本作品）。
var ErrDramaChaptersChanged = errors.New("drama chapters changed")

// ProduceDramaChapters 把预留章节加入制作，并在同一事务内追加对应的集（编号接在现有分集之后）、
// 把规划游标推到已加入制作的章节数。只在分集规划完成后允许：在途的规划按游标批次读取章节，中途插入会错位。
func (r *Repository) ProduceDramaChapters(projectID string, chapterIDs []string, episodes []model.DramaEpisode) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var production model.DramaProduction
		if err := tx.First(&production, "project_id = ?", projectID).Error; err != nil {
			return err
		}
		if !production.PlanDone {
			return ErrDramaPlanPending
		}
		now := time.Now()
		result := tx.Model(&model.ProjectUnit{}).Where("project_id = ? AND kind = ? AND status = ? AND id IN ?", projectID, model.ProjectUnitKindChapter, model.ProjectUnitStatusReserved, chapterIDs).
			Updates(map[string]any{"status": model.ProjectUnitStatusReady, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != int64(len(chapterIDs)) {
			return ErrDramaChaptersChanged
		}
		var count int64
		if err := tx.Model(&model.ProjectUnit{}).Where("project_id = ? AND kind = ? AND status <> ?", projectID, model.ProjectUnitKindChapter, model.ProjectUnitStatusReserved).Count(&count).Error; err != nil {
			return err
		}
		var number int
		if err := tx.Model(&model.DramaEpisode{}).Where("project_id = ?", projectID).Select("COALESCE(MAX(number), 0)").Scan(&number).Error; err != nil {
			return err
		}
		for i := range episodes {
			episodes[i].Number = number + 1 + i
		}
		if len(episodes) > 0 {
			if err := tx.CreateInBatches(&episodes, 200).Error; err != nil {
				return err
			}
		}
		return tx.Model(&model.DramaProduction{}).Where("project_id = ?", projectID).
			Updates(map[string]any{"plan_cursor": count, "plan_done": true, "updated_at": now}).Error
	})
}

func (r *Repository) DramaChaptersByIDs(projectID string, ids []string) ([]model.ProjectUnit, error) {
	var units []model.ProjectUnit
	if len(ids) == 0 {
		return units, nil
	}
	err := r.db.Where("project_id = ? AND id IN ?", projectID, ids).Order("position asc").Find(&units).Error
	return units, err
}

func (r *Repository) DramaCasts(projectID string) ([]model.DramaCast, error) {
	var casts []model.DramaCast
	err := r.db.Where("project_id = ?", projectID).Order("kind asc, position asc").Find(&casts).Error
	return casts, err
}

func (r *Repository) DramaCast(projectID string, id string) (*model.DramaCast, error) {
	var cast model.DramaCast
	if err := r.db.First(&cast, "id = ? AND project_id = ?", id, projectID).Error; err != nil {
		return nil, err
	}
	return &cast, nil
}

func (r *Repository) CreateDramaCasts(casts []model.DramaCast) error {
	if len(casts) == 0 {
		return nil
	}
	return r.db.Create(&casts).Error
}

func (r *Repository) DramaEpisodes(projectID string) ([]model.DramaEpisode, error) {
	var episodes []model.DramaEpisode
	err := r.db.Omit("subtitle_srt").Where("project_id = ?", projectID).Order("number asc").Find(&episodes).Error
	return episodes, err
}

func (r *Repository) DramaEpisode(projectID string, id string) (*model.DramaEpisode, error) {
	var episode model.DramaEpisode
	if err := r.db.First(&episode, "id = ? AND project_id = ?", id, projectID).Error; err != nil {
		return nil, err
	}
	return &episode, nil
}

func (r *Repository) MaxDramaEpisodeNumber(projectID string) (int, error) {
	var number int
	err := r.db.Model(&model.DramaEpisode{}).Where("project_id = ?", projectID).Select("COALESCE(MAX(number), 0)").Scan(&number).Error
	return number, err
}

func (r *Repository) LastDramaEpisode(projectID string) (*model.DramaEpisode, error) {
	var episode model.DramaEpisode
	err := r.db.Where("project_id = ?", projectID).Order("number desc").First(&episode).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &episode, nil
}

// AppendDramaEpisodes 写入规划出的分集并推进规划游标，保证重复回填不会生成重复集。
func (r *Repository) AppendDramaEpisodes(production *model.DramaProduction, episodes []model.DramaEpisode, expectedCursor int) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.DramaProduction{}).Where("project_id = ? AND plan_cursor = ?", production.ProjectID, expectedCursor).
			Updates(map[string]any{"plan_cursor": production.PlanCursor, "plan_done": production.PlanDone, "updated_at": time.Now()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrInvalidData
		}
		if len(episodes) == 0 {
			return nil
		}
		return tx.Create(&episodes).Error
	})
}

func (r *Repository) DramaShots(episodeID string) ([]model.DramaShot, error) {
	var shots []model.DramaShot
	err := r.db.Where("episode_id = ?", episodeID).Order("position asc").Find(&shots).Error
	return shots, err
}

func (r *Repository) DramaShot(projectID string, id string) (*model.DramaShot, error) {
	var shot model.DramaShot
	if err := r.db.First(&shot, "id = ? AND project_id = ?", id, projectID).Error; err != nil {
		return nil, err
	}
	return &shot, nil
}

func (r *Repository) CreateDramaTake(take *model.DramaTake) error {
	if take.TaskID != "" {
		var count int64
		if err := r.db.Model(&model.DramaTake{}).Where("task_id = ? AND owner_id = ? AND kind = ?", take.TaskID, take.OwnerID, take.Kind).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		var current int
		if err := tx.Model(&model.DramaTake{}).Where("owner_id = ? AND kind = ?", take.OwnerID, take.Kind).Select("COALESCE(MAX(version), 0)").Scan(&current).Error; err != nil {
			return err
		}
		take.Version = current + 1
		return tx.Create(take).Error
	})
}

func (r *Repository) DramaJobsByStatus(projectID string, statuses ...string) ([]model.DramaJob, error) {
	var jobs []model.DramaJob
	err := r.db.Where("project_id = ? AND status IN ?", projectID, statuses).Order("priority desc, created_at asc").Find(&jobs).Error
	return jobs, err
}

func (r *Repository) DramaJob(projectID string, id string) (*model.DramaJob, error) {
	var job model.DramaJob
	if err := r.db.First(&job, "id = ? AND project_id = ?", id, projectID).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

// LatestDramaJob 返回某集某类工作的最近一条记录，用于回看剧本改编的提示词与模型返回。
func (r *Repository) LatestDramaJob(projectID string, kind string, episodeID string) (*model.DramaJob, error) {
	var job model.DramaJob
	err := r.db.Where("project_id = ? AND kind = ? AND episode_id = ?", projectID, kind, episodeID).Order("created_at desc").First(&job).Error
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *Repository) RecentDramaJobs(projectID string, limit int) ([]model.DramaJob, error) {
	var jobs []model.DramaJob
	err := r.db.Where("project_id = ?", projectID).Order("updated_at desc").Limit(limit).Find(&jobs).Error
	return jobs, err
}

func (r *Repository) DramaJobCounts(projectID string) (map[string]int64, error) {
	var rows []struct {
		Status string
		Count  int64
	}
	if err := r.db.Model(&model.DramaJob{}).Select("status, COUNT(*) AS count").Where("project_id = ?", projectID).Group("status").Scan(&rows).Error; err != nil {
		return nil, err
	}
	counts := map[string]int64{}
	for _, row := range rows {
		counts[row.Status] = row.Count
	}
	return counts, nil
}

func (r *Repository) CreateDramaJobs(jobs []model.DramaJob) error {
	if len(jobs) == 0 {
		return nil
	}
	return r.db.Create(&jobs).Error
}

func (r *Repository) SaveDramaJob(job *model.DramaJob) error {
	job.UpdatedAt = time.Now()
	return r.db.Save(job).Error
}

// RecordDramaJobCost 按 attempts_json 乐观锁写入本次任务的去重记录与作业花费：结算后先落库再应用结果，
// 中断恢复时已记录的任务不会再记一次。作品花费按订单汇总（DramaBillingTotals），这里不改 drama_productions。
func (r *Repository) RecordDramaJobCost(job *model.DramaJob, previousAttempts string) error {
	result := r.db.Model(&model.DramaJob{}).
		Where("id = ? AND project_id = ? AND task_id = ? AND COALESCE(attempts_json, '') = ?", job.ID, job.ProjectID, job.TaskID, previousAttempts).
		Updates(map[string]any{"attempts_json": job.AttemptsJSON, "cost_microcredits": job.CostMicrocredits, "updated_at": time.Now()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrDramaJobChanged
	}
	return nil
}

// ErrDramaJobChanged 表示作业在读取后已被取消、重试或绑定到其他任务，本次写入作废。
var ErrDramaJobChanged = errors.New("短剧作业已变化，请重新读取")

// BindDramaJobTask 只在作业仍是派发前读到的状态与任务时写入新的绑定（整行写回），
// 防止用户在派发期间取消或重试后被覆盖回运行中，或复活已删除的作业。
func (r *Repository) BindDramaJobTask(job *model.DramaJob, expectedStatus, expectedTaskID string) error {
	job.UpdatedAt = time.Now()
	result := r.db.Model(job).
		Where("project_id = ? AND status = ? AND COALESCE(task_id, '') = ?", job.ProjectID, expectedStatus, expectedTaskID).
		Select("*").Omit("id", "project_id", "created_at").Updates(job)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrDramaJobChanged
	}
	return nil
}

// CancelDramaJobs 取消尚未派发的排队工作；已提交的任务由任务中心各自收尾。
func (r *Repository) CancelQueuedDramaJobs(projectID string, filter func(*gorm.DB) *gorm.DB) error {
	query := r.db.Model(&model.DramaJob{}).Where("project_id = ? AND status = ?", projectID, model.DramaJobQueued)
	if filter != nil {
		query = filter(query)
	}
	return query.Updates(map[string]any{"status": model.DramaJobCanceled, "updated_at": time.Now()}).Error
}

func (r *Repository) DeleteDramaProductionData(projectID string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		for _, entity := range []any{&model.DramaJob{}, &model.DramaTake{}, &model.DramaSegment{}, &model.DramaShot{}, &model.DramaEpisode{}, &model.DramaCast{}, &model.DramaProduction{}} {
			if err := tx.Where("project_id = ?", projectID).Delete(entity).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// FailedDramaCastErrors 返回各角色/场景最近一次参考图失败的原因，供收件箱展示真实错误。
func (r *Repository) FailedDramaCastErrors(projectID string) (map[string]string, error) {
	var jobs []model.DramaJob
	err := r.db.Select("cast_id", "error").
		Where("project_id = ? AND kind = ? AND status = ? AND cast_id <> ''", projectID, model.DramaJobCastImage, model.DramaJobFailed).
		Order("updated_at asc").Find(&jobs).Error
	errors := make(map[string]string, len(jobs))
	for _, job := range jobs {
		errors[job.CastID] = job.Error
	}
	return errors, err
}

// AttentionDramaShots 返回需要人工处理的失败镜头，供“待处理”收件箱使用。

func (r *Repository) DramaProjectsPage(userID string, search string, status string, page int, pageSize int) ([]model.Project, int64, error) {
	query := r.db.Model(&model.Project{}).
		Joins("JOIN drama_productions ON drama_productions.project_id = projects.id").
		Where("projects.user_id = ?", userID)
	if search = strings.TrimSpace(search); search != "" {
		query = query.Where("LOWER(projects.name) LIKE ? ESCAPE '\\'", "%"+strings.ToLower(escapeLike(search))+"%")
	}
	if status != "" {
		query = query.Where("drama_productions.status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var projects []model.Project
	err := query.Select("projects.*").Order("projects.updated_at desc").Offset((page - 1) * pageSize).Limit(pageSize).Find(&projects).Error
	return projects, total, err
}

// DramaStatusCounts 统计用户各状态的短剧数量，供列表筛选标签显示。
func (r *Repository) DramaStatusCounts(userID string) (map[string]int64, error) {
	var rows []struct {
		Status string
		Count  int64
	}
	err := r.db.Model(&model.DramaProduction{}).Select("status, COUNT(*) AS count").Where("user_id = ?", userID).Group("status").Scan(&rows).Error
	result := map[string]int64{}
	for _, row := range rows {
		result[row.Status] = row.Count
	}
	return result, err
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(value)
}

func (r *Repository) DramaProductionsForUser(userID string, projectIDs []string) (map[string]model.DramaProduction, error) {
	var productions []model.DramaProduction
	if len(projectIDs) == 0 {
		return map[string]model.DramaProduction{}, nil
	}
	if err := r.db.Where("user_id = ? AND project_id IN ?", userID, projectIDs).Find(&productions).Error; err != nil {
		return nil, err
	}
	result := make(map[string]model.DramaProduction, len(productions))
	for _, production := range productions {
		result[production.ProjectID] = production
	}
	return result, nil
}

// DramaEpisodeCounts 汇总项目列表卡片所需的集数与完成数。
func (r *Repository) DramaEpisodeCounts(projectIDs []string) (map[string][2]int64, error) {
	var rows []struct {
		ProjectID string
		Total     int64
		Done      int64
	}
	result := map[string][2]int64{}
	if len(projectIDs) == 0 {
		return result, nil
	}
	err := r.db.Model(&model.DramaEpisode{}).Select("project_id, COUNT(*) AS total, SUM(CASE WHEN stage = 'done' THEN 1 ELSE 0 END) AS done").
		Where("project_id IN ?", projectIDs).Group("project_id").Scan(&rows).Error
	for _, row := range rows {
		result[row.ProjectID] = [2]int64{row.Total, row.Done}
	}
	return result, err
}

// DramaCoverResources 取每个项目前几张已就绪的角色参考图，列表卡片拼成封面。
func (r *Repository) DramaCoverResources(projectIDs []string, limit int) (map[string][]string, error) {
	var casts []model.DramaCast
	result := map[string][]string{}
	if len(projectIDs) == 0 {
		return result, nil
	}
	err := r.db.Select("project_id", "image_resource_id", "position").Where("project_id IN ? AND kind = ? AND image_status = ?", projectIDs, "character", model.DramaMediaReady).
		Order("position asc").Find(&casts).Error
	for _, cast := range casts {
		if len(result[cast.ProjectID]) < limit {
			result[cast.ProjectID] = append(result[cast.ProjectID], cast.ImageResourceID)
		}
	}
	return result, err
}

// ===== 定向更新 =====
// 调度循环与用户操作并发写同一行时，整行 Save 会互相覆盖；以下方法只写各自负责的列，
// 并用状态或版本号做条件，过期的写入直接失效，由下一轮调度重新推导。
