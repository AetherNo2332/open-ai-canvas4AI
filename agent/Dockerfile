# syntax=docker/dockerfile:1.7

# TS 编译产物是平台无关的 JS，固定 BUILDPLATFORM 只编译一次，与 web 镜像同一策略。
FROM --platform=$BUILDPLATFORM node:22.19.0-alpine AS build
WORKDIR /agent
COPY agent/package.json agent/package-lock.json ./
RUN npm ci --ignore-scripts
COPY agent/tsconfig.json ./
COPY agent/src ./src
RUN npm run build

# 运行镜像按目标架构安装生产依赖（esbuild 等可选二进制由 npm 按平台挑选）。
FROM node:22.19.0-alpine
# qemu 模拟的 arm64 上 node/npm 会因默认 CPU 模型缺指令而 SIGILL（CI 实测 exit 132），
# 让模拟器暴露全部 CPU 特性；原生执行时该变量无副作用。
ENV QEMU_CPU=max
ARG BUILD_VERSION
ARG BUILD_COMMIT=unknown
ARG BUILD_TIME=unknown
WORKDIR /agent
ENV NODE_ENV=production
ENV CANVAS_BUILD_VERSION=${BUILD_VERSION}
ENV CANVAS_BUILD_COMMIT=${BUILD_COMMIT}
ENV CANVAS_BUILD_TIME=${BUILD_TIME}
# Harness 素材随镜像分发：远程部署没有仓库目录可挂载，覆盖时仍可只读挂载同名路径。
ENV CANVAS_AGENT_HARNESS_DIR=/agent/harness
COPY agent/package.json agent/package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts && npm cache clean --force
COPY --from=build /agent/dist/src ./dist/src
COPY agent/harness ./harness
USER node
# 无对外端口的独立进程；与其他编排一致，只做 PID 1 存活检查。
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 CMD ["node", "-e", "process.kill(1,0)"]
CMD ["node", "dist/src/server.js"]
