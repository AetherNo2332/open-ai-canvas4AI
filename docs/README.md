# 影策文档站

基于 Next.js 16 与 Fumadocs 的文档站，内容位于 `docs/content/docs/`。

## 本地开发

```bash
cd docs
bun install
bun run dev
```

默认地址 <http://127.0.0.1:3001/docs>。默认不启动开发服务器；需要预览时再运行。

## 构建与校验

```bash
cd docs
bun run types:check   # next typegen + tsc --noEmit
bun run build         # 生产构建，生成 .next/
bun run start         # 本地运行已构建站点
```

CI 的 `Docs build` 任务运行同一组 `types:check` 与 `build`；推送前至少本地跑一次。

## 内容约定

- 页面为 MDX，frontmatter 必填 `title`，可选 `description`。
- 目录顺序与标题用同目录的 `meta.json`（`title`、`pages`、`defaultOpen`）控制。
- 站内链接使用 `/docs/<路径>`，例如 `/docs/backend/http-api`。
- 图片放在 `docs/public/`，页面内以 `/images/...` 引用。
- 新增文档需要同时更新对应 `meta.json`，并在 `.gitignore` 放行该文件；仓库只跟踪显式列出的文档。
- 文档默认中文，不公开密码、Token、Cookie、真实账号或机器敏感路径。

## 目录

| 路径 | 说明 |
| --- | --- |
| `app/(home)` | 站点首页 |
| `app/docs` | 文档布局与页面路由 |
| `app/api/search` | 全站搜索接口 |
| `components/mdx.tsx` | MDX 组件映射 |
| `lib/source.ts` | Fumadocs 内容源（读取 `content/docs`） |
| `lib/layout.shared.tsx` | 导航与页头共用配置 |
| `source.config.ts` | Fumadocs MDX 集合与 frontmatter schema |
