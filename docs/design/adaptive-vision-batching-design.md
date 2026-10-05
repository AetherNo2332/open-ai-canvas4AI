# 自适应多图识图设计

## 目标

识图工具允许一次运行处理多张图片，同时避免把未知供应商上限、上下文窗口和大图片 payload 直接暴露为运行失败。系统按模型和渠道动态形成小批次，成功后复用视觉摘要，失败时只对可重试的容量错误拆分批次；任何不可恢复错误都必须终止运行，不能再次被租约领取。

## 范围与约束

- `visionSupported` 仍是后台模型是否支持 Agent 识图的必需能力声明。
- 单请求图片上限不再作为 Agent 工具的固定图片数量合同。
- 可选的 `visionMaxBatchImages` 和 `visionMaxBatchCost` 是管理员安全封顶；值为 `0` 表示未知。
- 原始图片只接受账号资源引用；每张图以服务端 SHA-256 绑定摘要。
- 已缓存且 SHA 未变化的图片只进入结构化摘要，不重新发送原图。
- 本设计只改变 Agent 识图链路，不改变图片生成、视频参考图或普通图片模型的供应商限制。

## 能力与运行时预算

文本模型能力增加两个可选字段：

```json
{
  "visionSupported": true,
  "visionMaxBatchImages": 0,
  "visionMaxBatchCost": 0
}
```

运行时为 `(channelModelID, protocol, model)` 保存动态探测状态，至少包含成功上限、最近失败原因、模型能力版本和更新时间。有效预算按下式计算：

```text
imageLimit = min(defaultProbeStart=4, learnedLimit if known, adminCap if > 0)
costLimit  = min(defaultCostLimit, learnedCost if known, adminCost if > 0)
```

管理员封顶只能降低预算，不能把未知值解释成无限。动态状态必须可失效：能力配置版本变化或超过保留时间后回到默认探测值。

每张图片计算保守视觉成本，至少考虑文件大小和像素面积；单张图片超过单图上限时直接返回不可重试错误。批次同时满足图片数量和成本预算。

## 批次生命周期

1. `canvas_inspect_image` 校验节点、资源归属、资源状态、SHA 和缓存。
2. 缓存命中项生成摘要回执，不进入原图队列；SHA 变化时旧缓存失效。
3. 未缓存项进入 `PendingImageInspections`，同一节点和同一 SHA 在本轮去重。
4. 工具回执全部写入 assistant/tool 历史后，按预算把待处理图片切成一个或多个批次。
5. 每个批次生成稳定 `batchID`，包含图片节点 ID、SHA、资源 key、预算、尝试次数和状态。
6. 每个批次追加一条 user 消息，消息中包含该批图片的文字回执和图片内容；不同批次之间保持 tool 消息连续性。
7. 批次成功后，模型通过工具提交 `summary.short` 与 `summary.detailed`；服务端重新校验 SHA 后写入节点 `metadata.visionCache`。

## 自适应失败处理

以下错误可触发拆分：图片数量超限、上下文超限、视觉 token 超限、payload 过大、请求体过大和明确的视觉 OOM/超时。当前批次大于一张时按二分拆成两批，分别重试；成功批次更新动态成功上限，但不超过管理员封顶。

鉴权、余额、模型不存在、参数错误、内容安全和服务端内部错误不触发拆分。单张图片仍失败时，记录原始 provider 错误并将运行置为 `failed`。每个运行有总拆分次数、总请求数和运行时限上限，达到任一上限立即失败。

## 状态与恢复

checkpoint 保存批次计划、已完成批次、动态预算快照和失败分类。恢复时跳过已成功批次，不重新发送已确认的图片。`failed`、`cancelled` 和 `completed` 状态不可被租约重新领取；租约 worker 只能领取明确可恢复的 `queued/running` 步骤。所有终止路径写入 `run_failed` 事件和机器可读原因。

## 兼容与迁移

- 旧模型配置没有 `visionSupported` 时继续按旧字段兼容推断；新后台不再把文本模型图片数量作为 Agent 工具配置项。
- 旧 checkpoint 中的单图 `PendingImageInspections` 自动视为一个批次。
- 旧 `visionCache` 结构继续读取；新增字段只向前兼容，不把摘要中的图片文字当作指令。
- 供应商仍可在最终 provider 校验层拒绝不支持的请求；Agent 层负责拆分和终态处理，不绕过供应商限制。

## 测试验收

- 默认 4 张图片一次成功；超过默认值按预算切批。
- 首次容量 400 后二分重试，并记录动态成功上限。
- 单图容量错误进入 `failed`，租约再次领取不会重新执行。
- 缓存命中不传原图；SHA 变化只重新传变化图片。
- checkpoint 恢复不重复发送成功批次。
- 管理员图片数和成本封顶均生效。
- 不可重试错误不拆分、不重复请求。
- tool 消息顺序保持 `assistant(tool_calls) → tool×N → user(image batch)`，避免再次触发 DeepSeek 400。
