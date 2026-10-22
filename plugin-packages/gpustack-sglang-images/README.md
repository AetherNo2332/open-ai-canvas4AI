# GPUStack 图像插件

自建 GPUStack（SGLang 后端）图像协议的声明式实现，贡献一个 `image` provider：

- 插件 ID：`gpustack-sglang-images`
- Provider：`gpustack-images`
- 能力：`image`
- 默认 Base URL：`http://gpustack.example.com:8080`（占位地址，安装后请在渠道配置中替换为实际的 GPUStack 网关地址）

## 能力

- **文生图**：`POST /v1/images/generations`（JSON）。
- **参考图编辑**：存在 `images` 时切到 `POST /v1/images/edits`（multipart，图片字节内联上传）。
- **不经过公网签名 URL**：`requiresPublicMediaUrls` 为 false，宿主直接内联素材字节，插件以 multipart 文件部件提交，本机/局域网部署无需签名地址与回环拉取。
- **结果地址**：响应映射把 GPUStack 返回的结果地址拼装为绝对地址，并声明 `resultEphemeral`，宿主在任务完成后立即下载并保存素材，画布与任务记录只保留宿主资源引用。

## 安装

把 `gpustack-sglang-images.yingce-plugin` 上传到插件中心即可。插件只使用声明式映射，不执行包内代码。

## 渠道配置

- Provider：GPUStack 图像（Qwen/MOSS）
- API Key：GPUStack 的 `gpustack_…` 密钥
- 模型：`qwen-image-2.1` 或 `moss-image-sft`

## 注意

上游需要返回绝对结果地址（SGLang 侧 `_fallback_image_urls` 已按实例 `host:port` 拼装）；否则宿主任务保存阶段会因相对地址下载失败报「作品已生成，但保存未完成：下载结果失败」。
