# W09 VM 报告缺陷修复与第二轮复测

## 版本与范围

输入是用户指定的 `W09-VM-TEST-REPORT.md`（2026-11-28）及其基线隔离复现、矩阵和关系证据。被测版本为集成基线 `6e20dd53be68c9d6a023f97996eb1589eceef3c5` 加 `w09-reviewed.patch`，VM 本地快照 `140d93f2ef48298fd3c0c8d39f7b32dbc92e12ca`；故障插桩提交 `9f20a284db63d90e8d770887b90e816f226230dd` 不属于生产修复。

本轮在 `fix/w09-recovery-on-integration` 修复报告的两处门禁问题和画布入口文案；不改 W09 提交/恢复生产逻辑、Schema 113、工具协议和预算。工作树中已有 VM hook 文件与调用点是接手时已有改动，保留，不纳入本轮修复增量包。

## 逐项处理

| 报告编号 | 源码判断与处理 | 复测标准 |
| --- | --- | --- |
| W09-BASE-1 | `cloud_agent_pi_vision_wiring_test.go` 两个函数间缺空行；使用 Go 1.25 gofmt 修正 | `backend:gofmt` 通过 |
| W09-BASE-2 | `TestPiMixedMalformedBatchPreservesSuccessfulWriteAcrossRestart` 在当前夹具已冻结业务工具目录后，用 `cloudAgentTools` 原始全集覆盖 canonical tools，却未同步目录身份；入口拒绝是预期完整性保护，不是生产摘要不确定。移除该过时覆盖与旧类别激活，保留当前冻结目录，增加重启后 hash 不变及验证通过断言；升级拒绝回归另增目录内容被改写的场景 | 原用例成功写入一次、非法调用被拒、重启重放不重复；旧版/被改写目录仍拒绝，不能放松生产校验 |
| W09-ENV-1 | `agentSDKOpenDatabase` 明确要求 `TEST_AGENT_POSTGRES_DSN` 指向 `canvas_event_integration`；CI 已使用正确库。是 VM 配置问题，不放宽库名校验 | 第二轮先检查专用库名称、cgo 与 Worker 构建，重跑完整 `backend:full`，不是只重跑 F03 |
| W09-OBS-1 | 分集建档预分配 canvasId，但实际画布由 Ensure API 首次创建；页面把未创建 ID 展示成地址误导使用。移除原始 ID，说明使用「进精修画布」首次自动创建，保留 Ensure 成功后导航 | 新作品首次进入、刷新再进均成功且同一 canvasId；创建失败保持详情且显示错误 |
| W09-OBS-2/3 | 未知 kind 不被既有 ticker 选择、关停后结果不明属于设计边界，不改为自动重发/退款 | 维持明确待核对及不重复语义，保留已知边界 |

## 本轮验证与剩余工作

原混合批次用例在修改前本机复现与 VM 相同的目录内容变化报错；修改后与非法参数/旧版合同专项通过。新增的目录内容改写拒绝场景单独验证。前端为说明文案调整，没有新增前端业务判定或更改后端创建契约。

本机验证（均针对本轮工作树）：

| 命令/检查 | 结果 |
| --- | --- |
| 原 `TestPiMixedMalformedBatchPreservesSuccessfulWriteAcrossRestart` 修复前 | 失败，复现 VM 的冻结目录内容变化报错 |
| `go test ./internal/app -run 'TestPi(MixedMalformedBatch\|Malformed\|NativeMalformed)\|TestAgentReadContractUpgrade' -count=1 -v` | 7 个顶层测试通过，10.885s |
| `go test ./internal/app -run '^TestAgentReadContractUpgradeRejectsOldExecution/changed_frozen_tools' -count=1 -v` | queued claim / returning worker 两个新增子场景通过，2.961s |
| Go1.25 `gofmt -l internal/app internal/repository` | 无输出，退出 0 |
| `go test ./internal/architecture -count=1` | 通过，3.606s |
| `go vet ./internal/app` | 退出 0 |
| Web `bun run typecheck` | 退出 0 |
| Web `bun test test/shortplay-canvas-entries.test.ts` | 3 pass / 0 fail |
| Web Prettier 检查本次详情页 | 通过 |
| `git diff --check` | 通过 |

仅做相关专项，不重跑已经获得 VM 证据的完整 W09 故障矩阵；完整 full 门禁、真实页面及真实渠道留给第二轮 VM。

第一轮报告的 A–D PASS 以实际场景展开为准：B01/B03 明确为图片/音频；B02 没有逐类型展开；C02 未明确跨项目/类型，C04 未明确 failed。第二轮先检查已有原始证据，确实缺失再补测。E03 的平台事件为空，应区分短剧直接 CreateTask 与创作平台提交路径；不可用空平台事件证明真实平台提交已覆盖。无须为了补证据修改生产路径。

发布仍需完整门禁通过，E04 真实图像/音频渠道和 F07 真实模型页面链路有授权配置与证据。确定性测试不能替代这些项目。
