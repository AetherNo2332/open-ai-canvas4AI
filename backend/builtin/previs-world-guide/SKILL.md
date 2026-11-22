---
name: previs-world-guide
description: "预演台世界引擎操作指南：用户要求在真实画布世界引擎里搭景、调整天气、摆放演员和道具、标记 A/B 点、让演员移动、画或复用路径、创建编辑删除运镜段、用时间轴修改并保存回读时使用。指导 Agent 先读真实场景，再通过 director 的 read、create_scene、apply_patch、preview 操作；路径与运镜正文按主题读取。"
metadata:
  skillId: yingce-previs-world-guide
  version: "1.1.0"
  author: "影策"
  owner: yingce-system
  tag: others
  sortWeight: 900
  source: 3
  createdAt: "2026-11-22T00:00:00Z"
  updatedAt: "2026-11-23T12:00:00Z"
---

# 预演台世界引擎

把用户目标落到画布里的真实预演场景。只在操作世界引擎时使用；创作方法交给相应创作技能。

## 何时读取主题

先完成一次 `director` 的 `read`。需要精确坐标时用场景或节点 ID 读取；返回的 `snapshotHash` 是下一次 `apply_patch` 的 `expectedSceneHash`。

| 用户目标 | 必读主题 |
| --- | --- |
| 新建场景、搭街景/园林/室内、天气、时间、程序化道具、阵列、拆单栋建筑 | [setup.md](references/setup.md) |
| 摆演员、角色卡、朝向、站位、姿态、演员脚底高度 | [actors.md](references/actors.md) |
| 从 A 到 B、标记点、走跑跳飞、沿路线移动 | [move.md](references/move.md) |
| 找用户画的参考轨迹、按标签复用、让演员沿摄影机轨迹走 | [paths.md](references/paths.md) |
| 摄影机、景别、机位高度、构图 | [camera.md](references/camera.md) |
| 推拉摇移、环绕、跟拍、手持、震动等运镜段 | [moves.md](references/moves.md) |
| 多节拍设计、镜头检查 review、跟拍景别与过渡 | [shots.md](references/shots.md) |
| 删除对象/路径/运镜、重画路径、时间轴编辑、保留原开始时间 | [edit.md](references/edit.md) |
| 保存后回读、自检、预览、交付视频 | [output.md](references/output.md) |
| 文戏完整范例、双人对话与多场景组合 | [recipes.md](references/recipes.md) |
| 动作戏完整范例、追逐、打斗与御剑 | [recipes_action.md](references/recipes_action.md) |
| 大场面完整范例、航拍揭示与群戏 | [recipes_epic.md](references/recipes_epic.md) |

行为规范是每次写入前都应读的入口：[rules.md](references/rules.md)。

## 工具边界

- 世界场景只有一个原生工具 `director`。`read` 返回安全的场景投影；`create_scene` 新建；`apply_patch` 写入；`preview` 请求浏览器录制白模视频。
- 说明书正文只通过本轮启用的原生 `read` 读取本技能文件。它不是世界场景工具，也不会增加权限。
- 先读后用：`director read` → 构造一项完整操作 → `director apply_patch` → 回读核对。审批与只读权限以平台回执为准。

## 场景里的真实身份

`read` 返回的 `scene.paths`、`objects[].motionPath`、`cameras[].motionPath`、`cameras[].moves`、`markers` 都带真实 ID 或标签。复用路径时用 `pathId`；删除运镜时用 `cameras[].moves[].id`；不要凭画面描述生成一组平行数据。

演员的 `transform.position[1]` 是脚底离地高度：站在地面为 0，站在 0.8 米台阶上为 0.8。路径点的 Y 是路径相对演员脚底的离地高度，写入关键帧的 `lift`。复用摄影机轨迹给演员时，路径高度可归零，但演员自身脚底高度必须保留。
