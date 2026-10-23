package database

import (
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

func MigrateCanvasTasks(db *gorm.DB) error {
	return db.AutoMigrate(&model.CanvasTaskBinding{}, &model.CanvasMaterializationJob{}, &model.CanvasTaskOutput{}, &model.CanvasTaskResource{}, &model.CanvasMediaVersion{})
}
