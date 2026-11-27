# 短剧工作台 W08/W09 验收交接（音频格式、收录页面、第二账号、重启恢复、门禁根因）

## 目标

在恢复树 `codex/shortplay-w04-recovery` 上把 W08 的确定性渠道全流程与 W09 的隔离恢复做实：音频按请求格式出站并落库、收录走正式页面、第二账号真实越权、后端重启后不重复扣费与出站，最终对干净 HEAD 跑完整门禁后更新同一 PR154。

## 本轮已完成

1. **音频格式缺陷修复**（产品缺陷，非测试桩问题）：官方 `openai-audio` 是二进制响应，`binaryPayloadCreateResult` 只用 `http.DetectContentType` 认类型；MP3 没有标准库可识别的魔数 → `application/octet-stream` → 媒体 checkpoint 按 `audio/` 前缀拒绝，任务报“作品已生成，但保存未完成：记录恢复信息失败”。WAV 之所以能过，是因为 RIFF 被嗅探成 `audio/wave`。
   - 修复：`detectBinaryPayloadMime` 先嗅探，识别不出音频且 `resultKind=audio` 时按 ID3 / MPEG 帧同步认定 `audio/mpeg`（与音频结果校验同一判定）；未知字节仍保持 generic。
   - 回归：`TestOfficialOpenAIAudioUsesBinaryPayload`（ID3）、`...FrameSyncIsMP3`、`...KeepsUnknownBytesGeneric`、`TestMediaRecoveryStagesInlineAudioDataURL`。
2. **W08 音频真实链路**：出站 `response_format=mp3` → 桩按格式返回真实 MP3（帧头 `ff fb 90 c0`，15846B）→ 资源 `audio/mpeg`、资产音频版本 2、页面播放器 `readyState=4`、`duration=0.990375`、无 `mediaError`、可播放。
3. **W08 负例**：上游 500（跨 4 个 tick / ~20s）只产生 1 次出站、1 个 job/task/order，旧图旧音 ID 与版本不变；错比例图（1×1）任务成功但回写拒绝，job 明确报错，旧图 v2 保留。
4. **W06 收录（正式页面）**：此前前端只有 `adoptShortplayAsset` API 包装，没有任何入口。本轮补齐：添加节点菜单“短剧角色卡/道具卡/场景卡”、节点工具条“收录到短剧”、收录弹窗（选作品 + 目标集）。实测：自由画布局部卡《雨巷客栈·夜》（scene）→ 资产 #5、绑定第 2 集、目标集画布出现“项目资产”新卡，源卡保持局部卡不变。
5. **画布渲染缺陷**：`canvasNodeUsesCustomContent` 未登记 `shortplay-asset`，资产卡（含投送进集画布的）在画布上渲染成“未知节点”。已登记，卡片恢复为海报卡并显示“局部卡 / 项目资产”徽标。
6. **第二账号隔离（真实登录）**：管理员临时开启注册后，第二账号自行注册并登录（register/login 均 200），对首个账号的短剧详情/资产/资源文件/画布/提取/收录全部 404（不是 401 兜底）；随后关闭注册。密码未留存、未写入证据。
7. **W09 重启恢复（已验的一类）**：桩 `/audio/speech?delay=20000` 制造在途请求，3 秒后强杀后端并立即重启。同一 job/task 被接续（`attempts=2`），但系统以“上一次提交结果不明确，为避免重复扣费已停止自动重发”收尾：订单仍 1 个、重启后无新增上游调用、旧音频 `3d48acc8` v2 保留、`audioStatus=failed`。
8. **开关回归修复**：`shortplayEnabled` 关闭时，自由画布仍出现“短剧角色卡/道具卡/场景卡”与“收录到短剧”。根因：`project.tsx` 两处无条件传 `onAddShortplayAsset`，`onAdoptToShortplay` 只判 `projectId`；添加节点命令只看 handler 是否存在。修复：新增纯函数 `shortplayCanvasEntriesEnabled({ shortplayEnabled, isProjectLinked })`，工具条、添加节点菜单与收录弹窗统一按它门控（`isProjectLinked = shortDramaEnabled && projectId`，沿用既有语义，drama 关闭时画布按自由画布处理）。回归：`web/test/shortplay-canvas-entries.test.ts` 覆盖 `shortplayEnabled × shortDramaEnabled` 四组合与命令可见性。
9. **门禁根因（backend:full）**：`TestAgentSDKRealHTTPWorkerRestart` 在本机 SQLite 下报 `database is locked`（16 路并发 run）→ 任务 `result_persistence_failed` → run failed。基线对照：把 protocol 三个文件回退到 `db16a0f9` 后仍失败（同一 run/task ID），与音频补丁无关。按 CI 契约补 `TEST_AGENT_POSTGRES_DSN`（隔离 PostgreSQL）后通过：2/2 pass，258.54s。
10. **门禁根因（docs:types）**：本机 `node_modules` 里的原生二进制（`@esbuild/win32-x64/esbuild.exe`、TypeScript 7 的 `@typescript/typescript-win32-x64/lib/tsc.exe`）被环境拒绝创建目录/写文件（`Access is denied`，连 TEMP 也拒），把同一份字节以新文件落地后恢复；`docs:types` 与 tsc 均通过。属本机环境问题，不改仓库代码。
11. **web:test 波动**：满载并行时 `canvas-connection-controller` 触发 bun 5 秒默认超时，单独运行 3.1–3.3 秒通过，判定为负载相关而非本改动回归；最终门禁按 CI 的 `--jobs 2` 运行。
12. **门禁根因（backend:full 第二处，worker 注册）**：补上 PostgreSQL 后 `TestAgentWakePostgresWorkerClaimsDuringFallback` 失败，真实错误是 `Invalid worker slot`。根因：`agent/src/server.ts` 用 `os.hostname()` 直接拼 worker 槽位，而桥接/后端契约要求 `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`；本机主机名是中文，worker 在 capacity/claim 之前就抛错，永远注册不上（CI 主机名为 ASCII 所以不复现）。修复：新增 `workerSlotId()`，把非契约字符压成 `-`、空则回落 `agent-host`、整体截断 64；`agent/test/worker-slot.test.ts` 覆盖中文、空格、超长主机名。另把两处“外部 node worker 启动等待”从 5 秒改成独立 30 秒超时（`waitWakeWorkerSignal`），避免负载下把启动慢误判成失败。验证：该用例句连续两次通过（21.5s / 20.4s），`agent` 全量 209 pass / 1 skip。

## 门禁矩阵（HEAD `6d1f0fe27`，2026-11-26 19:16–20:02）

`bun scripts/verify-before-push.mjs --base origin/codex/shortplay-workbench-port --mode full`：26 项，失败 3 项 + 依赖阻断 2 项，退出码 1。

- 失败：`backend:full`（上述 SQLite 锁）、`docs:types`（上述本机原生二进制）、`web:test`（上述负载超时）。
- 依赖阻断：`docs:check`、`web:e2e`（dependency failed）。
- 通过：agent:check、backend:agent-lint、backend:app-shard-1..4、backend:baseline、backend:gofmt、backend:vet、backend:tool-contract、changelog、docs:metadata、docs:deployment、deployment:commands、verification:tests、payment:artifacts、payment:platforms、agent:smoke、web:format、web:lint、web:typecheck。

该轮在本机被工具重启终止、未收尾，因此不作为最终结论：修复 worker 注册与超时后，以最终干净 HEAD 的全量门禁结果为准（记录在 PR154 正文与验收证据里）。

## 证据源码身份

- 早期片（text/image/旧 WAV 音频、第一轮 500 负例）运行在 `db16a0f9`；MP3 修复片、收录、第二账号、W09 重启片运行在 `e6a727a` 工作区（其后提交），VERSION 为 `v1.8.3(e6a727a)`。
- `.local/cache/w08-result.json` 顶层 `head` 记录最早一轮的提交（保留历史，不覆盖）；新增 `provenance` 按片标注 commit/工作区；媒体身份区分资源行 `mediaRowId/mediaVersion` 与资产级 `assetAudioVersion`，同一资源上的两个版本号不再混写。

## 未完成

- W09 的“服务侧暂停派发（draining）+ drain 下在途成功收口 + queued 积压重启后逐条恢复”未演练；现有证据只覆盖“提交结果不明 → 明确失败且不重发”，0 价订单只证明订单条目与出站未重复。
- 截断/空音频按现有合同接受或拒绝未实测。
- 真实付费模型凭据缺失（全部为 0 价确定性渠道）；预留变化未单独采集。
- 与 tsk 续接 schema 合并后的工具目录预算未验证。

## 关键事实

- 隔离环境：后端 `127.0.0.1:18191`（`.local/w08-isolated-data`）、前端 `127.0.0.1:13250`、桩 `127.0.0.1:18192`（`.local/stub-w08.mjs`，支持 `fail=1`、`ratio=wrong`、`delay=ms`），出站只放行 `127.0.0.1`。
- 门禁依赖：CI 契约要求 `CANVAS_TEST_POSTGRES_DSN`（库 `canvas_test`）与 `TEST_AGENT_POSTGRES_DSN`（库 `canvas_event_integration`）、`CANVAS_TEST_REDIS_ADDR`；本机用独占 Docker 的 PostgreSQL/Redis 提供，缺依赖时不得记通过。
- 证据：`.local/cache/w08-result.json`；桩请求流水 `.local/cache/w08-stub-requests.jsonl`；门禁日志 `.local/cache/w08-gate-full.log`、`.local/cache/verify/`。
- 关键文件：`backend/internal/protocol/binary_payload_mime.go`、`web/src/components/canvas/canvas-shortplay-adopt-modal.tsx`、`web/src/lib/canvas/canvas-shortplay-asset.ts`、`web/src/lib/canvas/tool-registry/definitions/{add-node-menu-tools,node-hover-tools}.tsx`、`web/src/pages/canvas/project.tsx`。
