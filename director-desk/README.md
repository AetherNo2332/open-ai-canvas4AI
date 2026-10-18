# director-desk(影策云端移植副本)

DirectorDesk 导演台在影策云端工作台的移植副本。上游来源、提交与许可见 [`NOTICE.md`](./NOTICE.md)。

## 这个目录是什么

- 上游本身是 Vite Web 应用(`index.html` + `src/main.ts` + `src/ui/*` 原生 DOM/CSS),Electron 只是外壳;因此移植方式是**保留原版应用形态**,按清单剥离桌面端能力。
- 本副本以独立应用构建,产出静态站点后由 nginx 同源托管在 `/desk/`,再由影策 web 的预演台以嵌入方式挂载。

## 与桌面端能力的边界

| 上游能力 | 云端处置 |
| --- | --- |
| Electron 外壳 / 自动更新(`updates/`、`ui/update-panel`)| 不进入本副本 |
| 本地工程文件读写(`ui/project-save`、`ui/file-locations`、`files/desktop-output`)| 移除,改由 host bridge 交给影策持久化(PrevisScene) |
| 本地素材库(`storage/local-library`)| 移除,改由 host bridge 走影策资源 API |
| MCP 宿主(`ui/ai-mcp`)| 移除,AI 改由 host bridge 接影策 Agent(保留 AI 面板与技能面板) |

## 构建

```bash
# 依赖安装(使用镜像源)
bun install --registry=https://registry.npmmirror.com

# 静态构建(base=/desk/,产物在 dist/)
bunx vite build
```

生产镜像由仓库根 `Dockerfile` 的 `desk-build` 阶段完成同样的构建,并把 `dist/` 拷入 web 运行镜像的 `/desk/`。

## 与宿主(影策)的接口

宿主能力只经由 host bridge 消息交换(协议版本与消息类型定义在 `src/host/contract.ts`),包括:场景加载/保存、资源请求、导出请求、AI 通道事件。应用不直接访问影策的数据库、Cookie 或内部地址。
