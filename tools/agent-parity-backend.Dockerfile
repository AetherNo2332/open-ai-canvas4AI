# syntax=docker/dockerfile:1.7
# Debian Go includes the C compiler needed by the real SQLite backend.
ARG GO_BUILDER_IMAGE=golang:1.25-bookworm
FROM ${GO_BUILDER_IMAGE} AS deps
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=$GOPROXY
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
COPY backend/third_party ./third_party
RUN go mod download

FROM deps AS backend-test
COPY backend ./
COPY plugin-packages /src/plugin-packages
COPY agent/harness /src/agent/harness
COPY web/test/fixtures /src/web/test/fixtures
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=tmpfs,target=/tmp \
    CGO_ENABLED=1 go test ./internal/canvas/capability -count=1 -timeout=10m
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=tmpfs,target=/tmp \
    CGO_ENABLED=1 go test ./internal/prompts -run '^Test(LoadAgentPoliciesUsesDocumentMetadata|ParsePolicyDocumentRejectsInvalidMetadata)$' -v -count=1 -timeout=10m
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=tmpfs,target=/tmp \
    CGO_ENABLED=1 go test ./internal/app ./internal/database ./internal/assets -run 'TestPiPreflight|TestAgentParity|TestCanvasAgentManualOperationConformance|TestAgentToolSchemaArtifact|TestUndoCloudAgentCanvas|TestMigrateSchemaV58|TestCloudAgent(ToolSchema|ToolArgumentValidation|ToolTableMatchesRuntimeDispatch|RegistersEligibleConcreteToolsFromStart|ToolCategoriesRespectCapabilityAndPermission|ToolLoopPersistsApprovalAndAppliesCanvasWrite|CanvasApprovalAdmissionFailureTerminatesRun|Storyboard|BatchTable|MixedCanvasReadsUnsupportedNodesWithoutGrantingCapabilities|NodeTypesExposeExecutableAllowList)' -v -count=1 -timeout=20m

FROM deps AS backend-history-test
COPY backend ./
COPY plugin-packages /src/plugin-packages
COPY agent/harness /src/agent/harness
COPY web/test/fixtures /src/web/test/fixtures
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=tmpfs,target=/tmp \
    CGO_ENABLED=1 go test ./internal/app -run '^TestAgentParityDatabase(History.*|DrawingReadDoesNotExposeNativeCredentials|AssetBindingReplacesResultMetadata)$' -v -count=1 -timeout=10m

FROM deps AS backend-repair-scope-test
COPY backend ./
COPY plugin-packages /src/plugin-packages
COPY agent/harness /src/agent/harness
COPY web/test/fixtures /src/web/test/fixtures
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=tmpfs,target=/tmp \
    CGO_ENABLED=1 go test ./internal/app -run '^TestAgentParityPiRepairScopeKeepsEligibleCatalogAndRestrictsExecution$|^TestPiPreflight' -v -count=1 -timeout=10m
FROM deps AS backend-runtime-phase-test
COPY backend ./
COPY plugin-packages /src/plugin-packages
COPY agent/harness /src/agent/harness
COPY web/test/fixtures /src/web/test/fixtures
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=tmpfs,target=/tmp \
    CGO_ENABLED=1 go test ./internal/app -run '^TestAgentParityRuntimePhasePreservesStorageErrorAndLeaseLoss$|^TestPiEventScheduler(PhaseFencingAfterSameOwnerTakeover|DurableToolAdmissionAndReceiptRecovery)$' -v -count=1 -timeout=10m

FROM deps AS build
COPY backend ./
COPY VERSION /src/VERSION
ARG BUILD_COMMIT=working-tree
ARG BUILD_TIME=unknown
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -trimpath -ldflags="-X infinite-canvas/backend/internal/buildinfo.Version=$(tr -d '\r\n' </src/VERSION) -X infinite-canvas/backend/internal/buildinfo.Commit=$BUILD_COMMIT -X infinite-canvas/backend/internal/buildinfo.BuildTime=$BUILD_TIME" -o /out/backend ./cmd/server

FROM golang:1.25-bookworm
WORKDIR /app
COPY --from=build /out/backend /usr/local/bin/infinite-canvas-backend
COPY plugin-packages /app/plugin-packages
ENV CANVAS_BACKEND_ADDR=:8080 CANVAS_BACKEND_DATA_DIR=/data GIN_MODE=release
EXPOSE 8080
CMD ["infinite-canvas-backend"]
