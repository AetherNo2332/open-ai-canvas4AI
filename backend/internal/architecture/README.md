# 后端架构守卫

`TestWorkspaceArchitecture` 用 Go AST 扫描后端模块里的全部生产 Go 源码，不加载 app，也不启动模型或数据库。测试文件、`testdata/`、`third_party/` 和以 `.` 开头的目录不在扫描范围内（`InScope`）。规模和协议规则沿用上游 ddcat 的 `backend/internal/agent/architecture`，依赖方向按 AGENTS.md 第 3 节的调用链。

## 全后端规则

- **规模**：单文件不超过 600 行（物理行），函数不超过 120 行（从 `func` 到右花括号），圈复杂度不超过 20。
  - 复杂度计入 if、for、range，非 default 的 case 和 select 分支，以及每个 `&&`、`||`。
- **函数字面量**：单独度量，不计入外层函数，但自身也受同样的上限约束。
  - ID 为 `<文件>:func:<外层函数或包级变量>.func<序号>`，序号按源码顺序从 1 开始，与行号无关。
  - 这样把逻辑挪进闭包就绕不过上限。
- **依赖方向**（`layerViolation`），调用链为 `handler -> service -> app -> 业务域包 -> repository/model -> database`：
  - `package_roles.go` 明确登记 internal 顶层包角色，未知包报告 `package_classification`，必须先补分类且不能用基线豁免；子包继承顶层角色。该分类不额外禁止域包之间的调用。
  - handler 不得 import app、repository、database，应该经 service 调用；
  - repository 和业务域包（`internal/` 下除 handler、service、app、model 以外的包）不得 import handler、service、app；
  - model 不得 import handler、service、app、repository、database；
  - service 和 app 是组合层，不受这条约束；`internal/` 之外的入口（cmd、payment-plugins）负责组装各层，也不受约束。
  - handler 不得 import `gorm.io/gorm`、`gorm.io/driver/*` 或 `database/sql`；持久化错误在 app 或业务域转换为业务错误，service 仅再导出（如 `service.EcommerceAgentNotFound`），handler 只映射响应。
  - `internal/agentmemory` 根包保持纯规则：不得 import repository、database 或 agentmemory/document。

## 职责与门面规则（responsibilities.go）

迁移单元在 app 保留的门面文件按精确路径登记在 `facadeFiles`。登记文件只能包含：

- 类型别名（`type X = owning.Sym`），不允许新定义 struct/interface；
- 常量与变量初始化只能是基本字面量或对拥有包符号的直接引用；
- 函数体必须是单 return 直接调用拥有包符号，参数只允许原有标识符，不得通过闭包或计算参数隐藏业务；不得声明 `init`。

`internal/service` 全包适用更严格的声明规则：只允许别名、常量和直接再导出变量，不允许函数声明及计算初始化。

业务函数体、带计算的初始化和规则无法判断的形态（如别名环）一律报告为 `facade_declaration`，不静默当成合法。新增迁移单元时在此登记其门面文件，不做文件名猜测。

## 云端 Agent 附加规则

云端 Agent 文件（`InAgentScope`）另外要求导出函数签名、类型别名和公开 DTO 字段不暴露非类型化 map：

- `internal/app` 下的 `cloud_agent.go`、`cloud_agent_*.go`、`agent_*.go`；
- `internal/handler` 下的 `agent*.go`、`internal_agent.go`；
- `internal/agentcontext/**`、`internal/agentmemory/**`、`internal/identity/worker/**`。

别名会在扫描范围内跨文件追踪，泛型类型参数按实例代入；同一泛型嵌套仍逐层检查，合法递归类型不会无限展开。`json.RawMessage` 和 `map[string]json.RawMessage` 可以作为协议边界。

## 基线

`baseline.json` 登记存量债务，每条为精确 ID、冻结时的数值和原因。不允许通配豁免，依赖方向违规也不能登记。`Check` 从不写基线，也没有自动接纳新问题的开关：

- 出现新的超标，或已登记条目的数值增长，测试失败；
- 某条债务改善（数值下降或问题消失）后，测试同样失败，要求在同一提交里下调或删除该条基线，防止改善后又悄悄回到旧值。

改代码时只能下调或删除基线条目，不得新增或调高。新代码必须满足上限。失败信息里会给出当前值和上限。

跨版本检查同时读取明确 Git 基准的旧基线和当前源码，防止同一提交同时扩大代码和额度：

```bash
go run ./cmd/check-architecture-baseline --base <可信基准SHA>
```

PR 使用 PR base，push 使用 before；缺失或全零基准失败。手动/被调用 CI 显式使用当前检出提交的第一父提交并打印说明。本地使用 `--base` 对应的 merge-base。原有 `Check` 和 stale-baseline 校验仍执行。

`baseline-moves.json` 初始为空。迁移声明为 `from/to/reason`，只用于本次比较；源与目标必须一对一，不能链式、复用、改规则或扩额。命令从 Git archive 扫描旧源码，核对旧问题存在、当前源问题消失、目标问题真实存在且实际数值不增长。声明不能证明业务语义等价，必须审查迁移实现；下一批无迁移的改动应清空声明。

```bash
cd backend
go test -count=1 ./internal/architecture
```

负例测试覆盖以下情形，它们只在临时目录里写源码文本，不编译违规样例：

- 越层 import，以及依赖违规不能登记例外；
- 别名隐藏的 map，以及 map 规则只约束 Agent 文件；
- 文件、函数、闭包和包级函数字面量的规模与复杂度；
- 基线增长、改善后未下调、通配和重复条目。

完整计划由 Backend tests（rest）分区覆盖本包；已映射的局部变更单独选择本包，并执行跨版本基线比较。
