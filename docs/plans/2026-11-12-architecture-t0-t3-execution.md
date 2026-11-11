# 架构治理 T0–T3 实施记录

对应计划：`2026-11-12-architecture-test-domain-governance.md`，本批只实现 T0–T3。首期整体仍是 T1–T7；T4–T7 待实施，T8–T10 是后续独立批次。

## T0：开发基线

- 目标为最新 fetch 后的 `upstream4ai/main`，目标与初始 HEAD 都为 `9fc248c34218cf882c9b3a189d9e74c5b54fc1cf`。
- 独立分支 `codex/architecture-t0-t3`，初始工作区干净。主工作区的用户修改及未跟踪文件未复制、覆盖或清理；只复制了用户指定的本计划文档。
- 工具：Node 26.1.0、Bun 1.3.9、Go 1.27.1 windows/amd64；项目要求 Go 1.25.0、Web packageManager bun@1.3.9。未切换或升级依赖；冻结锁文件安装 1450 包，113.87 秒。
- app 现有生产文件 503、测试文件 414。记忆文档仍在 app，轮询仍在 app；未迁移任何业务实现。
- 初始架构守卫：包内耗时 11.150 秒（包含首次扫描/缓存环境影响）；app 空用例编译入口 `go test -run '^$' ./internal/app` 为 0.676 秒。没有同环境 app 完整执行耗时和历史 CI 明细，不能据此宣称提速。
- 无新表、迁移、依赖升级或运行时 API 合同变化；不启动开发服务，不操作生产。

## T1：跨版本基线

- `CompareBaselines` 阻断同 PR 新增 allowance、增长 allowance；删除和下调允许。
- `BaselineMove` 一对一、不链式、不复用，保持规则和额度；命令结合 Git archive 的旧源码扫描和当前扫描，验证旧问题消失、目标存在且实际数值不增长。
- 命令必须提供可信非零 base；PR 用 PR base，push 用 before。手动/被调用 CI 使用检出提交第一父提交，明确打印来源；无法取得时失败。
- 保留现有 workspace 和 stale-baseline 检查，未修改 baseline.json 的额度。
- RED：新增接口不存在时测试编译失败；GREEN：架构包和命令测试通过，命令对实际目标基准通过。

## T2：统一验证计划（影子）

- `buildVerificationPlan` 为纯函数；声明式策略、反向依赖图和有限组合测试组分开。
- `go list -json` 提取 Imports/TestImports/XTestImports；未知代码、共享路径、包图缺失扩大范围。Git 基准或 diff 不可信则直接失败。
- Git 零字节 name-status 同时记录旧新路径；工作区模式包含已提交差异、已跟踪修改和未忽略新文件；显式 head 不混入工作区。
- runner 新增 `--mode`、`--plan-only`，默认 full，保留 jobs。实际门禁仍使用原完整相关执行方式；影子计划不用于跳过 CI。
- 本地保存 JSON，CI 上传 artifact；docs 门禁保留。plan-only 只收集差异和包图，不运行测试。
- RED：模块缺失；GREEN：选择、Git fixture、失败扩大和路径测试通过。

## T3：Web 调度

- 原 pretest 的八文件组：tiptap 版本、admin UI、announcement prompt、password reset、OSS settings、channel model form、price tier form、admin logical models；包含源码合同与真实模块导入回归。历史选择每个文件的具体隔离动机缺乏可复核证据，因此保守保持其共同进程边界，不合并到普通集合。
- canvas-grid 导入 React/SSR 及真实 CanvasViewport，保留原独立进程。
- welcome-title 已通过 harness 子进程隔离；此次保留包装测试。
- `bun run test` 调用唯一调度器；9 个隔离文件从普通集合排除，新增匹配 Bun 命名的文件自动发现。专项 `bun test <文件>` 保持可用。
- RED：调度模块不存在；GREEN：唯一分组、新文件发现、缺隔离文件、失败传播测试通过。

## 设计裁决

- 本次范围是 T0–T3，不以首期 T1–T7 完成作为本批退出条件；若理解错误，会低估后续业务迁移工作。
- Windows 不直接执行 skill 的 Bash 工作流包装，使用同等独立 worktree、测试与本记录；若记录不完整，会增加后续接手成本。
- 未选择/未执行的检查只作为计划或待验收，不记作通过。没有同环境基线，不承诺性能比例。

## 最终验证

- `node --test scripts/verification-plan.test.mjs web/scripts/run-tests.test.mjs`：12/12 通过，覆盖 Git fixture 和失败传播。
- `go test -count=1 ./internal/architecture ./cmd/check-architecture-baseline`：通过；最后记录包内 1.332 / 1.482 秒。命令测试含真实 Git fixture 的合法迁移、源仍存在拒绝及基准缺失拒绝。
- `go vet ./internal/architecture ./cmd/check-architecture-baseline`：通过；实际目标基准的 `go run ./cmd/check-architecture-baseline --base <目标SHA>` 通过。
- 真实干净检出的显式 head 计划与工作区 plan-only 已执行；后者不启动测试。输出保存于本地缓存，内容只含路径、规则和命令。
- `bun run test` 完整入口已执行一次：隔离八文件组 61 pass / 0 fail，2.73 秒；canvas-grid 4 pass / 0 fail，0.38 秒；普通 512 文件为 3470 pass / 5 skip / 9 fail，268.61 秒。共 521 个发现文件，三个组互斥。
- 失败分类：5 个 ci-image-release 测试 Bash 返回 127，原工作区同一测试也为 7 pass / 5 fail；2 个 http-ownership 测试把 Windows 反斜杠相对路径与正斜杠白名单比较，单独执行仍失败；2 个已有 harness（canvas-bottom-controls、canvas-reference-migration）全量时 exitCode=null，单独重跑通过，进程干扰/资源原因尚未确证。未删除测试、扩大超时或伪造成功。
- 普通集合的 5 skip 需要真实后端，未视为通过。完整 Web 门禁仍未通过，不能作为可推送证明。
- 独立只读评审发现并修复：工作区回改抵消已提交差异的漏选（新增 fixture RED→GREEN）；full 影子计划遗漏部分门禁（补充格式、vet、工具合同、lint、changelog、部署命令等声明）。评审未提出删除或绕过失败测试。
- CI、支付双架构、PostgreSQL/Redis、真实 Agent smoke、浏览器 E2E 和 docs 站完整构建尚未在本批本地覆盖；本批未推送，也未宣称通过完整推送前脚本。
