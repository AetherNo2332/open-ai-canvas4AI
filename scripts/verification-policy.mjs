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
      directory: "backend/internal/canvas/connection",
      importPath: "yingce/backend/internal/canvas/connection",
      group: "canvas-agent",
      pattern:
        "^(TestAgentParityGraphMatchesManualGenericAssociations|TestAgentGraphRejectsInvalidReferences|TestCloudAgentArrangeNodes|TestCloudAgentAddedNodeWithoutCoordinatesLandsOrdered|TestCloudAgentUpdateNodePositionPatch)",
    },
    {
      directory: "backend/internal/canvas/layout",
      importPath: "yingce/backend/internal/canvas/layout",
      group: "canvas-agent",
      pattern:
        "^(TestAgentParityGraphMatchesManualGenericAssociations|TestAgentGraphRejectsInvalidReferences|TestCloudAgentArrangeNodes|TestCloudAgentAddedNodeWithoutCoordinatesLandsOrdered|TestCloudAgentUpdateNodePositionPatch)",
    },
    {
      directory: "backend/internal/production",
      importPath: "yingce/backend/internal/production",
      group: "drama-export",
      pattern: "^TestDramaJianyingExport",
    },
    {
      directory: "backend/internal/jianying",
      importPath: "yingce/backend/internal/jianying",
      group: "drama-export",
      pattern: "^TestDramaJianyingExport",
    },
    {
      directory: "backend/internal/ecommercecatalog",
      importPath: "yingce/backend/internal/ecommercecatalog",
      group: "ecommerce",
      pattern: "^(TestEcommerceManagedSkill|TestIndustryDictionary)",
    },
    {
      directory: "backend/internal/ecommerceskills",
      importPath: "yingce/backend/internal/ecommerceskills",
      group: "ecommerce",
      pattern: "^(TestEcommerceManagedSkill|TestIndustryDictionary)",
    },
    {
      directory: "backend/internal/protocol",
      importPath: "yingce/backend/internal/protocol",
      group: "protocol",
      pattern:
        "^(TestProtocolRequestFromInput|TestProtocolResultManifest|TestDeclarativeProtocol|TestProviderTaskNotReadyStrictClassification|TestStreamingAgentParser)",
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
