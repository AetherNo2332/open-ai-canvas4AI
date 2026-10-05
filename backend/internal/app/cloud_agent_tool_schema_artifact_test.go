package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// agentToolSchemaArtifactPath 是 Go/Node 共用的工具 schema 制品。Pi worker 用同一份定义
// 校验"模型请求里的工具"与"服务端合格目录"是否一致，因此这份文件是双方合同的一部分。
func agentToolSchemaArtifactPath(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "..", "agent", "harness", "TOOL_SCHEMA.json")
}

type agentToolSchemaArtifact struct {
	SchemaVersion string           `json:"schemaVersion"`
	Tools         []map[string]any `json:"tools"`
}

// TestAgentToolSchemaArtifactMatchesRuntime 守住共用 schema 的漂移：Go 源码里改动了工具
// 或参数定义，却忘了重新生成制品时，这个用例必须失败。
//
// 更新方式：WRITE_TOOL_SCHEMA=1 go test ./internal/app -run TestAgentToolSchemaArtifactMatchesRuntime
func TestAgentToolSchemaArtifactMatchesRuntime(t *testing.T) {
	// 制品必须是"任意运行配置下工具集合的超集"：单次快照会随权限模式、视觉能力、
	// 技能与记忆开关变化（例如 canvas_inspect_image 只在 VisionEnabled 时暴露）。
	// 用最宽的请求生成，Node 侧才能用"快照 ⊆ 制品 + 参数一致"做校验。
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.VisionEnabled = true
	req.HasMemories = true
	req.SkillIDs = []string{"schema-artifact-skill"}
	artifact := agentToolSchemaArtifact{SchemaVersion: cloudAgentToolSchemaVersion, Tools: append(cloudAgentTools(req), nativeSkillReadToolSchema())}
	for _, request := range []CloudAgentRequest{
		{SubagentEnabled: true, PermissionMode: "read_only", ContextScope: []string{"canvas"}},
		{PermissionMode: "read_only", ContextScope: []string{"canvas"}, subagent: &SubagentRuntime{Depth: 1}},
	} {
		for _, tool := range cloudAgentTools(request) {
			name := stringField(tool["function"].(map[string]any), "name")
			if strings.Contains(name, "subagent") || name == "send_parent_message" {
				artifact.Tools = append(artifact.Tools, tool)
			}
		}
	}
	encoded, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')

	path := agentToolSchemaArtifactPath(t)
	if os.Getenv("WRITE_TOOL_SCHEMA") == "1" {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("已写入 %s（%d 个工具，schema %s）", path, len(artifact.Tools), artifact.SchemaVersion)
		return
	}

	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取共用工具 schema 制品失败（可先运行 WRITE_TOOL_SCHEMA=1 go test ./internal/app -run TestAgentToolSchemaArtifactMatchesRuntime 生成）: %v", err)
	}
	if strings.ReplaceAll(string(stored), "\r\n", "\n") != string(encoded) {
		t.Fatalf("agent/harness/TOOL_SCHEMA.json 与运行时工具定义不一致；请运行 WRITE_TOOL_SCHEMA=1 go test ./internal/app -run TestAgentToolSchemaArtifactMatchesRuntime 重新生成")
	}
	if artifact.SchemaVersion == "" || len(artifact.Tools) == 0 {
		t.Fatalf("工具 schema 制品不完整：version=%q tools=%d", artifact.SchemaVersion, len(artifact.Tools))
	}
}
