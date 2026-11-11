// Declarative ownership only. Selection remains advisory until T7.
export const policy = {
  module: "yingce/backend",
  packages: [
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
      pattern: "^(TestVideoPoll|TestVideoDownload)",
    },
  ],
  appFiles: {}, // Add only audited exact file -> integration group mappings.
  globalPrefixes: [
    ".github/",
    "scripts/verification-",
    "scripts/verify-before-push",
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
  ],
  contractDocs: [
    "docs/content/docs/backend/",
    "docs/content/docs/overview/docker",
    "docs/content/docs/agent/",
    "docs/content/docs/security/",
  ],
};
