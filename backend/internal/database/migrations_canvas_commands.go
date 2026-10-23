package database

import (
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

func MigrateCanvasCommands(db *gorm.DB) error {
	return db.AutoMigrate(&model.CanvasDocumentEpoch{}, &model.CanvasEntityVersion{}, &model.CanvasCommandReceipt{}, &model.CanvasCommandEvent{}, &model.CanvasActorHistory{}, &model.CanvasCommandResource{}, &model.CanvasCreateReceipt{}, &model.CanvasSnapshot{}, &model.CanvasSnapshotResource{})
}
