# 结构、连线与资产

## 读取足够的状态

`canvas_get_state` 的 `nodeIds` 是字符串数组，和 `focusNodeIds` 互斥。精读若干目标时用前者；需要有限深度邻居时用后者搭配 `depth`。连线可能单独分页，按 `hasMoreConnections` 和 `nextConnectionOffset` 续读，不能只检查第一页就创建重复边。

分镜用 `canvas_read_storyboard`：通读 `rows=5` 并翻页，需要完整某行时 `rows=1`。批量表用 `canvas_read_batch_table`。两者返回实际行 ID 与快照；行序号、标题、模型记忆中的旧行 ID 都不能替代它。

## 普通节点

`canvas_apply_ops` 的 `add_node` 需要 `id`、`nodeType`，可给 `title`、`content`；`update_node` 用已有 `id` 与 `patch`。创建与可写字段按节点能力卡选择：可连接不代表可创建或任意编辑。不要用普通节点的 `content`/Markdown 假装结构化分镜或批量表，也不要写任意 `metadata`、媒体 URL、任务状态或生成产物。

创建时通常省略坐标，让服务端落位。只在用户要求整理或新增节点造成重叠时整理；批量移动用 `canvas_arrange_nodes`。其 `dryRun` 是预演，不是已保存。

## 普通连线与端口

先确认源/目标真实存在、未锁定以及现有输入。能力回执的 `canvasConnections` 和 `connectionHandles` 用来理解手动连线限制；生成能力的 `canSource`/`canTarget`/`canReference` 不能代替连线规则。服务端还会核对节点类型、输入数量和模型容量，不把某个节点的限制类推为所有节点的限制。

`connect_nodes` 必须写在 `canvas_apply_ops` 中。以下 JSON 展示形状，`CURRENT_HASH`、`SOURCE_ID`、`TARGET_ID` 都需替换为真实读取值，边 ID 需确认未使用：

```json
{"snapshotHash":"CURRENT_HASH","ops":[{"type":"connect_nodes","id":"edge-new-1","fromNodeId":"SOURCE_ID","toNodeId":"TARGET_ID"}]}
```

- 普通连接省略端口字段；指定端口时必须引用真实行或列，不能编造通用 `input`/`output`。
- 相同端点加相同端口是重复；相同端点的不同分镜行端口可分别连接。检查端点和端口，不能仅凭边 ID 判断是否重复。
- 自连、背板端点、配置互连、未注册类型会拒绝。配置通常采用业务节点 → 配置节点；改变方向会改变业务含义，不能只为通过校验翻转。
- 批量表参考列输入用图片 → 表，目标端口 `batch-reference:<真实列ID>`。配置连接按能力规则通常是表 → 配置，不能把配置当参考图片输入。列的 `mentionToken` 从表读取回执复制，不自行编参考编号。
- 手动能连线不等于此资源可用于生成；建立连线也不会修改已提交任务的输入。

## 分镜绑定：选正确语义

| 意图 | 写入方式 |
| --- | --- |
| 给某镜头关联一个资产 | 资产 → 分镜，`toHandleId="row:<真实rowId>"` |
| 给所有镜头共享上下文 | 来源 → 分镜，`toHandleId="storyboard:context"` |
| 把已有图片/视频指定为某镜头产物 | 分镜 → 图片/视频，`fromHandleId="row:<真实rowId>"` |
| 明确设置资产角色、优先级或替换资产集合 | `canvas_edit_storyboard` 的 `patch.assetBindings` |

行端口会同步行绑定或产物元数据；因此只做符合用户目标的方向和端口。普通边不能替代行绑定。指定产物不会触发生成；要生成仍需生成工具。

修改分镜：`append` 传 `patch`，至少正数 `durationSeconds` 和剧情/运动描述，不传 `rowId`；`update` 传真实 `rowId` 和部分 `patch`；`remove` 传 `rowId`，不传 `patch`。

`assetBindings` 是**整组替换**。添加一项时先保留已读出的其他项；只有用户要求清空时传 `[]`。每项必须有真实 `nodeId`、schema 允许的 `role` 和 0–100 的 `priority`。`role=character` 必须引用角色卡，不能将普通人物照片冒充角色卡；其他绑定引用当前画布媒体资产或角色卡。同一节点不能重复绑定。示例在保留原绑定后作为 update 的 patch：

```json
{"assetBindings":[{"nodeId":"EXISTING_CHARACTER_NODE_ID","role":"character","priority":100}]}
```

角色卡精读 `kind=character`、`character.definition`、版本和媒体 readiness；空 `content` 不代表空卡。已有同名角色卡先复用，建新卡必须使用已保存且就绪的形象图。

## 批量表

按 `action` 只传对应字段：update 用真实 `rowId` 和 `patch`；remove 不带 patch；set_concurrency 只给 schema 允许的并发值；增减参考列不混入行字段。行 patch 只写允许的 enabled/inputNodeIds/prompt，图片 ID 必须来自当前画布。append 省略 inputNodeIds 会继承上一行，若不希望继承应显式给空数组。维护表不等于已执行批量生成；工具未提供执行能力时如实说明。
