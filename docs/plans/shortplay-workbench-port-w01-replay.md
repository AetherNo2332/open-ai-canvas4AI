# W01 两集样本的供体回放记录

本文件只登记 W01 的**可重放证据**，不改动供体清点正文（见 [W01 供体行为清点](shortplay-workbench-port-w01.md)）。供体固定提交 `13731aae6c4cefadc2a1719e3e7e59b22e249f94`，本机检出 `.local/donor-shortplay`（detached，回放后 `git status --porcelain` 为空）。

## 产物

| 文件 | 作用 |
|---|---|
| `testdata/shortplay-workbench-port/replay/two-episodes.json` | 两集样本：两集共用同一角色（资产 1），第二集复用该角色并新增衍生造型（资产 4，`derivedFrom=1`、`variantName="战袍"`），道具只在第一集、场景两集共用 |
| `scripts/shortplay-port/donor_workbench_fixture_test.go.txt` | 供体侧回放测试源（临时拷入供体 `backend/internal/shortplay` 后运行，运行完删除，保持供体干净） |

`testdata/shortplay-workbench-port/sample/` 与 `donor/inventory.json` 由 `scripts/shortplay-port/capture_workbench_donor.py` 采集器产出，是另一条证据链（清点 + 结构校验）；`replay/` 这条链的差别是**用供体自身实现执行断言**，不是脚本复述规则。

## 复现命令

```powershell
$t = "C:\Users\13537\.codex\worktrees\shortplay-canvas-port\open-ai-canvas4ai-main"
$donor = "$t\.local\donor-shortplay"

Copy-Item "$t\scripts\shortplay-port\donor_workbench_fixture_test.go.txt" `
  "$donor\backend\internal\shortplay\w01_fixture_replay_test.go" -Force
$env:SHORTPLAY_W01_FIXTURE = "$t\testdata\shortplay-workbench-port\replay\two-episodes.json"
Set-Location "$donor\backend"
go test -count=1 -run TestW01TwoEpisodeFixtureReplay -v ./internal/shortplay
Remove-Item "$donor\backend\internal\shortplay\w01_fixture_replay_test.go" -Force
```

## 实跑结果（2026-11-26）

```
=== RUN   TestW01TwoEpisodeFixtureReplay
--- PASS: TestW01TwoEpisodeFixtureReplay (0.00s)
PASS
ok  	infinite-canvas/backend/internal/shortplay	0.554s
```

回放后供体工作区 `git status --porcelain` 为空，供体仍停在固定提交。

## 断言覆盖（全部走供体函数与常量，不是复制规则）

| 面 | 供体符号 | 断言 |
|---|---|---|
| 显示名与回落 | `ResolveShortplayAsset`、`ShortplayDerivedDisplayName` | 名称 `林小满-战袍`、kind/别名/description/voice 回落父资产、appearance 用自身值 |
| 形象名预算与拒绝 | `ValidateShortplayVariantName`、`ShortplayDerivedDisplayNameRunes` | 预算 20−3−1=16；空值、含 `-`、与角色名同名、与别名同名四条均拒绝 |
| 造型解析 | `ResolveShortplayLook` | 完整显示名与 `variantName`（含空格归一）命中资产 4；基础名、别名、`默认` 词表与未知造型都回落 0 |
| 两集绑定 | 夹具断言 | 资产 1 同时绑定两集；衍生 4 只在第二集；道具 2 只在第一集；场景 3 两集共用 |
| 画布卡集合 | 夹具断言 | 每集卡集合与 `assetIds` 顺序一致，初始画布只有一个故事输入节点 |
| 图片/音色继承 | 夹具断言 + 供体模型注释 | 衍生图 `sourceImageId` 指向父资产当前图；`currentAudioId=0` 即继承父样本 |
| 提取批次 | `NextEpisodeBatch`、`ExtractRunes` | 默认 40000 字预算一批读完两集；小预算只读一集并返回 `next=2` |
| job 去重与优先级 | `ShortplayJobDedupeKey`、四个 Priority 常量 | 四种拼法与夹具模板一致；60/40/40/20 |

## 证据级别

- **实跑**：上面的 `go test` 输出与供体干净状态。
- **源码观察**：13 接口、4 job 种类、4 Agent 工具、6 表、5 个迁移号仍以清点文档为准，本文件不重复登记。
- **待验**：真实模型渠道出图/出音、真实 PostgreSQL 升级、正式页面全流程、Agent 审批链路；W02 起由目标代码回放迁移与仓储。

## 边界

- 样本是 `sanitized-synthetic`：按供体类型与规则构造，不是生产档案，不含真实账号、资源或付费回执。
- 回放只证明「供体纯函数/常量与样本一致」，不证明目标端已实现；目标端回放从 W02 的迁移与仓储测试开始。
- 夹具里第二集绑定 `[1,3,4]`（含基础角色 1）：集绑定来自提取的身份行，衍生造型是用户在基础行上新增的造型，靠画布卡的 `shortplayAssetLook` 选中，不替代基础行的集绑定。
