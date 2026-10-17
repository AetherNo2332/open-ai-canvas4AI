package repository

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"time"
)

func (r *Repository) BootstrapDesktopOwner(ctx context.Context) (model.LocalProfile, model.User, error) {
	candidate := model.LocalProfile{Singleton: 1, ID: kernel.NewID(), UserID: kernel.NewID(), CreatedAt: time.Now().UTC()}
	var profile model.LocalProfile
	var user model.User
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Start with a write, rather than upgrading a shared SQLite read lock.
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; err != nil {
			return err
		}
		if err := tx.First(&profile, "singleton = ?", 1).Error; err != nil {
			return err
		}
		if profile.ID == candidate.ID {
			user = model.User{ID: profile.UserID, Username: "local_" + profile.UserID[:8], DisplayName: "本地画布", Role: model.UserRoleAdmin, Status: model.UserStatusActive, CreatedAt: profile.CreatedAt, UpdatedAt: profile.CreatedAt}
			return tx.Create(&user).Error
		}
		if err := tx.First(&user, "id = ?", profile.UserID).Error; err != nil {
			return err
		}
		if user.Status != model.UserStatusActive {
			return errors.New("local owner is disabled")
		}
		return nil
	})
	return profile, user, err
}

func (r *Repository) DesktopProfile() (model.LocalProfile, error) {
	var profile model.LocalProfile
	err := r.db.First(&profile, "singleton = ?", 1).Error
	return profile, err
}
