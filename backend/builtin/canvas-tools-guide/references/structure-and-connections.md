# 结构、引用与资产

## 读取足够的状态

`canvas_get_state` 的 `nodeIds` 是字符串数组，单次最多8个，和 `focusNodeIds` 互斥；整画布目录省略 `nodeIds`。默认目录用 `view=catalog`；指定节点默认 `detail`；关系检查用 `references`；准备编辑时才用 `edit` 读取富文本与编辑字段。`connections` 默认 `none`，需要时用 `selected` 或 `incident`；连线只是只读结果，不能据此判断正文来源已被正确改编。正文截断时复制 `nextRead.arguments` 继续读取，不拼富文本树。

分镜用 `canvas_read_storyboard`：默认 `creative` 查看创作说明、时长、对白、资产名称/版本和有效输入；`summary` 检查缺口，`rows=[]` 属预期，检查 `summary` 和 `coverage`；`bindings` 返回逐行绑定与解析结果，不含创作正文。`coverage.complete=false` 只能说明已检查的行，不能外推整表。`full` 通读最多20行、单行精读用 `rows=1`；可用 `rowIds` 定位。只使用返回的 `rowId`，不从边反推绑定。行绑定为空不等于有效输入为空，也不等于资源不可用。批量表仍用 `canvas_read_batch_table`。

## 普通节点

`canvas_apply_ops` 每项必填 `type/id`；`add_node` 另需 `nodeType`，仅可另给 `title/content/x/y`。尺寸与样式（`width/height/folderStyle/folderTheme/frame/locked`）须用后续 `update_node.patch`，不能塞入 `add_node`。创建与可写字段按节点能力卡选择：能作为引用输入不代表能创建或任意编辑。不要用普通节点的 `content`/Markdown 假装结构化分镜或批量表，也不要写任意 `metadata`、媒体 URL、任务状态或生成产物，更不要写 `connections` 或托管归属标记。

创建时通常省略坐标，让服务端落位。只在用户要求整理或新增节点造成重叠时整理；本轮提供 `canvas_arrange_nodes` 时优先使用，其 `dryRun` 是预演，不是已保存。若未提供，只在本轮已授权 `canvas_apply_ops.update_node` 的情况下修改 `patch.x/y`，写后回读核对节点边界；无写权限则说明限制。`reorder_nodes` 只调整业务顺序，不排坐标。

`set_parent` 只改变容器归属，不负责排版。前端放入折叠文件夹的交互可能重新布局；不能把该行为类推为每次服务端分组。使用写入回执的新快照继续操作，需要精确位置时先回读；收到 `stale_snapshot` 必须重新读取，不能猜测是何操作改了坐标。

## 输入引用：声明字段，不连节点

Agent 不连接、不改接、不断开连线：`canvas_apply_ops` 没有边操作，`connections` 由服务端按下面这些字段派生和回收。用户仍可在画布上手动连线，那是界面能力；输入权威始终是字段。

| 输入 | 写入字段 |
| --- | --- |
| 普通媒体草稿（image/video/audio） | `update_node` 的 `patch.generationSpec.referenceBindings`，元素是现有 `ReferenceBinding` |
| compare、panorama、config 等无生成合同的输入节点 | `update_node` 的 `patch.inputBindings`，同一个 `ReferenceBinding` 结构 |
| 分镜所有镜头共享的引用 | 创建时 `references`，编辑时 `set_references` 的 `references`（真实节点 ID 或唯一完整名称） |
| 某些镜头的素材 | `canvas_edit_storyboard` 的 `set_assets`，用 `rowId` 或 `rowIds` 选择 |
| 某镜头的已有产物归属 | `canvas_edit_storyboard` 的 `bind_output`／`unbind_output` |

`referenceBindings`／`inputBindings` 的每个元素必须有真实 `nodeId`、该媒体类型允许的 `role` 和 `order`；显式 `[]` 表示清空当前作用域，字段缺失表示不改。清空后服务端不会再从旧关系把素材并回来，也不要手工重建那条关系。声明的素材必须真实存在、类型匹配且有容量；服务端仍会核对节点类型、输入数量和模型容量，不把某个节点的限制类推为所有节点。

`update_node` 只接受节点能力卡声明的字段：`connections`、`managedBy`、`referenceOwnerNodeId`、`referenceBindingId` 由服务端维护，写它们会被拒绝，也不要试图用 `metadata` 整块覆盖。

## 分镜：以创作和资产选择为中心

创建时省略 nodeId 和坐标，系统分配节点/行身份并落位。每行主要维护 durationSeconds、plotDescription（镜头创作说明）、dialogue；构图、表演、运镜、光影、节拍和连续性可自行组织在创作说明中。必要时用 imageGenerationPrompt/videoMotionPrompt 覆盖生成提示词。已有覆盖优先于创作说明，修改说明时核对覆盖是否也要更新或清空。旧细分列仍保存在行中供界面和高级编辑使用，不要求模型逐列填写。

| 意图 | 写入方式 |
| --- | --- |
| 给多个镜头统一改台词或时长 | update：rowIds 内所有行共用同一 patch；逐行不同内容必须分别调用 |
| 替换选定镜头的资产集合 | set_assets：rowId 或 rowIds + 完整 assets |
| 替换所有镜头共享上下文 | set_references：references 整体覆盖；[] 清空共享输入，显式行资产保留 |
| 新增/删除镜头 | append 单次新增一行，给完整创作行 patch；批量新建用 canvas_create_storyboard.rows（1–100）；remove 给 rowId 或 rowIds |
| 指定已有图片/视频为产物 | bind_output：rowId + outputMode + outputNodeId |
| 解除已有产物归属 | unbind_output：rowId + outputMode；媒体资源保留 |

每批只使用一个快照，一处目标或引用无效则整批不写。生成中的镜头不能删除。`set_assets` 整行覆盖：先读 `bindings`，将需保留的行级 `assetBindings` 和旧 `characters` 规范化为 `assets`，合并新增项后整体回写；只传新增项会丢已有行绑定。`[]` 同时清空行资产声明和旧角色列表，不清共享输入。共享继承输入不应未经判断复制到行资产。

`set_references` 整体替换共享列表，各行继承的有效输入和派生连线随之更新；不会清空显式行级 `assetBindings`。保留旧共享素材时继续列入共享列表，或按需要通过 `set_assets` 声明为行资产；连线消失不能单独证明行资产丢失。

每项资产只需 ref 和 role，系统解析节点、校验用途并管理顺序；ref 仅接受当前画布节点 ID 或唯一完整名称，同名多版本必须明确选择 ID，归档资产不能成为新引用。不使用模糊匹配，不从三视图提示词推测定妆版本。role=character 可引用角色卡或人物图，其他用途必须匹配实际媒体类型。

```json
{"action":"set_assets","nodeId":"BOARD_ID","snapshotHash":"LATEST_HASH","rowIds":["ROW_1","ROW_2"],"assets":[{"ref":"CHARACTER_NODE_ID","role":"character"},{"ref":"教室","role":"environment"}]}
```

生成只需分镜表与行身份，系统管理目标产物和回填；不要为生成手工创建媒体节点、绑定输出或调整位置。bind_output 只用于用户明确选择的已有产物，不触发生成，也不能写 imageNodeId/videoNodeId/taskId/status 绕过语义动作。长创作字段截断时复制 nextReads 中的 canvas_read_content 参数续读，rowId 和 field 都要保留。

素材库查询返回 scope=library_asset 和 assetId，它们不是画布引用。需要使用库素材时，先将其绑定到画布媒体节点，再用该节点 ID 或名称设置镜头资产。

角色卡的业务类型为 `kind=character`，底层存储 `type=text`。读取版本、`resolvedImage`、`representations` 和媒体 readiness；空 `content` 不代表空卡。来源未记录时 `sourceCandidates` 只表示同资源候选，不能据此认定版本。主图与三视图可分别指定；单图复用会显式标明。同名角色先核对版本。

素材留档用 `canvas_apply_ops` 的 `update_node.patch` 设置 `usageStatus=archived`，可给 `usageNote` 和同业务类型的 `supersededByNodeId`；恢复用 `active`。只作用于本画布，保留标题、文件、任务和既有显式引用；默认候选与搜索排除归档项，查旧版本时搜索加 `includeArchived=true`。不要把归档解释为资源失效，也不要自动改接已有引用。

## 批量表

按 `action` 只传对应字段：update 用真实 `rowId` 和 `patch`；remove 不带 patch；set_concurrency 只给 schema 允许的并发值；增减参考列不混入行字段。行 patch 只写允许的 enabled/inputNodeIds/textNodeIds/cells/prompt，图片 ID 必须来自当前画布。append 省略 inputNodeIds 会继承上一行，若不希望继承应显式给空数组。维护表不等于已执行批量生成；工具未提供执行能力时如实说明。
