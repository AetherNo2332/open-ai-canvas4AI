// Declarative ownership and audited integration groups shared by local and CI.
export const policy = {
  module: "yingce/backend",
  packages: [
    {
      directory: "backend/internal/canvas/previs",
      importPath: "yingce/backend/internal/canvas/previs",
      group: "previs",
      pattern: "^(TestPrevis|TestCloudAgentPrevis|TestCanvasPrevis)",
    },
    {
      directory: "backend/internal/identity/worker",
      importPath: "yingce/backend/internal/identity/worker",
      group: "worker",
      pattern:
        "^(TestAgentGrantAdapter|TestAgentWorkerScope|TestAgentRunGrant|TestPiAgent)",
    },
    {
      directory: "backend/internal/agentmemory/document",
      importPath: "yingce/backend/internal/agentmemory/document",
      group: "memory",
      pattern:
        "^(TestUserMemory|TestAgentMemory|TestMemory|TestCloudAgentRunFreezesMemory)",
    },
    {
      directory: "backend/internal/generation/polling",
      importPath: "yingce/backend/internal/generation/polling",
      group: "polling",
      pattern:
        "^(TestVideoPoll|TestVideoDownload|TestAsyncImagePoll|TestDeclarative)",
    },
    {
      directory: "backend/internal/providercatalog",
      importPath: "yingce/backend/internal/providercatalog",
      group: "provider-catalog",
      pattern:
        "^(TestAdminProviderCatalog|TestFetchChannelModelCatalog|TestChannelModelCatalogOption|TestInstallProviderCatalog|TestMetasoCatalogBuild)",
    },
    {
      directory: "backend/internal/workbench",
      importPath: "yingce/backend/internal/workbench",
      group: "project-workbench-read",
      pattern:
        "^(TestProjectWorkbenchRead|TestProjectUnitWorkspace|TestProjectOverview|TestProjectCanvasesPage|TestProjectAssetsPage|TestProjectAssetCandidatesPage)",
    },
    {
      directory: "backend/internal/observability",
      importPath: "yingce/backend/internal/observability",
      group: "observability",
      pattern:
        "^(TestAdminSystemPerformance|TestClearAdminRuntimeCache|TestPreviewDiagnosticBundle|TestExportDiagnosticBundle)",
    },
  ],
  appFiles: {}, // Add only audited exact file -> integration group mappings.
  globalPrefixes: [
    ".github/",
    "scripts/verification-",
    "scripts/verify-before-push",
    "scripts/go-test-shard",
    "Dockerfile",
    "docker-compose",
    "nginx.conf",
  ],
  backendShared: [
    "backend/go.mod",
    "backend/go.sum",
    "backend/internal/app/service.go",
    "backend/internal/database/",
    "backend/internal/model/",
    "backend/internal/app/task_",
    // Worker grants are a control-plane security boundary; keep cross-process
    // acceptance until a narrower, independently audited scope is available.
    "backend/internal/identity/worker/",
  ],
  contractDocs: [
    "docs/content/docs/backend/",
    "docs/content/docs/overview/docker",
    "docs/content/docs/agent/",
    "docs/content/docs/security/",
  ],
};
