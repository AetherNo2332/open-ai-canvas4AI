# Agent 架构守卫

`TestWorkspaceArchitecture` 用 Go AST 扫描 fork 云端 Agent 的生产源码，不加载 app、不启动模型或数据库。规则沿用上游 ddcat 的 `backend/internal/agent/architecture`，扫描范围换成 fork 的 Agent 文件布局（`InAgentScope`）：

- `internal/app` 下的 `cloud_agent.go`、`cloud_agent_*.go`、`agent_*.go`；
- `internal/handler` 下的 `agent*.go`、`internal_agent.go`；
- `internal/agentcontext/**`、`internal/agentmemory/**`。

检查内容：

- 单文件至多 600 行（物理行），函数至多 120 行（从 `func` 到右花括号），圈复杂度至多 20。复杂度计 if/for/range、非 default 的 case/select 分支以及 `&&`/`||`。
- 函数字面量单独度量：闭包不计入外层函数，但自身也受同样的上限，ID 为 `<文件>:func:<外层函数或包级变量>.func<序号>`，序号按源码顺序从 1 起，与行号无关。把逻辑挪进闭包不能绕过上限。
- 域包（`agentcontext`、`agentmemory`）不得 import `internal/app` / `internal/service`（含子包、匿名或重命名 import）。handler 与 app 本身按分层约定依赖 service/app，不受此条约束。
- 导出函数签名、类型别名和公开 DTO 字段不得暴露非类型化 map；别名只在扫描范围内的文件之间追踪（与上游一致，`internal/app` 里非 Agent 文件定义的类型不展开）。`json.RawMessage` 和 `map[string]json.RawMessage` 可以作为协议边界。

`baseline.json` 登记的是存量债务：每条为精确 ID、冻结时的数值和原因，没有通配豁免，依赖方向不能登记。`Check` 从不写基线，也没有自动接纳新问题的开关：

- 新出现的超标、或已登记条目的数值增长，测试失败；
- 某条债务改善（数值下降或问题消失），测试同样失败，要求在同一提交里把基线下调或删除，防止改善后又悄悄回到旧值。

改代码时只能下调或删除基线条目，不得新增或调高；新代码必须满足上限。

```bash
cd backend
go test -count=1 ./internal/architecture
```

负例测试覆盖越层 import、别名隐藏的 map、文件 / 函数 / 闭包 / 包级函数字面量的规模与复杂度、基线增长与未下调、通配与重复基线；它们只在临时目录里写源码文本，不编译违规样例。CI 的 Backend tests（rest）在全量测试前单独运行本包。
