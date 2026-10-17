package database

import (
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

const DesktopSchemaVersion = 3

func MigrateDesktop(db *gorm.DB) error {
	if err := db.AutoMigrate(&model.LocalProfile{}, &model.User{}, &model.AuthSession{}, &model.UserIdentity{}); err != nil {
		return err
	}
	if err := MigrateCanvasCommands(db); err != nil {
		return err
	}
	return MigrateCanvasTasks(db)
}
