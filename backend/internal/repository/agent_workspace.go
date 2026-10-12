package repository

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *Repository) ensureAgentWorkspace(tx *gorm.DB, userID, canvasID string) (*model.AgentWorkspace, error) {
	var workspace model.AgentWorkspace
	err := tx.Where("user_id = ? AND canvas_id = ?", userID, canvasID).First(&workspace).Error
	if err == nil {
		return &workspace, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	workspace = model.AgentWorkspace{ID: kernel.NewID(), UserID: userID, CanvasID: canvasID, AgentsMDHash: fmt.Sprintf("%x", sha256.Sum256(nil))}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&workspace).Error; err != nil {
		return nil, err
	}
	if err := tx.Where("user_id = ? AND canvas_id = ?", userID, canvasID).First(&workspace).Error; err != nil {
		return nil, err
	}
	return &workspace, nil
}

func (r *Repository) AgentWorkspaceSnapshot(userID, canvasID string) (*model.AgentWorkspaceSnapshot, error) {
	var snapshot model.AgentWorkspaceSnapshot
	err := r.db.Transaction(func(tx *gorm.DB) error {
		workspace, err := r.ensureAgentWorkspace(tx, userID, canvasID)
		if err != nil {
			return err
		}
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND user_id = ?", workspace.ID, userID).First(workspace).Error; err != nil {
				return err
			}
		}
		snapshot.ID, snapshot.UserID, snapshot.CanvasID = workspace.ID, workspace.UserID, workspace.CanvasID
		snapshot.AgentsMD, snapshot.AgentsMDHash, snapshot.Revision = workspace.AgentsMD, workspace.AgentsMDHash, workspace.Revision
		return tx.Where("workspace_id = ?", workspace.ID).Order("position asc, skill_id asc").Find(&snapshot.Skills).Error
	})
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (r *Repository) AgentWorkspaceByID(userID, workspaceID string) (*model.AgentWorkspace, error) {
	var workspace model.AgentWorkspace
	err := r.db.Where("id = ? AND user_id = ?", workspaceID, userID).First(&workspace).Error
	return &workspace, err
}

func (r *Repository) UpdateAgentWorkspace(userID, canvasID string, expectedRevision int64, agentsMD, agentsMDHash string) (int64, error) {
	var revision int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// Acquire the writer/row lock before reading. A deferred SQLite transaction
		// cannot upgrade a stale WAL read snapshot, even with busy_timeout.
		if err := tx.Model(&model.AgentWorkspace{}).Where("user_id = ? AND canvas_id = ?", userID, canvasID).UpdateColumn("revision", gorm.Expr("revision")).Error; err != nil {
			return err
		}
		workspace, err := r.ensureAgentWorkspace(tx, userID, canvasID)
		if err != nil {
			return err
		}
		result := tx.Model(&model.AgentWorkspace{}).Where("id = ? AND user_id = ? AND revision = ?", workspace.ID, userID, expectedRevision).Updates(map[string]any{"agents_md": agentsMD, "agents_md_hash": agentsMDHash, "revision": gorm.Expr("revision + 1")})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return r.agentWorkspaceConflict(tx, workspace.ID, userID)
		}
		revision = expectedRevision + 1
		return nil
	})
	return revision, err
}

func (r *Repository) ReplaceAgentWorkspaceSkills(userID, canvasID string, expectedRevision int64, rows []model.AgentWorkspaceSkill) (int64, error) {
	var revision int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.AgentWorkspace{}).Where("user_id = ? AND canvas_id = ?", userID, canvasID).UpdateColumn("revision", gorm.Expr("revision")).Error; err != nil {
			return err
		}
		workspace, err := r.ensureAgentWorkspace(tx, userID, canvasID)
		if err != nil {
			return err
		}
		result := tx.Model(&model.AgentWorkspace{}).Where("id = ? AND user_id = ? AND revision = ?", workspace.ID, userID, expectedRevision).UpdateColumn("revision", gorm.Expr("revision + 1"))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return r.agentWorkspaceConflict(tx, workspace.ID, userID)
		}
		if err := tx.Where("workspace_id = ?", workspace.ID).Delete(&model.AgentWorkspaceSkill{}).Error; err != nil {
			return err
		}
		for i := range rows {
			rows[i].WorkspaceID = workspace.ID
		}
		if len(rows) > 0 {
			if err := tx.Create(&rows).Error; err != nil {
				return err
			}
		}
		revision = expectedRevision + 1
		return nil
	})
	return revision, err
}

func (r *Repository) agentWorkspaceConflict(tx *gorm.DB, workspaceID, userID string) error {
	var workspace model.AgentWorkspace
	if err := tx.Select("revision").Where("id = ? AND user_id = ?", workspaceID, userID).First(&workspace).Error; err != nil {
		return fmt.Errorf("workspace revision read: %w", err)
	}
	return kernel.AgentWorkspaceConflict(workspace.Revision)
}
