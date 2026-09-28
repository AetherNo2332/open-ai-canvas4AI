# P1 SDK 可行性探针与扩展点 ADR

> 路线图 [`PI_CODING_AGENT_MIGRATION_ROADMAP.md`](./PI_CODING_AGENT_MIGRATION_ROADMAP.md) 阶段 P1 的交付物。
> 全部结论来自锁定包 `@earendil-works/pi-coding-agent@0.87.1` 的**导出 `.d.ts`、`.js` 实现与真实运行探针**，
> 不使用 CLI 私有文件、不 mock SDK、不复制 Coding Agent 内核。
> 探针套件：[`agent/test/pi-sdk-probe.test.ts`](./test/pi-sdk-probe.test.ts)。
> **权威证据**：仓库内 `cd agent && npm test` → **34/34 通过**（既有 25 + 探针 9，98.8 s，含 WSL 9p 导入开销）。
> 原生 FS 镜像下同一套件 **9/9，1.0 s**，用于快速迭代；两者结论一致。

## 0. 结论摘要

| 路线图假设的扩展点 | 结论 | 真实入口 |
| --- | --- | --- |
| 自定义 Provider | ✅ 存在且可用 | `ModelRuntime.registerProvider(id, { api, baseUrl, streamSimple, models })` |
| `SessionManager` 从 DB 重建 | ✅ 存在且可用（**公开方法**，不需要 JSONL） | `SessionManager.inMemory(cwd, options, entries)` |
| 动态工具扩展 | ✅ 存在且可用 | `createAgentSession({ customTools, noTools, tools })` + `session.setActiveToolsByName()` |
| `agent_settled` 完成语义 | ✅ 存在，每次运行恰好一次 | `session.subscribe()` 的 `agent_settled` |
| 压缩 hook | ✅ 存在且可用 | `pi.on("session_before_compact", …)` + `session.compact()` |
| 可取消 signal | ⚠️ **部分**：`PromptOptions` 无 `signal`，取消走 `session.abort()`；Provider 能收到 `options.signal` | 见 §3.2 |
| 内部模型调用全部过 Go | ✅ 可行（Provider 是唯一下游出口） | 见 §3.1 |

**P1 门槛判定：通过。** 迁移可以在不绕开 Go 模型/账务、且能可靠从数据库恢复会话的条件下承载目标合同。

## 1. 锁定的依赖

```jsonc
// agent/package.json（本轮追加，--save-exact）
"@earendil-works/pi-coding-agent": "0.87.1",   // 新增
"@earendil-works/pi-agent-core": "0.87.1",     // 原有
"@earendil-works/pi-ai": "0.87.1",             // 原有
"typebox": "1.3.27"                            // 原有
```

| 项 | 值 |
| --- | --- |
| npm `dist-tags` | `latest = 0.87.1`（即锁定版本就是当前最新） |
| 安装结果 | `added 119 packages, audited 213 packages, 0 vulnerabilities` |
| 包体 | 1108 文件 / 23 148 544 B 解包 |
| 供应链 | npm 签名 + SLSA provenance attestation 均存在 |
| 传递依赖（节选） | `pi-ai`、`pi-agent-core`、`pi-tui`、`@earendil-works/chord`、`typebox 1.3.27`、`undici`、`yaml`、`diff`、`semver`、`proper-lockfile`、`@silvia-odwyer/photon-node` |

**`exports` 映射（决定了可用的导入面）**：

```jsonc
{ ".": { types, import }, "./rpc-entry": { import }, "./client": { source }, "./experimental/plugin": { source } }
```

`./client` 与 `./experimental/plugin` **只有 `source`，没有构建产物** → 生产使用的唯一入口是包根 `.`。深路径导入被 `ERR_PACKAGE_PATH_NOT_EXPORTED` 拒绝（已实测），因此**不能**用 `pi-coding-agent/dist/core/sdk.js` 绕开 barrel。

## 2. 环境发现（影响验收方式，不影响架构）

| 位置 | 文件系统 | `import @earendil-works/pi-coding-agent` | `import pi-ai` |
| --- | --- | --- | --- |
| `/mnt/d/13537/open-ai-canvas-canary/agent` | WSL 9p | **75 624 ms** | 18 939 ms |
| 原生 ext4 | ext4 | **659 ms** | — |

- 同一份代码、同一把锁，**115 倍**差异完全来自 `/mnt/d` 的 9p 挂载。
- `ModelRuntime.create({ modelsPath: null, refreshOnCreate: false, allowModelNetwork: false })` 只需 **4 ms**，不发起网络。
- 结论：容器内冷启动正常；但**在本机 `/mnt/d` 上跑 SDK 测试每次进程要多付约 95 s**。本轮 Node 侧迭代在原生 FS 镜像（`rsync src/ test/` + 独立 `node_modules`）上进行，交付物仍在仓库内。
- ⚠️ 交接提示：评审者若在 `/mnt/d` 直接跑 `npm test` 会看到约 100 s 的"假慢"，不要据此判定 SDK 不可用或 worker 冷启动超标。

## 3. 逐项验证

### 3.1 Provider

**签名（导出类型实测）**

```ts
// AgentSessionConfig / CreateAgentSessionOptions / ModelRuntime
ModelRuntime.create(options?: CreateModelRuntimeOptions): Promise<ModelRuntime>
runtime.registerProvider(providerId: string, config: ProviderConfigInput): void
runtime.setRuntimeApiKey(providerId: string, apiKey: string, options?): Promise<void>
runtime.getModel(providerId: string, modelId: string): Model<Api> | undefined
runtime.streamSimple(model: Model<Api>, context: Context, options?): AssistantMessageEventStream

interface ProviderConfigInput {
  name?: string; baseUrl?: string; apiKey?: string; api?: Api;
  streamSimple?: (model: Model<Api>, context: TranscriptContext, options?: SimpleStreamOptions)
                 => AssistantMessageEventStream;
  headers?: Record<string,string>; authHeader?: boolean;
  models?: Array<{ id; name; api?; baseUrl?; reasoning: boolean; input: ("text"|"image")[];
                   cost: ModelCost; contextWindow: number; maxTokens: number; … }>;
  refreshModels?(context): Promise<…>;
}
```

**验证结果**

- `Api = KnownApi | (string & {})` → 自定义 API id（探针用 `canvas-bridge`）合法，不需要伪造已知协议。
- **`baseUrl` 在声明自定义 `models` 时是必需的**，否则 `validateExtensionProvider` 抛
  `Provider canvas: "baseUrl" is required when defining custom models.`。纯 `streamSimple` 的 Provider 也躲不掉。
  → Canvas Provider 用一个 RFC 2606 保留域名（探针用 `http://canvas-bridge.invalid/v1`）即可；因为 `streamSimple` 从不发起 HTTP，该地址**永远不会被访问**，天然满足「Node 不直接访问上游」。
- 真实能力（`contextWindow` / `maxTokens` / `reasoning` / `input` / `cost`）就是 `models[]` 的字段 →
  路线图 §3.1 要求「必须由 Go 的实际渠道/逻辑路由能力替换 Node 写死的 1M/32768/零费用」有直接落点：
  **Go 能力快照 → `ProviderModelConfig[]` → `registerProvider`**，不需要改 SDK。
- 流式合同（`AssistantMessageEvent` union，来自 `pi-ai`）：

  ```
  start → ( text_start → text_delta* → text_end
           | thinking_start → thinking_delta* → thinking_end
           | toolcall_start → toolcall_delta* → toolcall_end )* → 恰好一个 done | error
  ```

  `done = { reason: "stop"|"length"|"toolUse"|"deferred", message }`；
  `error = { reason: "aborted"|"error", error }`。厂商无关的构造器是 `createAssistantMessageEventStream()`（`pi-ai` 导出，供扩展使用）。
- **用量原样透传**：`AssistantMessage.usage` 是 `{ input, output, cacheRead, cacheWrite, cacheWrite1h?, reasoning?, totalTokens, cost{...} }`，探针断言脚本给定值原样到达 → 满足「返回真实 usage、缓存 token、stop reason」。
- **观测回调**：`StreamOptions` 提供 `onResponse?: (response, model) => void | Promise<void>`（收到响应、消费 body 之前）与 `OnPayload` 类回调；`custom-provider` 文档要求实现方在发送前调用 `options.onPayload`、收到响应后调用 `options.onResponse` → 路线图 §3.1 的观测要求有落点。
- **上下文形态**：`streamSimple` 收到的是 `TranscriptContext`（带 brand，只能由 `normalizeContext()` 产生）。提示与工具声明**由 transcript 的 system 消息携带**，取值用
  `getCurrentSystemPrompt(context.messages)` / `getCurrentTools(context.messages)` —— 探针正是用它们断言"模型看到了什么"。

**探针证据**

| 用例 | 断言 |
| --- | --- |
| `P1 provider: scripted stream reaches the model with system prompt and canvas-only tools` | 恰好 1 次请求；systemPrompt 含装配结果；工具声明 = `["canvas_get_state"]` |
| `P1 provider: thinking, text and tool call are balanced and usage is preserved verbatim` | 工具真的执行；工具结果触发第 2 次请求且 `messageRoles` 含 `toolResult`；无伪中止 |
| `P1 provider: cancelling an in-flight run aborts it and starts no follow-up request` | 取消后**恰好 1 次**请求、0 次工具执行、provider 通过 `options.signal` 观察到中止 |

### 3.2 取消语义（对路线图的修正）

```ts
interface PromptOptions {          // 实测：没有 signal 字段
  expandPromptTemplates?: boolean;
  images?: ImageContent[];
  streamingBehavior?: "steer" | "followUp";
  source?: InputSource;
  preflightResult?: (success: boolean) => void;
}
```

- `session.prompt(text, { signal })` **不会被遵守**（探针最初按路线图假设写，得到 2 次请求的实际结果，据此修正）。
- 正确入口：`session.abort(): Promise<void>`；`session.isStreaming` / `session.isIdle` 表示运行态。
- Provider 侧**能**观察到取消：`streamSimple(model, context, options?.signal)` 的 `signal` 会随 `abort()` 中止（探针断言 `abortedRequests ≥ 1`）。
- **含义**：路线图 §3.1「每次 Provider 请求携带…可取消 signal」在 Provider 层成立；但 Go 侧的"取消"必须经 `session.abort()` 触发，而不是给 prompt 传 signal。P3/P4 的取消合同按此实现。

### 3.3 SessionManager：从数据库重建（P2 的关键前置）

**签名**

```ts
class SessionManager {
  static inMemory(cwd?: string, options?: NewSessionOptions, entries?: FileEntry[]): SessionManager;
  static create(cwd: string, sessionDir?: string, options?): SessionManager;
  static open(path: string, sessionDir?: string, cwdOverride?: string): SessionManager;
  static continueRecent(cwd: string, sessionDir?: string): SessionManager;
  getEntries(): SessionEntry[];            getTree(): SessionTreeNode[];
  getLeafId(): string | null;              getLeafEntry(): SessionEntry | undefined;
  getEntry(id): SessionEntry | undefined;  getBranch(fromId?): SessionEntry[];
  getHeader(): SessionHeader | null;
  buildSessionContext(): SessionContext;    buildSessionProjection(): SessionProjection;
  appendMessage(message): string;           appendModelChange(provider, modelId): string;
  appendThinkingLevelChange(level): string; appendUsage(kind, provider, model, usage, note?): UsageEntry;
  appendCompaction(...);                    appendBranchSummary(...);
  appendContextEdit(targetId, replacement): string;
  branch(branchFromId): void;               branchWithSummary(...): string;
  resetLeaf(): void;                        createBranchedSession(leafId): string | undefined;
}
export function migrateSessionEntries(entries: FileEntry[]): void;
export function parseSessionEntries(content: string): FileEntry[];
export const CURRENT_SESSION_VERSION: number;   // v3
```

**验证结果**

- `SessionManager.inMemory(cwd, opts, entries)` 内部就是 `new SessionManager(cwd, "", undefined, false, options, entries)` ——
  **不需要 JSONL 文件**即可从外部持久化的 entry 数组重建会话。这**直接满足**路线图 §2.1 的要求：
  "P1 必须验证 SDK 0.87.1 将这些 DB entry 重新导入 `SessionManager` 的**公开方法**；若只能通过 JSONL 文件导入，文件只能作短时适配缓存"。
  → **结论：不需要 JSONL 适配层。Go 数据库是权威，`inMemory(..., entries)` 是恢复入口。**
- 探针 `P1 session: v3 entries rebuild …` 断言的往返等价性：
  导出 entries + leaf → 新建 session（**只**给 entries）→ `getLeafId()` 相同、`buildSessionContext().messages` 的 role 序列逐项相同、`getEntries()` 的 id 序列逐项相同。
- `getHeader().version === CURRENT_SESSION_VERSION`（3）；`migrateSessionEntries` / `parseSessionEntries` 可用于校验与旧格式迁移。

**⚠️ 对 P2 设计的重要否证：`entry_appended` 不是通用追加钩子。**

- `AgentSessionEvent` 确实包含 `{ type: "entry_appended"; entry: SessionEntry }`，但读实现后确认它**只在边界提交时发出**：
  `_commitBoundaryDrafts()`（压缩/上下文编辑等边界草稿）与"恢复省略"路径；
  `SessionManager` 的普通 `appendMessage()` **不产生**该事件。
- 探针 `P1 session: the entry tree is append-only …` 把这条写成**负向断言**（普通消息追加后 `entry_appended` 里 `type === "message"` 的数量为 0），
  防止 P2 依赖一个不会触发的钩子。
- **P2 正确做法**（已用探针验证可行性）：用公开只读 API 做差异持久化 ——
  `getEntries()` 的 id 序列是**严格追加**的（新序列的前缀等于旧序列），
  所以在每个不可重放边界上按 id 差集提交"新增 entries + `getLeafId()` + `getHeader()`"即可，无需私有钩子。
- `SessionManager` 的构造函数是 `private constructor()` → **TS 层面不能继承覆写 `_persist`**。不要走这条路。

### 3.4 动态工具披露

**签名与验证**

```ts
createAgentSession({ customTools?: ToolDefinition[], noTools?: "all" | "builtin",
                     tools?: string[], excludeTools?: string[], … })
session.getActiveToolNames(): string[]
session.getAllTools(): ToolInfo[]
session.setActiveToolsByName(toolNames: string[]): void   // 未知名字被忽略，并重建 system prompt
interface ToolDefinition {
  name; label; description; promptSnippet?; promptGuidelines?;
  parameters: TSchema;                       // TypeBox
  execute(toolCallId, params, signal, onUpdate, ctx): Promise<AgentToolResult>;
  executionMode?: "sequential" | "parallel";
  prepareArguments?(args): TParams;
}
```

| 用例 | 断言 |
| --- | --- |
| `P1 tools: disclosure is dynamic` | 允许清单登记两个工具但只激活母工具 → 第 1 次请求**只**声明母工具；`setActiveToolsByName([母, 子])` 后第 2 次请求声明两者；`canvas_totally_unknown` 无法激活 |
| `P1 tools: zero dangerous default tools` | `noTools="all"` 与 `noTools="builtin"` 两种模式下，`getAllTools()` 与模型声明中都**没有** `read/bash/edit/write/powershell/ls/grep/find` |
| `P1 chain: model → mother → opened category → child → result → next step` | 三步模型请求；第 1 步只见母工具，第 2 步见子工具，工具执行顺序 = `[母, 子]`，第 3 步的 `messageRoles` 含 `toolResult` |

**设计要点**

- `noTools` 语义（`.d.ts` 原文）：`"all"` = 一个工具都不启用；`"builtin"` = 关闭内置读写/bash，但保留扩展/自定义工具。
  本项目**建议 `noTools: "all"` + `tools: [<服务端快照里的合格画布工具名>]`**，两条独立约束同时生效。
- **`CreateAgentSessionOptions` 没有 `initialActiveToolNames`**（该字段只在 `AgentSessionConfig` 上）。
  → 想"只激活母工具"必须在 `createAgentSession()` 之后立即 `setActiveToolsByName([母工具])`；
  这正是 Go 快照下发母类型的落点，P4 按此实现。
- 披露变更**必须发生在工具 `execute()` 内或两次请求之间**：`execute()` 被 agent await 完成后才发下一个请求，
  在 `execute()` 里调用 `setActiveToolsByName` 是确定性的（探针最初用外部轮询等待，出现竞态；改为 `execute()` 内调用后稳定通过）。
- SDK 侧只负责"注册/激活"，**权限与参数准入仍必须由 Go 预检**：`setActiveToolsByName` 会静默忽略未知名字，
  不能作为授权依据。

**Pi 原生工具补丁（P4 可选，用于"忠实移植"）**：`pi-ai` 导出
`getToolStateChanges(previous, current) → { toolsAdded, toolsRemoved }`、`getCurrentTools(messages)`、
`getCurrentSystemPrompt(messages)`、`collapseSystemMessages(context)`、`resolveTranscript(context, supportsMidConvoSystem)`、
`declarationsEqual(left, right)`。
→ GAPS §"设计差异（待 Codex 复核）"提出的问题有了答案：Pi 原生的工具披露就是 system 消息里的 `toolsAdded`/`toolsRemoved` 增量，
当前自研 `ToolDisclosure` 可以逐步改为驱动 `setActiveToolsByName` 让 SDK 自行产出补丁，
但**不阻塞** P2/P3；`declarationsEqual` 还可直接用于 Go/Node schema 漂移校验。

### 3.5 生命周期事件与压缩 hook

**`AgentSessionEvent`（实测联合类型，与本项目相关的子集）**

| 事件 | 载荷 | 用途 |
| --- | --- | --- |
| `agent_settled` | — | ✅ SDK 本次运行已停（路线图 §1 完成语义） |
| `message_start` / `message_update` / `message_end` | `message`, `assistantMessageEvent` | 正文/推理 delta、检查点触发点 |
| `tool_execution_start` / `_update` / `_end` | 工具调用与结果 | 工具回执投影 |
| `compaction_start` / `compaction_end` | `reason: "manual"\|"threshold"\|"overflow"`；`result`, `aborted`, `willRetry`, `errorMessage` | 压缩因果顺序与入账 |
| `auto_retry_start` / `auto_retry_end` | `attempt`, `maxAttempts`, `delayMs`, `errorMessage`, `success` | **每次真实模型调用都要计费**的观测点 |
| `queue_update` | `steering: readonly string[]`, `followUp: readonly string[]` | 插话队列游标 |
| `entry_appended` | `entry` | ⚠️ 仅边界提交（见 §3.3） |
| `session_info_changed`, `thinking_level_changed` | — | 会话元数据 |

| 用例 | 断言 |
| --- | --- |
| `P1 lifecycle: agent_settled fires exactly once` | 完成一次运行后 `agent_settled` 恰好 1 次 |
| `P1 lifecycle: session_before_compact is reachable …` | `session.compact()` 返回真实 `CompactionResult`；`compaction_start`/`compaction_end` 都被观察到；扩展 `pi.on("session_before_compact")` 真的被调用；`getEntries()` 出现 `type === "compaction"` 的一等 entry |

**压缩前置条件（实测，避免误判"不可用"）**：`compact()` 在
`prepareCompaction(pathEntries, settings)` 返回空时抛
`Nothing to compact (session too small)`。返回空有两个原因：
① 末尾已是 `compaction` entry（→ `Already compacted`）；
② `messagesToSummarize` 与 `turnPrefixMessages` 都为空 —— 当 `accumulatedTokens` 一直没到 `keepRecentTokens` 时，
`cutIndex` 落在**第一个** cut point，导致待摘要区间为空。
→ 探针通过 `<agentDir>/settings.json` 写 `{"compaction":{"keepRecentTokens":20,"reserveTokens":10}}` 并给足轮次解决。
生产上真实窗口与压缩判据必须来自 Go 的能力快照。**本探针只证明 SDK 钩子与压缩条目可用，不代表采用 Pi 默认摘要策略**；Canvas 保留原有 Go 结构化语义压缩。生产接入应通过 `SessionBeforeCompactResult.compaction` 提供原算法生成并校验的自定义结果，同时验证最近完整轮次、失败保底和账务均与旧合同一致，不产生第二次默认摘要模型调用。

**资源装配入口（P4）**：`DefaultResourceLoader({ cwd, agentDir, settingsManager, systemPrompt, appendSystemPrompt,
additionalExtensionPaths, extensionFactories, noExtensions, noSkills, noPromptTemplates, noThemes, noContextFiles,
systemPromptOverride, appendSystemPromptOverride, agentsFilesOverride, … })`；
更完整的 cwd 绑定服务用 `createAgentSessionServices(options)` + `createAgentSessionFromServices({ services, sessionManager, … })`，
后者返回 `diagnostics[]`（`info|warning|error`）而不打印/退出 —— 适合把装配问题转成运行可见的错误（对应 Daily 的"启动期致命错误运行可见"教训）。

### 3.6 依赖树风险：`pi-ai` / `pi-agent-core` 存在两份模块实例

安装后实测（`agent/node_modules`）：

| 包 | 顶层版本 | `pi-coding-agent/node_modules` 内嵌版本 |
| --- | --- | --- |
| `@earendil-works/pi-ai` | `0.87.1` | `0.87.1` |
| `@earendil-works/pi-agent-core` | `0.87.1` | `0.87.1` |

即 SDK 自带一份内嵌副本，npm **没有**去重成同一模块实例。

- **风险**：跨实例的 `instanceof` 判断会失败；`TranscriptContext` 的 brand 是 `unique symbol`（编译期）类型，运行时被擦除，因此纯函数与数据结构不受影响。
- **已验证**：探针的 `streamSimple` 用**顶层** `pi-ai` 的 `createAssistantMessageEventStream()` 构造流，交给 **SDK 内嵌** `pi-ai` 的消费端，9/9 用例通过 —— SDK 按异步迭代器/鸭子类型消费，不依赖类身份。
- **约定（写入交接）**：
  1. 顶层 `pi-ai` / `pi-agent-core` 必须与 SDK 内嵌副本**同版本**（当前均为 `0.87.1`，升级时必须三处同时升）。
  2. 跨 SDK 边界不要使用 `instanceof`；优先使用 `pi-coding-agent` 的再导出类型。
  3. 若将来出现类型不兼容，改用 `createAgentSessionServices` + `createAgentSessionFromServices`，从 SDK 侧拿到一致的 `modelRuntime`。

## 4. 被否决 / 不采用的路径

| 路径 | 否决理由 |
| --- | --- |
| 深路径导入 `pi-coding-agent/dist/core/*` | `exports` 未导出，`ERR_PACKAGE_PATH_NOT_EXPORTED`（实测） |
| 继承 `SessionManager` 覆写 `_persist` 落库 | 构造函数为 `private constructor()`，TS 不支持继承 |
| 依赖 `entry_appended` 做逐条消息落库 | 普通消息追加不触发（探针负向断言） |
| 把 JSONL 当权威存储 | `inMemory(cwd, opts, entries)` 已能直接从 DB entry 重建，无需引入文件层 |
| 给 `session.prompt()` 传 `signal` 实现取消 | `PromptOptions` 无该字段；必须 `session.abort()` |
| 用 `baseUrl` 真发 HTTP 让 SDK 直连上游 | 违反硬边界；探针证明 `streamSimple` 完全接管后 `baseUrl` 从不被访问 |
| mock `AgentSession`/`SessionManager`/事件循环 | 探针用真实 SDK；本 ADR 的所有结论都来自真实运行 |

## 5. P1 退出条件核对（路线图 §5）

| 退出条件 | 状态 | 证据 |
| --- | --- | --- |
| 锁 `pi-coding-agent@0.87.1` 和依赖 | ✅ | `agent/package.json` / `package-lock.json`（119 包） |
| 真实 `createAgentSession` + in-memory `SessionManager` | ✅ | 探针 9 个用例全部用 `createAgentSession` + `SessionManager.inMemory` |
| 受控 `resourceLoader` | ✅ | `DefaultResourceLoader` + `extensionFactories` 注入内联扩展 |
| 仅 Canvas 测试工具 | ✅ | `noTools` + `tools` 允许清单 + `customTools`；危险工具为零（含负向断言） |
| Canvas Provider | ✅ | `ModelRuntime.registerProvider` + `streamSimple`，从不发起 HTTP |
| 验证动态工具 | ✅ | 母工具 → 打开类别 → 子工具 → 结果 → 下一步（三步请求全断言） |
| 验证 system patch | ✅ | `getCurrentSystemPrompt(context.messages)` 断言装配结果进入请求；`setActiveToolsByName` 触发 SDK 重建 system prompt |
| 验证 `agent_settled` | ✅ | 恰好一次 |
| 验证压缩 hook | ✅ | `session_before_compact` 触发 + `compaction` entry 落库 |
| 验证 session 导入/导出 | ✅ | leaf/context/entry-id 三重往返等价 |
| 写扩展点 ADR | ✅ | 本文件 |
| 默认危险工具为零 | ✅ | `noTools="all"` 与 `"builtin"` 双模式断言 |
| 不把旧 `Agent` mock 成成功 | ✅ | 探针不导入 `pi-agent-core` 的 `Agent`，也不 mock SDK |

## 6. 对 P2–P4 的直接约束（交接要点）

1. **P2**：新增 v3 entry/operation 表；恢复入口是 `SessionManager.inMemory(cwd, options, entries)`；
   持久化靠 `getEntries()` id 差集 + `getLeafId()`，**不要**依赖 `entry_appended`。
   `format_version` 只用 `CURRENT_SESSION_VERSION`，旧格式先 `migrateSessionEntries`。
2. **P2**：`inMemory` 不落盘 → 进程崩溃后必须能从 DB **完整**重建；这与"Go 数据库是唯一权威"一致。
3. **P3**：真实能力（`contextWindow`/`maxTokens`/`reasoning`/`input`/`cost`）由 Go 快照注入 `ProviderModelConfig`；
   Canvas Provider 的 `baseUrl` 用保留域名占位；`usage` 原样回传；取消链路是 Go → `session.abort()` → Provider `options.signal`。
4. **P4**：初始活跃工具集必须在 `createAgentSession()` 后立刻 `setActiveToolsByName([母工具])`；
   披露变更放在工具 `execute()` 内；生产压缩的触发阈值与保留完整轮次规则沿用 Go 原实现，真实窗口由 Go 能力快照提供，不启用 Pi 默认摘要。
5. **验收**：在 `/mnt/d` 跑 SDK 测试需预期 ~95 s 的导入开销；容器内或原生 FS 镜像下为亚秒级。
