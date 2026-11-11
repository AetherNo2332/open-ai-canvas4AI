package repository

import (
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"strings"
	"time"
	"yingce/backend/internal/model"
)

func (r *Repository) ArkPortraitUserGroups(userID string) ([]model.ArkPortraitGroup, error) {
	var groups []model.ArkPortraitGroup
	err := r.db.Where("user_id = ? AND scope = ?", userID, "user").Order("created_at asc").Find(&groups).Error
	return groups, err
}

// ArkPortraitGrantedGroups 返回授权给用户的平台素材组（全部用户或逐个授权）。
func (r *Repository) ArkPortraitGrantedGroups(userID string) ([]model.ArkPortraitGroup, error) {
	var groups []model.ArkPortraitGroup
	granted := r.db.Model(&model.ArkPortraitGroupGrant{}).Select("group_id").Where("user_id = ?", userID)
	err := r.db.Where("scope = ? AND (grant_all = ? OR id IN (?))", "platform", true, granted).Order("created_at asc").Find(&groups).Error
	return groups, err
}

func (r *Repository) CountArkPortraitUserGroups(userID string, kind string) (int64, error) {
	var count int64
	err := r.db.Model(&model.ArkPortraitGroup{}).Where("user_id = ? AND scope = ? AND kind = ?", userID, "user", kind).Count(&count).Error
	return count, err
}

func (r *Repository) ArkPortraitGroup(id string) (*model.ArkPortraitGroup, error) {
	var group model.ArkPortraitGroup
	if err := r.db.First(&group, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &group, nil
}

func (r *Repository) ArkPortraitGroupsByIDs(ids []string) (map[string]model.ArkPortraitGroup, error) {
	result := make(map[string]model.ArkPortraitGroup, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	var groups []model.ArkPortraitGroup
	if err := r.db.Where("id IN ?", ids).Find(&groups).Error; err != nil {
		return nil, err
	}
	for _, group := range groups {
		result[group.ID] = group
	}
	return result, nil
}

// ArkPortraitUserGroupForUser 只返回用户自建的素材组；平台素材组的写操作走管理员接口。
func (r *Repository) ArkPortraitUserGroupForUser(userID, id string) (*model.ArkPortraitGroup, error) {
	var group model.ArkPortraitGroup
	if err := r.db.Where("id = ? AND user_id = ? AND scope = ?", id, userID, "user").First(&group).Error; err != nil {
		return nil, err
	}
	return &group, nil
}

func (r *Repository) SaveArkPortraitGroup(group *model.ArkPortraitGroup) error {
	return r.db.Save(group).Error
}

// DeleteArkPortraitGroupCascade 删除组及其素材记录和授权；调用方须已在方舟侧删除成功。
func (r *Repository) DeleteArkPortraitGroupCascade(id string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("group_id = ?", id).Delete(&model.ArkPortraitAsset{}).Error; err != nil {
			return err
		}
		if err := tx.Where("group_id = ?", id).Delete(&model.ArkPortraitGroupGrant{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&model.ArkPortraitGroup{}).Error
	})
}

// ArkPortraitGroupGranted 判断平台素材组是否授权给用户。
func (r *Repository) ArkPortraitGroupGranted(groupID string, userID string) (bool, error) {
	var count int64
	err := r.db.Model(&model.ArkPortraitGroupGrant{}).Where("group_id = ? AND user_id = ?", groupID, userID).Count(&count).Error
	return count > 0, err
}

// ArkPortraitGrantedGroupIDs 返回 groupIDs 中逐个授权给用户的组。
func (r *Repository) ArkPortraitGrantedGroupIDs(userID string, groupIDs []string) (map[string]bool, error) {
	result := make(map[string]bool, len(groupIDs))
	if len(groupIDs) == 0 {
		return result, nil
	}
	var ids []string
	if err := r.db.Model(&model.ArkPortraitGroupGrant{}).Where("user_id = ? AND group_id IN ?", userID, groupIDs).Pluck("group_id", &ids).Error; err != nil {
		return nil, err
	}
	for _, id := range ids {
		result[id] = true
	}
	return result, nil
}

// ArkPortraitGroupGrantUsers 返回各平台素材组逐个授权的用户 ID。
func (r *Repository) ArkPortraitGroupGrantUsers(groupIDs []string) (map[string][]string, error) {
	result := make(map[string][]string, len(groupIDs))
	if len(groupIDs) == 0 {
		return result, nil
	}
	var grants []model.ArkPortraitGroupGrant
	if err := r.db.Where("group_id IN ?", groupIDs).Order("created_at asc").Find(&grants).Error; err != nil {
		return nil, err
	}
	for _, grant := range grants {
		result[grant.GroupID] = append(result[grant.GroupID], grant.UserID)
	}
	return result, nil
}

// ReplaceArkPortraitGroupGrants 以完整列表替换平台素材组的授权范围。
func (r *Repository) ReplaceArkPortraitGroupGrants(groupID string, grantAll bool, userIDs []string, actorID string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.ArkPortraitGroup{}).Where("id = ? AND scope = ?", groupID, "platform").Updates(map[string]any{"grant_all": grantAll, "updated_at": time.Now()}).Error; err != nil {
			return err
		}
		if err := tx.Where("group_id = ?", groupID).Delete(&model.ArkPortraitGroupGrant{}).Error; err != nil {
			return err
		}
		now := time.Now()
		for _, userID := range userIDs {
			if err := tx.Create(&model.ArkPortraitGroupGrant{ID: newRepositoryID(), GroupID: groupID, UserID: userID, CreatedBy: actorID, CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

type AdminArkPortraitGroupFilter struct {
	Kind    string
	Scope   string
	Expiry  string
	Keyword string
	Now     time.Time
	Limit   int
	Offset  int
}

const arkPortraitExpiringWindow = 30 * 24 * time.Hour

func (r *Repository) AdminArkPortraitGroups(filter AdminArkPortraitGroupFilter) ([]model.ArkPortraitGroup, int64, error) {
	query := r.db.Model(&model.ArkPortraitGroup{})
	if filter.Kind != "" {
		query = query.Where("ark_portrait_groups.kind = ?", filter.Kind)
	}
	if filter.Scope != "" {
		query = query.Where("ark_portrait_groups.scope = ?", filter.Scope)
	}
	query = arkPortraitExpiryFilter(query, filter.Expiry, filter.Now)
	if keyword := strings.ToLower(strings.TrimSpace(filter.Keyword)); keyword != "" {
		pattern := "%" + keyword + "%"
		users := r.db.Model(&model.User{}).Select("id").Where("lower(username) LIKE ? OR lower(display_name) LIKE ? OR lower(email) LIKE ?", pattern, pattern, pattern)
		query = query.Where("lower(ark_portrait_groups.name) LIKE ? OR lower(ark_portrait_groups.group_id) LIKE ? OR ark_portrait_groups.user_id IN (?)", pattern, pattern, users)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var groups []model.ArkPortraitGroup
	err := query.Order("ark_portrait_groups.created_at desc, ark_portrait_groups.id desc").Limit(filter.Limit).Offset(filter.Offset).Find(&groups).Error
	return groups, total, err
}

func arkPortraitExpiryFilter(query *gorm.DB, expiry string, now time.Time) *gorm.DB {
	soon := now.Add(arkPortraitExpiringWindow)
	switch expiry {
	case "valid":
		return query.Where("ark_portrait_groups.expires_at IS NULL OR ark_portrait_groups.expires_at > ?", soon)
	case "expiring":
		return query.Where("ark_portrait_groups.expires_at > ? AND ark_portrait_groups.expires_at <= ?", now, soon)
	case "expired":
		return query.Where("ark_portrait_groups.expires_at <= ?", now)
	default:
		return query
	}
}

type ArkPortraitGroupStats struct {
	Real     int64 `json:"real"`
	Virtual  int64 `json:"virtual"`
	IP       int64 `json:"ip"`
	Expiring int64 `json:"expiring"`
	Expired  int64 `json:"expired"`
}

func (r *Repository) ArkPortraitGroupStats(now time.Time) (ArkPortraitGroupStats, error) {
	var stats ArkPortraitGroupStats
	var rows []struct {
		Kind  string
		Count int64
	}
	if err := r.db.Model(&model.ArkPortraitGroup{}).Select("kind, COUNT(*) AS count").Group("kind").Scan(&rows).Error; err != nil {
		return stats, err
	}
	for _, row := range rows {
		switch row.Kind {
		case "real":
			stats.Real = row.Count
		case "virtual":
			stats.Virtual = row.Count
		case "ip":
			stats.IP = row.Count
		}
	}
	if err := arkPortraitExpiryFilter(r.db.Model(&model.ArkPortraitGroup{}), "expiring", now).Count(&stats.Expiring).Error; err != nil {
		return stats, err
	}
	if err := arkPortraitExpiryFilter(r.db.Model(&model.ArkPortraitGroup{}), "expired", now).Count(&stats.Expired).Error; err != nil {
		return stats, err
	}
	return stats, nil
}

// ArkPortraitAssetStatusCounts 按素材组统计各状态的素材数。
func (r *Repository) ArkPortraitAssetStatusCounts(groupIDs []string) (map[string]map[string]int64, error) {
	result := make(map[string]map[string]int64, len(groupIDs))
	if len(groupIDs) == 0 {
		return result, nil
	}
	var rows []struct {
		GroupID string
		Status  string
		Count   int64
	}
	if err := r.db.Model(&model.ArkPortraitAsset{}).Select("group_id, status, COUNT(*) AS count").Where("group_id IN ?", groupIDs).Group("group_id, status").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		if result[row.GroupID] == nil {
			result[row.GroupID] = map[string]int64{}
		}
		result[row.GroupID][row.Status] = row.Count
	}
	return result, nil
}

func (r *Repository) CreateArkPortraitAsset(asset *model.ArkPortraitAsset) error {
	return r.db.Create(asset).Error
}

func (r *Repository) SaveArkPortraitAsset(asset *model.ArkPortraitAsset) error {
	return r.db.Save(asset).Error
}

func (r *Repository) ArkPortraitAsset(id string) (*model.ArkPortraitAsset, error) {
	var asset model.ArkPortraitAsset
	if err := r.db.First(&asset, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &asset, nil
}

// ArkPortraitAssetsForGroups 返回素材组内的素材；statuses 为空时不过滤状态。
func (r *Repository) ArkPortraitAssetsForGroups(groupIDs []string, statuses []string) ([]model.ArkPortraitAsset, error) {
	if len(groupIDs) == 0 {
		return nil, nil
	}
	query := r.db.Where("group_id IN ?", groupIDs)
	if len(statuses) > 0 {
		query = query.Where("status IN ?", statuses)
	}
	var assets []model.ArkPortraitAsset
	err := query.Order("created_at asc").Find(&assets).Error
	return assets, err
}

func (r *Repository) CountArkPortraitGroupAssets(groupID string) (int64, error) {
	var count int64
	err := r.db.Model(&model.ArkPortraitAsset{}).Where("group_id = ?", groupID).Count(&count).Error
	return count, err
}

func (r *Repository) DeleteArkPortraitAsset(id string) error {
	return r.db.Where("id = ?", id).Delete(&model.ArkPortraitAsset{}).Error
}

// ArkPortraitSubmissionForLibraryAsset 返回素材库素材仍在审核、处理中或已通过的认证记录。
func (r *Repository) ArkPortraitSubmissionForLibraryAsset(userID string, libraryAssetID string) (*model.ArkPortraitAsset, error) {
	var asset model.ArkPortraitAsset
	err := r.db.Where("user_id = ? AND library_asset_id = ? AND status IN ?", userID, libraryAssetID, []string{"pending_review", "processing", "active"}).Order("created_at desc").First(&asset).Error
	if err != nil {
		return nil, err
	}
	return &asset, nil
}

func (r *Repository) SetArkPortraitAssetLibraryAsset(id string, libraryAssetID string) error {
	return r.db.Model(&model.ArkPortraitAsset{}).Where("id = ?", id).Updates(map[string]any{"library_asset_id": libraryAssetID, "updated_at": time.Now()}).Error
}

// ProcessingArkPortraitAssets 返回已提交方舟、等待审核结果的素材，最久未检查的排在前面。
func (r *Repository) ProcessingArkPortraitAssets(limit int) ([]model.ArkPortraitAsset, error) {
	var assets []model.ArkPortraitAsset
	err := r.db.Where("status = ? AND ark_asset_id <> ''", "processing").Order("updated_at asc").Limit(limit).Find(&assets).Error
	return assets, err
}

// TouchArkPortraitAsset 记录一次状态检查，让后台轮询轮转到其他素材。
func (r *Repository) TouchArkPortraitAsset(id string) error {
	return r.db.Model(&model.ArkPortraitAsset{}).Where("id = ?", id).Update("updated_at", time.Now()).Error
}

func (r *Repository) ArkPortraitReviewQueue(status string, limit int, offset int) ([]model.ArkPortraitAsset, int64, error) {
	query := r.db.Model(&model.ArkPortraitAsset{}).Where("status = ?", status)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	order := "created_at asc"
	if status != "pending_review" {
		order = "updated_at desc"
	}
	var assets []model.ArkPortraitAsset
	err := query.Order(order).Limit(limit).Offset(offset).Find(&assets).Error
	return assets, total, err
}

func (r *Repository) CountArkPortraitAssetsByStatus(status string) (int64, error) {
	var count int64
	err := r.db.Model(&model.ArkPortraitAsset{}).Where("status = ?", status).Count(&count).Error
	return count, err
}

func (r *Repository) ArkPortraitAssetsByIDs(ids []string) ([]model.ArkPortraitAsset, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var assets []model.ArkPortraitAsset
	err := r.db.Where("id IN ?", ids).Find(&assets).Error
	return assets, err
}

// ArkPortraitAssetAccess 是生成前校验方舟素材 ID 所需的归属与授权字段。
type ArkPortraitAssetAccess struct {
	ArkAssetID  string
	GroupID     string
	GroupUserID string
	Scope       string
	GrantAll    bool
	ExpiresAt   *time.Time
}

// ArkPortraitAssetAccessRows 返回已登记方舟素材 ID 所在素材组的归属与授权。未登记的 ID（例如方舟
// 公共虚拟人像）不在结果中。可信素材同步的绑定按共享资源建立，不参与这里的判断。
func (r *Repository) ArkPortraitAssetAccessRows(arkAssetIDs []string) ([]ArkPortraitAssetAccess, error) {
	if len(arkAssetIDs) == 0 {
		return nil, nil
	}
	var rows []ArkPortraitAssetAccess
	err := r.db.Table("ark_portrait_assets").
		Select("ark_portrait_assets.ark_asset_id AS ark_asset_id, ark_portrait_groups.id AS group_id, ark_portrait_groups.user_id AS group_user_id, ark_portrait_groups.scope AS scope, ark_portrait_groups.grant_all AS grant_all, ark_portrait_groups.expires_at AS expires_at").
		Joins("JOIN ark_portrait_groups ON ark_portrait_groups.id = ark_portrait_assets.group_id").
		Where("ark_portrait_assets.ark_asset_id IN ?", arkAssetIDs).
		Scan(&rows).Error
	return rows, err
}

// UpdateUserAssetPayload 在事务中读取并改写用户素材的 payload，同步分类列与 updatedAt，
// 供服务端写入方舟认证结果。mutate 返回 false 表示无需修改；素材不存在时返回 false。
func (r *Repository) UpdateUserAssetPayload(userID string, assetID string, mutate func(asset *model.Asset, payload map[string]any) (bool, error)) (bool, error) {
	changed := false
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var asset model.Asset
		if err := tx.First(&asset, "id = ? AND user_id = ?", assetID, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(asset.PayloadJSON), &payload); err != nil || payload == nil {
			return nil
		}
		ok, err := mutate(&asset, payload)
		if err != nil || !ok {
			return err
		}
		now := time.Now().UTC()
		payload["category"] = string(asset.Category)
		if asset.Status != "" {
			payload["status"] = string(asset.Status)
		}
		payload["updatedAt"] = now.Format(time.RFC3339Nano)
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if err := tx.Model(&model.Asset{}).Where("id = ? AND user_id = ?", asset.ID, userID).Updates(map[string]any{
			"category": asset.Category, "status": asset.Status, "payload_json": string(encoded), "updated_at": now,
		}).Error; err != nil {
			return err
		}
		changed = true
		return nil
	})
	return changed, err
}

// AssetRefsByIDPrefix 返回 ID 以 prefix 开头的素材（跨用户），用于找到平台素材加入各用户素材库的副本。
func (r *Repository) AssetRefsByIDPrefix(prefix string) ([]model.Asset, error) {
	var assets []model.Asset
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix)
	err := r.db.Select("id", "user_id").Where("id LIKE ? ESCAPE '\\'", escaped+"%").Find(&assets).Error
	return assets, err
}
