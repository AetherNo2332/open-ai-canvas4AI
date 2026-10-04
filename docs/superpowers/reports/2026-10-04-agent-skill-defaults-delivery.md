# Agent 默认技能交付与验证记录

日期：2026-10-04。工作区 `D:\open-ai-canvas`，分支 `feat/agent-skill-defaults`。

## 交付

接续 [handoff](../plans/2026-10-04-agent-skill-defaults-handoff.md)，按 native 方式完成 Tasks 7–13，以及 Task 14 验证与独立审查后的集中修复。其他协作者的首页、皮肤和 canary 合入提交保留。

| 提交 | 内容 |
| --- | --- |
| `77693989` | preset 移除 8 个上限，只拒绝空集合 |
| `8e1d1fc9` | 管理员 Agent 设置页默认技能编辑与 CAS API |
| `f100993a` | 登录用户只读默认摘要接口 |
| `fc50c978` | 删除前端运行状态分区与轮询，更新导航 |
| `05651dcf` | 选择器移除数量限制、默认来源展示 |
| `4b6c2e06` | 子智能体头像组件、布局测试、SVG/许可证；数据源留待 P3 |
| `ae6d6f16` | OpenAPI、五份专题文档同步 |
| `392426c2` | 集中修复真实 Pi 消费、事务基线、持久 revision/CAS、有效默认展示及预算明细 |

集中修复：

- 未安装的全局默认也能通过 Pi 装配和读取；仍校验当前公开/启用、版本归属、hash、ZIP。
- 全局元数据读取冻结 `SKILL.md`；用户技能使用冻结版本/hash。
- 会话基线与 Run、占位任务、积分预留同事务提交；拒绝请求不修改基线。
- `system_settings` 键 `agent_skill_defaults_revision` 保存单调修订号，清空不重置；事务锁定设置行再 CAS，GET 读一致快照。
- Run 摘要带 `source`。旧会话按自身冻结来源显示默认，新会话读取完整摘要，不依赖市场分页/安装；已启用列表保留用户选择开关。
- 标题和聊天展示真实 `budget/limit/actual`；重复 ID 返回明确错误，刷新响应使用请求序号。
- 文档修正实际 v51 迁移名称 `agent_skill_defaults`，原“当前 v42”标为历史。

## 验证

| 检查 | 结果 |
| --- | --- |
| 后端 `go test ./...` | 通过，退出码 0；internal/app 568.122 秒 |
| 本任务前端聚焦 | 16/16 通过，7 文件 |
| 前端 lint | 通过 |
| 前端 build（含 TypeScript） | 通过；常规 bundle/插件耗时警告 |
| 前端全量 | **2154 通过、11 失败，共 2165** |
| 修改的 Go 源码按 LF 检查 gofmt | 35 文件、0 未格式化；原 checkout 850 项 CRLF 噪音 |
| `git diff --check` | 通过 |

未声称分支全绿或可合并；未 push、开 PR、merge、部署、SSH 或操作容器。

### 失败来源

独立 `git archive` 快照共享相同依赖，运行五个相关测试文件：handoff `e89abfeb` **58/58**；canary 合入后、前端任务继续前 `20689871` **47 通过、11 失败**。当前全量恰为同一 11 项，未新增失败。它们是保留的 canary 合入问题，依然属于分支合并门槛：

1. `admin-ui-regressions.test.ts`：后台 token/shell 隔离。
2. `agent-operation-feed-view.test.tsx`：失败步骤展开/标记。
3. `canvas-visual-contrast.test.ts`：画布与节点背景区分。
4. 同文件：保留表面颜色。
5. 同文件：网格 token/透明度。
6. `create-canvas-handoff.test.ts`：click 不作重试上下文。
7. 同文件：Runtime 前失败关联。
8. 同文件：资产入新画布且项目先持久化。
9. `create-library-button.test.ts`：确认前不自动增加参考。
10. 同文件：视频同名模型组参考能力入口。
11. 同文件：首页默认图片生成模式。

### 审查及覆盖

初次独立分支审查 `cd13b79b..ae6d6f16`：0 Critical、8 Important，覆盖 Task 6。六项后端缺陷先在 RED 日志复现；前端先复现真实预算信封丢明细及缺失有效默认集 helper。原审查代理对 `ae6d6f16..392426c2` 限定复审确认：**8 个 Important 全部 addressed，无新增 Critical/Important，本次修复范围可以接受**。整个分支仍因 11 项前端失败不满足合并门槛。

后端回归覆盖 `TestReviewGlobalDefaultNativeMaterialization`、`TestReviewDefaultsRejectPrivateAfterConfiguration`、`TestReviewRejectedRunDoesNotPersistSkillBaseline`、`TestReviewRepeatedUserSelectionKeepsFrozenVersion`、`TestReviewSkillDefaultsClearPreservesRevision`、`TestReviewAdminSkillDefaultsDuplicateIsInvalid`。另有真实 **20 个未安装默认 → CreateRun → ClaimPiAgent → PiSkillFile**、容量拒绝续聊不改基线、两个独立 SQLite 文件库/WAL 连接同 revision 并发（一个成功、一个冲突）的集成覆盖。前端覆盖新/旧会话集合、空旧集合、无市场 Skill 对象的默认卡片及三种 budget 的标题/聊天呈现。

PostgreSQL 活体多实例测试、真实上游模型调用、浏览器 hover/键盘/读屏/响应式视觉验收未执行；不将服务测试或静态渲染表述为端到端部署结果。

## Rulings I made（台账顺序，含被替代的裁定）

1. Tasks 9→10、11→12 按依赖顺序。代价：调整顺序影响同文件落点。
2. 初始保留计划的 src/lib 布局测试位置，后被第 11 项替代。代价：原位置让生产 TypeScript 检查失败。
3. Task 2 CAS 风险原暂按可恢复覆盖接受、留 Task 4 加固，后提升为 Important，由第 12 项替代。代价：中间提交存在跨实例丢更新。
4. 会话技能采用 conversation_id+skill_id 复合主键。代价：偏离单列计划，避免第二技能被唯一键拒绝。
5. Models()/SQLite→PostgreSQL 登记归 Task 2。代价：遗漏会导致迁移丢技能配置。
6. Task 4 曾使用 max(row.revision)、空表 0，由第 12 项替代。代价：中间提交清空伪冲突/ABA。
7. handler 使用 service 类型 alias 保持 facade。代价：新增一个导出别名。
8. 用户要求 Task 6 起 native，前五任务保留 SDD，最终审查覆盖 Task 6。代价：后续无逐任务独立审查。
9. DLL 故障期间 Task 9 先于 Task 8 提交，接口不变、分别验证。代价：提交顺序偏离计划。
10. 头像遵循 AGENTS 的 transform/opacity、稳定运行环/reduced-motion，覆盖计划 width/pulse。代价：视觉细节与计划不同。
11. 布局测试最终放 web/test，生产不导入 Bun 类型。代价：文件位置变，接口不变。
12. CAS/清空修订号必须用持久设置行锁和一致快照加固。代价：锁竞争；PostgreSQL 真实集群尚待验证。
13. 每轮重发用户 SkillIDs 保持冻结版本；允许新 ID 和 global→user 升级。代价：已选用户版本升级需新会话，后续应提供显式升级合同。
14. 当前安装版本保留用户库显示元数据，旧冻结版本从入口取元数据。代价：旧显示名可能与原库标题不同，版本/hash 不变。
15. 按 handoff 要求一次 scoped re-review，覆盖 executing-plans 默认不复审。代价：一次额外限定审查。
16. 保留独立 canary 快照复现的 11 个无关失败。代价：分支全绿/合并门槛未满足。
17. P2 Workspace、P3–P5 Crew/SSE 留后续；头像来源为空，未声称浏览器验收。代价：当前无实时子智能体数据。

## Deferred minors

- enabled=2/-1 仍按 true，position 未规范化（当前管理 UI 只发 0/1）。
- disabled 死分支、Source 未强类型约束、低选择性索引。
- trim 注释、空 ID 错误类断言、单次 bool helper、相近路由测试、旧台账方法数量文案。
- Windows 常规滚动条/reduced-motion 下头像展开可能改变行高；接 P3 时验收。

随必修问题解决：caller slice 复制、排序 tie-break、重复 ID、刷新响应顺序。

## 复现和证据

Windows GCC 外部链接器 DLL 搜索失败 `0xc0000135`，仅在忽略目录内并置 collect2.exe、ld.exe 与 UCRT64 DLL，未修改系统编译器。

```powershell
Set-Location D:\open-ai-canvas\backend
go test '-ldflags=-extldflags=-BD:/open-ai-canvas/.superpowers/archive/2026-10-04-agent-skill-defaults-capacity/gcc-probe/' ./...
Set-Location D:\open-ai-canvas\web
bun test
bun run lint
bun run build
```

原 SDD 目录计划定向移至 `.superpowers/archive/2026-10-04-agent-skill-defaults-capacity/`，保留 progress.md、RED/GREEN、最终 backend/web/build 日志、两个源码基线和链接器夹具。只移出该计划原路径，其余工作区保留。实际归档状态待补录。
