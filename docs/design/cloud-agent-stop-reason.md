# 云端 Agent：上游终止原因（stop reason）与截断处置

状态：已实现，待在 dev 上真机复验（清单见 `../content/docs/progress/pending-test.mdx`）。

范围：只治理"这一步模型调用**为什么结束**"这一件事——把它从上游的权威字段读进来、记进事件流、
按它分支处置，并确保被截断的正文不会被当成本轮最终答复。不改压缩策略、不改看图机制、
不改计费与授权路径。

## 1. 问题（都有代码或数据依据）

1. **终止原因从未被读取。** 全后端没有一处解析 `finish_reason` / `stop_reason` / `incomplete_details`：
   流式解析只读 `delta.content` / `reasoning_content` / `delta.tool_calls`，非流式只读
   `message.content` / `message.tool_calls` / Claude 的 `text` / `thinking` / `tool_use`。
   实测（本地历史运行库 `api_call_logs.response_body`，只读）10/11 条 `cloud_agent_step` 响应体里
   带 `finish_reason`，终止块的值是 `"tool_calls"`——**信号一直在，只是被丢掉了**。
   另外取证上限会把超长流整条丢弃或截掉终止块，所以事后也无法从报文补读。
2. **恢复阶梯只认错误字符串。** `cloudAgentTruncatedToolArguments` 匹配 `"工具参数不是完整 JSON"`、
   `cloudAgentEmptyModelOutput` 匹配 `"没有返回内容"`。这能覆盖"解析失败"，覆盖不了
   **"任务成功但输出被输出上限截断"**——它与正常结束在服务端完全同形。
   这个现象有量级：单步输出上限的说明里记着"实测 222 步的 P50 为 1187 token、**15% 顶到旧上限
   6144 后被截断**"（见 `../content/docs/backend/http-api.mdx` 的单步边界条目）。
3. **截断正文可能被发布成最终答复。** 完成闸门原先只有 `pending_plan` 与 `pending_interjection`
   两类阻塞；一段被截断的纯正文（无工具调用）只要待办清单已对账，就会以 `final: true` 发布。
4. **事件流读不出这件事。** 事件只有类型与 payload，没有 step 级终止原因字段，因此"某一步是不是被
   截断了"在事后只能靠字符数、错误文案之类的副证据猜。

## 2. 设计

三层，逐层只做一件事：

| 层 | 落点 | 做什么 |
| --- | --- | --- |
| 协议层 | `internal/app/provider_text.go` | 把各协议的终止原因抓出来，写进内部结果契约的 `stopReason`（原文）与 `stopReasonKind`（词表） |
| 词表层 | `internal/app/cloud_agent_stop_reason.go` | 归一化、截断判据、事件载荷、升级重试的记账 |
| 运行时 | `internal/app/cloud_agent_runtime.go`、`cloud_agent_completion.go` | 每步落 `model_step_stop` 事件；截断时按阶梯重试一次；截断正文进不了最终答复 |

### 2.1 内部词表

上游各协议的说法不统一，服务端按语义收敛（不按协议名分支，取值本身不重叠）：

| 词表值 | 含义 | 上游取值 |
| --- | --- | --- |
| `stop` | 正常结束 | `stop`、`end_turn`、`stop_sequence`、Responses `completed` |
| `tool_calls` | 要求调用工具 | `tool_calls`、`function_call`、`tool_use` |
| `length` | **输出被输出上限截断** | `length`、`max_tokens`、`max_output_tokens`、Responses `incomplete` |
| `context_limit` | 输入撞上模型上下文窗口 | `model_context_window_exceeded` |
| `pause` | 服务端工具挂起 | `pause_turn` |
| `refusal` | 拒答或内容过滤 | `refusal`、`content_filter` |
| `unknown` | 拿不到或认不出 | 空值、未识别取值、Responses `failed` |

只有 `length` 算"内容被截断"：`context_limit` 是输入预算问题，关思考或放大输出预算都治不了它，
不能被误当成截断掩盖过去。取不到原因时显式记 `unknown`，不默认成正常结束。
这一条与上游文档一致：Anthropic《Handling stop reasons》要求按 `stop_reason` 分支
（`tool_use` 执行工具、`max_tokens` 走截断处理、`end_turn` 收尾），不要按 content 猜。

### 2.2 结果契约

模型任务的 `result_json` 增加两个键，随其它键一起进 `tasks.result_json`：

| 键 | 类型 | 语义 |
| --- | --- | --- |
| `stopReason` | string | 上游原文（如 `length` / `max_tokens` / `end_turn`），拿不到为空串 |
| `stopReasonKind` | string | 词表值；上游原文为空或认不出时是 `unknown` |

旧任务结果没有这两个键 → 空串 → `unknown`，不改变任何既有分支（不为旧数据加兼容层，也不需要迁移）。

### 2.3 事件：`model_step_stop`

每一步模型调用结束后落一条，载荷只放枚举、计数与短标识（事件载荷上限 128 KiB）：

`step`、`taskId`、`stopReason`、`stopReasonKind`、`terminalSeen`(bool)、`truncated`(bool)、
`disposition`、`textBytes`、`reasoningBytes`、`toolCalls`。

`disposition` 是这一步的处置结论（`accept` / `retry` / `fail`）：先定处置再产生任何副作用，
所以事件里记的处置与实际行为必然一致。`terminalSeen=false` 表示上游没给出"生成已终结"的权威信号
（即 `unknown`）——它不等于"正常结束"，只是当下没有证据。

它回答的正是过去读不出来的问题：这一步是"说完了"、"要调工具"，还是"被截断了"，以及这一步产出
了多少正文——不需要再靠 8003 字符这种副证据反推（那其实是 `reasoning_message` 事件的展示截断：
`truncateRunes(reasoning, 8000)` 加上省略号，与账本无关）。

### 2.4 处置

判据只有一条：**这一步的输出是否被输出上限截断（`length`）**。截断步的正文与工具调用都可能是
半截的，所以整步作废——与"半截动作不许落到画布上"是同一个取舍。

| 情形 | `disposition` | 处置 |
| --- | --- | --- |
| `length`，第一次 | `retry` | 整步作废：不写 assistant/canonical、不记观察、**不执行任何已解析出的工具调用**；关思考 + 放大输出预算重发同一步一次（`TruncatedStepEscalated` 上限 1） |
| `length`，重试后仍截断 | `fail` | 如实终止本轮（`run_failed`，`reason=truncated_output`），保留已完成的工作，不发布半截正文 |
| 其它任何取值 | `accept` | 只记事件，控制流不变 |

`length` 之外一律不走这条阶梯：`context_limit` 是输入预算问题，`pause` / `refusal` 是上游语义，
关思考或放大输出预算都治不了它们，也不能被误当成截断掩盖过去。

**兜底不放行截断正文**：收尾闸门另有一条 `truncated_output` 阻塞（`Final=false`）。正常流程下截断
在处置阶段就被拦住了，这条是二次防护——即使将来有人调整处置顺序，半截正文仍进不了最终答复。
`finish_run` 路径不受影响：它的 summary 来自已解析成功的工具参数，参数完整即内容完整。

## 3. 验收

`cd backend && go test ./internal/app/ -run 'StopReason|Truncated|StreamingAgentParser|ParseAgentToolPayload|NormalStop' -count=1`：

- 归一化真值表：各协议取值、大小写与空格、空值与未识别取值；
- `context_limit` / `pause` / `refusal` / `stop` / `tool_calls` 一律**不**判为截断；
- Responses 的 `incomplete_details.reason` 区分"输出到顶"与"内容过滤"；
- 流式解析：OpenAI / Claude / Responses 三协议的终止块都能落到 `stopReasonKind`；
- 非流式解析：三条分支同样能落到 `stopReasonKind`；
- 端到端：截断 → `TruncatedStepEscalated=1` + 关思考 + `model_step_stop`（`disposition=retry`）
  → 重试请求确实关思考 → 再次截断时本轮 `failed`（`reason=truncated_output`）、`disposition=fail`、
  终止说明含"截断"且不含"对账"；
- 截断步**不执行工具**：同一批解析出的工具调用被整步作废（`state.Calls` 为空、无 `tool_completed`）；
- 回归：正常结束（`stop`）仍然 `completed` 且 `final=true`。

## 4. 回滚

改动集中在 3 个文件 + 2 个新文件，无数据迁移、无表结构变更。回滚 = 还原这些文件并把
`VERSION` 回退；事件表里多出的 `model_step_stop` 行对旧代码无影响（未知类型前端本来就不消费）。

## 5. 未做（后续候选，按证据排序）

1. **"拿不到终止信号"仍按可用处理**（评审提出，本轮未采纳）：严格口径是"已支持协议若没有终结信号，
   就不许把正文当最终答复"，理由是"没有证据"不等于"说完了"。本轮只把它记成
   `terminalSeen=false` + `stopReasonKind=unknown`，不改变控制流——因为本地取证（`api_call_logs.response_body`）
   里出现过"末块被 128KiB 取证上限切掉"导致 finish_reason 全为 null 的样本，**无法区分"上游没给"与
   "我们没记到"**；在 dev 上用固定响应样本量出覆盖面之前就硬拦，可能把部分 OpenAI 兼容上游的正常运行
   集体判死。等有覆盖率数据后再决定是否升级为阻断。
2. **看图归因字段**：`canvas_inspect_image` 的调用没有"首看 / 同图重复回执 / 账本复用 / 预算拒绝"
   的落点，一次 140 次看图的运行无法从事件流归因；裁剪事件只报数量，不报被裁的 nodeId 与字节。
   评审同时指出应把"工具回执"与"实际送达模型的图片集合"分成两个事件（工具给了图 ≠ 模型收到了图）。
3. **两套 token 估算器并存**：压力/压缩用非 ASCII ×1，请求硬闸与正文卸载用 ×1.5，CJK 场景差 1.5 倍。
4. **计划推进 vs 心跳不可分**：等值 `plan_update` 仍写 `plan_updated` + `tool_completed`，
   外部看不出"在推进"还是"在重复"。
5. **`ForceThinkingOff` / `BoostStepOutputBudget` 一旦置位全轮无复位路径**：一次空输出或截断后，
   本轮余下所有步骤都关着思考。
6. **工具失败闭环**：失败指纹有三套口径，且没有"后续是否解决"的关联字段。
