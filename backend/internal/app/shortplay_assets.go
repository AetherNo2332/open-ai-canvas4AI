package app

import (
	"errors"
	"net/http"
	"strings"

	"yingce/backend/internal/repository"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

// W04 资产读写与提取入队：列资产、写基础资产身份/设定、把 extract 排进 shortplay job 队列。
// 归属与项目类型校验统一走 shortplayRowForUser；:id 同时接受 shortplayId 与 projectId。

// ShortplayAssets 列出作品全部资产，按项目内序号升序。
func (s *Service) ShortplayAssets(userID, idOrProject string) ([]shortplay.AssetSummary, error) {
	owner, err := s.shortplayRowForUser(userID, idOrProject)
	if err != nil {
		return nil, err
	}
	rows, err := s.repo.ShortplayAssets(owner.ID)
	if err != nil {
		return nil, err
	}
	items := make([]shortplay.AssetSummary, 0, len(rows))
	for _, row := range resolveShortplayAssetRows(rows) {
		items = append(items, shortplay.BuildAssetSummary(row))
	}
	return items, nil
}

// UpsertShortplayAsset 新建或更新一条基础资产：AssetID>0 走 revision CAS 更新，
// 否则按项目内下一个序号新建；衍生造型留给 W05，本分片明确拒绝。
func (s *Service) UpsertShortplayAsset(userID, idOrProject string, req shortplay.UpsertAssetRequest) (shortplay.AssetSummary, error) {
	owner, err := s.shortplayRowForUser(userID, idOrProject)
	if err != nil {
		return shortplay.AssetSummary{}, err
	}
	if req.AssetID > 0 {
		return s.updateShortplayAsset(owner.ID, req)
	}
	if req.DerivedFrom != 0 {
		return s.createShortplayDerivedAsset(owner.ID, req)
	}
	return s.createShortplayAsset(owner.ID, req)
}

func (s *Service) updateShortplayAsset(shortplayID string, req shortplay.UpsertAssetRequest) (shortplay.AssetSummary, error) {
	existing, err := s.repo.ShortplayAssetByID(shortplayID, req.AssetID)
	if errors.Is(err, repository.ErrShortplayAssetNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
		return shortplay.AssetSummary{}, NewAppError(http.StatusNotFound, "资产不存在")
	}
	if err != nil {
		return shortplay.AssetSummary{}, err
	}
	if existing.DerivedFrom != 0 {
		return s.updateShortplayDerivedAsset(shortplayID, existing, req)
	}
	row, err := shortplay.ApplyBaseAssetWrite(*existing, req)
	if err != nil {
		return shortplay.AssetSummary{}, BadAuthRequest(err.Error())
	}
	applied, err := s.repo.UpdateShortplayAsset(&row, existing.Revision)
	if err != nil {
		return shortplay.AssetSummary{}, err
	}
	if !applied {
		return shortplay.AssetSummary{}, NewAppError(http.StatusConflict, "资产已被其他窗口修改，请刷新后重试")
	}
	row.Revision = existing.Revision + 1
	return shortplay.BuildAssetSummary(row), nil
}

func (s *Service) createShortplayAsset(shortplayID string, req shortplay.UpsertAssetRequest) (shortplay.AssetSummary, error) {
	rows, err := s.repo.ShortplayAssets(shortplayID)
	if err != nil {
		return shortplay.AssetSummary{}, err
	}
	// 序号由「现有最大 +1」得出，并发建档可能撞复合主键；冲突时重读一次再取下一个序号。
	for attempt := 0; attempt < 2; attempt++ {
		row, buildErr := shortplay.NewBaseAssetRow(shortplayID, shortplay.NextAssetID(rows), req)
		if buildErr != nil {
			return shortplay.AssetSummary{}, BadAuthRequest(buildErr.Error())
		}
		createErr := s.repo.CreateShortplayAsset(&row)
		if createErr == nil {
			return shortplay.BuildAssetSummary(row), nil
		}
		if attempt == 0 {
			if rows, err = s.repo.ShortplayAssets(shortplayID); err != nil {
				return shortplay.AssetSummary{}, err
			}
			continue
		}
		return shortplay.AssetSummary{}, createErr
	}
	return shortplay.AssetSummary{}, NewAppError(http.StatusConflict, "资产序号冲突，请重试")
}

// isShortplayUniqueViolation 判断唯一索引冲突：并发连点提取时第二次插入命中
// idx_shortplay_jobs_dedupe_active，调用方应回已有在途 job 而不是报错。
func isShortplayUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") || strings.Contains(message, "duplicate key") ||
		strings.Contains(message, "idx_shortplay_jobs_dedupe_active")
}
