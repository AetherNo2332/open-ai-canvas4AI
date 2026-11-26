# shortplay W07 交接记录（上下文切换用）

## Goal

在 4AI 既有 Agent 工具合同内完成短剧工作台 W07：资产读面已接入；本轮先交付
`shortplay_asset` 的 create/update（含解锁），走「审批预览 → 执行」链路，并补权限与
重放测试、重生成 harness。整项任务的验收：W01–W09 全部通过后按最终干净 HEAD 跑完整
门禁并普通快进同一 PR154。

## Done（已验证）

- W04–W06 已提交并在 `docs/plans/shortplay-workbench-port.md` 记录：提取走平台文本任务、
  参考图/音色 job、调度先收口 running 再领 queued、资产卡投送/收录/画布回写。
- W07 读面 `shortplay_list_assets` 已接入：`backend/internal/app/shortplay_agent_tools.go`，
  schema/目录/分类/只读缓存/Markdown 描述齐全；测试
  `TestCloudAgentListShortplayAssetsReadsBoundProject` PASS；harness 已再生成。
- 工具目录实测（本树，含 `shortplay_asset`）：29 个工具、60313 字节；距 64000 上限 3687 字节。该数字只代表
  本树，不代表与 tsk 续接 schema 合并后的预算。
- `internal/architecture`、OpenAPI 路由合同、前端 tsc/eslint 在 W06 批次通过。

## Done（W07 写面第一片）

- `shortplay_asset` create/update（含解锁）已接入审批预览→执行链路：
  `shortplay_agent_write.go`（prepare/apply）、schema（`cloud_agent_tool_compile.go`）、
  策略写标记、`cloudAgentWrite` 分类、`agent_tools_canvas_edit` 分类、预览 case、
  执行 case、Markdown 描述；harness 已再生成。
- 行为：只接受集画布（自由画布/跨账号拒）；create 同分类同名幂等（批准重放不重复建行/建卡）；
  update 空字段不覆盖、`locked:false` 解锁、分类不可改、衍生造型拒；写入后绑集并刷新资产卡。
- 测试（`shortplay_agent_write_test.go`）：预览无副作用、create 重放幂等、update 解锁且保留
  未提交字段、跨账号拒、自由画布拒、只读 run 不暴露工具。全 PASS（app 17.4s）。
- `internal/architecture` PASS（读工具复杂度超限已用合并 case + 收窄 director 守卫修复）。
- 工具目录本树：29 个、60313 字节、余量 3687 字节（不声明两 PR 合并预算通过）。

## Next（按序，第一项是下一动作）

1. W07 第二片：`shortplay_asset` 增加 `place` / `image` / `audio` action（或独立工具），
   复用 `PlaceShortplayAssetCards` 与队列方法，仍走审批；补拒绝审批/批准重放与迟到回调测试。
2. W06 正式页面回归：投送、拖动后重跑、刷新、收录、回写、双账号隔离。
3. W08 确定性渠道跨进程全流程；W09 隔离恢复演练与完整门禁。
4. 最后：先 bump `VERSION` + `CHANGELOG` 提交，再在最终 HEAD 跑完整门禁，成功才
   普通快进更新同一 PR154。

## Open questions

- 与 tsk 续接 schema 合并后 64000 是否仍够：未合并，未验证；本树余量仅作参考。
- 真实文本/图片/音频凭据缺失：媒体与提取只能验到明确失败；单列，不阻塞确定性项。

## Key facts

- 唯一施工树：`C:\Users\13537\.codex\worktrees\shortplay-canvas-port\open-ai-canvas4ai-recovery`
  分支 `codex/shortplay-w04-recovery`；默认工作目录即此树。
- PR154 远端头：`codex/shortplay-workbench-port@8d910e9`；未推送、未合并、未直推 main。
- 4AI Agent 工具接线点（全部在 `backend/internal/app`）：
  `cloud_agent_tool_compile.go`（schema）、`agent_tool_catalog.go`（策略/装载）、
  `cloud_agent_tools.go`（`cloudAgentWrite` 审批分类）、`cloud_agent_tool_categories.go`、
  `cloud_agent_write_approval.go`（预览）、`cloud_agent_tool_dispatch.go`（执行）、
  `agent-tool-descriptions.md`、`agent/harness/TOOL_SCHEMA.json`（生成物）。
- 参考实现：`cloud_agent_character_create.go` 的 `prepareCloudAgentCharacterCreate` /
  `applyCloudAgentCharacterCreate`（预览无副作用、执行重新 prepare、事务内写）。
- 短剧域可复用：`shortplay.NewBaseAssetRow`、`shortplay.ApplyBaseAssetWrite`、
  `shortplay.NextAssetID`、`repo.UpdateShortplayAsset`（revision CAS）、
  `Service.shortplayBindAssetsToEpisode`、`Service.injectShortplayAssetCards`。
- 画布归属：`repo.CanvasProjectForUser` → `canvas.ProjectID` → `repo.ShortplayByProject`；
  分集：`repo.ShortplayEpisodeByCanvas`。自由画布与无作品画布必须拒绝资产写。
- 验证命令：`go build ./internal/... ./cmd/server`；`go test -count=1 -run '...' ./internal/app`；
  `go test -count=1 ./internal/architecture`；`go test -count=1 -run TestOpenAPI ./internal/handler`。
- 本机前端 `tsc` 需 `--noEmit --incremental false`（tsbuildinfo 写入受限）；`web` 构建
  若被阻断，用隔离 Linux Docker 跑项目原命令，不复用旧 CI 结果。
- 禁止：后台/第二写者、强推、新开 PR、直推 main、自动合并/生产部署、`prune`、删他人资源。

