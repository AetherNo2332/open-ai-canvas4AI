FROM node:22.19.0-alpine AS build
WORKDIR /agent
COPY agent/package*.json ./
RUN npm ci --ignore-scripts
COPY agent/tsconfig.json ./
COPY agent/src ./src
RUN npm run build

FROM node:22.19.0-alpine
WORKDIR /agent
ARG CANVAS_AGENT_BUILD_COMMIT=unknown
ENV NODE_ENV=production
ENV CANVAS_AGENT_BUILD_COMMIT=$CANVAS_AGENT_BUILD_COMMIT
ENV CANVAS_AGENT_HARNESS_DIR=/agent/harness
COPY agent/package*.json ./
RUN npm ci --omit=dev --ignore-scripts && npm cache clean --force
COPY --from=build /agent/dist/src ./dist/src
COPY agent/harness ./harness
USER node
HEALTHCHECK --interval=15s --timeout=3s --start-period=30s --retries=3 CMD ["node", "dist/src/health-probe.js"]
CMD ["node", "dist/src/server.js"]
