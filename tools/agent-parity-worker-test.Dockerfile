# syntax=docker/dockerfile:1.7
# Reuse the original worker build's cached compiler layer; no dependency change.
FROM node:22.19.0-alpine AS compiler
WORKDIR /agent
COPY agent/package*.json ./
RUN npm ci --ignore-scripts

FROM canvas-agent-parity-agent:working
USER root
WORKDIR /tmp/canvas-agent-parity-tests/agent
COPY --from=compiler /agent/node_modules/typescript /tmp/parity-typescript
COPY agent/package.json agent/tsconfig.json ./
COPY agent/src ./src
COPY agent/test ./test
COPY agent/harness ./harness
COPY agent/scripts ./scripts
COPY backend/internal/prompts/agent-system-policy.md ../backend/internal/prompts/agent-system-policy.md
COPY backend/internal/prompts/agent-media-policy.md ../backend/internal/prompts/agent-media-policy.md
COPY backend/internal/app/agent-tool-descriptions.md ../backend/internal/app/agent-tool-descriptions.md
RUN ln -s /agent/node_modules node_modules && node /tmp/parity-typescript/bin/tsc -p tsconfig.json
ARG WORKER_TEST_PATTERN=.*
RUN node --test --test-concurrency=1 --test-timeout=300000 --test-force-exit --test-name-pattern="$WORKER_TEST_PATTERN" \
    dist/test/tool-contracts.test.js dist/test/tool-disclosure.test.js \
    dist/test/harness-sync.test.js dist/test/system-prompt.test.js \
    dist/test/prompt-contract.test.js dist/test/runner.test.js \
    dist/test/session-history.test.js dist/test/subagent-wire.test.js
