<p align="center">
  <img src="web/public/logo.svg" width="80" alt="ArrowFX">
</p>

<h1 align="center">ArrowFX</h1>

<p align="center">用画布组织创作，让 Agent 协助完成剧本、资产与分镜。</p>

<p align="center">
  <a href="https://github.com/AetherNo2332/open-ai-canvas4AI">本仓库</a> ·
  <a href="docs/content/docs/overview/features.mdx">功能清单</a> ·
  <a href="docs/content/docs/backend/production-deployment.mdx">生产部署</a> ·
  <a href="CHANGELOG.md">更新日志</a>
</p>

ArrowFX 是基于 [影策 / Open AI Canvas](https://github.com/ddcat-ai/open-ai-canvas) 二次开发的 AI 影视创作工作台。它把文本、图片、视频、音频、角色卡和分镜表放在同一张画布中，通过模型渠道、异步任务与云端 Agent 连接创作步骤。

本仓库维护画布与 Agent 的交互、结构化分镜、资产引用、技能运行和自部署能力。站点名称、助手名称、主题与圆角可在后台配置；代码默认品牌为 ArrowFX。

项目仍在持续开发。当前能力和验收边界分别见[功能清单](docs/content/docs/overview/features.mdx)与[待测试清单](docs/content/docs/progress/pending-test.mdx)。

## 能做什么

| 能力 | 当前实现 |
| --- | --- |
| 自由画布 | 文本和媒体节点、分组与排版、缩放与小地图、撤销重做、版本记录、导入导出和只读分享。 |
| 角色与分镜 | 角色卡、参考素材、结构化分镜表；共享引用与逐镜资产绑定、素材版本选择、媒体预览和生成结果回填。 |
| 多媒体生成 | 通过已配置渠道执行文本、图片、视频和音频任务；参考图、首尾帧等输入按模型能力提供。 |
| 云端 Agent | 持久化对话、流式回复、画布读写、批量操作与任务跟踪；支持只读、审批和自动执行模式，工具按需披露。 |
| Skills 与规则 | 内置及安装技能、后台全局默认技能、对话技能选择；“提示词与规则”统一编辑账户全局提示词和当前画布 Workspace 文档。 |
| 任务与素材 | 查看任务状态与产出，管理素材分类与来源，复用生成素材，校验资源引用和访问权限。 |
| 预演与剪辑 | 3D 预演工作台、画布时间线和项目剪辑台；支持片段编排与导出，服务端字幕转写和渲染需配置相应依赖。 |
| 管理后台 | 用户、系统渠道与模型管理、可选逻辑模型、用量与积分、对象存储、插件、功能开关、站点外观和 Agent 执行限制。 |

短剧项目、任务中心、插件等入口受后台功能开关控制。模型能力取决于实际配置的渠道；本项目不附带模型 API Key 或生成额度。

## 本地运行

推荐先使用 Docker 本地构建，包含前端、后端和 Agent Worker。宿主机只需 Git、Docker 和 Compose 插件，无需先安装 Go 或 Bun。

### 1. 获取代码与配置

```bash
git clone https://github.com/AetherNo2332/open-ai-canvas4AI.git open-ai-canvas
cd open-ai-canvas
cp .env.example .env
```

编辑 `.env`，将 `CANVAS_AGENT_INTERNAL_TOKEN` 填为至少 32 字符的随机令牌。backend 与 Agent 必须使用相同值；可用下面的命令生成，再将结果填入配置：

```bash
openssl rand -hex 32
```

### 2. 构建并启动

```bash
docker compose -f docker-compose.local.yml up -d --build
```

打开 <http://localhost:3000>。该编排只公开 web 的 `3000` 端口，backend 与 Agent 在容器网络内通信；本地数据库和上传资源保存在 `backend-data` 命名卷中。

```bash
# 查看服务状态与 Agent 日志
docker compose -f docker-compose.local.yml ps
docker compose -f docker-compose.local.yml logs -f --tail=100 agent
```

### 3. 完成首次配置

1. 首个注册账号自动成为管理员，后续公开注册默认关闭。
2. 在个人设置的“渠道”中配置 Base URL、API Key 和模型，或由管理员配置系统渠道及渠道模型。个人渠道受功能开关控制；逻辑模型是可选高级路径。
3. 进入自由画布，添加素材、角色卡或分镜表，再选择生成模型或使用 Agent。
4. 在 Skills 页面安装或创建技能，再通过 Agent 面板顶部的技能图标选择；后台启用的全局默认技能会自动加入新会话。

纯文本对话不需要公开资源地址。使用 Agent 看图或向模型传入本地素材时，需要配置上游能够访问的 `CANVAS_PUBLIC_BASE_URL`，或可用的对象存储分发地址；`localhost` 不能作为远端模型可访问的资源入口。

## 六份 Compose 如何选择

| 文件 | 用途 | Agent 与端口 |
| --- | --- | --- |
| [docker-compose.local.yml](docker-compose.local.yml) | 本地构建并运行应用，默认 SQLite；适合体验和联调。 | 默认包含 Agent；web 为 `3000`，backend 不公开端口。 |
| [docker-compose.yml](docker-compose.yml) | 基础本地编排。 | Agent 需加 `--profile pi`；web 为 `3000`。 |
| [docker-compose.dev.yml](docker-compose.dev.yml) | 挂载源码的前后端开发环境。 | 不包含 Agent；web 为 `3000`，backend 绑定 `127.0.0.1:8080`。 |
| [docker-compose.deploy.yml](docker-compose.deploy.yml) | PostgreSQL、Redis、独立迁移和三份应用镜像的生产部署。 | 默认包含 Agent；web 由 `CANVAS_HTTP_PORT` 控制，默认 `3000`。 |
| [docker-compose.build.yml](docker-compose.build.yml) | 为 deploy 提供 backend、web、Agent 的源码构建覆盖。 | 作为叠加文件使用；源码安装脚本负责提供必填镜像变量。 |
| [docker-compose.server.yml](docker-compose.server.yml) | 服务器源码构建，使用宿主机目录保存数据。 | 默认包含 Agent；web 绑定 `127.0.0.1:5173`，供入口代理接入。 |

只有 dev 编排支持 `CANVAS_WEB_HOST_PORT` 与 `CANVAS_BACKEND_HOST_PORT` 两个端口覆盖变量。六份文件是不同运行方式，不能任意叠加；默认编排不包含监控、字幕转写或独立预演渲染服务。

## Linux 服务器部署

### 从源码安装

```bash
curl -fsSL https://raw.githubusercontent.com/AetherNo2332/open-ai-canvas4AI/main/scripts/install-server.sh | sudo bash
```

脚本默认安装到 `/opt/open-ai-canvas`，安装 Docker、获取本仓库源码、生成数据库密码及 Agent 内部令牌，使用 PostgreSQL、Redis 和独立迁移服务，构建并启动 backend、web 和 Agent。默认访问 `http://服务器IP:3000`；可通过 `INSTALL_DIR`、`CANVAS_HTTP_PORT`、`REPOSITORY_URL`、`REPOSITORY_REF` 调整安装参数。

公网使用前应配置 HTTPS、准确的 CORS Origin 和资源公开地址，并先完成管理员注册。数据目录、卷和备份的说明见[生产部署](docs/content/docs/backend/production-deployment.mdx)。

### 使用已发布镜像

先从[本仓库 Releases](https://github.com/AetherNo2332/open-ai-canvas4AI/releases)选择包含当前部署编排的版本，将下方占位替换为实际版本：

```bash
curl -fsSL https://raw.githubusercontent.com/AetherNo2332/open-ai-canvas4AI/main/scripts/install-server-image.sh \
  | sudo env CANVAS_IMAGE_TAG='v<已发布版本>' bash
```

此路径需要该版本已发布 backend、web、Agent 三份 GHCR 镜像，以及 Host Updater 的 Linux 二进制和校验清单；服务器需要 systemd，CPU 架构为 amd64 或 arm64。脚本会固定三份应用镜像的仓库 digest，不以 `latest` 作为部署版本。

镜像安装脚本会安装 Host Updater。首次安装后，需在安装目录执行下面的命令，让 backend 读取新生成的更新服务令牌：

```bash
cd /opt/open-ai-canvas
sudo docker compose --env-file .env -f docker-compose.deploy.yml up -d backend
```

自定义安装目录时使用对应路径。源码安装路径不会自动安装 Host Updater。旧版 updater、旧 Compose 和缺失 Agent 的迁移步骤见[生产部署](docs/content/docs/backend/production-deployment.mdx)。

## 开发

前端使用 Bun，后端要求 Go 1.25，独立 Agent Worker 要求 Node.js 22.19.0 或更高版本。使用 SQLite 的宿主机开发还需要可用的 CGO / C 编译环境。

以下两个终端均从仓库根目录开始：

```bash
# 终端一：后端，使用独立开发数据目录
mkdir -p .local/project-workbench-debug
cd backend
CANVAS_BACKEND_ADDR=127.0.0.1:8080 \
CANVAS_BACKEND_DATA_DIR=../.local/project-workbench-debug \
go run ./cmd/server
```

```bash
# 终端二：前端
cd web
bun install --frozen-lockfile
bun run dev
```

前端默认将 `/api` 代理到 `http://127.0.0.1:8080`，可用 `VITE_API_PROXY_TARGET` 修改。Windows 也可在仓库根目录运行 [scripts/start-local.ps1](scripts/start-local.ps1)。

上述宿主机命令和 Windows 脚本只启动前后端。调试云端 Agent 时，还需运行 `agent/` Worker，并给 backend 和 Worker 设置相同内部令牌；Worker 的后端地址改为 `http://127.0.0.1:8080`，Harness 目录指向 `agent/harness`。构建与启动命令见 [agent/package.json](agent/package.json)。

使用 Docker 开发编排前，需要先创建它挂载的数据和缓存目录：

```bash
mkdir -p .local/project-workbench-debug .local/cache/go-build .local/cache/go-mod
LOCAL_UID=$(id -u) LOCAL_GID=$(id -g) \
  docker compose -f docker-compose.dev.yml up --build
```

此命令适用于 Bash 环境；dev 编排不启动 Agent。测试、构建及协作约定见 [AGENTS.md](AGENTS.md) 和 [CONTRIBUTING.md](CONTRIBUTING.md)，按改动范围运行验证。

## 运行组成与可选依赖

```text
浏览器 / web
    │ API、资源请求与 SSE
    ▼
backend ── 模型渠道 / 资源存储
    ▲
    │ 内部鉴权、任务领取与执行回执
agent / Pi Worker
```

| 目录 | 职责 |
| --- | --- |
| `web/` | React、TypeScript、Vite 工作区，画布、分镜、素材与任务界面。 |
| `backend/` | Go、Gin、GORM 服务，认证、权限、任务、资源、模型中转和 Agent 编排。 |
| `agent/` | 基于 Pi 的独立 Worker，执行模型与工具循环，通过内部接口访问后端。 |
| `director-desk/` | 集成的导演台应用与桥接代码；运行模式由后台配置控制。 |
| `renderer/` | 可选白模视频预演渲染服务，不属于默认 Compose。 |
| `plugin-packages/` | 模型及编辑等插件包。 |
| `docs/` | 功能、接口、设计和验收资料；具体索引见下方文档入口。 |

画布时间线包含浏览器本地导出路径；项目剪辑台的服务端渲染需要后端可用的 FFmpeg，自动字幕需要 whisper.cpp 服务。浏览器内的 3D 预演与截帧可本地完成；DirectorDesk 或 Agent 提交的后台白模视频渲染需要 renderer 及对应服务地址、独立令牌。可选依赖需单独部署并将配置传入对应进程，安装基础应用不会自动提供这些服务。

## 部署与数据边界

- 模型生成需要自行配置渠道及凭据，具体输入能力、耗时和费用由渠道决定。
- 生产只公开 web，backend、数据库、Redis 和 Agent 保留在内部网络；使用 HTTPS，设置明确的 `CANVAS_CORS_ORIGINS`。
- 默认拒绝后端访问本机和私网模型地址；可信服务通过 `CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS` 精确放行。
- 保护 `.env`、数据库、上传文件、备份和 `.settings-key`，不要向公开 Issue 提交真实密钥、Cookie 或原始生产日志。
- 生产升级前备份数据库、资源和配置；容器健康与启动成功不能替代真实登录、生成、Agent 对话和资源访问验收。

更多要求见[安全策略](SECURITY.md)。

## 文档入口

- [功能清单](docs/content/docs/overview/features.mdx)
- [Agent 工具与按需披露](docs/content/docs/backend/agent-tools.mdx)
- [Agent 提示词与规则](docs/content/docs/backend/agent-prompt-policy.mdx)
- [Agent 读取视图与配额](docs/content/docs/backend/agent-tool-reads.mdx)
- [生产部署与升级边界](docs/content/docs/backend/production-deployment.mdx)
- [HTTP API](docs/content/docs/backend/http-api.mdx) · [数据库结构](docs/content/docs/backend/backend-database.mdx)
- [代码功能地图](docs/content/docs/backend/code-map.mdx)
- [待办](docs/content/docs/progress/todo.mdx) · [待测试](docs/content/docs/progress/pending-test.mdx)
- [更新日志](CHANGELOG.md) · [贡献指南](CONTRIBUTING.md)

## 许可证与上游致谢

本项目采用 [MIT](LICENSE) 协议。ArrowFX 在 [ddcat-ai/open-ai-canvas](https://github.com/ddcat-ai/open-ai-canvas)（影策）基础上继续开发；影策源于 [basketikun/infinite-canvas](https://github.com/basketikun/infinite-canvas) 的早期版本。

感谢原作者、上游贡献者与社区的工作。原有版权、署名及第三方权利说明保留在 [LICENSE](LICENSE) 和 [NOTICE](NOTICE) 中；各依赖与插件遵循其自身许可证。
