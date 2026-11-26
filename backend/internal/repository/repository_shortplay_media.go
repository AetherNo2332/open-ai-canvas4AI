package repository

import (
	"errors"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

// W05 参考图产物的写入：状态机 CAS、指针推进、版本递增。
// 历史行保留（钉版与「父资产换脸后衍生图过期」都靠它），当前图只有一张。

// ErrShortplayMediaIDTaken 表示媒体历史行序号被并发抢走，调用方应重算一次 max+1。
var ErrShortplayMediaIDTaken = errors.New("shortplay media id taken")

// ShortplayAssetImageWrite 是一次图片产物的写入内容。Prompt/Appearance 只做复现留档，
// SourceImageID 是衍生形象「以父资产哪一版为底图」的唯一记录。
type ShortplayAssetImageWrite struct {
	ResourceID    string
	SourceImageID int
	Prompt        string
	Appearance    string
}

// NextShortplayAssetImageID 返回作品内下一张图片行的序号（max+1，从 1 起，不复用）。
func (r *Repository) NextShortplayAssetImageID(shortplayID string) (int, error) {
	var maxID int
	if err := r.db.Model(&model.ShortplayAssetImage{}).Where("shortplay_id = ?", shortplayID).
		Select("COALESCE(MAX(id), 0)").Scan(&maxID).Error; err != nil {
		return 0, err
	}
	return maxID + 1, nil
}

// CreateShortplayAssetImage 写入一行图片产物；主键冲突转成 ErrShortplayMediaIDTaken。
func (r *Repository) CreateShortplayAssetImage(row *model.ShortplayAssetImage) error {
	if err := r.db.Create(row).Error; err != nil {
		if isUniqueConstraintError(err) {
			return ErrShortplayMediaIDTaken
		}
		return err
	}
	return nil
}

// ShortplayAssetImageByID 按 (shortplayId, id) 取一行图片产物。
func (r *Repository) ShortplayAssetImageByID(shortplayID string, id int) (*model.ShortplayAssetImage, error) {
	var row model.ShortplayAssetImage
	if err := r.db.First(&row, "shortplay_id = ? AND id = ?", shortplayID, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// ShortplayAssetImages 返回某个资产的全部图片产物行，按 version 升序。
func (r *Repository) ShortplayAssetImages(shortplayID string, assetID int) ([]model.ShortplayAssetImage, error) {
	var rows []model.ShortplayAssetImage
	err := r.db.Where("shortplay_id = ? AND asset_id = ?", shortplayID, assetID).
		Order("version asc").Find(&rows).Error
	return rows, err
}

// SetShortplayAssetImageFrom 以状态机 CAS 落一张新图和指针：
// 只允许从 allowed（在途状态）收口；RowsAffected==0 表示旧任务撞上新图，返回 applied=false。
func (r *Repository) SetShortplayAssetImageFrom(shortplayID string, assetID int, write ShortplayAssetImageWrite, allowed []string) (bool, error) {
	if len(allowed) == 0 {
		return false, errors.New("参考图落库必须给允许的前置状态")
	}
	applied := false
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var maxVersion, maxID int
		if err := tx.Model(&model.ShortplayAssetImage{}).
			Where("shortplay_id = ? AND asset_id = ?", shortplayID, assetID).
			Select("COALESCE(MAX(version), 0)").Scan(&maxVersion).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.ShortplayAssetImage{}).Where("shortplay_id = ?", shortplayID).
			Select("COALESCE(MAX(id), 0)").Scan(&maxID).Error; err != nil {
			return err
		}
		image := &model.ShortplayAssetImage{
			ShortplayID:   shortplayID,
			ID:            maxID + 1,
			AssetID:       assetID,
			Version:       maxVersion + 1,
			Prompt:        write.Prompt,
			Appearance:    write.Appearance,
			SourceImageID: write.SourceImageID,
			ResourceID:    write.ResourceID,
			Status:        shortplay.ImageReady,
			CreatedAt:     time.Now(),
		}
		if err := tx.Create(image).Error; err != nil {
			return err
		}
		// CAS 必须在同一条 UPDATE 上：先读状态再写会让两个并发写者都通过。
		result := tx.Model(&model.ShortplayAsset{}).
			Where("shortplay_id = ? AND id = ? AND image_status IN ?", shortplayID, assetID, allowed).
			Updates(map[string]any{
				"current_image_id": image.ID,
				"image_status":     shortplay.ImageReady,
				"image_version":    gorm.Expr("image_version + 1"),
				"updated_at":       time.Now(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errShortplayImageStateRejected
		}
		applied = true
		return nil
	})
	if errors.Is(err, errShortplayImageStateRejected) {
		return false, nil
	}
	return applied, err
}

// errShortplayImageStateRejected 只在事务内触发回滚；对外表现为 applied=false。
var errShortplayImageStateRejected = errors.New("参考图状态已变化")

// UpdateShortplayAssetImageStatus 只推进参考图生命周期，不动图本身（入队 / 开跑 / 失败）。
// image_version 不在这里递增：图没变，版本就不该动。
func (r *Repository) UpdateShortplayAssetImageStatus(shortplayID string, assetID int, status string) error {
	return r.db.Model(&model.ShortplayAsset{}).
		Where("shortplay_id = ? AND id = ?", shortplayID, assetID).
		Updates(map[string]any{"image_status": status, "updated_at": time.Now()}).Error
}

// RecordShortplayAssetImageFailure 记录一次失败尝试：落一行 status=failed，
// 不碰 current_image_id，上一张可用图继续可用。
func (r *Repository) RecordShortplayAssetImageFailure(shortplayID string, assetID int, write ShortplayAssetImageWrite) error {
	for attempt := 0; attempt < 2; attempt++ {
		err := r.db.Transaction(func(tx *gorm.DB) error {
			var maxVersion, maxID int
			if err := tx.Model(&model.ShortplayAssetImage{}).
				Where("shortplay_id = ? AND asset_id = ?", shortplayID, assetID).
				Select("COALESCE(MAX(version), 0)").Scan(&maxVersion).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.ShortplayAssetImage{}).Where("shortplay_id = ?", shortplayID).
				Select("COALESCE(MAX(id), 0)").Scan(&maxID).Error; err != nil {
				return err
			}
			return tx.Create(&model.ShortplayAssetImage{
				ShortplayID:   shortplayID,
				ID:            maxID + 1,
				AssetID:       assetID,
				Version:       maxVersion + 1,
				Prompt:        write.Prompt,
				Appearance:    write.Appearance,
				SourceImageID: write.SourceImageID,
				ResourceID:    write.ResourceID,
				Status:        shortplay.ImageFailed,
				CreatedAt:     time.Now(),
			}).Error
		})
		if err == nil {
			return nil
		}
		if attempt == 0 && isUniqueConstraintError(err) {
			continue
		}
		return err
	}
	return ErrShortplayMediaIDTaken
}

// ShortplayAssetAudioWrite 是一次音色样本的写入内容，与图片同形（无 appearance）。
type ShortplayAssetAudioWrite struct {
	ResourceID    string
	SourceAudioID int
	Prompt        string
}

// NextShortplayAssetAudioID 返回作品内下一段音频行的序号（max+1，从 1 起，不复用）。
func (r *Repository) NextShortplayAssetAudioID(shortplayID string) (int, error) {
	var maxID int
	if err := r.db.Model(&model.ShortplayAssetAudio{}).Where("shortplay_id = ?", shortplayID).
		Select("COALESCE(MAX(id), 0)").Scan(&maxID).Error; err != nil {
		return 0, err
	}
	return maxID + 1, nil
}

// ShortplayAssetAudioByID 按 (shortplayId, id) 取一行音频产物。
func (r *Repository) ShortplayAssetAudioByID(shortplayID string, id int) (*model.ShortplayAssetAudio, error) {
	var row model.ShortplayAssetAudio
	if err := r.db.First(&row, "shortplay_id = ? AND id = ?", shortplayID, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// ShortplayAssetAudios 返回某个资产的全部音色样本行，按 version 升序。
func (r *Repository) ShortplayAssetAudios(shortplayID string, assetID int) ([]model.ShortplayAssetAudio, error) {
	var rows []model.ShortplayAssetAudio
	err := r.db.Where("shortplay_id = ? AND asset_id = ?", shortplayID, assetID).
		Order("version asc").Find(&rows).Error
	return rows, err
}

// SetShortplayAssetAudioFrom 以状态机 CAS 落一段新样本并推进指针，语义与图片一致：
// 只允许从 allowed（在途状态）收口；RowsAffected==0 表示旧任务撞上新样本。
func (r *Repository) SetShortplayAssetAudioFrom(shortplayID string, assetID int, write ShortplayAssetAudioWrite, allowed []string) (bool, error) {
	if len(allowed) == 0 {
		return false, errors.New("音色样本落库必须给允许的前置状态")
	}
	applied := false
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var maxVersion, maxID int
		if err := tx.Model(&model.ShortplayAssetAudio{}).
			Where("shortplay_id = ? AND asset_id = ?", shortplayID, assetID).
			Select("COALESCE(MAX(version), 0)").Scan(&maxVersion).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.ShortplayAssetAudio{}).Where("shortplay_id = ?", shortplayID).
			Select("COALESCE(MAX(id), 0)").Scan(&maxID).Error; err != nil {
			return err
		}
		audio := &model.ShortplayAssetAudio{
			ShortplayID:   shortplayID,
			ID:            maxID + 1,
			AssetID:       assetID,
			Version:       maxVersion + 1,
			Prompt:        write.Prompt,
			SourceAudioID: write.SourceAudioID,
			ResourceID:    write.ResourceID,
			Status:        shortplay.ImageReady,
			CreatedAt:     time.Now(),
		}
		if err := tx.Create(audio).Error; err != nil {
			return err
		}
		result := tx.Model(&model.ShortplayAsset{}).
			Where("shortplay_id = ? AND id = ? AND audio_status IN ?", shortplayID, assetID, allowed).
			Updates(map[string]any{
				"current_audio_id": audio.ID,
				"audio_status":     shortplay.ImageReady,
				"audio_version":    gorm.Expr("audio_version + 1"),
				"updated_at":       time.Now(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errShortplayImageStateRejected
		}
		applied = true
		return nil
	})
	if errors.Is(err, errShortplayImageStateRejected) {
		return false, nil
	}
	return applied, err
}

// UpdateShortplayAssetAudioStatus 只推进音色样本生命周期，不动样本本身。
func (r *Repository) UpdateShortplayAssetAudioStatus(shortplayID string, assetID int, status string) error {
	return r.db.Model(&model.ShortplayAsset{}).
		Where("shortplay_id = ? AND id = ?", shortplayID, assetID).
		Updates(map[string]any{"audio_status": status, "updated_at": time.Now()}).Error
}

// RecordShortplayAssetAudioFailure 记录一次失败尝试：落一行 status=failed，
// 不碰 current_audio_id，上一段可用样本继续可用。
func (r *Repository) RecordShortplayAssetAudioFailure(shortplayID string, assetID int, write ShortplayAssetAudioWrite) error {
	for attempt := 0; attempt < 2; attempt++ {
		err := r.db.Transaction(func(tx *gorm.DB) error {
			var maxVersion, maxID int
			if err := tx.Model(&model.ShortplayAssetAudio{}).
				Where("shortplay_id = ? AND asset_id = ?", shortplayID, assetID).
				Select("COALESCE(MAX(version), 0)").Scan(&maxVersion).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.ShortplayAssetAudio{}).Where("shortplay_id = ?", shortplayID).
				Select("COALESCE(MAX(id), 0)").Scan(&maxID).Error; err != nil {
				return err
			}
			return tx.Create(&model.ShortplayAssetAudio{
				ShortplayID:   shortplayID,
				ID:            maxID + 1,
				AssetID:       assetID,
				Version:       maxVersion + 1,
				Prompt:        write.Prompt,
				SourceAudioID: write.SourceAudioID,
				ResourceID:    write.ResourceID,
				Status:        shortplay.ImageFailed,
				CreatedAt:     time.Now(),
			}).Error
		})
		if err == nil {
			return nil
		}
		if attempt == 0 && isUniqueConstraintError(err) {
			continue
		}
		return err
	}
	return ErrShortplayMediaIDTaken
}

// UpdateShortplayJobRunState 只推进在途 job 的 taskId 与载荷，不结束它。
func (r *Repository) UpdateShortplayJobRunState(id, taskID, payloadJSON string) error {
	updates := map[string]any{"task_id": taskID, "updated_at": time.Now()}
	if payloadJSON != "" {
		updates["payload_json"] = payloadJSON
	}
	return r.db.Model(&model.ShortplayJob{}).Where("id = ?", id).Updates(updates).Error
}
