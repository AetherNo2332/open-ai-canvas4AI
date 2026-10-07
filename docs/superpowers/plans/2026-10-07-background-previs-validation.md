# 后台预演本地验收（2026-10-08）

本地实现已形成真实后台执行链：`previs_preview → 持久任务 → renderer → MP4/PNG → 资源登记 → 画布事务提交 → Agent回执`。关闭用户网页不会移除执行器。生产192.168.90.200:3000未升级，本报告中的画布和媒体来自隔离测试，不是用户原生产画布的成果。

工作树：`canary`；分支：`codex/previs-background`；基础版本：`69df3f11`。用户已有的`MEDIA_POLICY.md`和`SYSTEM_POLICY.md`改动保留。技能库中的「影策短剧制作工作流」本地更新为1.3.0；执行和成功闸门由后端合同保证，技能描述不能代替真实资源和节点。

## 真实产出证据

测试没有打开用户画布或导演台网页，没有运行Pi模型。Linux无头Chromium运行实际Three.js视口，后端独立探测下载的MP4并登记资源，最后重读持久画布。

| 镜头 | Task ID | 视频资源 | 预览资源 | 画布节点 |
|---|---|---|---|---|
| 1 / static | ag6c0596913af7ef8e02ccdf3973280905 | 4590e47556b90d0b6790a47f0b790f51 | ec3bb61304fab22fdd5f35b971152240 | ag0a6adafe5497af99e4f1de75edccb497 / ag7c8a9c61af09ba65f55c522aa59bed99 |
| 2 / push_in | ag98c9f011905ed7cbb5719331b3396b15 | 3fbacc08fb72c54bf6534a428a1c0113 | 09df405d6c782d52351dac422ec94b73 | ag8767447c332ef46a6c337fba905d60f4 / aga9ed105e51269fcdf57f27df68137d63 |
| 3 / pan_right | ag39b7870bfa3cc3d5aaa1f0f55bd1fb28 | 8571b785bc7f8112a5197ee5e24b1351 | a3c251ca431ff131c22233b0fd64a3d9 | agef28b3f755b857ef6cfaa03735c78b7b / agdc879ab37d2b617ff0e2803a347c2031 |

三个MP4均为H264、360×640、4帧、500ms；三个PNG均为360×640。合计6个资源、6个节点，三镜测试92.28秒。这是短片正确性验证，不能推出长镜头吞吐或目标服务器性能。真实演员、体块、运动机位和起末帧像素变化另由Node渲染测试验证；构图帧也已人工查看。

- [镜头1视频](../../../../artifacts/director-ssh-20261007/background-previs-20261008/shot-1.mp4)、[镜头2视频](../../../../artifacts/director-ssh-20261007/background-previs-20261008/shot-2.mp4)、[镜头3视频](../../../../artifacts/director-ssh-20261007/background-previs-20261008/shot-3.mp4)。
- [持久画布结果](../../../../artifacts/director-ssh-20261007/background-previs-20261008/canvas.json)、[真实验收日志](../../../../artifacts/director-ssh-20261007/background-previs-20261008/e2e-final.log)。

第三镜渲染期间将Pi run置为failed并执行清理。预演仍成功保存和写回；Pi仍为failed，没有被迟到完成复活。这是真实渲染配合持久状态注入，不是杀死真实Pi进程的测试。

## 真实冲突与恢复

任务`ag07156386b2a73ce5b74db249d3f9ea6a`冻结后改变目标相机FOV。视频和预览已保存，回写报告`target_changed`，没有添加成功节点。还原目标后创建恢复操作`ag0f25e2f895387776c4874e72c2d5ffe5`，只补画布关联：

- 视频资源始终为`e1af5dad2f8cd3f01edc6fc35c5154d4`，预览始终为`405415a0bd983a1404f406049b1a8e84`。
- `renderAttempts=1`，恢复没有再次启动Chromium，没有新增第二份媒体；原任务保留failed历史，恢复操作成功并添加2个节点。
- [恢复视频](../../../../artifacts/director-ssh-20261007/background-previs-20261008/recovery.mp4)、[恢复画布](../../../../artifacts/director-ssh-20261007/background-previs-20261008/recovery-canvas.json)。

## 人工、画布和Pi异常覆盖

“自动”表示状态/协议/事务测试；“真实”表示真实浏览器、编码、资源或画布链路。表中待验收项没有被记成通过。

| 设计场景 | 当前证据 | 仍待运行的场景 |
|---|---|---|
| H1 插话/暂停/取消 | 既有持久插话与ask_user入口；预演失败隔离、用户取消及取消后拒写自动测试；暂停只在下一安全边界阻止新投递 | 网页中的送达反馈、暂停/单镜头/全run交互；真实渲染中人工操作 |
| H2 审批 | 待答不入队、拒绝不执行、场景变化后旧审批失效自动测试；沿用现有审批幂等合同 | 关闭页面后重开、双击决定、拒绝后迟到批准的完整用户流程 |
| H3 画布修改 | 真实机位变化冲突；删除目标、无关节点修改、合并最新文档自动测试 | 人工同时编辑界面的完整流程 |
| C1 保存后回写失败 | 真实资源保留及只回写恢复；相同资源ID、渲染次数1、原失败历史 | 生产存储/数据库故障注入 |
| C2 丢失回执/重复请求 | 同call提交幂等、已提交结果只补receipt、稳定节点自动测试；Node对真实完整产物重启对账 | 运行中的实际HTTP断线与后端进程重启 |
| C3 配额/权限/坏画布 | 总存储配额、画布上限、坏JSON、账号停用、资源缺失、跨用户资源拒绝、资源租约到期后清理引用自动测试 | 目标部署环境对应故障注入 |
| C4 关闭再开 | 后端持久画布重读、媒体独立探测；前端强制刷新与完整回归 | 实际网页断SSE/缓存过期/重开播放和明暗主题 |
| P1 Pi退出 | 真实渲染中Pi failed+cleanup不撤销任务，Pi不复活；取消区别自动测试 | 真实Pi进程终止、恢复后对账 |
| P2 receipt前崩溃 | 持久call/task检查点、重复提交仅1任务、完成receipt重放自动测试；Agent恢复回归 | 真实进程在指定检查点被杀死 |
| P3 租约/checkpoint | 旧owner不能写回、旧renderer迟到失败不能覆盖新owner；普通任务接管有限等待旧renderer租约RED→GREEN，等待时继续检查DB租约 | 实际数据库/Redis断连与会话revision冲突组合 |
| P4 renderer/编码故障 | Node崩溃有界重试、确定性资源/WebGL失败不重试、空文件拒绝；真实Linux渲染、探测和本任务进程回收 | 实际运行中Chromium被杀/卡住等外部故障 |
| P5 取消/完成竞态 | Node取消与迟到完成、旧执行器与新owner；Go取消/旧租约原子拒写及Pi终态保护 | 多进程同时取消/接管的压力测试 |
| P6 等待与停滞 | 等待沿用MediaTaskID；阶段/帧数为实际进展，续租不算进展；5分钟停滞/30分钟总超时合同已实现 | 真实5分钟只续租不推进及控制面同时停滞 |
| P7 模型超时/预算 | 已授权预演与Pi失败隔离，后台执行不调用模型；真实失败Pi状态下产物可读 | 真实模型超时、压缩失败、预算耗尽的完整运行 |

## 自动检查与复现

- web：全量2189/2189；专项20/20；TypeScript检查与生产Vite构建通过。
- Agent：全量157/157，包含harness漂移、工具描述大小及恢复协议。
- renderer：Linux12/12，包含真实离线演员/体块/运动机位、H264探测、像素变化、完整产物重启和本任务进程无残留。重新封装固定镜像后，在`--network none --workdir /app`下不挂载源码复验12/12，耗时27.08秒。
- Go：预演、Pi清理/取消/看门狗、资源引用与HTTP鉴权专项通过；最终接管回归15个顶层测试通过，耗时152.12秒（真实E2E在此命令按环境跳过，已在独立命令中通过）；当前源码`go build ./...`退出0。

在`canary`运行。`.local/previs-test.env`使用测试专用的至少32字符密钥，不提交或打印。测试使用预先准备的Linux Go模块/cache卷；renderer只读源码挂载属于测试编排。

```powershell
docker compose --env-file .local/previs-test.env -p canvas-previs-verify -f docker-compose.previs-test.yml run --rm --no-deps backend-tests
docker compose --env-file .local/previs-test.env -p canvas-previs-verify -f docker-compose.previs-test.yml run --rm --no-deps -e PREVIS_REAL_BACKEND= backend-tests go test ./internal/app -run 'TestBackgroundPrevis|TestPrevisRendererClient' -count=1 -v -timeout=8m
```

前端独立入口：在`web`运行`bun --bun ./node_modules/vite/bin/vite.js build --config vite.previs.config.ts`。本地封装：`./tools/build-previs-runtime.ps1 -Image canvas-previs-renderer:verify-final`。后者复用已验证Vite产物，不能替代生产Dockerfile的冻结锁文件完整构建。

## 审查与实施取舍

独立审查提出6项Important，均先复现失败再修复并跑回归：旧renderer捕获可变owner覆盖接管者、failed资源重试绕过总配额、failed恢复操作永久卡住再次请求、预览缺持久引用、历史颜色规范化造成错误冲突、完成任务未确认释放导致16任务容量耗尽。另补普通后端接管等待旧renderer租约的RED→GREEN测试。没有待处理的Minor审查项。

按决策顺序记录取舍和代价：

1. 复用现有隔离工作树与基础版本，保留用户policy改动；发布前须比较最新fork与生产，代价是这轮不能证明当前生产合并兼容性。
2. 用户“开始执行”授权本地实现与验证，计划没有再设批准门；若范围理解错误，可撤回本地提交，尚无生产改动。
3. 沿用项目原生CDP，不增加Playwright依赖；代价是浏览器协议与进程清理由本项目维护。
4. Windows SQLite/加载表现不稳定后改用Linux Go缓存验证；其他容器不动，代价是Windows原生后端运行没有被证明。
5. renderer并发1、软件CPU渲染，最初保留16个终态任务6小时；审查后增加两资源检查点的owned ack，正常完成释放容量。未保存任务仍最多16个并限期回收，代价是容量不足会明确排队失败，过期产物不能只回写恢复。
6. 暂停使用下一安全边界的持久ask_user；普通插话不立即停止在途渲染，代价是需要显式取消来立即终止。
7. 本地使用已验证Vite bundle加固定runtime封装；完整冻结安装生产构建因长期无进展及磁盘竞争停止。代价是上线前仍需完成该构建，不把本地镜像当生产发布验证。
8. Linux实测要求匹配的chromium-swiftshader、init及本任务进程组/profile清理；线程上限从128改256，CPU2/内存2GiB保留。代价是允许更多线程，目标机负载需另测。
9. 测试Go镜像复用renderer的FFprobe与musl/SDL3动态库，构建时先验证probe能运行；测试源码只读挂载。代价是这份测试镜像不代表完整生产后端镜像。
10. 每原任务每模式最多8次明确回写恢复，失败操作保留历史；超过上限需新授权任务，代价是不能无限点击恢复绕过容量。

生产升级、回退、长片性能和上表人工运行项仍未执行；这些是验证边界，不是已上线能力。测试媒体和脱敏日志保存在`artifacts/director-ssh-20261007/background-previs-20261008`，不包含测试密钥、用户素材或系统提示。

本地运行取证中的一次固定镜像测试因命令默认工作目录为`/app/renderer`，将测试静态路径解析到不存在目录，出现`viewport_load_timeout`。改为`--workdir /app`复验；这次失败归属测试命令，记录原日志以便复查，不改大载入超时掩盖路径错误。

收尾保留当前本地分支与工作树，不合并或推送；停止的仅为`canvas-previs-verify`，不删除其持久卷。计划运行临时目录在证据复制后清理；验收报告和实施计划纳入本地Git提交，媒体/日志保留在artifacts中。
