# 短剧工作台 W08/W09 验收交接（音频格式、收录页面、第二账号、重启恢复）

## 目标

在恢复树 `codex/shortplay-w04-recovery` 上把 W08 的确定性渠道全流程与 W09 的隔离恢复做实：音频按请求格式出站并落库、收录走正式页面、第二账号真实越权、后端重启后不重复扣费与出站，最终对干净 HEAD 跑完整门禁后更新同一 PR154。

## 本轮已完成

1. **音频格式缺陷修复**（产品缺陷，非测试桩问题）：官方 `openai-audio` 是二进制响应，`binaryPayloadCreateResult` 只用 `http.DetectContentType` 认类型；MP3 没有标准库可识别的魔数 → `application/octet-stream` → 媒体 checkpoint 按 `audio/` 前缀拒绝，任务报“作品已生成，但保存未完成：记录恢复信息失败”。WAV 之所以能过，是因为 RIFF 被嗅探成 `audio/wave`。
   - 修复：`detectBinaryPayloadMime` 先嗅探，识别不出音频且 `resultKind=audio` 时按 ID3 / MPEG 帧同步认定 `audio/mpeg`（与音频结果校验同一判定）；未知字节仍保持 generic。
   - 回归：`TestOfficialOpenAIAudioUsesBinaryPayload`（ID3）、`...FrameSyncIsMP3`、`...KeepsUnknownBytesGeneric`、`TestMediaRecoveryStagesInlineAudioDataURL`。
2. **W08 音频真实链路**：出站 `response_format=mp3` → 桩按格式返回真实 MP3（帧头 `ff fb 90 c0`，15846B）→ 资源 `audio/mpeg`、`audioVersion=2`、页面播放器 `readyState=4`、`duration=0.990375`、无 `mediaError`、可播放。
3. **W08 负例**：上游 500（跨 4 个 tick / ~20s）只产生 1 次出站、1 个 job/task/order，旧图旧音 ID 与版本不变；错比例图（1×1）任务成功但回写拒绝，job 明确报错，旧图 v2 保留。
4. **W06 收录（正式页面）**：此前前端只有 `adoptShortplayAsset` API 包装，没有任何入口。本轮补齐：添加节点菜单“短剧角色卡/道具卡/场景卡”、节点工具条“收录到短剧”、收录弹窗（选作品 + 目标集）。实测：自由画布局部卡《雨巷客栈·夜》（scene）→ 资产 #5、绑定第 2 集、目标集画布出现“项目资产”新卡，源卡保持局部卡不变。
5. **画布渲染缺陷**：`canvasNodeUsesCustomContent` 未登记 `shortplay-asset`，资产卡（含投送进集画布的）在画布上渲染成“未知节点”。已登记，卡片恢复为海报卡并显示“局部卡 / 项目资产”徽标。
6. **第二账号隔离（真实登录）**：管理员临时开启注册后，第二账号自行注册并登录（register/login 均 200），对首个账号的短剧详情/资产/资源文件/画布/提取/收录全部 404（不是 401 兜底）；随后关闭注册。密码未留存、未写入证据。
7. **W09 重启恢复**：桩 `/audio/speech?delay=20000` 制造在途请求，3 秒后强杀后端并立即重启。同一 job/task 被接续（`attempts=2`），但系统以“上一次提交结果不明确，为避免重复扣费已停止自动重发”收尾：订单仍 1 个、重启后无新增上游调用、旧音频 `3d48acc8` v2 保留、`audioStatus=failed`。

## 未完成

- W09 的“服务侧暂停派发（draining）”与在途回调结算未单独演练。
- 截断/空音频是否被拒未按现有合同实测。
- 真实付费模型凭据缺失（全部为 0 价确定性渠道）；预留变化未单独采集。
- 与 tsk 续接 schema 合并后的工具目录预算未验证。

## 关键事实

- 隔离环境：后端 `127.0.0.1:18191`（`.local/w08-isolated-data`）、前端 `127.0.0.1:13250`、桩 `127.0.0.1:18192`（`.local/stub-w08.mjs`，支持 `fail=1`、`ratio=wrong`、`delay=ms`），出站只放行 `127.0.0.1`。
- 证据：`.local/cache/w08-result.json`（含音频 MP3 片、格式观察、500 负例、错比例负例、收录、第二账号、W09）；桩请求流水 `.local/cache/w08-stub-requests.jsonl`。
- 关键文件：`backend/internal/protocol/manifest_adapter.go`、`web/src/components/canvas/canvas-node-content.tsx`、`web/src/components/canvas/canvas-shortplay-adopt-modal.tsx`、`web/src/lib/canvas/tool-registry/definitions/{add-node-menu-tools,node-hover-tools}.tsx`、`web/src/pages/canvas/use-canvas-node-operations.ts`。
- 门禁：`bun scripts/verify-before-push.mjs --base origin/codex/shortplay-workbench-port --mode full`（本机需 `GOTOOLCHAIN=go1.25.0`、`CHROME_BIN` 指向本机 Chrome；`docs:types` 需容器等价）。
