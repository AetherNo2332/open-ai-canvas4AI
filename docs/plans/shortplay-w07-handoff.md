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
- 工具目录实测（本树，含 `shortplay_asset`）：29 个工具、约 60.6KB（上一片实测 60593 字节，
  距 64000 上限余 3407）；最终门禁前重测并登记。该数字只代表本树，不代表与 tsk 续接 schema
  合并后的预算。
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
- `shortplay_asset` 第二片：`place/image/audio` action 已接入同一审批链路（只入队，不直接提交付费任务）；place 重复执行只刷新同一张卡，image 重复入队复用同一 job（单测覆盖）。

## Done（W07 引擎级审批闭环，62eaea793 之后）

- 根因（已复现并修复）：`shortplay_asset` 只加了 schema/分类/写标记，**没有**接进
  `cloudAgentWriteApprovalPreview` 与 `runCloudAgentCanvasWriteTool` 两个 engine 分支，
  真实 `executeCloudAgentToolCall` 把它兜底成 canvas ops：审批预览走
  `prepareCloudAgentCanvasMutation` → `decodeCloudAgentCanvasArgs` 把 `action` 判成未知字段，
  于是降级为 schema_error 工具回执，run 停在 `running`、`Approval=nil`。修复即补这两个 case。
- 修复后引擎级测试（`shortplay_agent_approval_engine_test.go`，全部走真实
  `executeCloudAgentToolCall` + `DecideCloudAgentApproval`）：
  - create 挂起成 `waiting_approval`，预览标题正确，画布/资产/job/卡零写入；
  - 拒绝后 run=`rejected`、Approval 清空，再推进会也不产生资产/卡/job/任务；
  - 批准决定重放幂等；worker 重启后执行一次 = 1 资产 1 卡、0 job、0 新任务；
    重复派发已执行步骤按无效调用状态终止，不再写第二行；
  - audio 批准后重启执行 = 1 个 `asset_audio` job、0 付费任务；重复派发被 CAS 拒绝，job 仍 1；
  - place 批准执行 = 1 张卡、集资产绑定 1 次；重复派发被 CAS 拒绝，不叠卡、不重复绑定；
  - 跨账号画布与自由画布：run 决定拒绝，不开审批卡、零写入。
- 回归证据：`go build ./internal/... ./cmd/server` exit 0；`go vet ./internal/app ./internal/shortplay`
  exit 0；`go test ./internal/app -run 'Shortplay|TestAgentToolSchemaArtifactMatchesRuntime|TestCloudAgentToolDispatch|TestDirector'`
  PASS（39.9s）；`go test ./internal/architecture` PASS。
- 未改 schema/描述，harness 无需再生成。

## Next（按序，第一项是下一动作）

1. W06 正式页面回归：投送、拖动后重跑、刷新、收录、回写、双账号隔离。
2. W08 确定性渠道跨进程全流程；W09 隔离恢复演练与完整门禁。
3. 最后：先 bump `VERSION` + `CHANGELOG` 提交，再在最终 HEAD 跑完整门禁，成功才
   普通快进更新同一 PR154。

## Open questions

- 与 tsk 续接 schema 合并后 64000 是否仍够：未合并，未验证；本树余量仅作参考。
- 真实文本/图片/音频凭据缺失：媒体与提取只能验到明确失败；单列，不阻塞确定性项。
- 重复派发“已执行步骤”在引擎侧按无效调用状态终止（run=failed），属既有协议守卫；
  真实 worker 只会派发待执行调用，本次未改该守卫语义。

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


