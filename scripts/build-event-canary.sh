#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
build() {
  docker build --network=host --build-arg HTTP_PROXY="${HTTP_PROXY:-}" --build-arg HTTPS_PROXY="${HTTPS_PROXY:-}" --build-arg NO_PROXY="${NO_PROXY:-}" "$@"
}
build --build-arg BUILD_COMMIT="$(git rev-parse --short HEAD)" -t "${EVENT_BACKEND_IMAGE:-canvas-event-backend:local}" -f backend/Dockerfile .
build -t "${EVENT_AGENT_IMAGE:-canvas-event-agent:local}" -f agent/Dockerfile .
build -t "${EVENT_WEB_IMAGE:-canvas-event-web:local}" -f Dockerfile .
