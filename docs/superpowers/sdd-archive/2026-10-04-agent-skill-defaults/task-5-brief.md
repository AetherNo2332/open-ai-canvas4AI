### Task 5: 管理员 HTTP 接口与路由注册

**Files:**
- Create: `backend/internal/handler/admin_agent_skill_defaults.go`
- Create: `backend/internal/handler/admin_agent_skill_defaults_test.go`
- Modify: `backend/internal/handler/api.go`（在 `RegisterAdminRoutes` 等注册处旁追加 `RegisterAdminAgentSkillDefaultsRoutes`）
- Modify: `backend/internal/handler/api_test.go`（`wanted` 表追加两条）

**Interfaces:**
- Consumes: Task 4 的两个 service 方法。
- Produces: `GET /api/admin/agent/skill-defaults` → `ok(c, AgentSkillDefaultsView)`；`PUT /api/admin/agent/skill-defaults`，body `{ revision int64, items []AgentSkillDefaultItem }`，成功 `ok(c, { revision })`，失败 `failService`（409/400 + reason + details）。注册函数签名照 `handler/admin_analytics.go:13`：`func RegisterAdminAgentSkillDefaultsRoutes(r *gin.RouterGroup, svc *service.Service)`。

- [ ] **Step 1: 写失败测试** — 照 `handler/admin_observability_test.go`：注册后断言 `GET|PUT /api/admin/agent/skill-defaults` 两条路由存在；`api_test.go` 的 `wanted` map 追加 `"GET /api/admin/agent/skill-defaults": false`、`"PUT /api/admin/agent/skill-defaults": false`。
- [ ] **Step 2: 运行确认失败** — `go test ./internal/handler/ -run "AdminAgentSkill|RegisterCanvasAPI"`。
- [ ] **Step 3: 实现** — handler 内 `currentUser` + 直接调 service，无业务判断；PUT 用 `c.ShouldBindJSON`。
- [ ] **Step 4: 运行确认通过** — 同 Step 2。
- [ ] **Step 5: Commit** — `feat(agent): 技能默认配置 - 管理员默认技能查询与保存接口`

