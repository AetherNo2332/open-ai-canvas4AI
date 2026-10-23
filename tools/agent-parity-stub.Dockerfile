FROM node:22.19.0-alpine
RUN apk add --no-cache ffmpeg
WORKDIR /fixtures
COPY tools/agent-parity-model-stub.mjs ./
COPY tools/agent-parity-media-stub.mjs ./
USER node
CMD ["node", "/fixtures/agent-parity-model-stub.mjs"]
