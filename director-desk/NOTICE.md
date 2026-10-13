# NOTICE — DirectorDesk 上游来源

本目录是 [mangfufu/director-desk](https://github.com/mangfufu/director-desk) 的**移植副本**,用于影策云端工作台的导演预演台。

| 项 | 值 |
| --- | --- |
| 上游仓库 | https://github.com/mangfufu/director-desk |
| 上游提交 | `f3afacf` — "Release 0.4.11: motion, staging, history and plugin tools" |
| 上游版本 | `0.4.11` |
| 许可 | MIT(见 `LICENSE`) |
| 入库范围 | `src/`、`skills/`、`public/`、`index.html`、`package.json`、`package-lock.json`、`tsconfig.json`、`vite.config.mjs`、`LICENSE` |
| 未入库 | `desktop/`(Electron 壳)、`docs/`、`examples/`、`scripts/`、`tests/`、`.git/` |

## 影策侧修改说明

1. 按移植计划剥离桌面端能力(Electron 外壳、本地工程文件读写、自动更新、MCP 宿主、本地素材库),这些能力改由宿主(影策)通过显式 host bridge 提供。
2. 被剥离的入口以显式"云端不可用"提示替代,不保留静默失败路径。
3. 运行时依赖保持上游口径(`three`、`cannon-es`、`mediabunny`);Electron 相关依赖不进入本副本。

修改均记录在影策仓库的提交历史中(见 `docs/superpowers/plans/2026-10-13-director-desk-cloud-port.md`)。
