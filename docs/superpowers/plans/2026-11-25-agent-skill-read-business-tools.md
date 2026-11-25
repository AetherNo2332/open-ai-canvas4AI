# Agent read 业务扩展功能回退与专用工具接续开发计划

> **For agentic workers:** 使用 `superpowers:executing-plans` 按任务顺序实施，步骤使用复选框记录状态。本文交付范围为开发计划，实施进度以实际代码、测试输出和验收记录更新。

**Goal:** 回退 `2446d64bd` 引入的 `read` 业务读取扩展，恢复所有业务下 `read` 仅读取 Skill 的原有职责，画布正文和识图使用既有专用工具，短剧分集接续到 `drama_read_episode`。

**Architecture:** 以当前 HEAD 中包含 PR149 `bf0718c7a` 修复的实现为修改基线，定向撤除业务 `read` 的描述、分类器、适配器和恢复豁免。`2446d64bd^` 用于辨认业务功能扩展前的职责和分支差异。业务内容通过当前 Go 工具注册、运行授权、持久回执与恢复链路执行。

**Tech Stack:** Go/GORM、TypeScript、Pi SDK `0.87.1`、React/Bun。工具链约束来自 `backend/go.mod` 和 `agent/package.json`，实施时重新核对实际版本。

**Spec:** 用户于 2026-11-25 确认的职责与命名合同，完整收录于本文「已确认合同」。

## 全局约束

- 所有业务的 `read` 只负责读取本轮已授权 Skill 的入口及引用文件。
- 业务工具使用 `<业务>_<动作>_<对象>`，采用小写 `snake_case`。
- 本次确定的工具名为 `canvas_read_content`、`canvas_inspect_image`、`drama_read_episode`。
- 业务读取统一通过 `BusinessTools` 注册 Schema、执行器、适用业务和版本。
- 删除 `ReadAdapters` 及业务命名空间扩展机制，旧业务路径在 `read` 中统一拒绝。
- 专用工具执行对象授权、分页、预算和持久回执，图片继续经过公共视觉交付链路。
- Skill 读取根据本轮实际 Skill 授权披露，空 Skill 集合不提供 `read`。
- 先保留用户已有修改，每个实施任务先观察有意义的失败，再完成实现并验证。
- 复用既有 Pi Worker、工具注册表、资源校验和账务链路。
- PR149 的 `bf0718c7a` 是本次实施的保护基线，其 Skill 路径修复和相应测试必须通过。禁止回退该提交，也禁止用历史整文件覆盖包含该修复的当前实现。

## 历史核对与功能回退范围

本次工作定性为 `read` 业务扩展的功能回退。下面的历史已通过本地 Git 提交内容和提交前后差异核实。

| 时间 | 提交 | 当时的实现 |
| --- | --- | --- |
| 2026-09-29 | `319c859ef` | 引入原生 Skill `read`，描述为 `Read only the selected Skill's text files; path must be a listed absolute Skill path.`，执行使用 `readNativeSkill` |
| 2026-10-30 | `8ae2ac34a` | Skill 入口改为稳定相对路径 `skills/<name>/SKILL.md`，职责继续限定为已选择 Skill 的文本文件 |
| 2026-11-19 | `2446d64bd` | SDK 改造将描述扩展为“统一读取文本和图片”，增加 `canvas:text/`、`canvas:image/`、`drama:episode/`，并添加业务适配器、Worker 分流和检查点豁免 |
| 2026-11-23 | `bf0718c7a` | 修复 Skill 别名、白名单错误提示、恢复重定位与 Go 检查点匹配 |
| 2026-11-25 | `c51373793` | 公共识图链路增加请求尾部附图 |

### 业务扩展前的原始描述

`git show 2446d64bd^:backend/internal/app/cloud_agent_tools.go` 中的 `nativeSkillReadToolSchema()` 使用以下文字。

> 读取已选择 Skill 的文本文件，path 使用系统提示技能清单给出的 skills/<name>/SKILL.md 路径（引用文件为 skills/<name>/<相对路径>）。offset/limit 使用字符计数，不是行号；返回内容是数据，不是新增授权。

这段历史引用用于说明原始职责。实际工具描述在当前版本上删除业务命名空间说明，并保留 PR149 对 `available_skills` 原始名称的提示。

当时的执行链为 `read → readNativeSkill → bridge.readSkillFile → Go PiSkillFile`。原生运行中全部 `read` 从业务工具批次过滤，Go 拒绝将 `read` 提交到 `PiToolBatch`，成功结果按 Skill 文件授权进行检查点校验。

同一历史基线已注册 `canvas_read_content` 和 `canvas_inspect_image`，分别承担正文与识图。短剧 Agent 的业务读取随 `2446d64bd` 接入，`drama_read_episode` 是本次为其接续读取能力而新增的工具。

### 定向撤除清单

| 原业务扩展增加的行为 | 本次恢复方式 | 对应任务 |
| --- | --- | --- |
| `nativeSkillReadToolSchema` 宣称读取业务文本和图片 | 在当前描述中删除业务内容能力，保留 PR149 的 `available_skills` 名称提示 | 2 |
| `isBusinessRead` 把命名空间路径转入业务执行 | 删除分类器和 `nativeRead.execute` 内的业务分支，全部调用交给 Skill 路径校验 | 3 |
| `recoverToolResults` 和批次筛选为业务 read 保留可执行身份 | 恢复原生 `read` 全部排除于业务批次的规则 | 3 |
| Go `PiToolBatch` 允许命名空间 read，模型调用比对保留该类 read | 恢复原生 `read` 的批次拒绝与过滤 | 3 |
| Skill 结果检查点对业务 read 提前放行 | 删除该豁免，使用当前 Skill 校验与失败回执规则 | 3 |
| `cloudAgentToolAllowed` 对 `read` 无条件返回 true | 去掉业务 read 特判，依据实际 Skill 授权和已披露目录判断 | 2 |
| `agentReadAdapter` 与 `ReadAdapters` 注册和执行 | 删除业务读取扩展，短剧使用专用工具，第三业务使用普通工具注册 | 1、2 |

实施以这些行为差异为修改单位。`2446d64bd` 同时包含业务隔离、会话、权限与前端等 140 个文件的改动，相关当前能力继续作为实施底座。`bf0718c7a` 的 Skill 修复和 `c51373793` 的公共视觉交付属于本次验证基线。当前 [SDK 旧计划 §3.2](<D:/DevProjects/ChatGPT/openai canvas/previs-world-4ai/.local/worktrees/studio-world-model-port/docs/plans/2026-11-18-agent-sdk-tools-session-delivery.md:80>) 中的“统一 read”设计在实施后标明已被本功能回退取代，并保留其历史验收来源。

### PR149 的 Skill 路径修复保护清单

用户明确要求保护 `bf0718c7a`。实施时保留下面的全部行为，并通过当前入口和检查点验证。

| 代码位置 | 必须保留的行为 |
| --- | --- |
| `agent/src/native-skills.ts` 的 `resolveSkillAlias` | 在本轮已启用 Skill 内，优先匹配 `nativeName`，支持唯一的人类 Skill ID 或显示名称，歧义时报错 |
| 同文件的 `resolveSkillPath` | 相对路径先解析别名，再验证冻结文件白名单。缺失 Skill 或文件时列出可用名称或文本文件 |
| 同文件的 `rebaseNativeSkillPath` | 恢复旧 Worker 的读取路径时，使用同一别名规则重定位到当前 Skill 根目录，并继续执行授权和路径校验 |
| `backend/internal/app/cloud_agent_pi_checkpoint.go` 的 `nativeSkillReadAlias`、`nativeReadCheckpointCall` | Go 与 Worker 使用一致的别名和白名单规则，人类 ID 或显示名称读取成功后能配对并保存检查点 |
| 当前 `nativeSkillReadToolSchema` | 保留 `<name>` 取系统提示 `available_skills` 原值的指引，支持既有合法路径 |
| `agent/test/native-skills.test.ts`、`backend/internal/app/cloud_agent_native_skill_test.go` | 保留已实现的别名、错误提示、恢复和检查点断言，补齐因业务 read 删除产生的交界覆盖 |

`cloud_agent_pi_checkpoint.go` 中本次删除的范围是 `piCheckpointNativeSkillRead` 内针对 `agentBusinessRead(call)` 的提前放行分支。该函数后续的 Skill 结果校验继续执行，`nativeSkillReadAlias` 和文件路径匹配逻辑属于受保护实现。`agent/src/native-skills.ts` 中本次删除的是 `isBusinessRead`，其余路径解析按当前实现验证。

复核命令在仓库根目录执行。

```powershell
git show 2446d64bd^:backend/internal/app/cloud_agent_tools.go
git show 2446d64bd^:agent/src/runner.ts
git show 2446d64bd -- backend/internal/app/cloud_agent_tools.go backend/internal/app/cloud_agent_pi_tool_batch.go backend/internal/app/cloud_agent_pi_checkpoint.go agent/src/native-skills.ts agent/src/runner.ts
git show bf0718c7a -- agent/src/native-skills.ts backend/internal/app/cloud_agent_pi_checkpoint.go
```

## 评审关注点

1. 混合工具批次中，Skill `read` 只走 Skill 通道，业务工具保持模型返回的调用身份、参数和持久回执。任务 2、3 覆盖。
2. 同用户跨项目、跨用户和执行时撤权均拒绝，短剧工具不能进入画布运行。任务 1、2 覆盖。
3. 升级遇到旧目录、待执行业务 `read` 或待回传结果时，拒绝继续执行并正确结束占用，历史仍可查阅。任务 3、4 覆盖。
4. 正文分页保持 Unicode 边界，短剧分集分页可读到末尾，显式非法范围返回错误。任务 1、5 覆盖。
5. 摘要命中和限额回执保持真实状态，需要交付原图时，图片位于完整工具结果之后，失败重试不能重复消耗交付次数。任务 5、6 覆盖。

## 已确认合同

### 工具职责与参数

| 工具 | 职责 | 参数与行为 |
| --- | --- | --- |
| `read` | 读取 Skill 入口及其授权文本引用文件 | `path` 必填，`offset`、`limit` 可选。沿用 `skills/<name>/SKILL.md`、授权引用路径及原生运行时的路径解析和恢复规则 |
| `canvas_read_content` | 分页读取节点正文或分镜行字段 | `nodeId` 必填，`rowId`、`field`、`offset`、`limit` 按现有合同处理 |
| `canvas_inspect_image` | 观察图片、复用有效摘要、显式重看、对比及保存观察摘要 | 使用既有 `nodeId`、`refresh`、`purpose`、`sha256`、`summary` 参数及预算规则 |
| `drama_read_episode` | 分页读取当前短剧项目内的分集和镜头 | `episodeId` 必填，`offset` 默认 0，`limit` 默认 12000，范围 1–12000，按 Unicode 字符分页 |

`read` 对 `canvas:text/…`、`canvas:image/…`、`drama:episode/…` 和第三业务命名空间返回可纠正的工具错误。授权 Skill 的原生绝对路径和恢复重定位仍按 Skill 快照校验，任意宿主机文件及 URL 被拒绝。

### 命名规则

| 动作 | 含义 | 本次示例 |
| --- | --- | --- |
| `read` | 读取已有正文或结构化业务内容 | `canvas_read_content`、`drama_read_episode` |
| `inspect` | 请求模型观察媒体内容 | `canvas_inspect_image` |
| `get` | 查询一个对象的状态或元数据 | 现有 `task_get` 作为平台工具使用 |
| `list` | 列举集合 | 新业务使用业务前缀及明确对象名 |
| `search` | 按条件搜索 | 现有 `canvas_search_nodes` |

新接入业务通过具名工具扩展，例如测试业务使用 `library_read_document({documentId})`。工具名使用单下划线分隔的小写字母和数字段，总长最多 64 字符，首字符为字母，格式校验使用 `^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`。已有平台工具和多动作工具按其现有合同注册，本次命名治理聚焦读取与识图工具。

### 结果与权限

- Skill 继续返回原生文本结果，并记录 `native_skill_read` 或 `native_skill_read_failed`。
- 画布专用工具沿用各自结果合同。
- 短剧结果包含 `projectId`、`episodeId`、`content`、`contentType`、`visibility`、`offset`、`nextOffset`、`totalRunes`、`hasMore` 和 `adopted=false`。`content` 延续分集与镜头序列化后的分页文本。
- 短剧分页省略 `limit` 时取默认值，显式 `limit=0`、负数、超上限和未知参数均拒绝。超过正文末尾的 `offset` 沿用原读取行为，返回末尾空页。
- Scope 来自已认证 Run。工具参数只有对象 ID 和读取选项，不接受覆盖用户或项目归属的字段。
- `drama_read_episode` 计入共享读取次数，并经现有工具操作回执保证相同调用重放不重复执行。

## 当前基线与差距

核对工作树为 `D:/DevProjects/ChatGPT/openai canvas/previs-world-4ai/.local/worktrees/studio-world-model-port`，分支 `codex/studio-world-model-port`，HEAD 为 `64bf5a05b6ddef527d507b5780853e02f441a6bd`。该 HEAD 包含 `c51373793` 的公共链路尾部附图改造。

本次写计划前已有两处工作区修改，实施前需要重新核对并保护。

- 已修改 `backend/internal/model/models_canvas_history.go`。
- 未跟踪 `backend/internal/canvas/repro_studio_migration_test.go`。

| 当前事实 | 开发影响 | 源码 |
| --- | --- | --- |
| `AgentDefinition`、注册表和能力快照都持有读取适配器 | 删除字段、注册函数参数、校验及克隆逻辑 | [agent_definition_registry.go](<D:/DevProjects/ChatGPT/openai canvas/previs-world-4ai/.local/worktrees/studio-world-model-port/backend/internal/app/agent_definition_registry.go>)、[agent_tool_registry.go](<D:/DevProjects/ChatGPT/openai canvas/previs-world-4ai/.local/worktrees/studio-world-model-port/backend/internal/app/agent_tool_registry.go>) |
| `read` 路径会被 Go 和 Worker 分成 Skill 与业务两类 | 清理正常执行、混合批次、检查点和恢复时的业务分支 | [agent_read_adapter.go](<D:/DevProjects/ChatGPT/openai canvas/previs-world-4ai/.local/worktrees/studio-world-model-port/backend/internal/app/agent_read_adapter.go>)、[runner.ts](<D:/DevProjects/ChatGPT/openai canvas/previs-world-4ai/.local/worktrees/studio-world-model-port/agent/src/runner.ts>) |
| 短剧系统提示和本轮对象上下文写入 `readPath` | 改为具名工具及 `episodeId` | [cloud_agent_run_create.go](<D:/DevProjects/ChatGPT/openai canvas/previs-world-4ai/.local/worktrees/studio-world-model-port/backend/internal/app/cloud_agent_run_create.go>) |
| 工具离线全集与支持列表主要从画布配置生成 | 新增短剧工具时同步全集、支持列表和 AST 守卫 | [agent_tool_catalog.go](<D:/DevProjects/ChatGPT/openai canvas/previs-world-4ai/.local/worktrees/studio-world-model-port/backend/internal/app/agent_tool_catalog.go>)、[cloud_agent_tool_dispatch_test.go](<D:/DevProjects/ChatGPT/openai canvas/previs-world-4ai/.local/worktrees/studio-world-model-port/backend/internal/app/cloud_agent_tool_dispatch_test.go>) |
| 能力目录版本为 1，Worker 仅接受 0、1，内部协议为 `canvas-pi-wire/v7` | 同步协议边界及旧运行处置 | [cloud_agent_capability_budget.go](<D:/DevProjects/ChatGPT/openai canvas/previs-world-4ai/.local/worktrees/studio-world-model-port/backend/internal/app/cloud_agent_capability_budget.go>)、[capability-directory.ts](<D:/DevProjects/ChatGPT/openai canvas/previs-world-4ai/.local/worktrees/studio-world-model-port/agent/src/capability-directory.ts>) |

### 功能回退的发布边界

本次功能回退会改变执行语义。历史 SDK 改造曾为业务 read 将 Worker wire 升至 v5，其原因记录在 [SDK 当时的交付计划](<D:/DevProjects/ChatGPT/openai canvas/previs-world-4ai/.local/worktrees/studio-world-model-port/docs/plans/2026-11-18-agent-sdk-incremental-delivery.md:224>)。本计划在当前 v7 基础上建议继续递增到 `canvas-pi-wire/v8`，用协议身份隔开两种 read 语义。

实施建议为能力目录升至 2，`canvas.default` 升为 v2，交互式只读 `drama.analysis` 升为 v3，工具 Schema 从当前 `cloud-agent-tools/v29` 升为 v30。Pi SDK 使用锁定版本 `0.87.1`，会话文件格式使用现有格式 3。

新版服务端拒绝旧目录运行继续执行，包括带旧业务 `read` 的 pending batch。先复用现有 `agent_capability_directory_unavailable`、`stopIncompatibleAgentDirectory` 和清理流程。旧历史记录保持原始工具名和结果，展示和查询不进行重新执行。旧会话可以通过新一轮生成新版能力目录。

上述版本号和中断旧运行的方式是本计划提出的实施决策。发布时需要协调 backend 与 agent，回退也需要两者配套。不存在表结构迁移，持久化 StateJSON 的执行合同有版本切换。

## 任务 0 · 建立实施记录

**文件**

- 创建实施记录 `docs/plans/2026-11-25-agent-skill-read-business-tools-acceptance.md`，实施开始后记录命令、HEAD、失败与未覆盖项。
- 读取根目录 `AGENTS.md`、锁文件和本文。

**步骤**

- [ ] 在上述工作树执行 `git status --short --branch`、`git log -3 --oneline`，记录相对本文基线的变化。
- [ ] 执行本文历史复核命令，按“定向撤除清单”定位 `2446d64bd` 的业务 read 改动，以及之后 Skill 修复和公共识图改动的交界。
- [ ] 对照 PR149 保护清单记录当前受保护函数和已有断言，确认后续差异以当前 HEAD 为基线。
- [ ] 记录已有修改的路径，确认本次编辑范围。
- [ ] 执行 `go version`、`node --version`、`bun --version`，核对项目版本要求。记录 agent 依赖和既有构建产物状态。
- [ ] 在实施记录中建立任务状态和证据表，区分专项测试、跨进程冒烟、真实模型和真实页面。

退出条件为实施基线可追溯，任务 1–4 按依赖顺序在同一交付分支完成后再用于联调。

## 任务 1 · 短剧专用读取工具贯通

**文件**

- 新建 `backend/internal/app/cloud_agent_drama_read.go` 和 `cloud_agent_drama_read_test.go`，承载参数、结果类型和业务读取。
- 修改 `backend/internal/app/cloud_agent_business.go`，迁出当前 `agentReadDramaEpisode` 的业务内容读取。任务 1 中的旧适配函数仅转接新实现，任务 2 一起删除。
- 修改 `backend/internal/app/cloud_agent_tool_compile.go`、`agent_tool_catalog.go`、`cloud_agent_tools.go`。
- 修改 `backend/internal/app/cloud_agent_read_tool.go`、`cloud_agent_read_cache.go`、`cloud_agent_tool_dispatch.go`、`cloud_agent_tool_feedback.go`、`cloud_agent_tool_categories.go`。
- 修改 `backend/internal/app/agent_definition_registry.go`、`agent-tool-descriptions.md` 和 `cloud_agent_tool_dispatch_test.go`。
- 调整 `backend/internal/app/cloud_agent_sdk_test.go` 的短剧专用工具断言。

**接口**

- 新增 `cloudAgentDramaEpisodeReadArgs`，字段为 `EpisodeID string`、`Offset *int`、`Limit *int`，JSON 名称为 `episodeId`、`offset`、`limit`。
- 新增 `cloudAgentDramaEpisodeReadResult`，字段对应本文确定的短剧结果合同。
- 新增 `func (s *Service) cloudAgentReadDramaEpisode(userID string, state *cloudAgentRuntime, call cloudAgentCall) (cloudAgentDramaEpisodeReadResult, error)`。
- 参数校验与对象查询复用现有 ID 校验、JSON 解码、`dramaForUser`、`DramaEpisode(projectID, episodeID)` 和 `DramaShots`。

**步骤**

- [ ] 先增加 `TestDramaReadEpisodeTool`、`TestDramaReadEpisodeScopeAndPagination`、`TestDramaReadEpisodeBudgetAndReplay`。覆盖合法分集与镜头、同用户另一项目、另一用户、项目权限撤销、非法参数、Unicode 分页和完整读取。
- [ ] 从 `backend/` 执行 `go test ./internal/app -run '^TestDramaReadEpisode' -count=1 -v`，记录缺少工具或执行器的失败。
- [ ] 按接口迁移读取逻辑。Scope 必须为 drama，项目和分集必须实时授权，显式空 ID 和非法分页被拒绝。
- [ ] 在现有编译器中注册 `drama_read_episode`，业务策略限定为 drama。离线全集和 `CloudAgentSupportedToolNames()` 覆盖所有已注册业务，并去重共同平台工具。实际模型目录继续由业务定义筛选。
- [ ] 在事务外读取分支通过 `cloudAgentReadToolCached(..., s)` 准备短剧结果，检查点只结算已有结果。把工具加入只读次数统计，返回现有预算错误。
- [ ] 修改短剧业务定义选择 `drama_read_episode`，模型提示改为调用该工具。本轮上下文携带 `episodeId` 与工具调用提示，移除 `readPath`。
- [ ] 同步参数纠错示例和工具分类，确保错误不会按画布写入处理。同步工具表与运行分派守卫，覆盖短剧分支及全部业务工具的并集。
- [ ] 从 `backend/` 执行 `go test ./internal/app -run 'TestDramaReadEpisode|TestAgentSDKDrama|TestCloudAgentToolTableMatchesRuntimeDispatch' -count=1 -v`，确认合法读取、权限拒绝和同调用回执重放。

退出条件为短剧分集可通过已披露工具进入真实工具批次、得到持久回执，跨对象调用和超过读取预算均被拒绝。

## 任务 2 · SDK 移除业务 read 扩展

**文件**

- 修改 `backend/internal/app/agent_definition_registry.go`、`agent_tool_registry.go`、`agent_tool_catalog.go`、`cloud_agent_tools.go`。
- 修改 `backend/internal/app/cloud_agent_tool_dispatch.go`、`cloud_agent_capability_snapshot.go`。
- 删除 `backend/internal/app/agent_read_adapter.go`，迁移其测试中的资源授权覆盖。
- 修改 `backend/internal/app/agent_read_adapter_test.go`、`agent_definition_registry_test.go`、`agent_third_business_test.go`、`cloud_agent_sdk_test.go`、`cloud_agent_capability_budget_test.go`。
- 检索 `backend/internal/service/` 中相关类型别名和所有注册函数调用方，按编译影响同步。

**接口**

- `func newAgentDefinitionRegistry(definitions []AgentDefinition) (*agentDefinitionRegistry, error)`。
- `func newAgentRegistry(definitions []AgentDefinition, additionalTools []agentToolRegistration) (*agentDefinitionRegistry, error)`。
- `AgentDefinition` 和 `AgentCapabilitySnapshot` 删除 `ReadAdapters`、`ReadAdapterVersions` 及适配器注册字段。
- `CompileAgentCapabilities` 根据 `admission.HasSkills` 设置 `SkillRead` 并决定是否加入 `read`。
- `nativeSkillReadToolSchema()` 统一描述 Skill 读取，删除 `agentScopedReadToolSchema` 的业务路径拼装。

**步骤**

- [ ] 增加 `TestAgentReadIsSkillOnlyAcrossBusinesses`。对 canvas、drama 和测试 library 分别验证有 Skill、无 Skill 两种目录，`read` 只随 Skill 授权出现。
- [ ] 增加 `TestAgentBusinessReadCannotUseReadTool`，直接向 Go 通道提交四种旧命名空间，即使无能力快照也不得进入业务读取或识图。用正常错误结果表达拒绝，给出使用本轮具名工具的修正提示。
- [ ] 执行 `go test ./internal/app -run '^TestAgent(ReadIsSkillOnlyAcrossBusinesses|BusinessReadCannotUseReadTool)$' -count=1 -v`，观察当前业务路径仍能执行或目录误披露的失败。
- [ ] 按 `2446d64bd` 的差异删除适配器类型、注册字段、命名空间解析器、图片与正文适配函数及对应的派发分支。Go 的 `case "read"` 只承担对错误通道调用的明确拒绝，同时删除 `cloudAgentToolAllowed` 对 `read` 的无条件放行。
- [ ] 清理定义校验、克隆、目录编译、快照校验和所有测试夹具对适配器的引用，Skill 读取权限继续独立授予。核对 `compileCloudAgentToolsForRuntime` 等旧编译入口，有效业务运行只在拥有 Skill 授权时提供 `read`，离线全集使用明确的合成 Skill 配置生成。
- [ ] 把第三业务测试改为 `library_read_document({documentId})`，通过 `additionalTools` 注册并绑定 business、Schema、版本、执行器和对象授权函数。保留实际模型任务、Worker 接管和回执重放断言。
- [ ] 工具注册名称校验收紧为本文的小写 `snake_case` 和 64 字符上限。增加合法名称、大小写、连字符、重复下划线、数字开头和超长名称测试。业务读取工具用明确业务前缀，平台工具仍受平台注册身份校验。
- [ ] 把原统一图片读取测试迁到 `canvas_inspect_image` 的授权和回执测试，去掉要求限额必须报适配器错误的旧断言。
- [ ] 执行 `go test ./internal/app -run 'TestAgent(ReadIsSkillOnly|BusinessReadCannot|DefinitionRegistry|ToolAdmission|SDKThirdBusiness|CapabilityThirdBusiness|ResourceScope)|TestNativeSkillReadTool' -count=1 -v`。

退出条件为业务 read 功能扩展已撤除，SDK 通过业务工具注册提供读取能力，第三业务可注册并执行专用工具。

## 任务 3 · 恢复 Worker、批次和检查点的原生 Skill read 行为

**文件**

- 修改 `agent/src/native-skills.ts`、`agent/src/runner.ts`。
- 修改 `backend/internal/app/cloud_agent_pi_tool_batch.go`、`cloud_agent_pi_checkpoint.go`。
- 修改 `agent/test/native-skills.test.ts`、`runner.test.ts`、`session-history.test.ts`。
- 修改 `backend/internal/app/cloud_agent_native_skill_test.go`，必要时新增 `cloud_agent_skill_read_contract_test.go` 承载跨入口回归。

**接口**

- 沿用 `readNativeSkill(...)`、`rebaseNativeSkillPath(...)` 和 `NativeSkillReadRejected`。
- 删除 Go 的 `agentBusinessRead` 和 TypeScript 的 `isBusinessRead`。
- 原生运行中所有名为 `read` 的调用均进入 Skill 路径。业务专用工具经 `executeBusinessTool` 和 Go tool batch。

**步骤**

- [ ] 将 Worker 当前“业务 read 进入 durable batch”的测试改为旧业务路径拒绝测试，覆盖 canvas、drama、library 和畸形参数。断言产生 `isError` Skill 回执，未调用 `startToolBatch`、`executeTool` 或业务读取。
- [ ] 增加混合批次测试，模型同时返回 Skill `read` 与 `drama_read_episode`，只有后者进入 Go 批次。检查调用 ID、结果配对、执行顺序和单步调用数量。
- [ ] 增加当前 Run 的 pending Skill read 恢复测试，以及旧会话历史业务 read 的展示测试。当前 Skill 失败不能被记为成功，历史记录不能触发业务执行。
- [ ] 从 `agent/` 构建后执行 `node --test --test-timeout=20000 --test-force-exit dist/test/native-skills.test.js dist/test/runner.test.js dist/test/session-history.test.js`，记录预期失败。
- [ ] 在当前代码上按业务扩展差异清单撤除 Worker 正常执行、批次筛选、未完成调用恢复三个位置的业务 read 分支。编辑后对照 PR149 保护清单检查 Skill 别名、路径、读取预算、租约及原生检查点实现。
- [ ] 复跑 `bf0718c7a` 对应的别名、歧义拒绝、已授权文件白名单、路径重定位与 Go 检查点匹配测试，确认恢复原职责后当前 Skill 修复仍生效。
- [ ] 扩充 `TestNativeSkillReadToolDescribesStableRelativeLocation`，断言描述仍包含 `skills/<name>/SKILL.md` 和 `available_skills` 原值指引，同时已移除业务命名空间说明。
- [ ] 从 `agent/` 执行 `npm run build`，再执行 `node --test --test-timeout=20000 --test-force-exit dist/test/native-skills.test.js`。从 `backend/` 执行 `go test ./internal/app -run '^TestNative(ReadCheckpointAcceptsRelativeSkillPath|ReadCheckpointRequiresPairedCallAndRecordsSafeEvent|SkillReadToolDescribesStableRelativeLocation)$' -count=1 -v`。将 PR149 相关测试输出单列为必过证据。
- [ ] Go 原生工具批次拒绝全部 `read`，与计费模型输出比对时筛去全部原生 `read`。Skill 检查点取消业务 read 豁免，成功读取必须有合法 Skill 调用及对应授权读取证据。
- [ ] 执行上述 Worker 专项及 `go test ./internal/app -run 'TestNative(MixedBatch|FailedRead|ReadCheckpoint|SkillFile|SkillReads)|TestAgentBusinessReadCannotUseReadTool' -count=1 -v`。

退出条件为正常调用、混合批次和恢复入口共享 Skill-only 语义，PR149 的路径修复及检查点配对测试通过。业务调用重放继续复用 Go 持久回执。

## 任务 4 · 功能回退的目录与协议发布切换

**文件**

- 修改 `backend/internal/app/cloud_agent_capability_budget.go`、`cloud_agent_capability_snapshot.go`、`agent_definition_registry.go`。
- 修改 `agent/src/capability-directory.ts`、`agent/src/bridge.ts` 和 `agent/harness/PI_WIRE_IDENTITY.json`。
- 修改 `backend/internal/handler/internal_agent.go`、`internal_agent_protocol_test.go`。
- 修改 `backend/cmd/server/stream_drain_test.go` 中固定的内部协议头。
- 修改 `agent/test/capability-directory.test.ts`、`bridge-wire-contract.test.ts`、`readiness.test.ts`、`runner.test.ts` 中的版本夹具。
- 修改 `backend/internal/app/cloud_agent_capability_budget_test.go`，新增 `cloud_agent_read_upgrade_test.go`。

**接口**

- 新能力目录版本为 2，内部协议身份为 `canvas-pi-wire/v8`，工具 Schema 为 `cloud-agent-tools/v30`。
- 新业务定义为 `canvas.default` v2、交互式只读 `drama.analysis` v3。
- 沿用 `agent_capability_directory_unavailable` 作为旧运行拒绝原因。

**步骤**

- [ ] 增加 `TestAgentReadContractUpgradeRejectsOldExecution`，覆盖 queued、待工具回执、待恢复模型步骤和旧 Worker 接管。旧目录 1、legacy 0 或缺少目录的可执行恢复均不能绕过新合同。
- [ ] 增加 `TestAgentReadContractUpgradePreservesHistory`，旧会话能查历史、按权限取消或归档，并能创建新 Run。新 Run 使用新定义和目录，历史工具调用不重新执行。
- [ ] 增加新版 backend 拒绝 v7 Worker、新版 Worker 拒绝旧目录的测试。失败必须发生在新业务执行和新增计费模型任务之前。
- [ ] 运行版本与升级专项，确认旧代码不满足上述边界，再同步目录、定义、内部协议、`cloudAgentToolSchemaVersion` 和全部身份制品。
- [ ] 在已有不兼容目录处理路径上检查 Run 终态、租约释放、持有订单释放及已发生费用的结算。对在途或已提交媒体任务按既有任务恢复合同核对，确保不重复提交或退款。
- [ ] 检查 `Capabilities == nil` 的旧执行回退路径，所有可执行入口均落实旧运行拒绝。历史查询和用户终止入口使用其原有对象权限校验。
- [ ] 执行 `go test ./internal/app -run 'TestAgent(ReadContractUpgrade|CapabilityIncompatibleDirectory)|TestNativeClaimUnavailableSnapshot' -count=1 -v`。
- [ ] 执行 `go test ./internal/handler -run '^TestInternalAgent(RequiresVersionedPiWireIdentity|ProtocolIdentityMatchesSharedArtifact)$' -count=1 -v`，并复跑受协议头变更影响的 server drain 测试。
- [ ] 从 `agent/` 执行能力目录、bridge wire 和 readiness 专项，确认 v8、目录 2、定义身份和恢复时的不可变性。

退出条件为两端版本一致，旧合同被明确拒绝，终态与账务清理有测试证据，历史查阅和新一轮可用。

## 任务 5 · 正文与识图的公共链路回归

**文件**

- 调整 `backend/internal/app/cloud_agent_sdk_test.go`、`agent_read_adapter_test.go` 的旧入口测试，保留必要资源隔离覆盖后删除空文件。
- 复用 `backend/internal/app/cloud_agent_pi_vision_tail_test.go`、`cloud_agent_vision_reinspection_test.go`、`cloud_agent_vision_delivery_test.go`。
- 调整 `agent/test/session-history.test.ts`，仅在新增断言需要时修改公共实现。

**步骤**

- [ ] 验证 `canvas_read_content` 的普通正文、分镜 `rowId+field`、Unicode 分页、`hasMore` 和续读参数，检查真实调用方仍能读取到末尾。
- [ ] 通过 `canvas_inspect_image` 覆盖首次看图、同 SHA 有效摘要复用、换图、显式刷新、对比、单图限额、全轮预算、资源解绑和资源未就绪。
- [ ] 对真实 `PiModelStep` 生成的模型任务做断言，所有工具结果完整出现后才附原图，摘要命中时没有额外图片输入，附图失败后允许按既有规则重试。
- [ ] 区分 `imagePending`、`imageAttached` 和摘要复用。单图限额的正常无图回执按公共识图合同处理，不能重新引入适配器的强制刷新或空图报错逻辑。
- [ ] 执行 `go test ./internal/app -run 'TestPiVision|TestCloudAgentVision(Refresh|RepeatRead|Limits|Budget|Rejects)|TestVisionProjection|TestAgentResourceScope|TestDramaReadEpisodeScopeAndPagination' -count=1 -v`。
- [ ] 如公共链路发生必要修复，先保留失败用例，再运行对应专项。按真实出站任务区分确定性像素验证和真实模型识别效果。

退出条件为专用工具承载所需正文和识图能力，尾部附图和摘要复用具有直接模型任务证据。

## 任务 6 · 端到端接线、前端呈现与文档

**文件**

- 修改 `backend/internal/handler/agent_sdk_e2e_test.go`、`agent/test/agent-sdk-driver.mjs`、`web/test/agent-sdk.e2e.test.ts`。
- 按实际断言需要修改 `agent/test/agent-sdk-browser-smoke.mjs`。
- 修改 `web/src/lib/canvas/agent-tool-presentation.ts`、`web/test/agent-tool-presentation.test.ts`、`web/test/agent-tool-count.test.tsx`，核对 `web/src/pages/projects/studio/agent-analysis.tsx` 的真实回执展示。
- 修改 `docs/content/docs/backend/agent-sdk.mdx`、`docs/content/docs/backend/http-api.mdx` 和必要的读取专题。
- 修改 `backend/internal/app/agent-tool-descriptions.md`、`agent/harness/TOOL_DESCRIPTIONS.md`，重新生成 `agent/harness/TOOL_SCHEMA.json`。
- 更新 `docs/plans/2026-11-25-agent-skill-read-business-tools-acceptance.md` 和 `docs/index.md`。旧 SDK 计划、验收记录增加指向新合同的说明并保留历史证据。

**步骤**

- [ ] 把确定性模型夹具改为画布正文调用 `canvas_read_content`、画布图片调用 `canvas_inspect_image`、短剧分集调用 `drama_read_episode`。
- [ ] 更新回执断言的工具名，保留双账户、双对象、四会话、SSE 续读和 Worker 重启测试。Worker 中断应明确发生在业务专用工具回执已提交且 Pi 结果未检查点保存的时刻。
- [ ] 前端补齐 `drama_read_episode` 的“读取分集”与 `read` 的“读取技能”名称，结果状态按实际回执呈现，核对失败与重试文案。业务逻辑和权限继续由后端执行。
- [ ] 工具清单测试覆盖离线全集与前端已知工具名，所有业务过滤后目录有各自的正确专用工具。
- [ ] 从 `backend/` 执行 `go run ./cmd/export-agent-tool-schema -output ../agent/harness/TOOL_SCHEMA.json`，同步说明制品，再执行 `go test ./internal/app -run 'TestAgentToolSchemaArtifactMatchesRuntime|TestCloudAgentToolTableMatchesRuntimeDispatch' -count=1 -v`。
- [ ] SDK 文档记录本次业务 read 功能回退、恢复后的 Skill 描述、业务工具命名、注册示例、权限和发布规则。删除当前使用说明中的业务 `read` 引导。旧计划和验收增加替代关系说明，第三业务示例使用 `library_read_document`。
- [ ] 跨进程验证前从 `agent/` 执行 `npm run build`，确认 Node、Bun 和 Worker 制品存在，再从 `backend/` 执行 `go test ./internal/handler -run '^TestAgentSDKRealHTTPWorkerRestart$' -count=1 -v -timeout 600s`。该用例必须实际运行并检查跨对象隔离、真实像素、回执重放和模型请求计数。
- [ ] 从 `web/` 执行 `bun test test/agent-tool-presentation.test.ts test/agent-tool-count.test.tsx test/agent-canvas-actions.test.ts`。真实 HTTP 场景由上一项 Go 集成测试注入临时地址和测试登录态。
- [ ] 记录真实模型任务验收，使用配置好的视觉模型识别测试图片并复核附图顺序。记录渠道、模型、Run 与任务 ID，凭据保留在原配置中。
- [ ] 真实页面验收使用 Studio 画布与短剧分集分析入口，检查 Skill 读取、正文、看图、短剧建议、取消、刷新恢复和旧运行提示。记录模型为确定性替身或真实模型。
- [ ] PR149 路径场景补一条跨进程验收，模型分别使用已启用 Skill 的原生名、人类 ID 和唯一显示名称读取文件，成功结果能保存检查点并在 Worker 接管后继续运行。歧义、未授权 Skill 和未列入白名单的文件返回真实错误。

退出条件为真实 HTTP、Go、Node/Pi 和持久化闭环通过，前端显示与工具事件一致，文档和离线制品符合新合同。真实模型与页面如未执行，应在验收记录保留待验状态。

## 最终验证与交付

完成任务专项后执行以下交付检查。失败、缺依赖或跳过分别记录，不能写为通过。

| 检查 | 执行位置与命令 | 通过条件 |
| --- | --- | --- |
| Go 相关回归 | `backend/` 执行 `go test ./internal/app -run 'TestAgent|TestDramaReadEpisode|TestNative|TestPiVision|TestCloudAgentVision|TestCloudAgentTool' -count=1 -timeout 600s` | 所选测试完成，新增回归和受影响既有用例通过 |
| Worker 回归 | `agent/` 执行 `npm test` | 构建及测试完成，旧业务 read 拒绝和专用工具恢复用例通过 |
| PR149 保护验收 | 执行任务 3 的 Skill 路径与检查点专项，并检查相关差异 | 别名解析、白名单提示、重定位及成功结果配对均通过，受保护修复完整 |
| 架构守卫 | `backend/` 执行 `go test -count=1 ./internal/architecture` | 注册、读取和新增结果类型符合守卫，改善后的基线按规则下调 |
| 文档检查 | `docs/` 执行 `bun run types:check` | 文档类型与链接约束通过 |
| 去除旧入口 | 根目录执行下方定向检索并审阅结果 | 当前生产入口及当前使用说明中没有业务 read 扩展，历史资料和明确拒绝测试可保留旧字符串 |
| 交付审阅 | `git diff --check`、`git diff --stat`、`git status --short` | 改动聚焦，用户原有修改有清晰归属 |

```powershell
rg -n 'ReadAdapters|ReadAdapterVersions|agentReadBusinessContent|agentBusinessRead|isBusinessRead|readPath' backend agent/src web/src docs/content/docs
rg -n 'canvas:text/|canvas:image/|drama:episode/' backend agent web docs/content/docs -g '!**/*lock*'
```

每个任务完成后核对差异和证据，实施提交按逻辑聚焦。任务 1–4 构成一次合同切换，使用同一组配套制品发布。后续若进入推送阶段，按根 `AGENTS.md` 执行共享验证计划及其选中的检查，更新 `VERSION` 和变更日志，并依据实际 CI 报告结果。开发期间的专项通过只证明其覆盖范围。

### 功能回退的发布与制品回滚安排

1. 发布本次功能回退前，盘点旧目录运行和在途任务，优先让旧运行结束，记录仍需中断的 Run 与已提交任务。按用户的服务器操作规则先复盘实际部署、数据挂载、影响和制品回滚路径。
2. 协调切换 backend 与 agent，验证 v8 握手、目录 2、新工具清单和旧目录拒绝。前端随工具呈现变更交付。
3. 对仍存活的旧运行检查终态和账务释放，新建画布、短剧运行完成最小业务冒烟。
4. 发布失败需要回滚制品时，backend 与 agent 使用配套旧制品，新目录 2 的未结束运行也按不兼容规则处置。回滚镜像不会恢复已经终止的运行或取消已经发生的费用。
5. 本次 StateJSON 合同升级不要求改表或批量改写历史。历史数据保持可追溯，待执行调用不能改名后冒充新模型输出。

### 最终交付物

- 实际实现与测试，覆盖 Skill-only `read`、具名业务工具、权限预算、恢复和视觉交付。
- 新版 Schema、协议身份制品、工具描述、SDK 接入说明及前端功能文案。
- 验收记录，包含精确 HEAD、命令与输出位置、真实执行和跳过情况、真实模型与页面结果及待验项。
- 本次功能回退的发布与制品回滚执行记录，在实际部署发生时补全。

## 当前进度

| 阶段 | 状态 |
| --- | --- |
| 职责与命名合同 | 用户已确认 |
| 功能回退历史依据 | 已核对原生 Skill 引入、稳定路径、业务扩展及后续修复提交 |
| PR149 `bf0718c7a` 保护要求 | 用户已明确，已写入代码边界、专项验证和跨进程验收 |
| 当前源码与调用方核对 | 已完成本文涉及入口的只读核对 |
| 开发落地计划 | 已编写 |
| 产品代码修改 | 待实施 |
| 单元测试与跨进程验收 | 待实施 |
| 真实模型、真实页面及部署验收 | 待实施 |
