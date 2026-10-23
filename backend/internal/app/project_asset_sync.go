package app

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

type SyncProjectAssetRequest struct {
	ExpectedProjectRevision int64   `json:"expectedProjectRevision"`
	Category                *string `json:"category"`
	FolderID                *string `json:"folderId"`
}

type ProjectAssetSyncState struct {
	ProjectRevision int64                `json:"projectRevision"`
	Asset           *ProjectAssetSummary `json:"asset"`
}

func projectAssetSyncError(err error) error {
	if errors.Is(err, repository.ErrProjectAssetSyncConflict) {
		return &AppError{Status: 409, Code: 409, Reason: "project_asset_sync_conflict", Message: "项目素材已变化，请重试", Retryable: true}
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return NotFound("项目或素材不存在")
	}
	return err
}

func (s *Service) GetProjectAssetSync(userID, projectID, assetID string) (ProjectAssetSyncState, error) {
	var state ProjectAssetSyncState
	err := s.repo.ProjectAssetSyncTransaction(userID, projectID, assetID, nil, func(tx *repository.Repository, project *model.Project, asset *model.Asset) error {
		state.ProjectRevision = project.Revision
		linked, err := tx.ProjectAssetLinked(projectID, asset.ID)
		if err != nil || !linked {
			return err
		}
		scoped := &Service{repo: tx}
		summary, err := scoped.projectAssetSummary(userID, projectID, asset)
		state.Asset = &summary
		return err
	})
	return state, projectAssetSyncError(err)
}

func (s *Service) SyncProjectAsset(userID, projectID, assetID string, req SyncProjectAssetRequest) (ProjectAssetSyncState, error) {
	var state ProjectAssetSyncState
	if req.ExpectedProjectRevision < 0 {
		return state, BadAuthRequest("项目版本无效")
	}
	err := s.repo.ProjectAssetSyncTransaction(userID, projectID, assetID, &req.ExpectedProjectRevision, func(tx *repository.Repository, _ *model.Project, asset *model.Asset) error {
		scoped := &Service{repo: tx}
		category := asset.Category
		if req.Category != nil {
			category = model.AssetCategory(strings.TrimSpace(*req.Category))
		}
		if !validAssetCategory(category) {
			return BadAuthRequest("不支持的资产业务分类")
		}
		// Resolve even for existing links: invalid targets must roll back the CAS.
		folder, err := scoped.resolveProjectAssetFolderID(projectID, req.FolderID)
		if err != nil {
			return err
		}
		linked, err := tx.ProjectAssetLinked(projectID, asset.ID)
		if err != nil {
			return err
		}
		if !linked {
			if _, err := scoped.LinkProjectAsset(userID, projectID, LinkProjectAssetRequest{AssetID: asset.ID, Category: string(category), FolderID: req.FolderID}); err != nil {
				return err
			}
		} else {
			if req.Category != nil && category != asset.Category {
				if err := tx.SetProjectAssetCategory(userID, projectID, asset.ID, category, time.Now()); err != nil {
					return err
				}
			}
			if req.FolderID != nil {
				link, err := tx.ProjectAssetLink(projectID, asset.ID)
				if err != nil {
					return err
				}
				if link.FolderID != folder {
					position, err := tx.NextProjectAssetPosition(projectID, folder)
					if err != nil {
						return err
					}
					if err := tx.MoveProjectAsset(projectID, asset.ID, folder, position); err != nil {
						return err
					}
				}
			}
		}
		current, err := tx.AssetForUser(userID, asset.ID)
		if err != nil {
			return err
		}
		summary, err := scoped.projectAssetSummary(userID, projectID, current)
		if err != nil {
			return err
		}
		project, err := tx.ProjectForUser(userID, projectID)
		if err != nil {
			return err
		}
		state = ProjectAssetSyncState{ProjectRevision: project.Revision, Asset: &summary}
		return nil
	})
	if err != nil {
		return ProjectAssetSyncState{}, projectAssetSyncError(err)
	}
	return state, nil
}
