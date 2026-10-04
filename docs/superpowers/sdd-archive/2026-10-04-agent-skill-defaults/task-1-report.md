# Task 1 报告：数据模型、迁移 v51 与 kernel 错误码

## 状态：DONE

## 实现内容

### 1. `backend/internal/model/agent_skills_config.go`（新建）
- `AgentSkillDefault`：字段与计划逐一对应 —— `ID`(primaryKey, size:36)、`Scope`(size:16, "global")、`SkillID`(size:36)、`SkillVersionID`(size:36)、`Position`(int)、`Enabled`(bool)、`Revision`(int64, default:0)、`UpdatedBy`(size:36)、`CreatedAt`/`UpdatedAt`。唯一索引 `idx_agent_skill_defaults_scope_skill (scope, skill_id)`（priority 1/2）。
- `AgentConversationSkill`：`ConversationID`(size:80)、`SkillID`(size:36)、`SkillVersionID`、`ContentHash`(size:64)、`Source`(size:16, 取值 global/user)、`Position`、`CreatedAt`。唯一索引 `idx_agent_conversation_skills_conversation_skill (conversation_id, skill_id)`。
- **实现偏差（必要）**：`AgentConversationSkill` 的主键定为 `(conversation_id, skill_id)` 复合键（两个字段都带 `primaryKey`），而不是把 ConversationID 做单列主键。首次实现用单列主键时，SQLite 为该单列生成的隐式唯一索引 `sqlite_autoindex_...` 会拒绝同一会话的第二行技能（测试 `TestAgentSkillConfigTablesEnforceCompositeUniqueness` 捕获了这个 bug：UNIQUE constraint failed: agent_conversation_skills.conversation_id）。复合主键与唯一索引 `(conversation_id, skill_id)` 语义一致，也匹配"一个会话多行技能"的领域事实（先例：`CloudAgentPiEntry` 的 `(session_id, sequence)` 复合主键）。model 注释已说明该约束。

### 2. `backend/internal/database/migrations.go`（修改）
- `CurrentSchemaVersion` 50 → 51。
- 新增 checksum 常量 `agentSkillDefaultsChecksum = "sha256:agent-skill-defaults-v46-20261004"`（命名/取值格式照既有 `cloudAgentPiSessionsChecksum` 等惯例：sha256 前缀 + 名称 + 内部序号 v46 + 日期）。
- `schemaMigrations` 追加 `{version: 51, name: "agent_skill_defaults", apply: tx.AutoMigrate(&model.AgentSkillDefault{}, &model.AgentConversationSkill{})}`，照 v42 写法。
- 未改动 `PreviousUpstreamSchemaVersion`（43）与让位搬迁表（v51 不在其作用范围）。

### 3. `backend/internal/kernel/error_codes.go`（修改）
- 新增三个 reason 常量：`ReasonAgentSkillBudgetExceeded = "agent_skill_budget_exceeded"`、`ReasonAgentSkillDefaultsRevisionConflict = "agent_skill_defaults_revision_conflict"`、`ReasonAgentSkillDefaultsInvalid = "agent_skill_defaults_invalid"`。gofmt 对齐导致 const 块整体重排（纯空白变化）。

### 4. `backend/internal/kernel/errors.go`（修改）
- `AgentSkillBudgetExceeded(details map[string]any)`：Status/Code = 400，Reason = `agent_skill_budget_exceeded`，Message 固定用户可读文案，Details 透传（预算名/上限/实际值）。
- `AgentSkillDefaultsConflict(currentRevision int64)`：Status/Code = 409（`CodeConflict`），Reason = `agent_skill_defaults_revision_conflict`，Details = `{"currentRevision": <int64>}`。
- `AgentSkillDefaultsInvalid(message string, details map[string]any)`：Status/Code = 400，Reason = `agent_skill_defaults_invalid`，Message 由调用方传入（含技能 ID），Details 透传。

## TDD 证据

**RED**（先写两个测试文件，运行失败，均为编译失败——model 与 kernel 符号尚不存在）：
```
internal\database\agent_skills_config_test.go:17:36: undefined: model.AgentSkillDefault
internal\database\agent_skills_config_test.go:17:91: undefined: model.AgentConversationSkill
internal\kernel\agent_skill_errors_test.go:8:12: undefined: AgentSkillBudgetExceeded
internal\kernel\agent_skill_errors_test.go:12:22: undefined: ReasonAgentSkillBudgetExceeded
...
FAIL infinite-canvas/backend/internal/database [build failed]
FAIL infinite-canvas/backend/internal/kernel [build failed]
```

**中间失败（捕获真实 bug）**：实现后 `TestAgentSkillConfigTablesEnforceCompositeUniqueness` 失败 —— 会话表第二行技能被单列主键的隐式唯一索引拒绝（见上文偏差说明）。用临时测试 dump `Migrator().GetIndexes()` 确认了 `sqlite_autoindex_agent_conversation_skills_1 unique=true cols=[conversation_id]`，改为复合主键后消除（临时 dump 测试已删除）。

**GREEN**：
```
$ cd backend && go test ./internal/database/ ./internal/kernel/
ok      infinite-canvas/backend/internal/database       7.775s
ok      infinite-canvas/backend/internal/kernel         (cached)
```
`go vet ./internal/database/ ./internal/kernel/` 通过。

## 测试覆盖

`backend/internal/database/agent_skills_config_test.go`：
1. `TestMigrateSchemaCreatesAgentSkillConfigTables`：sqlite 内存库（照 `cloud_agent_recovery_test.go` 的 `database.Open` 写法）+ 全量 `MigrateSchema(db)`；断言两张表存在（`db.Migrator().HasTable`）、两个唯一索引存在（`HasIndex`）、`CurrentSchemaVersion == 51`、`RequireSchemaVersion(db)` 接受。
2. `TestAgentSkillConfigTablesEnforceCompositeUniqueness`：真实插入验证唯一索引行为——同 `(scope, skill_id)` 默认技能重复行被拒、同 `(conversation_id, skill_id)` 会话技能重复行被拒、同会话不同技能（user 来源）可共存。

`backend/internal/kernel/agent_skill_errors_test.go`：断言三个构造器的 Status/Code/Reason/Message/Details（含 conflict 的 `currentRevision` int64 与 details 透传）。

## 变更文件（commit 8d3a6a7d）

- 新建 `backend/internal/model/agent_skills_config.go`
- 新建 `backend/internal/database/agent_skills_config_test.go`
- 新建 `backend/internal/kernel/agent_skill_errors_test.go`
- 修改 `backend/internal/database/migrations.go`
- 修改 `backend/internal/kernel/error_codes.go`
- 修改 `backend/internal/kernel/errors.go`

## 自审结论

- 名称与计划逐字一致：`AgentSkillDefault`、`AgentConversationSkill`、`AgentSkillBudgetExceeded`、`AgentSkillDefaultsConflict`、`AgentSkillDefaultsInvalid`、三个 reason 字符串、唯一索引列。
- 未注册进 `database.Models()`（唯一表清单）：**有意的范围决策**。计划 Task 1 只要求迁移 v51；把表加进 `Models()` 会同时影响 `cmd/migrate-sqlite-postgres` 的 `verifyMigrationCoverage`（该工具的 `migrations()` 表清单会缺这两张表导致其测试失败），那是迁移工具职责，属 Task 1 文件清单之外的改动。Task 2（repository）与 Task 4/6 的 app 测试通过迁移路径建表，不受影响。建议 controller 在后续任务评审时确认 `Models()` 注册放在哪个任务（若 Task 2 文件清单不含 `schema.go`，需要在评审时裁定）。
- gofmt：工作区为 CRLF 检出（`core.autocrlf=true`，仓库无 `.gitattributes` Go 规则），`gofmt -l` 对所有 CRLF 文件（含未修改的既有文件如 `cloud_agent_pi_session.go`）都会报 CR-only 噪音。已逐文件验证：对本次 6 个文件，"格式化后还原 CRLF"与"格式化前"内容逐字节一致，即 gofmt 差异全部为 CR 噪音，无真实格式问题。提交入库为 LF（git 规范化），CI 的 gofmt 检查基于 LF 内容。
- 所有新文件为 CRLF 工作区检出风格，`git diff` 无整文件行尾噪音（migrations.go +6-1、errors.go +15、error_codes.go 33 行重排均为真实变更）。
- 迁移幂等性由既有 `TestMigrateSchemaRecordsAndValidatesVersion`（含 `MigrateSchema` 二次执行）与 v51 记录校验覆盖，全部通过。

## 疑虑 / 备注

1. `Models()` 注册与 `migrate-sqlite-postgres` 表清单：见上文自审，需 controller 裁定归属任务。
2. `agent_skill_budget_exceeded` 用 400（CodeInvalidArgument）而非 429：计划明确写 status 400，已照办。
3. 未运行全仓 `go test ./...`（任务限定两个包）；`internal/app` 等包依赖 `database.Models()` 的测试不受本次改动影响（未改 `Models()`）。
