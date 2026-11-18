package database

import (
	"gorm.io/gorm"

	"yingce/backend/internal/model"
)

func init() {
	schemaMigrations = append(schemaMigrations, migration{
		version: 108, name: "agent_shared_preferences_scope_memory", checksum: "agent-shared-preferences-scope-memory-v108-20261118",
		apply: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.UserAgentSharedPreferences{}, &model.AgentScopeMemory{}, &model.AgentScopeMemoryWrite{})
		},
	})
}
