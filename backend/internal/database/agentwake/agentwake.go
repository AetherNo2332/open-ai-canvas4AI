// Package agentwake appends scheduler wake events inside the producer's
// transaction. It stays separate from internal/model so the model package only
// declares data shapes.
package agentwake

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"yingce/backend/internal/model"
)

// Append uses the caller's transaction for every write: counter initialization,
// the locked increment, the sequence read and the event insert. The counter row
// lock survives until commit, so readers cannot observe a later event before an
// earlier uncommitted producer.
func Append(tx *gorm.DB, event model.AgentWakeEvent) error {
	counter := model.AgentEventCounter{ID: 1}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&counter).Error; err != nil {
		return err
	}
	if err := tx.Model(&model.AgentEventCounter{}).Where("id = ?", 1).UpdateColumn("value", gorm.Expr("value + 1")).Error; err != nil {
		return err
	}
	if err := tx.First(&counter, 1).Error; err != nil {
		return err
	}
	event.Sequence = counter.Value
	event.CreatedAt = time.Now()
	return tx.Create(&event).Error
}
