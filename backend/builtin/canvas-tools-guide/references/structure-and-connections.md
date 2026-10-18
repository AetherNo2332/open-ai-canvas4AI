# 结构、引用与资产

## 读取足够的状态

`canvas_get_state` 的 `nodeIds` 是字符串数组，和 `focusNodeIds` 互斥。精读若干目标时用前者；需要有限深度邻居时用后者搭配 `depth`。`connections` 是服务端按引用字段派生的只读结果，可能单独分页：按 `hasMoreConnections` 和 `nextConnectionOffset` 续读，用来说明现有关系，不是可写输入。

分镜用 `canvas_read_storyboard`：通读 `rows=5` 并翻页，需要完整某行时 `rows=1`。批量表用 `canvas_read_batch_table`。两者返回实际行 ID 与快照；行序号、标题、模型记忆中的旧行 ID 都不能替代它。

## 普通节点

`canvas_apply_ops` 的 `add_node` 需要 `id`、`nodeType`，可给 `title`、`content`；`update_node` 用已有 `id` 与 `patch`。创建与可写字段按节点能力卡选择：能作为引用输入不代表能创建或任意编辑。不要用普通节点的 `content`/Markdown 假装结构化分镜或批量表，也不要写任意 `metadata`、媒体 URL、任务状态或生成产物，更不要写 `connections` 或托管归属标记。

创建时通常省略坐标，让服务端落位。只在用户要求整理或新增节点造成重叠时整理；批量移动用 `canvas_arrange_nodes`。其 `dryRun` 是预演，不是已保存。

## 输入引用：声明字段，不连节点

Agent 不连接、不改接、不断开连线：`canvas_apply_ops` 没有边操作，`connections` 由服务端按下面这些字段派生和回收。用户仍可在画布上手动连线，那是界面能力；输入权威始终是字段。

| 输入 | 写入字段 |
| --- | --- |
| 普通媒体草稿（image/video/audio） | `update_node` 的 `patch.generationSpec.referenceBindings`，元素是现有 `ReferenceBinding` |
| compare、panorama、config 等无生成合同的输入节点 | `update_node` 的 `patch.inputBindings`，同一个 `ReferenceBinding` 结构 |
| 分镜所有镜头共享的引用 | `canvas_create_storyboard` 顶层 `referenceNodeIds`，或 `canvas_edit_storyboard` 的 `set_references` |
| 某个镜头的素材 | `canvas_edit_storyboard` `update` 的 `patch.assetBindings` |
| 某镜头的已有产物归属 | `canvas_edit_storyboard` 的 `bind_output`／`unbind_output` |

`referenceBindings`／`inputBindings` 的每个元素必须有真实 `nodeId`、该媒体类型允许的 `role` 和 `order`；显式 `[]` 表示清空当前作用域，字段缺失表示不改。清空后服务端不会再从旧关系把素材并回来，也不要手工重建那条关系。声明的素材必须真实存在、类型匹配且有容量；服务端仍会核对节点类型、输入数量和模型容量，不把某个节点的限制类推为所有节点。

`update_node` 只接受节点能力卡声明的字段：`connections`、`managedBy`、`referenceOwnerNodeId`、`referenceBindingId` 由服务端维护，写它们会被拒绝，也不要试图用 `metadata` 整块覆盖。

## 分镜：选正确语义

| 意图 | 写入方式 |
| --- | --- |
| 给某镜头关联一个资产 | `canvas_edit_storyboard` `update`，`patch.assetBindings` 传完整数组 |
| 给所有镜头共享上下文 | `set_references` 的 `referenceNodeIds`（空数组清空） |
| 把已有图片/视频指定为某镜头产物 | `bind_output`：`rowId`+`outputMode`(image/video)+`outputNodeId` |
| 解除某镜头产物归属 | `unbind_output`：`rowId`+`outputMode`，媒体资源保留 |
| 明确设置资产角色、优先级或替换资产集合 | 同上 `patch.assetBindings` |

`bind_output` 只改归属，不重写产物内容，也不触发生成；要生成仍需生成工具。不要写 `imageNodeId`／`videoNodeId`／`taskId`／`status` 绕过语义动作。

修改分镜：`append` 传 `patch`，至少正数 `durationSeconds` 和剧情/运动描述，不传 `rowId`；`update` 传真实 `rowId` 和部分 `patch`；`remove` 传 `rowId`，不传 `patch`；`set_references`／`bind_output`／`unbind_output` 只传各自声明的字段，不混行 patch。

`assetBindings` 是**整组替换**。添加一项时先保留已读出的其他项；只有用户要求清空时传 `[]`。每项必须有真实 `nodeId`、schema 允许的 `role` 和 0–100 的 `priority`。`role=character` 必须引用角色卡，不能将普通人物照片冒充角色卡；其他绑定引用当前画布媒体资产或角色卡。同一节点不能重复绑定。示例在保留原绑定后作为 update 的 patch：

```json
{"assetBindings":[{"nodeId":"EXISTING_CHARACTER_NODE_ID","role":"character","priority":100}]}
```

角色卡精读 `kind=character`、`character.definition`、版本和媒体 readiness；空 `content` 不代表空卡。已有同名角色卡先复用，建新卡必须使用已保存且就绪的形象图。

## 批量表

按 `action` 只传对应字段：update 用真实 `rowId` 和 `patch`；remove 不带 patch；set_concurrency 只给 schema 允许的并发值；增减参考列不混入行字段。行 patch 只写允许的 enabled/inputNodeIds/textNodeIds/cells/prompt，图片 ID 必须来自当前画布。append 省略 inputNodeIds 会继承上一行，若不希望继承应显式给空数组。维护表不等于已执行批量生成；工具未提供执行能力时如实说明。
