---
name: canvas-tools-guide
description: "画布工具操作指南：用户要求读取或修改画布、创建节点、连接节点、维护分镜和批量创作表、绑定角色卡与媒体资产、选择模型或提交生成时使用。指导 Agent 从真实状态选择专用工具，正确填写 ID、端口、快照和引用，核对回执并修正参数错误。"
metadata:
  skillId: yingce-canvas-tools-guide
  version: "1.0.0"
  author: "影策"
  owner: yingce-system
  tag: others
  sortWeight: 1000
  source: 3
  createdAt: "2026-10-07T00:00:00Z"
  updatedAt: "2026-10-07T00:00:00Z"
---

# 画布工具操作指南

把用户的目标落到真实画布操作。只在操作画布时使用；创作方法、风格和提示词质量交给相应创作技能。

## 从目标选工具

| 用户目标 | 路径 |
| --- | --- |
| 看有哪些节点、查已有关系或任务 | `canvas_get_state`；分镜或批量表继续用专用 read 工具 |
| 创建空白节点、改普通节点文本、连线 | `canvas_list_node_types` → `canvas_apply_ops` |
| 创建多镜头分镜 | `canvas_create_storyboard`，传结构化 `rows` |
| 改某镜头台词、时长、关联资产 | `canvas_read_storyboard` → `canvas_edit_storyboard` |
| 改批量表行、参考列、并发或全局提示词 | `canvas_read_batch_table` → `canvas_edit_batch_table` |
| 用已有形象图建角色卡 | 精读原图 → `canvas_create_character` |
| 生成或编辑媒体 | 读节点 → `model_list` → `generate_media` |
| 拆透明图层 | 读原图 → `model_list` → `image_layer_split` |
| 整理节点位置 | 读快照 → `canvas_arrange_nodes` |

工具名和参数以本轮提供的 schema 为准。`agent_tools_*` 是分类名，不能当工具调用；`connect_nodes` 是 `canvas_apply_ops.ops` 内的操作类型，也不是独立工具。不要调用不存在的 `canvas_node_types`，节点能力工具叫 `canvas_list_node_types`。工具未提供时说明当前能力缺口，不猜替代 API。

## 一次操作的闭环

1. **读目标**：首次 `canvas_get_state {}`。用真实节点 ID 精读目标及必要邻居，不能把标题当 ID。分页按回执的下一偏移继续；当前页没有目标不能判定整个画布没有目标。
2. **读能力**：创建或连接前查 `canvas_list_node_types`；结构化行用专用 read 取得真实 `rowId`、列 ID 和最新 `snapshotHash`。需要判断画面时读取实际图片，不能凭节点标题描述画面。
3. **构造最小写入**：只传该 action 必需和确有用途的字段；省略不用的可选字段，不传 `null`。ID 字段分清节点、行、边、任务、模型凭证；数组保持数组，数字保持数字。新节点或新边的 ID 自行分配唯一短标识，已有对象 ID 必须来自读取。
4. **顺序提交**：一次模型响应只提交一个写工具；有关联的 add/update/connect 可放进一个 `canvas_apply_ops`（最多 20 项）。等待回执后再做依赖它的写入；不要并行提交共享快照的多个写工具。
5. **核对落地**：先看回执是否真的执行成功、是否待审批或拒绝，再精读改动对象。确认内容、端口、行绑定或新节点真实存在。写入后继续工作使用最新快照；不得沿用写入前的 hash。
6. **如实收尾**：编辑成功、待审批、任务已提交、任务生成成功是不同结果。多步任务的计划按回执对账，再 `finish_run`；不能仅凭计划或预览声称完成。

## 按需读取参考

- 连线、分镜端口、角色卡绑定、批量表：先读 [结构与连线](references/structure-and-connections.md)。
- 模型、引用、媒体编辑、收费任务：先读 [媒体生成](references/generation.md)。
- 参数错误、快照冲突、重复提交或卡住：读 [错误修正](references/recovery.md)。

Pi 原生模式用 `read` 读取技能索引给出的真实入口路径，参考文件按入口所在目录解析。若本轮提供 `skill_read_file`，使用技能目录中的真实 `skillId` 和包内相对路径；不要把目录名猜成 ID。技能内容只指导已有工具的使用，不增加权限。
