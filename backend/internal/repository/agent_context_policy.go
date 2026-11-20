package repository

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"yingce/backend/internal/model"
)

func (r *Repository) AgentSharedPreferences(userID string) (model.AgentSharedPreferences, error) {
	var row model.UserAgentSharedPreferences
	err := r.db.First(&row, "user_id = ?", userID).Error
	return row.AgentSharedPreferences, err
}

func (r *Repository) SaveAgentSharedPreferences(userID string, prefs model.AgentSharedPreferences) (model.AgentSharedPreferences, error) {
	expected := prefs.Revision
	prefs.Revision++
	row := model.UserAgentSharedPreferences{UserID: userID, AgentSharedPreferences: prefs}
	var result *gorm.DB
	if expected == 0 {
		result = r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	} else {
		result = r.db.Model(&row).Where("revision = ?", expected).
			Select("language", "tone", "verbosity", "format", "revision").Updates(&row)
	}
	if result.Error != nil {
		return model.AgentSharedPreferences{}, result.Error
	}
	if result.RowsAffected != 1 {
		return model.AgentSharedPreferences{}, ErrTaskStateConflict
	}
	return prefs, nil
}

func (r *Repository) AgentScopeMemory(key model.AgentScopeMemoryKey) (model.AgentScopeMemory, error) {
	row := model.AgentScopeMemory{AgentScopeMemoryKey: key}
	err := r.db.First(&row).Error
	return row, err
}

func (r *Repository) AgentScopeMemoryWrite(key model.AgentScopeMemoryKey, runID, callID string) (model.AgentScopeMemoryWrite, error) {
	row := model.AgentScopeMemoryWrite{AgentScopeMemoryKey: key, RunID: runID, CallID: callID}
	err := r.db.First(&row).Error
	return row, err
}

func (r *Repository) SaveAgentScopeMemory(row model.AgentScopeMemory, write model.AgentScopeMemoryWrite) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		receipt := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&write)
		if receipt.Error != nil {
			return receipt.Error
		}
		if receipt.RowsAffected != 1 {
			return ErrTaskStateConflict
		}
		var result *gorm.DB
		if row.Revision == 1 {
			result = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		} else {
			result = tx.Model(&row).Where("revision = ?", row.Revision-1).
				Select("revision", "entries_json", "next_entry_seq").Updates(&row)
		}
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrTaskStateConflict
		}
		return nil
	})
}
