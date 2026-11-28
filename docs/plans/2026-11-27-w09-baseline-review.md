# W09 审核与基线移植记录

## 范围与来源

- 目标基线：`6e20dd53be68c9d6a023f97996eb1589eceef3c5`，主干与 PR153–155 的本地集成提交。
- 施工分支：`fix/w09-recovery-on-integration`，复用已有基线工作树。
- 来源：`codex/shortplay-w09-recovery`，HEAD `b6f832a6995ee4c904359fac8defe5d79aae1581` 上的未提交修改。接手时为 17 个 tracked 修改和 3 个 untracked 文件；后端 16 个 tracked 文件与 3 个新增文件作为移植输入，待测文档重新结合基线编写。
- 原 W09 工作树、主集成工作树均保留；没有合并来源分支整条历史，没有覆盖集成分支的 Agent 续跑改动。
- 本记录描述代码审查与本地验证；不代表部署、真实渠道或多进程验收完成。

## 目标职责与审核结论

基线源码没有修复 W09 的四个核心问题：任务创建后再单独更新 job 绑定；running 且 task_id 为空的 job 不会被普通在途查询选中；三类结果分支未处理 submission_uncertain；媒体 worker 只在 tick 入口检查 drain。相关入口是 `shortplay_extract_worker.go`、`shortplay_asset_image.go`、`shortplay_asset_audio.go` 与 `repository_shortplay_jobs.go`，因此本次需要移植恢复实现，而不是只补文档。

任务创建事务仍由 repository 负责，内部 `TaskCreateHook` 把业务绑定放入同一事务；HTTP 请求不能传入该回调。app 的短剧调度负责选择模型、提交、恢复策略和资产状态投影；repository 提供条件绑定与收口。三个生产提交入口均改用 `submitShortplayTaskForJob`，其他任务调用方显式不传业务 hook，外部 API 不变。

审核认可任务创建与 job 绑定的原子性设计：创建任务、适用的计费预留与 job 条件绑定同事务。绑定 UPDATE 取得行锁后，其他恢复 writer 等待提交再检查条件；恢复先赢时绑定返回未命中，迟到任务与费用写入整体回滚。不能把“进入任务事务”误解为已经锁住 job 行；PostgreSQL 测试分别验证两种竞争顺序。

### 审核发现并修正

1. **迟到提交仍可能覆盖新 job 的资产状态（重要）**。原补丁在绑定被恢复拒绝后，图片/音频提交函数仍在事务外把资产标记为 failed；外层按 ID 无条件结束旧 job，还会覆盖待核对原因。新增回归先观察到图片、音频两项失败，再将未绑定任务的结束改为条件更新，资产 failed 投影与该更新同事务。移除提交前及提交失败后的无保护状态写入；条件未命中时保留其他 writer 的结果。普通提交失败另有回归确认 job 和资产都会失败收口。
2. **旧版孤儿恢复缺少媒体状态投影（集成修正）**。移除事务外 queued 写入后，模拟旧版写回中断的图片/音频回归暴露空媒体状态无法通过结果 CAS。找回任务时把 job 绑定和空/queued/running 媒体状态恢复放在同一事务；ready/failed 状态及 current 指针保留，避免重复样本。两类原有孤儿收口回归和 SQLite/PostgreSQL 状态保留测试通过。
3. **媒体领取前的 drain 复查不足（补强）**。在 orphan/running 查询后、实际 Claim 前再次检查 drain/context，缩小检查后继续领取的窗口。它仍不是与 BeginDrain 共用锁的严格零领取保证。
4. 更正仓储注释：静默超时不证明进程死亡，事务先开始也不等于先获得 job 行锁。

### 保留的产品及部署边界

- 没有持久租约/心跳；绑定前仍活着的慢提交允许被恢复失败，之后绑定回滚。若产品要求所有活跃慢提交继续运行，需要另行设计持久所有权。
- 新代码不能约束旧 writer 的非原子写法，升级必须先排空旧 writer。不能据此承诺滚动混跑。
- 结构化反查按用户、项目、类型读取候选后解析 JSON；旧任务输入若已丢失关联，只能待核对，不猜测任务或自动重发。
- 本次沿用已绑定任务的结果处理与媒体 CAS；没有把整个短剧调度改造成持久领取租约系统。
- `submission_uncertain` 的短剧 job 失败不等于上游确定失败；任务、账务和未知费用必须留给既有核对流程。
- 不改表结构、Schema 113、工具 Schema、Pi 协议或能力目录版本。

## 本轮验证

使用 Go 1.25 工具链，与仓库格式约束一致。运行日志保存在该工作树忽略目录 `.local/w09-review/`，不提交本地环境或日志。

| 验证 | 结果及覆盖边界 |
| --- | --- |
| 新增迟到提交回归（修复前） | 图片、音频均失败，明确复现替代资产从 running 被改成 failed |
| `go test ./internal/app -run 'TestW09\|Test.*Shortplay' -count=1 -v` | 64 个顶层测试通过，无跳过；含三类任务恢复、歧义、drain/context、图片/音频结果与新增提交失败竞争 |
| `go test ./internal/app -run 'Test(Creation\|CreateTask\|TaskCreation\|DramaTask\|CloudAgentAdmission\|CloudAgentMediaBatch\|CloudAgentTaskEnqueue)' -count=1 -v` | 25 个顶层测试通过，无跳过；覆盖共享任务入口的相邻调用方 |
| `go test ./internal/repository -count=1` | 通过，配置隔离 PostgreSQL DSN；不代表所有可选依赖测试都已运行 |
| PostgreSQL / 恢复仓储专项 | 隔离 PostgreSQL 17，W09 两类竞争、结构化匹配、候选歧义、条件收口、媒体终态保留均实际通过；SQLite 对应用例同时通过 |
| `go test ./internal/architecture -count=1` | 通过，未增加架构豁免或放宽基线 |
| `go vet ./internal/app ./internal/repository ./internal/architecture` | 通过 |
| `go build ./...` | 通过 |
| `go test ./... -run '^$' -count=1` | 全部后端测试包编译通过；只编译，不代表全量测试已执行 |

PostgreSQL 使用本轮新建、仅绑定本机端口且数据位于临时文件系统的专用容器；未读写已有 W09、短剧或 Studio 容器。仓储竞争在一个 Go 测试进程内使用并发连接，合成积分账户和订单；未启动真实任务 dispatcher、双后端或上游生成，所以“上游只收到一次”“真实付费订单唯一”仍待验。失败回滚测试没有插入成功的平台提交记录，表计数为零不能单独证明该路径已经做过真实平台提交验收。

完整 `internal/app`、完整 handler、PG/Redis 双后端与进程关停、真实模型/浏览器、文档站构建及推送前 full 门禁未在本轮执行。操作步骤和通过标准统一维护在 `pending-test.mdx` 的“短剧 W09 排空与恢复”小节；W09 发布验收继续保持未完成。
