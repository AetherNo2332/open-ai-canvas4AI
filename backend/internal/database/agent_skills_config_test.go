package database

import (
	"testing"

	"infinite-canvas/backend/internal/model"
)

func TestMigrateSchemaCreatesAgentSkillConfigTables(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: "file:agent-skill-config-migration?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable(&model.AgentSkillDefault{}) || !db.Migrator().HasTable(&model.AgentConversationSkill{}) {
		t.Fatal("迁移 v51 未创建 agent_skill_defaults / agent_conversation_skills 表")
	}
	if !db.Migrator().HasIndex(&model.AgentSkillDefault{}, "idx_agent_skill_defaults_scope_skill") {
		t.Fatal("迁移 v51 未创建默认技能 (scope, skill_id) 唯一索引")
	}
	if !db.Migrator().HasIndex(&model.AgentConversationSkill{}, "idx_agent_conversation_skills_conversation_skill") {
		t.Fatal("迁移 v51 未创建会话技能 (conversation_id, skill_id) 唯一索引")
	}
	if CurrentSchemaVersion != 51 {
		t.Fatalf("CurrentSchemaVersion = %d, want 51", CurrentSchemaVersion)
	}
	if err := RequireSchemaVersion(db); err != nil {
		t.Fatalf("RequireSchemaVersion 拒绝 v51 结构：%v", err)
	}
}

func TestAgentSkillConfigTablesEnforceCompositeUniqueness(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: "file:agent-skill-config-unique?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	first := model.AgentSkillDefault{ID: "asd-1", Scope: "global", SkillID: "skill-1", SkillVersionID: "sv-1", Enabled: true, Revision: 1}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	duplicate := first
	duplicate.ID = "asd-2"
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("同一 (scope, skill_id) 的重复默认技能行应被唯一索引拒绝")
	}
	conversation := model.AgentConversationSkill{ConversationID: "conv-1", SkillID: "skill-1", SkillVersionID: "sv-1", Source: "global"}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	duplicateConversation := conversation
	if err := db.Create(&duplicateConversation).Error; err == nil {
		t.Fatal("同一 (conversation_id, skill_id) 的重复会话技能行应被唯一索引拒绝")
	}
	other := conversation
	other.SkillID = "skill-2"
	other.Source = "user"
	if err := db.Create(&other).Error; err != nil {
		t.Fatalf("同一会话下的不同技能应可共存：%v", err)
	}
}
