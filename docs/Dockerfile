ARG BUN_IMAGE=oven/bun:1.3.9-alpine
FROM --platform=$BUILDPLATFORM ${BUN_IMAGE} AS bun
FROM --platform=$BUILDPLATFORM node:22.19.0-alpine AS build
COPY --from=bun /usr/local/bin/bun /usr/local/bin/bun
WORKDIR /app
COPY package.json bun.lock ./
RUN --mount=type=cache,target=/root/.bun/install/cache bun install --frozen-lockfile
COPY . .
ARG DOCS_BUILD_ID=development
ARG DOCS_BUILD_COMMIT=unknown
ARG DOCS_BUILD_TIME=unknown
ENV NEXT_TELEMETRY_DISABLED=1 DOCS_BUILD_ID=$DOCS_BUILD_ID DOCS_BUILD_COMMIT=$DOCS_BUILD_COMMIT DOCS_BUILD_TIME=$DOCS_BUILD_TIME
RUN bun run types:check && bun run build

FROM node:22.19.0-alpine AS runtime
WORKDIR /app
ARG DOCS_BUILD_ID=development
ARG DOCS_BUILD_COMMIT=unknown
ARG DOCS_BUILD_TIME=unknown
LABEL tv.yingce.docs.build="{\"buildId\":\"${DOCS_BUILD_ID}\",\"commit\":\"${DOCS_BUILD_COMMIT}\",\"builtAt\":\"${DOCS_BUILD_TIME}\",\"deploymentContract\":1}"
ENV NODE_ENV=production NEXT_TELEMETRY_DISABLED=1 HOSTNAME=0.0.0.0 PORT=3001
COPY --from=build --chown=node:node /app/.next/standalone ./
COPY --from=build --chown=node:node /app/.next/static ./.next/static
COPY --from=build --chown=node:node /app/public ./public
USER node
EXPOSE 3001
HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=6 CMD node -e "fetch('http://127.0.0.1:3001/docs/api/health').then(r=>{if(!r.ok)process.exit(1)}).catch(()=>process.exit(1))"
CMD ["node", "server.js"]
