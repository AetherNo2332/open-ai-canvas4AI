# 媒体生成的操作路径

## 先明确要编辑还是生成

只建空白节点、改提示词或声明引用字段，用 `canvas_apply_ops`；连线由服务端按引用字段派生，没有连接节点的操作。用户要求实际产出媒体才走生成路径；不能为了完成连接任务附带收费生成。

生成前精读实际输入，核对资源就绪、当前任务及用户要求。`outputReference` 说明该节点的输出能否作参考，不说明本节点生成输入是否正确。角色卡读取其 definition 和 imageReference/audioReference，不只看 content。

## 引用与模型选择

候选素材的 `observationStatus` 只是观察事实，不要求关系梳理任务逐张识图。视觉对比时同批读取要比较的两张图片并给 `purpose=compare`；需要重新附送已缓存原图时加 `refresh=true`，先检查 `inspectionBudget` 是否支持同批对比。普通观察优先复用有效摘要；未实际看见的部分如实标为未知。

1. `sourceNodeId` 用能力卡允许的文本来源；图片、视频、音频和角色卡放 `referenceNodeIds`。提示词里的“参考图1”、节点标题或已有连线本身不能替代生成参数中的真实 ID。
2. 分镜模式同时给 `storyboardNodeId` 与 `storyboardRowId`：提示词、引用和视频时长从该行读取，不要另传 prompt、referenceNodeIds、sourceNodeId 或 durationSeconds；先把素材写进行绑定，再生成。
3. 识字和图像理解使用模型多模态能力；标注、局部修改和图层分离使用参考生图提示词，明确目标区域、输出内容和必须保留的部分，传原图真实 `referenceNodeIds`，不虚构临时标注图或 URL。
4. 传实际 `mode` 和 `referenceNodeIds` 查询 `model_list`，素材或模式变化后重新查；分镜可用 `storyboardNodeId` 和 `storyboardRowId`，或用 `storyboardRowIds` 批量查询。相同实际输入能力合并为一份模型目录，逐行保留问题；服务端用同一份行解析结果筛选。目录为空就说明无匹配模型，不猜模型名绕过筛选。
5. 原样复制返回的 `selectionId` 字符串到生成参数，不传 `selection` 对象，不混用其他模型选择字段。核对返回的时长、画幅、音频、参考数量、价格等能力，不能照搬其他模型的参数。
6. 分镜生成给 mode+storyboardNodeId+storyboardRowId，省略产物 nodeId/title 和位置。系统分配目标、使用项目及模型默认参数，并回填该行；不提前创建媒体节点或连线。普通生成仍按 schema 提供 prompt/nodeId/title/referenceNodeIds。分镜已有产物时，省略 nodeId 仍会核对原产物占用，不能自动换新目标绕过保护。需要重新生成时先取得用户要求并明确处理旧产物归属。

图片编辑把原图纳入 referenceNodeIds，prompt 明确保留和修改内容；不伪造新图片 URL。使用角色卡时直接传角色卡节点 ID，工具会附带其有效设定和表示，通常不再重复传同一张三视图。

拆图层和视觉编辑统一用 `generate_media` 的图片模式。生成图片不等同于修改绘图原生矢量记录；需要可编辑矢量时由画布编辑器维护。

## 提交后

- 当前审批模式由运行时决定：待审批表示尚未提交生成，不声称已生成；auto 模式也需通过模型、预算和资源准入。
- taskId 从提交回执取得。用 `task_get` 查任务；queued/running 仍在处理，succeeded 后读取实际产物并核对目标节点。不要用节点 ID 查询任务。
- `taskSubmitted` 只表示曾提交后台任务，true/false 都不能证明生成成功；预演存在 `taskSubmitted=true` 且 `status=failed` 的回执。以 `status` 或 `task_get` 及实际产物为准，失败不能汇报为交付完成。
- 确需视觉验收时用 `canvas_inspect_image` 接收真实图片，再按其回执记录摘要。相同版本已看过时使用画面或缓存，不循环请求附图。
- 准入失败与任务失败分开：未提交的参数错误按回执修正；已提交任务失败要告知用户，重新收费生成需要用户明确要求及当前审批流程。超时或断流不能证明未提交，先查任务和画布，不能直接重发。
