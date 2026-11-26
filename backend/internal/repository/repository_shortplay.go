package repository

import (
	"errors"
	"strings"
	"time"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// 独立 shortplay 工作台的持久化。所有资产读写都必须带 shortplayId：资产身份是
// (shortplay_id, id) 复合主键，项目内整数 id 跨项目可以重复，只有带作品身份才唯一。

// ErrShortplayNotFound 表示作品不存在或不属于当前账号的读取范围。
var ErrShortplayNotFound = errors.New("shortplay not found")

// ErrShortplayNameTaken 表示同一账号下已有同名作品（projects 的唯一约束）。
var ErrShortplayNameTaken = errors.New("shortplay name taken")

// ShortplayListItem 是列表行：作品名与画幅来自 projects 容器行，集数来自 shortplay_episodes。
type ShortplayListItem struct {
	ProjectID    string    `json:"projectId"`
	ShortplayID  string    `json:"shortplayId"`
	Name         string    `json:"name"`
	AspectRatio  string    `json:"aspectRatio"`
	EpisodeCount int64     `json:"episodeCount"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// ShortplayByID 按域配置 ID 读作品（含归属校验）。
func (r *Repository) ShortplayByID(id string) (*model.Shortplay, error) {
	var row model.Shortplay
	if err := r.db.First(&row, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// ShortplayByProject 按项目容器 ID 读域配置。
func (r *Repository) ShortplayByProject(projectID string) (*model.Shortplay, error) {
	var row model.Shortplay
	if err := r.db.First(&row, "project_id = ?", projectID).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// ShortplaysByUser 按用户列作品，按更新时间倒序。
func (r *Repository) ShortplaysByUser(userID string) ([]model.Shortplay, error) {
	var rows []model.Shortplay
	err := r.db.Where("user_id = ?", userID).Order("updated_at desc").Find(&rows).Error
	return rows, err
}

// ListShortplayItems 返回列表行：容器行 + 集数聚合，一次查询完成。
func (r *Repository) ListShortplayItems(userID string) ([]ShortplayListItem, error) {
	var rows []ShortplayListItem
	err := r.db.Table("shortplays").
		Select("projects.id AS project_id, shortplays.id AS shortplay_id, projects.name AS name, projects.aspect_ratio AS aspect_ratio, COUNT(shortplay_episodes.id) AS episode_count, projects.created_at AS created_at, projects.updated_at AS updated_at").
		Joins("JOIN projects ON projects.id = shortplays.project_id").
		Joins("LEFT JOIN shortplay_episodes ON shortplay_episodes.shortplay_id = shortplays.id").
		Where("shortplays.user_id = ?", userID).
		Group("projects.id, shortplays.id, projects.name, projects.aspect_ratio, projects.created_at, projects.updated_at").
		Order("projects.updated_at desc").
		Scan(&rows).Error
	return rows, err
}

// ShortplayEpisodes 按集号升序返回全部集。
func (r *Repository) ShortplayEpisodes(shortplayID string) ([]model.ShortplayEpisode, error) {
	var rows []model.ShortplayEpisode
	err := r.db.Where("shortplay_id = ?", shortplayID).Order("number asc").Find(&rows).Error
	return rows, err
}

// ShortplayEpisodeByID 按作品身份读一集；跨作品一律返回 not found。
func (r *Repository) ShortplayEpisodeByID(shortplayID, episodeID string) (*model.ShortplayEpisode, error) {
	var row model.ShortplayEpisode
	if err := r.db.First(&row, "shortplay_id = ? AND id = ?", shortplayID, episodeID).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// ShortplayEpisodeByCanvas 按画布地址读集行；未命中返回 not found（自由画布是正常状态）。
func (r *Repository) ShortplayEpisodeByCanvas(canvasID string) (*model.ShortplayEpisode, error) {
	var row model.ShortplayEpisode
	if err := r.db.First(&row, "canvas_id = ?", canvasID).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// CreateShortplayProject 在同一事务内写入项目容器、域配置与全部集行。
//
// 项目名唯一约束冲突返回 ErrShortplayNameTaken，整个事务回滚：不允许出现「项目建了、
// 域配置没建」或「域配置建了、集没建」的半成品。并发建档时数据库唯一索引决定赢家。
func (r *Repository) CreateShortplayProject(project *model.Project, row *model.Shortplay, episodes []model.ShortplayEpisode) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(project).Error; err != nil {
			if isUniqueConstraintError(err) {
				return ErrShortplayNameTaken
			}
			return err
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		if len(episodes) > 0 {
			if err := tx.Create(&episodes).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ShortplayAssets 按项目内序号返回全部资产，并补齐当前图/音的读时投影。
func (r *Repository) ShortplayAssets(shortplayID string) ([]model.ShortplayAsset, error) {
	var rows []model.ShortplayAsset
	if err := r.db.Where("shortplay_id = ?", shortplayID).Order("id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	if err := r.attachShortplayAssetMedia(shortplayID, rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// ShortplayAssetByID 按 (shortplayId, id) 读一行；跨项目读不到，返回 ErrShortplayAssetNotFound。
func (r *Repository) ShortplayAssetByID(shortplayID string, id int) (*model.ShortplayAsset, error) {
	var row model.ShortplayAsset
	if err := r.db.First(&row, "shortplay_id = ? AND id = ?", shortplayID, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrShortplayAssetNotFound
		}
		return nil, err
	}
	rows := []model.ShortplayAsset{row}
	if err := r.attachShortplayAssetMedia(shortplayID, rows); err != nil {
		return nil, err
	}
	return &rows[0], nil
}

// ErrShortplayAssetNotFound 表示资产不在该作品内（含跨项目访问）。
var ErrShortplayAssetNotFound = errors.New("shortplay asset not found")

// NextShortplayAssetID 返回项目内下一个资产序号（现有最大 +1，不复用已删除编号）。
func (r *Repository) NextShortplayAssetID(shortplayID string) (int, error) {
	var maxID int
	err := r.db.Model(&model.ShortplayAsset{}).Where("shortplay_id = ?", shortplayID).
		Select("COALESCE(MAX(id), 0)").Scan(&maxID).Error
	if err != nil {
		return 0, err
	}
	return maxID + 1, nil
}

// CreateShortplayAsset 写入一行新资产。ID 由调用方按 NextShortplayAssetID 分配；
// 复合主键冲突说明并发抢到了同一个序号，调用方应重试一次而不是覆盖。
func (r *Repository) CreateShortplayAsset(row *model.ShortplayAsset) error {
	return r.db.Create(row).Error
}

// UpdateShortplayAsset 以 revision CAS 更新资产设定；row.Revision 是期望值，写成功后自增。
// 返回 false 表示版本已被其他写入推进，调用方必须重新读取后再决定。
func (r *Repository) UpdateShortplayAsset(row *model.ShortplayAsset, expectedRevision int64) (bool, error) {
	result := r.db.Model(&model.ShortplayAsset{}).
		Where("shortplay_id = ? AND id = ? AND revision = ?", row.ShortplayID, row.ID, expectedRevision).
		Updates(map[string]any{
			"kind":                 row.Kind,
			"name":                 row.Name,
			"aliases_json":         row.AliasesJSON,
			"description":          row.Description,
			"appearance":           row.Appearance,
			"voice":                row.Voice,
			"locked":               row.Locked,
			"derived_from":         row.DerivedFrom,
			"variant_name":         row.VariantName,
			"render_rule":          row.RenderRule,
			"episode_numbers_json": row.EpisodeNumbersJSON,
			"revision":             expectedRevision + 1,
			"updated_at":           time.Now(),
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// ShortplayJobs 返回作品最近若干条 job，按创建时间倒序；详情页用。
func (r *Repository) ShortplayJobs(shortplayID string, limit int) ([]model.ShortplayJob, error) {
	if limit <= 0 {
		limit = 40
	}
	var rows []model.ShortplayJob
	err := r.db.Where("shortplay_id = ?", shortplayID).
		Order("created_at desc").Limit(limit).Find(&rows).Error
	return rows, err
}

// ShortplayJobByDedupeKey 查同键且未结束的 job；没有时返回 gorm.ErrRecordNotFound。
func (r *Repository) ShortplayJobByDedupeKey(dedupeKey string) (*model.ShortplayJob, error) {
	var row model.ShortplayJob
	if err := r.db.Where("dedupe_key = ? AND status IN ?", dedupeKey, []string{"queued", "running"}).
		First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// EnqueueShortplayJob 入队一条 job。唯一索引只覆盖未结束行：并发或连点同键时，
// 第二次插入冲突返回已有行与 created=false，调用方据此回「已在途」而不是报错。
func (r *Repository) EnqueueShortplayJob(row *model.ShortplayJob) (*model.ShortplayJob, bool, error) {
	if err := r.db.Create(row).Error; err != nil {
		if !isUniqueConstraintError(err) {
			return nil, false, err
		}
		existing, findErr := r.ShortplayJobByDedupeKey(row.DedupeKey)
		if findErr != nil {
			return nil, false, findErr
		}
		return existing, false, nil
	}
	return row, true, nil
}

// UpdateShortplaySettings 保存归一后的 settingsJson，并在同一事务里推进容器行的 updated_at。// 列表按 projects.updated_at 倒序，只写域配置行会让「刚改过设置的作品」仍排在旧位置。
func (r *Repository) UpdateShortplaySettings(shortplayID, settingsJSON string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Shortplay{}).Where("id = ?", shortplayID).
			Updates(map[string]any{"settings_json": settingsJSON, "updated_at": time.Now()}).Error; err != nil {
			return err
		}
		// 容器行缺失不阻断设置保存：域配置行仍在，detail 会按外键读取项目并报 404。
		return tx.Model(&model.Project{}).
			Where("id = (SELECT project_id FROM shortplays WHERE id = ?)", shortplayID).
			Update("updated_at", time.Now()).Error
	})
}

// UpdateProjectDefaultImageModel 把设置里的默认图片模型投影到容器行，供 agent 的
// project_meta 读取。空串表示用户没选默认，按「跟随全局」展示。
func (r *Repository) UpdateProjectDefaultImageModel(userID, projectID, imageModel string) error {
	return r.db.Model(&model.Project{}).
		Where("id = ? AND user_id = ?", projectID, userID).
		Updates(map[string]any{"default_image_model": imageModel, "updated_at": time.Now()}).Error
}

// attachShortplayAssetMedia 批量补齐当前图/音的 resourceId、sourceId 与衍生过期标记。
func (r *Repository) attachShortplayAssetMedia(shortplayID string, rows []model.ShortplayAsset) error {
	if len(rows) == 0 {
		return nil
	}
	images, err := r.shortplayImagesByID(shortplayID, collectShortplayIDs(rows, func(row model.ShortplayAsset) int { return row.CurrentImageID }))
	if err != nil {
		return err
	}
	audios, err := r.shortplayAudiosByID(shortplayID, collectShortplayIDs(rows, func(row model.ShortplayAsset) int { return row.CurrentAudioID }))
	if err != nil {
		return err
	}
	for index := range rows {
		applyShortplayMediaProjection(&rows[index], images, audios)
	}
	markShortplayDerivedStaleness(rows)
	return nil
}

func collectShortplayIDs(rows []model.ShortplayAsset, pick func(model.ShortplayAsset) int) []int {
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		if id := pick(row); id > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

func (r *Repository) shortplayImagesByID(shortplayID string, ids []int) (map[int]model.ShortplayAssetImage, error) {
	images := map[int]model.ShortplayAssetImage{}
	if len(ids) == 0 {
		return images, nil
	}
	var found []model.ShortplayAssetImage
	if err := r.db.Where("shortplay_id = ? AND id IN ?", shortplayID, ids).Find(&found).Error; err != nil {
		return nil, err
	}
	for _, image := range found {
		images[image.ID] = image
	}
	return images, nil
}

func (r *Repository) shortplayAudiosByID(shortplayID string, ids []int) (map[int]model.ShortplayAssetAudio, error) {
	audios := map[int]model.ShortplayAssetAudio{}
	if len(ids) == 0 {
		return audios, nil
	}
	var found []model.ShortplayAssetAudio
	if err := r.db.Where("shortplay_id = ? AND id IN ?", shortplayID, ids).Find(&found).Error; err != nil {
		return nil, err
	}
	for _, audio := range found {
		audios[audio.ID] = audio
	}
	return audios, nil
}

func applyShortplayMediaProjection(row *model.ShortplayAsset, images map[int]model.ShortplayAssetImage, audios map[int]model.ShortplayAssetAudio) {
	if image, ok := images[row.CurrentImageID]; ok {
		row.ImageResourceID = image.ResourceID
		row.ImageSourceID = image.SourceImageID
	}
	if audio, ok := audios[row.CurrentAudioID]; ok {
		row.AudioResourceID = audio.ResourceID
		row.AudioSourceID = audio.SourceAudioID
	}
}

// markShortplayDerivedStaleness 标记衍生行的过期：当前图/音不是按父资产当前产物生成的。
// 只是提示，不阻止引用，也不自动重做。
func markShortplayDerivedStaleness(rows []model.ShortplayAsset) {
	baseByID := map[int]model.ShortplayAsset{}
	for _, row := range rows {
		if row.DerivedFrom == 0 {
			baseByID[row.ID] = row
		}
	}
	for index := range rows {
		row := &rows[index]
		if row.DerivedFrom == 0 {
			continue
		}
		parent, ok := baseByID[row.DerivedFrom]
		if !ok {
			continue
		}
		if parent.CurrentImageID != 0 && row.ImageSourceID != parent.CurrentImageID {
			row.BaseImageStale = true
		}
		if row.CurrentAudioID != 0 && parent.CurrentAudioID != 0 && row.AudioSourceID != parent.CurrentAudioID {
			row.BaseAudioStale = true
		}
	}
}

// isUniqueConstraintError 判断唯一约束冲突；SQLite 与 PostgreSQL 的驱动错误文本不同，
// 这里只用于把「同名项目」翻译成业务错误，其余错误原样上抛。
func isUniqueConstraintError(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	message := err.Error()
	return strings.Contains(message, "UNIQUE constraint failed") ||
		strings.Contains(message, "duplicate key value violates unique constraint") ||
		strings.Contains(message, "SQLSTATE 23505")
}
