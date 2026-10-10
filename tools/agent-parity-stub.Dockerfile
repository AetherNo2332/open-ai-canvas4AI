FROM node:22.19.0-alpine
WORKDIR /fixtures
COPY tools/agent-parity-model-stub.mjs ./
USER node
CMD ["node", "/fixtures/agent-parity-model-stub.mjs"]
