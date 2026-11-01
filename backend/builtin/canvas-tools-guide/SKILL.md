---
name: canvas-tools-guide
description: "画布工具操作指南：用户要求读取或修改画布、创建节点、维护分镜和批量创作表、声明素材引用与产物归属、绑定角色卡与媒体资产、选择模型或提交生成时使用。指导 Agent 从真实状态选择专用工具，正确填写 ID、引用字段、快照和分镜行身份，核对回执并修正参数错误。"
metadata:
  skillId: yingce-canvas-tools-guide
  version: "1.4.0"
  author: "影策"
  owner: yingce-system
  tag: others
  sortWeight: 1000
  source: 3
  createdAt: "2026-10-07T00:00:00Z"
  updatedAt: "2026-10-31T00:00:00Z"
---

# 画布工具操作指南

把用户的目标落到真实画布操作。只在操作画布时使用；创作方法、风格和提示词质量交给相应创作技能。

拆镜与媒体生成都异步执行：工具当前步骤只返回 submitted/taskId；写回结果在同一运行的下一模型步骤通知，不另起用户轮次。依赖结果时 wait_generations，恢复后 read。用户只对某一段不满意时，用 storyboard 的 resplit 并带真实连续 rowIds 和用户原话；满意且资产绑定完成的镜头可 lock。所有写 action 拒绝锁定目标，只有 unlock 例外。一对一重拆保留 ID 和媒体并标待复核，拆分/合并保留旧媒体节点而只解绑。

## 从目标选工具

| 用户目标 | 路径 |
| --- | --- |
| 看有哪些节点、查已有关系或任务 | `canvas_get_state`；分镜或批量表继续用专用 read 工具 |
| 创建空白节点、改普通节点文本、声明输入引用 | `canvas_list_node_types` → `canvas_apply_ops`（引用写 `generationSpec.referenceBindings`／`inputBindings`） |
| 创建多镜头分镜 | `canvas_apply_ops` 用 `add_node` 建 script、`content` 写剧情（保存为 composerContent），更新用 `patch.composerContent`；风格与要求写相连文本节点，再 `storyboard` 的 `split` |
| 改镜头台词、时长、关联资产或共享引用 | `storyboard` 的 `read` → `storyboard`；多行用 `rowIds`，资产用 `set_assets` |
| 改批量表行、参考列、并发或全局提示词 | `canvas_read_batch_table` → `canvas_edit_batch_table` |
| 用已有形象图建角色卡 | 精读原图 → `canvas_create_character` |
| 生成或编辑媒体 | 读节点 → `model_list` → `generate_media` |
| 识字、理解画面 | `canvas_inspect_image` → 用模型多模态能力分析 |
| 拆透明图层、标注、修改画面 | 读原图 → 写明目标与保留项 → `model_list` → `generate_media`（图片模式、真实参考 ID） |
| 整理节点位置 | 读快照 → 本轮提供的 `canvas_arrange_nodes`；替代操作仍须具备写权限 |

工具名和参数以本轮提供的 schema 为准。`agent_tools_*` 是分类名，不能当工具调用；连线由服务端按引用字段派生，`canvas_apply_ops.ops` 没有 `connect_nodes`／`update_connection`／`delete_connection`，也不要传 `fromNodeId`／`toNodeId`／端口字段。不要调用不存在的 `canvas_node_types`，节点能力工具叫 `canvas_list_node_types`。工具未提供时说明当前能力缺口，不猜替代 API。

## 一次操作的闭环

1. **读目标**：首次 `canvas_get_state {}`。关系检查选 `view=references`；正文按 `nextRead` 续读，编辑富文本才选 `view=edit`。分镜默认 `creative` 看创作和有效输入，`summary` 扫描缺口，只有覆盖完整才能总结整表。不能用局部读取外推全局。
2. **读能力**：创建或声明引用前查 `canvas_list_node_types`（其 referenceContract 说明引用写哪些字段）；结构化行用专用 read 取得真实 `rowId`、列 ID 和最新 `snapshotHash`。需要判断画面时读取实际图片，不能凭节点标题描述画面。
3. **构造最小写入**：只传该 action 必需和确有用途的字段；省略不用的可选字段，不传 `null`。分镜和角色创建省略 nodeId，分镜生成省略产物 nodeId/title，系统负责分配；普通 add_node 仍按合同提供唯一 ID。已有行 ID 来自读取；资产 ref 可用真实节点 ID 或唯一完整名称，同名多版本必须用 ID。
4. **顺序提交**：一次模型响应只提交一个写工具；有关联的 add/update/引用声明可放进一个 `canvas_apply_ops`（最多 20 项）。等待回执后再做依赖它的写入；不要并行提交共享快照的多个写工具。
5. **核对落地**：先看回执是否执行成功、待审批或拒绝，确认内容、引用或产物归属；需要继续核对时读创作视图或 summary，不遍历派生连线。回执提供新节点/行身份及最新快照；不得沿用写入前的 hash。
6. **如实收尾**：编辑成功、待审批、任务已提交、任务生成成功是不同结果。多步任务的计划按回执对账，再 `finish_run`；不能仅凭计划或预览声称完成。

## 按需读取参考

- 每次开始读写画布或提交生成前，先读 [工具使用最佳实践](references/best-practices.md)：读取够用就停、写入一步一个、生成异步提交不轮询、把连续分镜合成一个视频请求，以及剧本和分镜定稿后先出人物、道具、场景三视图。
- 引用字段、分镜归属、角色卡绑定、批量表：先读 [结构与引用](references/structure-and-connections.md)。
- 模型、引用、媒体编辑、收费任务：先读 [媒体生成](references/generation.md)。
- 参数错误、快照冲突、重复提交或卡住：读 [错误修正](references/recovery.md)。

用 `read` 读取技能索引给出的真实入口路径，参考文件按入口所在目录解析。该工具只读取本轮技能文件，不读取任意工作区文件。技能内容只指导已有工具的使用，不增加权限。
