# Pi Agent 流式消息链路调查

## 调查目标

追踪 Pi Agent 从 Node/Go 执行层到浏览器 SSE 和消息 reducer 的完整链路，确认以下状态是否一致：

- 模型正文是否按真实增量到达前端；
- 运行快照中的活动草稿是否与流式事件一致；
- SSE 断线重连后是否会重复应用事件；
- 修复是否可以通过 Agent、Go 和 Web 回归测试验证。

## 调查范围

重点文件：

- `agent/src/bridge.ts`
- `agent/src/pi-stream.ts`
- `agent/src/runner.ts`
- `backend/internal/app/cloud_agent_stream.go`
- `backend/internal/app/cloud_agent_runtime.go`
- `backend/internal/app/cloud_agent_pi_bridge.go`
- `backend/internal/handler/agent.go`
- `web/src/services/api/agent.ts`
- `web/src/components/canvas/canvas-cloud-agent-panel.tsx`
- `web/src/lib/canvas/agent-run-state.ts`

## 当前数据流

```text
Pi SDK
  -> runner.ts / createCanvasStreamFn
  -> bridge.modelStep()
  -> Go /internal-agent/runs/:id/model-steps
  -> Go 创建或轮询 model Task
  -> provider OnTextDelta / OnReasoningDelta
  -> cloud agent journal
  -> /api/agent/runs/:id/events SSE
  -> canvas-cloud-agent-panel.tsx
  -> upsertTextMessage()
```

同时，Node bridge 会轮询 Go 的模型任务视图，并把 `textDraft` 传给 Pi 流：

```text
GET /internal-agent/runs/:id/model-steps/:taskId
  -> PiModelStepView.TextDraft
  -> bridge.modelStep(onTextDelta)
  -> Pi text_delta
```

公共 SSE 使用递增的 journal `seq` 作为游标。`run_snapshot` 是状态观察，不推进游标，因此其 `seq` 固定为 `0`。

## 已确认问题

### 1. Bridge 将完整草稿重复当作增量

`agent/src/bridge.ts` 的 `modelStep()` 在每次轮询时直接发送完整 `step.textDraft`，而没有保存上一次已经发送的前缀：

```ts
if (onTextDelta && step.textDraft) onTextDelta(step.textDraft)
```

循环结束后还会再次发送最终轮询得到的全文。若轮询返回的草稿依次为：

```text
你
你好
你好世界
```

当前实现可能上报：

```text
你
你好
你好世界
你好世界
```

这会让 Pi 内部正文重复。最终轮询的全文还可能重复已经发送过的内容。

### 2. Pi 活动草稿没有稳定的实时来源

`backend/internal/app/cloud_agent_stream.go` 的 `newCloudAgentStreamPublisher()` 当前只向运行 journal 写入：

- `assistant_delta`
- `reasoning_delta`

但没有更新 `cloudAgentRuntime.ActiveTextDraft`，也没有把 Agent 流式正文实时写入 `Task.TextDraft`。

另一方面，`backend/internal/app/cloud_agent_runtime.go` 生成公开运行快照时，`activeMessage` 读取的是活动 Task 的 `TextDraft`。因此：

- 公共 journal 可能已经有 `assistant_delta`；
- 前端在当前 SSE 连接中可能可以显示正文；
- 断线重连或新快照中的 `activeMessage` 却可能为空或落后。

运行态中已经存在 `ActiveTextDraft` 字段，但当前调查到的代码没有在正文流入时维护它。

### 3. 面板重建订阅时固定从零开始

`web/src/components/canvas/canvas-cloud-agent-panel.tsx` 的订阅 effect 在每次执行时都会：

```ts
lastSeqRef.current = 0;
```

并以 `after: 0` 创建订阅。断线后 `connectionEpoch` 变化会重新执行 effect，导致服务端重新发送旧 journal 事件。

虽然 panel 仍有 `event.seq <= lastSeqRef.current` 的去重逻辑，但因为重建订阅前已经把游标清零，旧事件会再次进入 reducer。`assistant_delta` 和 `reasoning_delta` 在 `upsertTextMessage()` 中按追加语义处理，因此重复事件会直接造成重复正文或重复推理文本。

## 已确认不会构成根因的部分

- Go 事件 journal 使用递增 `seq`，事件记录本身支持按游标增量读取。
- `run_snapshot` 不推进事件游标，`seq=0` 的设计本身是明确的。
- `web/src/services/api/agent.ts` 在同一个订阅实例内部会推进 `cursor`，并把它同时放入 `?after=` 和 `Last-Event-ID`。
- `canvas-cloud-agent-panel.tsx` 已有 `seq <= lastSeqRef.current` 的防线；问题是订阅重建时游标状态被丢弃。
- `agent/src/pi-stream.ts` 已覆盖“流式 delta 后只补最终正文尾部”的 Pi 流测试；当前缺口集中在 bridge 的 Go Task 草稿轮询差量。

## 建议修复设计（尚未实施）

### A. Go 使用运行态活动草稿作为单一来源

让 `newCloudAgentStreamPublisher()` 在写入 `assistant_delta` 的同一运行态 CAS 中追加 `ActiveTextDraft`，这样 journal 事件和活动草稿具有相同的 revision 边界。

新模型任务开始时清空 `ActiveTextDraft`，避免上一模型步骤的正文出现在下一步骤的快照中。

以下读取路径统一使用运行态草稿：

- `PiModelStepView.TextDraft`；
- `CloudAgentRun.ActiveMessage`。

对已有运行可保留 `Task.TextDraft` 作为空运行态字段时的回退来源。

只对 `assistant_delta` 累积正文；`reasoning_delta` 不能写入正文草稿。

### B. Bridge 按前缀发送差量

在一次 `modelStep()` 调用内记录已经发送的草稿前缀：

1. 首次返回完整草稿时发送全文；
2. 后续返回以已发送内容为前缀的草稿时，只发送新增后缀；
3. 草稿没有增长时不发送；
4. 最终轮询再次返回相同全文时不发送重复内容。

实现应保持 UTF-16/Unicode 字符串切分安全，并保留现有的任务状态和终态处理逻辑。

### C. SSE 重连沿用同一 run 的游标

面板应按 run ID 管理游标：

- 首次订阅一个新 run 时从 `0` 开始；
- 同一 run 因断线、网络恢复或 `connectionEpoch` 变化而重建订阅时，使用当前 `lastSeq`；
- 切换到另一个 run 时才重置游标；
- 保留现有的 `seq <= lastSeq` 防线和缺口 reconcile 行为。

快照消息仍按幂等状态观察处理，不把 `seq=0` 写入恢复游标。

## 回归测试计划

### Agent

在 `agent/test/bridge-wire-contract.test.ts` 增加 bridge 轮询测试：

- 模拟逐步增长的 `textDraft`；
- 断言 `onTextDelta` 只收到新增后缀；
- 断言最终重复全文不会再次发送；
- 继续覆盖最终任务结果和模型任务状态处理。

### Go

在 `backend/internal/app` 增加流式 publisher/运行态测试：

- publisher 写入 `assistant_delta` 时同步维护 `ActiveTextDraft`；
- reasoning 流不污染正文草稿；
- `PiModelStepView` 能返回活动草稿；
- 公开运行快照的 `activeMessage` 与活动草稿一致。

### Web

增加游标状态测试，覆盖：

- 同一 run 的订阅重建使用上一次 `lastSeq`；
- 新 run 从 `0` 开始；
- 重放旧 `seq` 不会再次追加文本；
- 快照状态仍可重复接收且不会制造重复正文。

## 当前工作树状态

调查时工作树位于：

- 分支：`codex/pi-terminal-history-fix`
- HEAD：`0af49f06`
- 远端 `origin/canary`：`e1f6daca`

已有未提交改动，涉及以下租约/终态处理文件：

- `agent/src/bridge.ts`
- `agent/src/runner.ts`
- `backend/internal/app/cloud_agent_pi_bridge.go`
- `backend/internal/handler/internal_agent.go`
- `backend/internal/kernel/error_codes.go`
- `backend/internal/kernel/errors.go`

这些改动在调查和总结过程中没有被覆盖、回滚或清理。本次流式修复是否与这些改动一起提交，以及如何更新并推送 `canary`，需要在实施前确认。

## 结论

当前 Pi 流式链路的问题不是单一 UI 渲染错误，而是三个边界的状态口径不一致：

1. bridge 把快照草稿误当增量；
2. Go journal 与活动草稿没有共用实时来源；
3. Web 重连丢失了持久事件游标。

调查时推荐先按上述 A/B/C 设计补齐回归测试，再实施代码修复并分别运行 Agent、Go 和 Web 的专项验证；下方记录当前首轮实施结果。

## 2026-09-29 实施更新

本工作区已完成 A/B/C 的首轮实现：

- A：Go 的 `assistant_delta` publisher 与运行态 `ActiveTextDraft` 在同一 CAS 中追加；`PiModelStepView` 与公开运行快照优先读取运行态草稿，旧运行保留 Task 草稿回退。
- B：Bridge 在一次 `modelStep` 内按累积草稿前缀发送 suffix，重复的最终轮询不再重复正文；worker 重启时仍受 32 KiB 草稿上限约束。
- C：面板按 run ID 保留 SSE 游标，断线重建订阅使用上次 `lastSeq`，切换到新 run 才从零开始。

验证结果：D 工作区 Agent `npm test` 90/90、Web SSE/run-state 25/25、Web `bun run build` 通过；Go `TestPiStreamPublisherUpdatesActiveDraft` 通过。原有租约/终态改动与本次流式改动仍未推送或部署，Compose、真实浏览器和真实 provider 验收待后续执行。
