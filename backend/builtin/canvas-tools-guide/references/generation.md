# 媒体生成的操作路径

## 先明确要编辑还是生成

只建空白节点、改提示词或连线，用 `canvas_apply_ops`。用户要求实际产出媒体才走生成路径；不能为了完成连接任务附带收费生成。

生成前精读实际输入，核对资源就绪、当前任务及用户要求。`outputReference` 说明该节点的输出能否作参考，不说明本节点生成输入是否正确。角色卡读取其 definition 和 imageReference/audioReference，不只看 content。

## 引用与模型选择

1. `sourceNodeId` 用能力卡允许的文本来源；图片、视频、音频和角色卡放 `referenceNodeIds`。提示词里的“参考图1”、节点标题或连线本身不能替代生成参数中的真实 ID。
2. 临时标注图只能用 `image_annotation_render` 回执返回的 ID 放 `referenceTransientIds`；不把它当节点 ID，不构造 URL。
3. 传实际 `mode` 和 `referenceNodeIds` 查询 `model_list`，素材或模式变化后重新查。目录为空就说明无匹配模型，不猜模型名绕过筛选。
4. 原样复制返回的 `selectionId` 字符串到生成参数，不传 `selection` 对象，不混用其他模型选择字段。核对返回的时长、画幅、音频、参考数量、价格等能力，不能照搬其他模型的参数。
5. 按 `generate_media` 当前 schema 填 mode/prompt/nodeId/title/referenceNodeIds 和适用的可选字段。不要先创建一个已绑定任务或已有产物的节点再尝试覆盖；新结果通常用新的唯一节点 ID，仅当工具确认草稿空闲且可复用时续用。

图片编辑把原图纳入 referenceNodeIds，prompt 明确保留和修改内容；不伪造新图片 URL。使用角色卡时直接传角色卡节点 ID，工具会附带其有效设定和表示，通常不再重复传同一张三视图。

拆图层用 `image_layer_split` 自己的 schema，不给它塞 `generate_media` 的 mode/size/durationSeconds。

## 提交后

- 当前审批模式由运行时决定：待审批表示尚未提交生成，不声称已生成；auto 模式也需通过模型、预算和资源准入。
- taskId 从提交回执取得。用 `task_get` 查任务；queued/running 仍在处理，succeeded 后读取实际产物并核对目标节点。不要用节点 ID 查询任务。
- 确需视觉验收时用 `canvas_inspect_image` 接收真实图片，再按其回执记录摘要。相同版本已看过时使用画面或缓存，不循环请求附图。
- 准入失败与任务失败分开：未提交的参数错误按回执修正；已提交任务失败要告知用户，重新收费生成需要用户明确要求及当前审批流程。超时或断流不能证明未提交，先查任务和画布，不能直接重发。
