# syntax=docker/dockerfile:1.7
FROM oven/bun:1.3.9 AS web-build
WORKDIR /app/web
COPY web/package.json web/bun.lock ./
RUN --mount=type=cache,target=/root/.bun/install/cache bun install --frozen-lockfile --cache-dir=/root/.bun/install/cache
COPY web ./
RUN bun --bun ./node_modules/vite/bin/vite.js build --config vite.previs.config.ts

FROM node:22.19.0-alpine3.22 AS renderer-runtime
# Pin the browser and codec packages together with their Alpine repository.
RUN apk add --no-cache chromium=142.0.7444.59-r0 ffmpeg=6.1.2-r2 \
    && mkdir -p /data/jobs && chown -R node:node /data
RUN apk add --no-cache chromium-swiftshader=142.0.7444.59-r0
WORKDIR /app/renderer
COPY renderer/src ./src
COPY renderer/package.json ./
COPY renderer/public ./public
ENV CHROME_BIN=/usr/bin/chromium-browser PREVIS_DATA_DIR=/data/jobs PORT=8081
USER node
EXPOSE 8081
HEALTHCHECK --interval=15s --timeout=3s --start-period=15s --retries=3 CMD node -e "fetch('http://127.0.0.1:8081/health').then(r=>{if(!r.ok)process.exit(1)}).catch(()=>process.exit(1))"
CMD ["node", "src/server.mjs"]

# Local verification may reuse the freshly verified Vite build.
FROM renderer-runtime AS runtime-prebuilt
COPY web/dist-previs /app/web/dist-previs

FROM renderer-runtime AS production
COPY --from=web-build /app/web/dist-previs /app/web/dist-previs
