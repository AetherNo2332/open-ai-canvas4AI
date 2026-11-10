package app

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"strings"
	"time"
	"yingce/backend/internal/model"
)

func (s *Service) AdminArkReviewQueue(actor *model.User, status string, page int, pageSize int) (*AdminArkReviewPage, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	status = firstNonEmpty(strings.TrimSpace(status), arkPortraitAssetPendingReview)
	if status != arkPortraitAssetPendingReview && status != arkPortraitAssetRejected {
		return nil, BadAuthRequest("审核状态无效")
	}
	page, pageSize = normalizeAdminPage(page, pageSize)
	items, total, err := s.repo.ArkPortraitReviewQueue(status, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, err
	}
	groupIDs := make([]string, 0, len(items))
	userIDs := make([]string, 0, len(items))
	for _, item := range items {
		groupIDs = append(groupIDs, item.GroupID)
		userIDs = append(userIDs, item.UserID)
	}
	groups, err := s.repo.ArkPortraitGroupsByIDs(uniqueStrings(groupIDs))
	if err != nil {
		return nil, err
	}
	users, err := s.repo.UsersByIDs(uniqueStrings(userIDs))
	if err != nil {
		return nil, err
	}
	views, err := s.arkAssetViewsForUser("", items)
	if err != nil {
		return nil, err
	}
	for index := range views {
		views[index].Uploader = arkUserSummary(users, items[index].UserID)
		if group, ok := groups[items[index].GroupID]; ok {
			views[index].Group = &ArkPortraitGroupRef{ID: group.ID, Name: group.Name, Kind: group.Kind, Scope: group.Scope}
		}
	}
	return &AdminArkReviewPage{Items: views, Total: total, Page: page, PageSize: pageSize}, nil
}

// AdminApproveArkAssets 通过待审核素材并提交方舟；单条失败保留为待审核并返回原因，不影响其他条目。
func (s *Service) AdminApproveArkAssets(ctx context.Context, actor *model.User, ids []string) (*AdminArkReviewResult, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	ids, err := arkReviewIDs(ids)
	if err != nil {
		return nil, err
	}
	setting, err := s.arkCredentialSetting()
	if err != nil {
		return nil, err
	}
	items, err := s.repo.ArkPortraitAssetsByIDs(ids)
	if err != nil {
		return nil, err
	}
	result := &AdminArkReviewResult{Succeeded: []string{}, Failed: []AdminArkReviewFailed{}}
	found := make(map[string]bool, len(items))
	for index := range items {
		asset := &items[index]
		found[asset.ID] = true
		if err := s.approveArkAsset(ctx, actor, setting, asset); err != nil {
			result.Failed = append(result.Failed, AdminArkReviewFailed{ID: asset.ID, Error: err.Error()})
			continue
		}
		result.Succeeded = append(result.Succeeded, asset.ID)
	}
	for _, id := range ids {
		if !found[id] {
			result.Failed = append(result.Failed, AdminArkReviewFailed{ID: id, Error: "素材不存在"})
		}
	}
	if err := s.appendAdminAudit(actor, "ark_asset.approve", "ark_portrait_asset", strings.Join(result.Succeeded, ","), "通过方舟素材上传", map[string]any{"succeeded": len(result.Succeeded), "failed": len(result.Failed)}); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) approveArkAsset(ctx context.Context, actor *model.User, setting arkPrivateAssetSettingValue, asset *model.ArkPortraitAsset) error {
	if asset.Status != arkPortraitAssetPendingReview {
		return errors.New("该素材已处理，请刷新列表")
	}
	group, err := s.adminArkGroup(asset.GroupID)
	if err != nil {
		return err
	}
	resource, err := s.repo.ResourceForUser(asset.UserID, asset.ResourceID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.New("上传者已删除素材文件")
	}
	if err != nil {
		return err
	}
	if !arkPortraitGroupAvailable(*group, setting) {
		return errors.New("素材组所在的方舟项目已更换，无法提交")
	}
	if arkGroupExpired(*group, time.Now()) {
		return errors.New("真人素材组授权已过期")
	}
	if err := s.submitArkPortraitAsset(ctx, setting, *group, asset, resource); err != nil {
		return err
	}
	now := time.Now()
	asset.ReviewedBy, asset.ReviewedAt = actor.ID, &now
	return s.repo.SaveArkPortraitAsset(asset)
}

// AdminRejectArkAssets 驳回待审核素材；原因会展示给上传者。
func (s *Service) AdminRejectArkAssets(actor *model.User, ids []string, reason string) (*AdminArkReviewResult, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	ids, err := arkReviewIDs(ids)
	if err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > 200 {
		return nil, BadAuthRequest("请填写 1-200 个字的驳回原因")
	}
	items, err := s.repo.ArkPortraitAssetsByIDs(ids)
	if err != nil {
		return nil, err
	}
	result := &AdminArkReviewResult{Succeeded: []string{}, Failed: []AdminArkReviewFailed{}}
	now := time.Now()
	for index := range items {
		asset := &items[index]
		if asset.Status != arkPortraitAssetPendingReview {
			result.Failed = append(result.Failed, AdminArkReviewFailed{ID: asset.ID, Error: "该素材已处理，请刷新列表"})
			continue
		}
		asset.Status, asset.Error, asset.ReviewedBy, asset.ReviewedAt = arkPortraitAssetRejected, reason, actor.ID, &now
		if err := s.repo.SaveArkPortraitAsset(asset); err != nil {
			return nil, err
		}
		result.Succeeded = append(result.Succeeded, asset.ID)
	}
	if err := s.appendAdminAudit(actor, "ark_asset.reject", "ark_portrait_asset", strings.Join(result.Succeeded, ","), "驳回方舟素材上传", map[string]any{"reason": reason, "count": len(result.Succeeded)}); err != nil {
		return nil, err
	}
	return result, nil
}

func arkReviewIDs(ids []string) ([]string, error) {
	ids = uniqueStrings(ids)
	if len(ids) == 0 || len(ids) > 100 {
		return nil, BadAuthRequest("每次请选择 1-100 份素材")
	}
	return ids, nil
}
