package database

import (
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// agentSessionsMigrationVersion 建立通用 Agent Session 根记录（业务范围不可变、单写者租约）。
const agentSessionsMigrationVersion int64 = 86

const agentSessionsChecksum = "sha256:agent-service-sessions-v86-20261106"

// agentContextMigrationVersion 建立非画布会话的消息与按范围划分的记忆笔记。
const agentContextMigrationVersion int64 = 87

const agentContextChecksum = "sha256:agent-service-context-v87-20261106"

// agentServiceMigrations 由 migrations_creation_platform.go 的 init 接在平台迁移之后登记。
var agentServiceMigrations = []migration{
	{version: agentSessionsMigrationVersion, name: "agent_service_sessions", checksum: agentSessionsChecksum, apply: migrateAgentSessions},
	{version: agentContextMigrationVersion, name: "agent_service_context", checksum: agentContextChecksum, apply: migrateAgentContext},
}

// AgentServiceModels 是 Agent 服务的持久结构，顺序即迁移与复制顺序。
func AgentServiceModels() []any {
	return []any{&model.AgentSession{}, &model.AgentSessionMessage{}, &model.AgentMemoryNote{}}
}

func migrateAgentSessions(tx *gorm.DB) error {
	return tx.AutoMigrate(&model.AgentSession{})
}

func migrateAgentContext(tx *gorm.DB) error {
	return tx.AutoMigrate(&model.AgentSessionMessage{}, &model.AgentMemoryNote{})
}
