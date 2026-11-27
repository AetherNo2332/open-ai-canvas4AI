package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func TestCloudAgentGenerationContinuationPrompts(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return strings.ReplaceAll(string(raw), "\r\n", "\n")
	}

	canonical := read("agent-tool-descriptions.md")
	harnessTools := read(agentToolDescriptionsArtifactPath(t))
	mediaPolicy := read(filepath.Join(filepath.Dir(agentToolDescriptionsArtifactPath(t)), "MEDIA_POLICY.md"))
	systemPolicy := read(filepath.Join(filepath.Dir(agentToolDescriptionsArtifactPath(t)), "SYSTEM_POLICY.md"))
	for name, text := range map[string]string{
		"canonical tools": canonical,
		"harness tools":   harnessTools,
		"media policy":    mediaPolicy,
		"system policy":   systemPolicy,
	} {
		if strings.Contains(text, "wait_generations") {
			t.Fatalf("%s still documents wait_generations", name)
		}
	}

	for name, text := range map[string]string{
		"canonical tools": canonical,
		"harness tools":   harnessTools,
	} {
		for _, section := range []string{"finish_run", "generate_media", "storyboard"} {
			body := markdownToolSection(t, text, section)
			switch section {
			case "finish_run":
				assertContainsAll(t, name+" finish_run", body, "continuation", "all_generations_terminal", "instruction")
			case "generate_media":
				assertContainsAll(t, name+" generate_media", body, "后台", "finish_run.continuation", "不要等待", "轮询")
			case "storyboard":
				assertContainsAll(t, name+" storyboard", body, "后台", "finish_run.continuation")
			}
		}
	}

	assertContainsAll(t, "media policy", mediaPolicy, "后台", "all_generations_terminal", "轮询", "不自动重新收费")
	assertContainsAll(t, "system policy", systemPolicy, "finish_run.continuation", "本批实际登记", "不轮询")

	rawSchema := read(agentToolSchemaArtifactPath(t))
	var artifact agentToolSchemaArtifact
	if err := json.Unmarshal([]byte(rawSchema), &artifact); err != nil {
		t.Fatalf("decode tool schema artifact: %v", err)
	}
	if artifact.SchemaVersion != "cloud-agent-tools/v30" {
		t.Fatalf("tool schema version = %q, want cloud-agent-tools/v30", artifact.SchemaVersion)
	}
	if strings.Contains(rawSchema, "wait_generations") {
		t.Fatal("tool schema artifact still exposes wait_generations")
	}
	var finish map[string]any
	for _, tool := range artifact.Tools {
		function, _ := tool["function"].(map[string]any)
		if stringField(function, "name") == "finish_run" {
			finish = function
			break
		}
	}
	if finish == nil {
		t.Fatal("finish_run is missing from the tool schema artifact")
	}
	properties, _ := finish["parameters"].(map[string]any)["properties"].(map[string]any)
	continuation, _ := properties["continuation"].(map[string]any)
	continuationProperties, _ := continuation["properties"].(map[string]any)
	when, _ := continuationProperties["when"].(map[string]any)
	values, _ := when["enum"].([]any)
	if len(values) != 1 || values[0] != model.AgentGenerationContinuationAllTerminal {
		t.Fatalf("finish_run.continuation.when enum = %#v", values)
	}

	wire := read(filepath.Join(filepath.Dir(agentToolSchemaArtifactPath(t)), "PI_WIRE_IDENTITY.json"))
	if !strings.Contains(wire, `"protocolVersion": "canvas-pi-wire/v8"`) {
		t.Fatalf("wire identity was not bumped to v8: %s", wire)
	}
	if cloudAgentToolSchemaVersion != "cloud-agent-tools/v30" {
		t.Fatalf("runtime tool schema version = %q", cloudAgentToolSchemaVersion)
	}
}

func markdownToolSection(t *testing.T, text, name string) string {
	t.Helper()
	start := strings.Index(text, "## "+name+"\n")
	if start < 0 {
		t.Fatalf("missing ## %s section", name)
	}
	body := text[start+len("## "+name+"\n"):]
	if end := strings.Index(body, "\n## "); end >= 0 {
		body = body[:end]
	}
	return strings.TrimSpace(body)
}

func assertContainsAll(t *testing.T, label, text string, tokens ...string) {
	t.Helper()
	for _, token := range tokens {
		if !strings.Contains(text, token) {
			t.Fatalf("%s is missing %q", label, token)
		}
	}
}
