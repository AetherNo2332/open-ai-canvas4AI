# Pi Agent 流式消息一致性设计

## 目标

修复 Pi Agent 从 Go 模型任务到浏览器 SSE 的三处一致性问题：重复发送正文、重连后缺少活动草稿、同一运行重连时重复应用已消费事件。成功标准是：一次模型步骤的正文在 Pi 内部只按新增后缀产生 `text_delta`，运行快照和轮询看到同一份活动草稿，浏览器重连不会重复追加持久化事件。

## 约束

- 保留当前工作树中已有的租约终态修复，不回滚或覆盖。
- 不改变公共 SSE 事件类型和递增 `seq` 语义；`seq=0` 仍只表示快照派生事件。
- 不把推理文本写入正文草稿。
- 只修复当前 Pi/Agent 流程需要的状态和去重，不引入无关重构。

## 数据流设计

### Go 运行态草稿

`newCloudAgentStreamPublisher` 继续把批量 delta 写入运行 journal。对于 `assistant_delta`，在同一个 `MutateCloudAgent` CAS 中追加 `cloudAgentRuntime.ActiveTextDraft`，使 journal 事件与快照草稿来自同一次持久化状态转移。`reasoning_delta` 只写 journal，不修改正文草稿。

创建新的活动模型任务时清空 `ActiveTextDraft`。`PiModelStepView` 和公共运行快照优先读取运行态草稿，并在旧运行没有该字段时回退到 `Task.TextDraft`。活动任务结束后，现有检查点/终态逻辑继续控制是否展示活动草稿。

### Pi bridge 差量

`CanvasBridge.modelStep` 为一次模型步骤维护已发送草稿前缀。每次轮询只在服务端草稿以该前缀开头时发送新后缀；未增长、重复全文或已发送前缀之后的最终轮询不会再产生 delta。若服务端返回较短草稿，视为尚未增长，不回退已发送前缀。

### 浏览器 SSE 游标

面板按 `runId` 保存最后消费的持久化事件序号。首次订阅新运行时从零开始；同一运行因网络断线或连接 epoch 变化重建订阅时，把当前序号作为 `after`。现有事件处理器的 `seq <= lastSeq` 去重继续保留，快照事件不推进游标。

## 回归测试

- Agent bridge：服务端草稿依次返回前缀增长和重复最终值时，断言回调只收到每段后缀。
- Go Agent：publisher 产生正文事件时，断言 `ActiveTextDraft` 与 journal 同步，Pi 轮询/运行快照读取该草稿；推理事件不得污染正文。
- Web：游标状态在同一 run 重建订阅时保留，在切换 run 时归零；SSE 重新发送旧事件不会被再次应用。

## 非目标

不改变 provider 的最终结果格式、账单逻辑、事件 journal 的排序规则，也不改变普通文本任务的独立回放协议。
