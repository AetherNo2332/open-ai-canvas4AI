# 后端架构守卫

`TestWorkspaceArchitecture` 用 Go AST 扫描后端模块里的全部生产 Go 源码，不加载 app，也不启动模型或数据库。测试文件、`testdata/`、`third_party/` 和以 `.` 开头的目录不在扫描范围内（`InScope`）。规模和协议规则沿用上游 ddcat 的 `backend/internal/agent/architecture`，依赖方向按 AGENTS.md 第 3 节的调用链。

## 全后端规则

- **规模**：单文件不超过 600 行（物理行），函数不超过 120 行（从 `func` 到右花括号），圈复杂度不超过 20。
  - 复杂度计入 if、for、range，非 default 的 case 和 select 分支，以及每个 `&&`、`||`。
- **函数字面量**：单独度量，不计入外层函数，但自身也受同样的上限约束。
  - ID 为 `<文件>:func:<外层函数或包级变量>.func<序号>`，序号按源码顺序从 1 开始，与行号无关。
  - 这样把逻辑挪进闭包就绕不过上限。
- **依赖方向**（`layerViolation`），调用链为 `handler -> service -> app -> 业务域包 -> repository/model -> database`：
  - handler 不得 import app、repository、database，应该经 service 调用；
  - repository 和业务域包（`internal/` 下除 handler、service、app、model 以外的包）不得 import handler、service、app；
  - model 不得 import handler、service、app、repository、database；
  - service 和 app 是组合层，不受这条约束；`internal/` 之外的入口（cmd、payment-plugins）负责组装各层，也不受约束。

## 云端 Agent 附加规则

云端 Agent 文件（`InAgentScope`）另外要求导出函数签名、类型别名和公开 DTO 字段不暴露非类型化 map：

- `internal/app` 下的 `cloud_agent.go`、`cloud_agent_*.go`、`agent_*.go`；
- `internal/handler` 下的 `agent*.go`、`internal_agent.go`；
- `internal/agentcontext/**`、`internal/agentmemory/**`。

别名会在扫描范围内跨文件追踪。`json.RawMessage` 和 `map[string]json.RawMessage` 可以作为协议边界。

## 基线

`baseline.json` 登记存量债务，每条为精确 ID、冻结时的数值和原因。不允许通配豁免，依赖方向违规也不能登记。`Check` 从不写基线，也没有自动接纳新问题的开关：

- 出现新的超标，或已登记条目的数值增长，测试失败；
- 某条债务改善（数值下降或问题消失）后，测试同样失败，要求在同一提交里下调或删除该条基线，防止改善后又悄悄回到旧值。

改代码时只能下调或删除基线条目，不得新增或调高。新代码必须满足上限。失败信息里会给出当前值和上限。

```bash
cd backend
go test -count=1 ./internal/architecture
```

负例测试覆盖以下情形，它们只在临时目录里写源码文本，不编译违规样例：

- 越层 import，以及依赖违规不能登记例外；
- 别名隐藏的 map，以及 map 规则只约束 Agent 文件；
- 文件、函数、闭包和包级函数字面量的规模与复杂度；
- 基线增长、改善后未下调、通配和重复条目。

CI 的 Backend tests（rest）分片在全量测试前单独运行本包。
