# Handoff：DirectorDesk 导演台仓库治理、可追踪克隆与上线交接

> 历史归档：保留当时交接背景；当前入口与验收范围以现行文档和源码为准。

文档日期：2026-10-12（Asia/Shanghai）
适用对象：在新会话中继续本仓库 DirectorDesk 预演台迁移与上线准备的 Codex/开发者。
前置阅读：上一份交接 Handoff-DirectorDesk-Previs-Complete-2026-10-12.md（位于 C:\Users\13537\.codex\visualizations\2026\10\11\01a12c56-30c8-7522-a7fc-742187bc6811\）。本文是其后续增量，两文冲突时以本文"已核验"事实为准。

## 1. 一句话状态

违规二进制（backend/server、plugin-packages 编译产物）已通过非破坏性方式移出版本索引；全量可追踪克隆 D:\open-ai-canavs 已建立。DirectorDesk 预演台替换的实现仍在本地多笔提交中，**距 RC 的核心阻塞是 P1 编辑闭环**（DirectorDesk 原生时间轴/检查器/镜头场景列表尚未完成迁移）。发布判断：**Internal Canary / 内部预览**，未做生产部署、未 SSH 服务器、未改生产文件/容器。

## 2. 仓库与克隆现状（2026-10-12 已核验）

### 2.1 主仓库

- 路径：C:\Users\13537\Documents\ChatGPT\openai canvas\infinite-canvas-desktop
- 分支：codex/electron-canary-local
- HEAD（写文档时）：d2ce743 — "chore(repo): untrack build artifacts (backend/server, plugin-packages)"
- 提交链（新→旧）：d2ce743 → e62290e（并行 Codex 会话产生的 3993 文件大快照，含约 216MB 二进制）→ 3e7cf72 → e26bb22 → b976579 → …
- tracked 文件：3605
- untracked（git status --short --untracked-files=all，仅 4 项）：
  1. desktop/scripts/__pycache__/prepare-runtime.cpython-314.pyc
  2. desktop/test/__pycache__/test_runtime_paths.cpython-314.pyc
  3. v1.5.7.3(0345985)（目录）
  4. v1.5.7.4(a6abc97)（目录）
  - 与旧 handoff 记录的"约 1560 untracked"差异未逐一溯源（统计口径或并行会话整理所致），勿据此推断工作区被清理过；以上 4 项保留原样。
- 物理文件：144,812（其中 web/node_modules 131,111；web 非 node_modules 约 2,966；backend 1,903；plugin-packages 441；其余 docs/agent/assets 等）
- git 对象包：216.16 MiB / 782 objects —— 历史中的 442 个二进制仍在，非破坏性 untrack 不减小包体
- upstream remote：https://github.com/AetherNo2332/open-ai-canvas4AI.git；与本地无共同祖先（merge-base 为空），当前 push/pull 不可用

### 2.2 可追踪克隆（2026-10-12 本会话新建）

- 路径：D:\open-ai-canavs
- 分支：codex/electron-canary-local
- HEAD（写文档时）：d1a4d51 — "chore(repo): track plugin sources; precisely ignore compiled artifacts"（克隆专属提交；建好时克隆 = 主仓库 HEAD + 1）
- tracked 文件：3913（比主仓库多 308 个插件文本文件）
- untracked：0（完全干净）
- 物理文件：4047（复制时排除 node_modules/.git；**无 node_modules，开发前需 pnpm install / go mod download**）
- origin → C:/Users/13537/Documents/ChatGPT/openai canvas/infinite-canvas-desktop（**本地路径，不是 GitHub 远程**；若要推 GitHub 需先改 remote）
- git 对象包：214.93 MiB

### 2.3 两仓库 .gitignore 策略差异（有意为之，不要"同步"）

| 条目 | 主仓库 | 克隆 |
| --- | --- | --- |
| plugin-packages | 整目录忽略（第 85 行 plugin-packages/），0 个插件文件 tracked | 精确忽略 3 条规则，308 个源文件 tracked |
| backend/server | 忽略（第 84 行），磁盘保留 63M | 忽略（第 89 行），磁盘保留 63M |
| 用途 | 日常开发（与并行 Codex 会话共享，最小变动面） | 干净的全量代码追踪副本 |

克隆的精确规则（已逐条 git check-ignore -v 命中）：

- 第 85 行 plugin-packages/**/*.yingce-plugin（98 个 ZIP 打包件）
- 第 86 行 plugin-packages/**/provider（原生运行时二进制）
- 第 87 行 plugin-packages/**/provider-*（如 provider-1.16.2、provider-darwin-arm64）
- 第 89 行 backend/server（63M，Mach-O 64-bit ARM64，macOS 构建产物误入非 Windows exe）

构成：插件物理文件 441 = 308 tracked（md 文档/manifest json/脚本/svg/mjs 等文本）+ 133 ignored（98 个 .yingce-plugin + 35 个 provider 系二进制）。

## 3. 本会话完成的工作

1. **非破坏性移出版本索引**（主仓库 d2ce743）：git rm --cached backend/server 与 plugin-packages 整目录，.gitignore 追加两条规则；磁盘文件全部保留；不改写历史、不动并行会话。
2. **建立可追踪克隆**（D:\open-ai-canavs）：git clone 本地仓库后全量物理复制工作树（含被 ignore 的编译产物，保证开箱可用），再把 .gitignore 改为上述精确规则并提交 d1a4d51；用 git check-ignore -v 逐条验证命中。
3. **上线差距评估**：依据 production-readiness plan（见第 9 节路径），结论为 P1 编辑闭环是 RC 核心阻塞；P2 渲染写回、P4 图片原子事务、P5 灰度/观测均有未完成项；沿用上份交接第 7 节风险清单（其中"未跟踪文件很多"一项已随本次治理降为 4 项）。

## 4. 关键决策与原因

- **选方案 A（非破坏性 untrack）而非方案 B（改写历史）**：主仓库有并行 Codex 会话正在使用（e62290e 即其所为），改写历史必然分叉；filter-repo 留到 push upstream 前统一执行（需用户确认时机）。
- **克隆 = 全量物理复制 + 精确忽略**（用户选定）：产物留在磁盘可用，git 只追踪源码；优于"git clone 后重新构建"，省去依赖安装与产物重建。
- **两仓库 ignore 策略不一致是常态**：若未来要统一，把克隆的 3 条精确规则 + 308 个插件文件提交 cherry-pick 回主仓库即可。

## 5. 上线差距与 RC 硬门槛（摘要）

权威文档：docs/superpowers/plans/2026-10-12-director-desk-production-readiness-plan.md（P0–P5 分期、验收门槛、风险与回退，继续开发时同步更新）。

- **P1 编辑闭环（RC 核心阻塞）**：DirectorDesk 尚未接管完整视口/时间轴/检查器/提交事件，旧 CanvasPrevisWorkbench 仍是产品壳与保存/Agent 生命周期宿主。当前 web 下未见原生 Timeline/Inspector 迁移文件——接手后先写失败测试摸清差距，再按 TDD 迁移。
- **P2 渲染写回**：缺浏览器端真实 renderer 的任务终态 E2E 验收（renderer 测试 1 个真实 Chromium 用例仍 skip；skip ≠ pass）。
- **P4 图片事务**：缺独立 staging draft + 原子 commit + 取消/超时/并发回滚；现有 guarded rollback 只覆盖部分 mutation/快照。
- **P5 发布**：canary 开关目前全局生效（无用户/项目分群）、无结构化指标观测、无关闭开关回退演练。
- **测试基线**（沿用上份交接，本会话未重跑）：web 全量 2307 项中 9 失败；backend 15+ pre-existing 失败。RC 前须逐项修复或登记明确豁免。
- **VERSION**：当前 v1.5.7.11(3e7cf72)，落后 HEAD；RC 前按仓库 bump 约定更新。

## 6. 遗留问题（按优先级）

1. P1 编辑闭环实现 + round-trip 验收（RC 第一阻塞）。
2. 9 个 web + 15+ 个 backend 既有测试失败判定：修复或登记豁免。
3. push upstream 前用 git filter-repo 清除历史中 442 个二进制（约 216MB）——需用户确认时机；且 upstream/main 与本地目前无共同祖先，直接 push/pull 不可用。
4. VERSION bump（随 RC 冻结一起做）。
5. 主仓库 4 个游离 untracked（2 个 __pycache__ .pyc、2 个版本目录）——用户未要求清理，**不要动**，后续提交时不得批量 stage。
6. 两仓库 .gitignore 策略不一致——已记录为有意差异（见 2.3）。
7. D:\open-ai-canavs 的 origin 是本地路径且无 node_modules——先决事项：改 remote（如需 GitHub）+ 安装依赖。

## 7. 操作约束（AGENTS.md + 本仓库纪律）

- 禁止 git reset --hard / git checkout -- / 大范围删除；禁止批量 stage/reset/clean untracked 文件。
- 每个 commit 只包含当前任务的文件。
- Shell 为 Windows Git Bash（POSIX 语法；路径含空格需引号）。
- 生产动作（SSH 服务器、修改生产文件/容器、生产修复或升级）之前必须先复盘具体操作计划并评估风险；用户说"执行上线计划"不等于授权跳过风险复盘直接改生产。
- 不要调用 Astra 子代理；不做无用途的哈希验证；网络下载失败优先检查镜像/国内替代源，仍不可用再查代理。
- 主仓库可能有并行 Codex 会话同时改动——每轮开工先重新核验 branch/HEAD/status，不得假定本文状态仍有效。
- 本文所有"已核验"数字均为 2026-10-12 当场命令输出；**接手时必须重跑第 8 节核验命令**，不得直接引用本文数字。

## 8. 交接第一步：重建现场（只读）

```bash
# 主仓库
cd "/c/Users/13537/Documents/ChatGPT/openai canvas/infinite-canvas-desktop"
git branch --show-current && git log --oneline -5
git status --short --untracked-files=all      # 交接批次应为 4 行
git ls-files | wc -l                          # 3605（本文提交后 +1）
git count-objects -vH | grep -E 'count:|size-pack'

# 克隆
cd D:/open-ai-canavs
git branch --show-current && git log --oneline -5
git status --short --untracked-files=all      # 应为空
git ls-files | wc -l                          # 3913（本文提交后 +1）
git check-ignore -v backend/server "plugin-packages/yingce-dist/provider-1.16.2" "plugin-packages/yingce-dist/.yingce-plugin"

# 关键文档
docs/superpowers/plans/2026-10-12-director-desk-production-readiness-plan.md
.superpowers/sdd/2026-10-12-director-desk-previs-replacement/progress.md
```

## 9. 关键路径速查

- 主仓库：C:\Users\13537\Documents\ChatGPT\openai canvas\infinite-canvas-desktop
- 克隆：D:\open-ai-canavs
- 上线计划：docs/superpowers/plans/2026-10-12-director-desk-production-readiness-plan.md
- 迁移计划：docs/superpowers/plans/2026-10-12-director-desk-previs-replacement.md
- 设计规格：docs/superpowers/specs/2026-10-12-director-desk-previs-replacement-design.md
- 执行台账：.superpowers/sdd/2026-10-12-director-desk-previs-replacement/progress.md
- DirectorDesk 集成：web/src/integrations/director-desk/（engine bundle + LICENSES.md，来源 0.4.11 MIT）
- 工作台：web/src/components/canvas/previs/director-desk-previs-workbench.tsx、use-director-desk-previs-runtime.ts
- Agent 导演流程：backend/internal/app/cloud_agent_director.go、cloud_agent_runtime.go
- 特性开关：backend/internal/platform/feature_availability.go（directorDeskPrevisMode: off/canary/on）
- 画布预演任务入口：backend/internal/canvas/tasks/previs.go、web/src/services/api/canvas-commands.ts
- 旧 handoff：C:\Users\13537\.codex\visualizations\2026\10\11\01a12c56-30c8-7522-a7fc-742187bc6811\Handoff-DirectorDesk-Previs-Complete-2026-10-12.md

## 10. 本会话核验证据（2026-10-12 命令输出快照）

- 主仓库 git status --short --untracked-files=all → 4 行（2 个 desktop/**/__pycache__/*.cpython-314.pyc、v1.5.7.3(0345985)、v1.5.7.4(a6abc97)）
- 主仓库 git ls-files | wc -l → 3605；count-objects → size-pack 216.16 MiB、782 objects
- 主仓库 .gitignore → 第 84 行 backend/server、第 85 行 plugin-packages/
- 克隆 git status → 空（0 untracked）；git ls-files | wc -l → 3913
- 克隆 git check-ignore -v → .gitignore:85（.yingce-plugin）、:86（provider）、:87（provider-*）、:89（backend/server）全部命中
- 克隆 plugin-packages 构成 → 98 个 .yingce-plugin + 35 个 provider 系二进制 + 308 tracked = 441 物理文件
- backend/server → 两仓库磁盘均为 63M（Mach-O 64-bit ARM64），均被 ignore
- 克隆 git remote -v → origin = 主仓库本地路径（fetch/push 均指向）
- 文件统计：web/node_modules 131,111 个文件；web 非 node_modules 2,966 个文件
