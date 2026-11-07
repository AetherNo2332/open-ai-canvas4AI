# Pi Agent 同源 Session Runtime（C2）

> 本文记录 C2 的第一条垂直切片：把真实 Pi SDK Session 的创建、恢复和工作区隔离抽成通用工厂，并让现有画布 runner 通过该工厂运行。总控、短剧和电商 Host 的接入、业务工具网关和长会话跨两轮压缩仍属于后续 C2 验收。

## 已落地的 Runtime 边界

`agent/src/session-runtime.ts` 提供 `createSessionRuntime`。调用方只提供工作区、Session ID、Go 持久化的 Pi entries、模型 provider、系统提示、扩展和工具名称；工厂统一负责：

- 创建不访问外部模型目录或网络的 `ModelRuntime`，注册 Host 提供的 provider 和模型；
- 创建独立的 `SettingsManager` 与 `DefaultResourceLoader`，关闭宿主机扩展、Skills、模板、主题和上下文文件发现；
- 按调用方提供的显式 Skills 路径恢复资源，并允许 Host 校验冻结的 Skill 快照；
- 用 `SessionManager.inMemory` 从稳定 Session ID 和持久 entries 重建真实 Pi Session；
- 将完整工具目录作为 SDK lifetime allowlist，同时由 Host 扩展决定当前边界实际披露哪些工具；
- 在初始化失败时清理本次工作区，成功时把清理责任交回运行 Host。

画布 runner 继续负责 Go 模型桥、工具回执、检查点、原生压缩、图片历史和错误恢复；它只组装这些 Host 能力，然后调用 `createSessionRuntime`。因此现有画布路径已经不再直接创建 `ModelRuntime`、`DefaultResourceLoader`、`SessionManager` 或 `AgentSession`，后续业务 Agent 可以复用同一入口。

## C2 当前证据

| 场景 | 证据 |
| --- | --- |
| 真实 SDK Session 创建与 entries 恢复 | `agent/test/session-runtime.test.ts`：恢复持久 entry、稳定 Session ID 和独立工作区 |
| 画布生产路径使用统一工厂 | `agent/test/runner.test.ts`：runner 委托 `createSessionRuntime`，工厂内部调用 `createAgentSession` |
| 工具 lifetime allowlist | `agent/test/runner.test.ts`：runner 传递完整 Go 工具目录，Runtime 转为 SDK `tools` allowlist |
| 画布恢复、工具、压缩和错误回归 | `cd agent && npm test`：202 项，201 通过、1 跳过、0 失败 |

## 尚未闭合的 C2 验收

- C2-A 还需要总控、短剧和电商各自的 Host 通过同一个 Runtime 启动，并留下跨业务运行证据；
- C2-B 还需要把通用工具 Schema 与 Go 工具网关回调接入 Runtime，覆盖伪造调用和撤权即时失效；
- C2-C 还需要验证跨 Session entries、技能和工具回执的拒绝路径；
- C2-D 还需要用确定性长会话完成两轮原生压缩、费用归属和重放幂等验收。

这条切片保持了画布现有行为：模型步骤仍由 Go 准入和计费，工具副作用仍由 Go 回执确认，Pi 只负责同源 Session 循环、工具调度和原生压缩。
