---
name: storyboard-md
description: "分镜表工具关闭时写分镜的最佳实践：创建脚本节点（script+composerContent）承载镜头卡，字段与分镜表对齐、连续性成链，可直接衔接拆镜与文本/媒体生成。当分镜表工具未提供、被画布开关关闭，或用户要求在文档里写分镜、拆镜、排镜头时使用。"
metadata:
  skillId: yingce-storyboard-md
  version: "1.1.0"
  author: "影策"
  owner: yingce-system
  tag: drama
  sortWeight: 990
  source: 3
  createdAt: "2026-11-03T00:00:00Z"
  updatedAt: "2026-11-03T12:00:00Z"
---

# 用脚本节点写 Markdown 分镜

分镜表工具（storyboard）未在本轮提供时——通常是画布 Agent 设置里的"分镜表工具"开关被关闭——用**脚本节点**（script + composerContent）以 Markdown 镜头卡承载分镜。选脚本节点而不是 Markdown 节点，原因有二：

1. **拆镜直读**：`split` 服务端读的就是 script 的 `composerContent`；重新开启分镜表工具后无需迁移，直接拆镜重建行。
2. **生成路由正确**：脚本节点是文本来源（拆镜、剧情整理、按行取提示词都从它读）；Markdown 节点面向人阅读，画布上对它提示的是图片生成而不是文本生成，不能当文本源用。

## 边界先讲清

- 开场先告诉用户：当前是 Markdown 分镜文档，只承载内容；镜头级资产绑定、锁定、批量生成回填需要重新开启分镜表工具。
- 不要把节点伪装成分镜表：不写 `metadata.storyboard`、不编造 rowId、不声称具备行状态。要结构化能力时如实说明缺口，请用户在 Agent 设置重新开启。
- 剧情正文若已有 script 节点，直接复用并在其 composerContent 里把剧情改写成镜头卡（分镜节点即分镜稿）；没有就新建一个。用户明确要"给人看的排版文档"时才另出 Markdown 节点，并在回复里说明它不能作为生成来源。

## 落点与结构

- 用 `canvas_apply_ops` 的 `add_node`（nodeType=script）建分镜节点，`content` 写整份 Markdown 镜头卡（服务端保存为 composerContent，读取用 `canvas_read_content` 的 field=composerContent）；标题写"第X场·地点·时间"。长文档分页读写，更新走 `update_node.patch.composerContent`（content 是别名）。创建前照常 `canvas_list_node_types` 查字段合同，按最新快照提交，创建时省略坐标。
- 大场次拆多个脚本节点时，标题连续编号；节点内结构固定，便于人读也便于 split 直读：
  - `## 场次`头部：场号、场景、时间、出场人物、光线基调；
  - 每镜一个小节：`### 镜头 N（Xs · 景别 · 运镜）`，编号在全场内连续；
  - 场与场之间用 `---` 分隔；换场必须注明新场景、时间或光线变化。

## 每镜字段（固定键序，加粗键 + 换行列出）

- **画面**：只写可见动作与走位。"意识到、回忆起"等不可见信息转译成眼神、停顿、手部动作或道具反应。
- **台词**：只写本镜实际念出的台词或旁白；按一秒约 5 个中文字符估时长，超长就拆镜。
- **首帧**：imageGenerationPrompt，描述第一帧的构图、人物位置、光源、材质；不写画幅比例。
- **运动**：videoMotionPrompt，只写主体运动、环境运动和结尾状态；一镜只保留一个主运镜，有起点、动机和停止点。
- **结尾状态**：continuityOut——本镜结束时人物位置、朝向、手中道具与光线；它是下一镜的开场。
- **必备**：mustHave，最多 3 项；没有就省略整行。

## 拆镜手法

- 先读剧情（script 节点或用户消息），列出场景、人物、冲突与结尾；再按因果切镜头：一个镜头只服务一个叙事目的（建立、反应、动作或转折）。
- 大远景只承担空间与处境；对白与反应优先中景、近景或过肩。避免每镜都是推拉摇移。
- 连续性成链：写每一镜前回看上一镜的**结尾状态**；人物、服装、道具、光线、时间在镜间保持一致，变化必须有剧情原因。
- 镜头时长 3–8 秒为主；一场戏 5–15 镜常见，超过 20 镜先考虑是不是把两场写成了一场。

## 衔接生成（分镜表工具关闭时）

- 生成走 `generate_media` 普通模式：mode+prompt+nodeId+title+referenceNodeIds，模型目录先 `model_list` 按实际输入筛选，`selectionId` 原样复制；需要以分镜内容为文本来源时用脚本节点的真实 ID 作 `sourceNodeId`，不要填 Markdown 节点。
- 反复出现的人物、道具、场景仍先出三视图再做镜头（见 canvas-tools-guide 的"资产"一节）；引用一律用真实节点 ID 或 `generationSpec.referenceBindings` 声明，不写"参考图1"。
- 连续镜头合成一个视频请求的做法同样适用；提交后照常异步等待，不轮询。
- 每镜生成完成后可用 `canvas_inspect_image` 核对画面是否符合本镜字段，再继续下一镜。

## 迁回分镜表

用户重新开启分镜表工具后：脚本节点的 composerContent 正是 `split` 读取的剧情——直接调用 `split` 让服务端按镜头卡重建行；或逐镜把字段誊到 `update` 的 patch 里。镜头小节结构与行字段一一对应，誊写时不要改写事实。
