# 分镜输入框发送到当前 Agent 会话 Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans to implement this plan task-by-task in the current session. Steps use checkbox syntax for tracking.

**Goal:** 分镜表输入框将剧情与镜头约束作为提示词模板发送到当前 Agent 会话，由 Agent 结合上下文与技能提出方案、询问用户确认后编辑该表。

**Architecture:** 节点仅负责保存剧情、选择镜头数与单镜时长；页面构造目标明确的请求，通过 Agent 面板提供的发送句柄复用现有会话提交/插话、权限、技能、幂等及恢复机制。删除此入口的独立文本拆镜任务，画风与预设作为可用上下文，不作为规划的前置条件。

**Tech Stack:** React 19、TypeScript、现有 Go Agent API；Bun/React 行为测试与 TypeScript 检查。

**Spec:** 本文“功能约定”与当前用户请求。

## 功能约定

- 点击发送自动打开当前 Agent 对话并发送；空闲沿当前会话续轮，运行中走插话。没有对话时由现有面板创建，不人为另开分镜会话。
- 节点移除模型选择，镜头数（自动/1–10）与单镜时长（自动/5/10/15/30秒）在简洁与专业模式均可选择；Agent 模型和权限沿用面板当前配置。
- 模板记录目标节点真实 ID、原始剧情和镜头约束。先读取目标表及相关文本/项目上下文，按适用的已启用技能工作，再提出整体基调、拆镜节奏和数量/时长方案，通过 ask_user 询问确认。
- 没有画风节点或有效预设也能提出方案；存在的画风是参考。确认前不改镜头行、不创建画风节点或生成媒体；确认后编辑原表，遵守现有权限、引用及快照校验，保留既有媒体并回读核对。
- 提示词要求确认属于模型行为约定，不新增独立服务端审批状态机；不宣称模板能保证模型永不跳过确认。
- 发送只表示已送达/已提交 Agent，不表示拆镜完成。失败保留节点剧情与 Agent 原草稿；未知 POST 复用原请求身份，不能新建另一条收费请求。
- 会话恢复、连接中断、账号/画布变化时拒绝不安全发送，不自动排队到另一个对话。重复点击在同一待提交阶段只发送一次。
- 章节分镜等其他生成入口及已有任务恢复不在本次迁移范围；Agent 记忆与压缩算法不修改。

## Global Constraints

- 保留现有皮肤、圆角与作者编辑租约/保存确认，不增加依赖或生产操作。
- 仅运行必要定向测试和类型检查，不运行全套构建。
- 在现有隔离 worktree 实施；完成后按当前会话的本地 main 流程整合，不推送远端。

## Review Focus

1. 当前会话运行中：进入原 run 插话，不新建 run，保留 Agent 输入草稿。
2. 上一次 POST 结果未知：原 body/key/parent 与原草稿可恢复，新内容不能覆盖原请求。
3. 首次恢复或切换账号/画布：旧句柄和迟到回执不能发到新会话或清空新草稿。
4. 无画风、无预设及自由描述风格：可规划并要求确认，不要求先创建画风节点。
5. 固定数量/时长及已有镜头：约束明确指向原表，用户确认前不授权替换/生成媒体。

## Task 1: 提示词模板与可选上下文

**Files:** Create `web/src/lib/canvas/storyboard-agent-request.ts`, `web/test/storyboard-agent-request.test.ts`; modify `web/src/lib/canvas/canvas-storyboard-context.ts`, `web/test/canvas-storyboard-context.test.ts`.

**Interfaces:** `buildStoryboardAgentPrompt(node: CanvasNodeData, prompt: string): string`；上下文 readiness 不再因缺少有效画风预设拒绝规划。

- [x] 添加模板、固定/自动约束、目标节点、先确认后写入及无画风场景的失败回归。
- [x] 实现集中模板和可选画风上下文，保留角色引用的实际版本校验。
- [x] 运行这两个定向测试文件。

## Task 2: 当前 Agent 对话的外部发送入口

**Files:** Modify `web/src/components/canvas/canvas-cloud-agent-panel.tsx`, `web/src/services/cloud-agent-conversations.ts`, `web/test/fixtures/cloud-agent-panel-sync.harness.tsx`.

**Interfaces:** `CanvasCloudAgentPanelHandle.sendPrompt({canvasId,userScope,prompt,focusNodeIds}): Promise<boolean>`。外部调用保持面板草稿，复用 submit/interject；待确认记录可保存 `composerDraft?: string`，恢复时不得以外部模板覆盖它。

- [x] 添加真实 React 面板回归：空闲续轮、运行插话、保留草稿、重复点击、未知请求及旧作用域拒绝。
- [x] 实现发送句柄、同步提交锁与接受结果，保留旧提交参数及 API 合同。
- [x] 运行隔离面板行为测试，验证原历史同步用例仍通过。

## Task 3: 替换节点入口

**Files:** Modify `web/src/components/canvas/canvas-script-node.tsx`, `web/src/pages/canvas/project.tsx`, `web/src/pages/canvas/use-canvas-storyboard.ts`，更新直接引用旧 props 的相邻测试。

**Interfaces:** 页面 `requestStoryboardToAgent(nodeId: string, prompt: string): Promise<boolean>` 连接模板与当前面板句柄；节点保存确认后等待发送结果，短期发送状态不冒充生成任务状态。

- [x] 移除输入框模型选择，在两种工作区模式均保留数量与单镜时长。
- [x] 将节点发送与该表的手动重试入口转向 Agent；删除 hook 中不再使用的旧拆镜函数及依赖，不改图片/视频批次功能。
- [x] 定向检查节点入口、作者草稿与编辑保存行为；执行前端 TypeScript 检查。

## Task 4: 文档、验证与交付

**Files:** Modify 专题文档、`docs/content/docs/overview/features.mdx`、`docs/content/docs/progress/pending-test.mdx`、`CHANGELOG.md`、`VERSION`。

- [x] 自检实际请求只进入 Agent，旧生成任务调用不再从节点发送；说明确认是模型模板约定的边界。
- [x] 记录定向自动化与真实模型场景验收的区别，不修改 Agent 记忆。
- [x] 先提交实现，再预览更新日志、更新内部版本并检查；将已验证提交合并本地 main。

## 执行与验证记录

- Task 1: complete。先复现无画风/无预设被阻止与模板缺失，再实现模板及可选画风；两个定向文件共9项通过。
- Task 2: complete。新增发送句柄前6项外部发送回归失败；实现后通过。最终面板真实React行为16项通过，API恢复持久化定向5项通过。
- Task 3: complete。旧入口/简洁模式缺控件先失败；迁移后节点边界4项、真实作者保存确认1项通过；前端TypeScript检查通过。
- Final review: 独立非Astra复核发现插话在途切会话发送锁残留；新增用例先失败后通过。请求身份锁与会话切换修复后定向复核确认关闭，未发现新增重要问题。
- Ruling: 沿用当前会话模型/权限/技能，运行中作为插话；不通过预填再要求用户多点一次发送。历史尚未就绪时拒绝发送并保留剧情，避免排队误投到另一会话。
- Ruling: 确认采用用户要求的Agent提示词模板，不新增服务端审批状态机；真实模型是否遵循以及真实浏览器场景留待验收。未修改Agent记忆、压缩算法、章节拆镜或生产环境。
- Ruling: 采用现有隔离worktree与必要定向测试，无全套测试、构建或CI监控。Git整合状态以本地main实际提交为准，不推送远端。
