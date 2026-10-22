# GPUStack 图像接口字段

## 协议身份

- 插件 ID：`gpustack-sglang-images`。
- Provider ID：`gpustack-images`。
- 能力：`image`。
- 默认 Base URL：`http://gpustack.example.com:8080`（GPUStack 网关）。
- 鉴权驱动：`bearer`，密钥字段 `apiKey`。
- 创建：无参考图 `POST /v1/images/generations`（JSON），有参考图 `POST /v1/images/edits`（multipart）。
- 生命周期：同步响应。

## 配置字段

| 字段 | 类型 | 必填 | 含义 |
| --- | --- | --- | --- |
| `apiKey` | secret | 是 | GPUStack API Key |

## 统一字段映射

| 统一字段 | 类型 | 必填 | 上游映射 | 说明 |
| --- | --- | --- | --- | --- |
| `model` | string | 是 | `model` | GPUStack 模型名。 |
| `prompt` | string | 是 | `prompt` | 图片提示词。 |
| `images` | media[] | 否 | `multipart file(image)` | 参考图字节内联上传，不经公网签名地址。 |
| `aspectRatio` | string | 否 | `size` | 像素 `宽x高` 或比例；像素向上对齐到 32 的倍数。 |
| `imageCount` | integer | 否 | `n` | 输出数量，默认 1。 |
| `providerOptions` | object | 否 | `gpustack-images.steps`、`gpustack-images.responseFormat` | 插件命名空间扩展。 |

## 参考图传输方式

`requiresPublicMediaUrls: false`。宿主在请求前把画布资源读成内联字节（`data:` URL），插件以 multipart 文件部件 `image` 提交给 `/v1/images/edits`。整条链路不出公网、不做签名、上游不回环拉取：

```
画布资源 → 宿主读取字节 → multipart(image) → GPUStack → SGLang edits
```

## 尺寸规则

| 输入 | 输出 |
| --- | --- |
| `1024x1024` / `1280x720` | 按 32 向上取整（`1280x736`） |
| `1:1` | `1024x1024` |
| `16:9` / `9:16` | `1024x576` / `576x1024` |
| `4:3` / `3:4` | `1024x768` / `768x1024` |
| `3:2` / `2:3` | `1152x768` / `768x1152` |
| `21:9` | `1024x448` |
| 其它/空 | `1024x1024` |

Qwen-Image 2.1 与 MOSS-Image 都要求宽高为 32 的倍数，故统一向上对齐。

## 上游请求模板逐字段清单

| 上游位置 | 值或转换表达式 |
| --- | --- |
| `create.method` | `POST` |
| `create.path` | `/v1/images/generations`；有参考图时 `/v1/images/edits` |
| `create.contentType` | `application/json`；有参考图时 `multipart/form-data` |
| `create.body.model` | `request.model` |
| `create.body.prompt` | `request.prompt` |
| `create.body.size` | 尺寸规则见上 |
| `create.body.num_inference_steps` | `providerOptions.gpustack-images.steps`，默认 `25` |
| `create.body.response_format` | `providerOptions.gpustack-images.responseFormat`，默认 `url` |
| `create.body.n` | `request.imageCount`，默认 `1` |
| `create.files[0]` | `name=image`，来源 `request.images`（内联字节） |

## 响应映射

- 状态：同步成功（`status: succeeded`）。
- 图片：`response.data[]` 逐项映射；
  - `url`：绝对地址原样使用；相对地址时拼上默认 Base URL；
  - `dataUrl`：上游返回 `b64_json` 时拼 `data:image/png;base64,`。
- `resultEphemeral: true`：宿主立即下载保存，画布只保存宿主资源引用。
- 错误：`error.code` / `code`，消息 `error.message` / `detail` / `error`。

## GPUStack 侧要求

GPUStack 的 SGLang 后端需要返回绝对结果地址（实例 `host:port` + `/v1/images/{id}/content`）。相对地址会导致宿主保存阶段下载失败。
