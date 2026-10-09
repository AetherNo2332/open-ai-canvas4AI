package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentHarnessWorkspaceIsLoadedAndHashed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CANVAS_AGENT_HARNESS_DIR", dir)
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("AGENTS.md", "Project rules")
	write("AGENT.md", "Legacy rules")
	write("SOUL.md", "Assistant voice")
	write("TOOLS.md", "Tool usage notes")
	system, _, err := LoadAgentPolicies()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(system.Text, "Project rules") || !strings.Contains(system.Text, "Assistant voice") || !strings.Contains(system.Text, "Tool usage notes") || strings.Contains(system.Text, "Legacy rules") {
		t.Fatalf("unexpected harness content: %q", system.Text)
	}
	firstHash := system.Hash
	write("AGENTS.md", "Changed project rules")
	system, _, err = LoadAgentPolicies()
	if err != nil || system.Hash == firstHash {
		t.Fatalf("harness change was not reflected in policy hash: %v", err)
	}
}

func TestAgentHarnessRequiresProjectInstructions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CANVAS_AGENT_HARNESS_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "TOOLS.md"), []byte("notes"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadAgentPolicies(); err == nil {
		t.Fatal("tool notes alone must not form a harness")
	}
}

func TestAgentHarnessAcceptsLegacyAgentFilename(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CANVAS_AGENT_HARNESS_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("Legacy project rules"), 0600); err != nil {
		t.Fatal(err)
	}
	system, _, err := LoadAgentPolicies()
	if err != nil || !strings.Contains(system.Text, "Legacy project rules") {
		t.Fatalf("legacy project file was not loaded: %v", err)
	}
}

func TestAgentHarnessRejectsSymlinkAndOversizedFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CANVAS_AGENT_HARNESS_DIR", dir)
	path := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 20*1024+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadAgentPolicies(); err == nil {
		t.Fatal("oversized instructions were accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.md")
	if err := os.WriteFile(target, []byte("rules"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := LoadAgentPolicies(); err == nil {
		t.Fatal("symlinked instructions were accepted")
	}
}

func TestLoadAgentPoliciesUsesDocumentMetadata(t *testing.T) {
	system, media, err := LoadAgentPolicies()
	if err != nil {
		t.Fatal(err)
	}
	// 版本号是策略文件的公开合同：工具披露规则改变时一同更新。
	if system.ID != "cloud-agent-system" || system.Version != 10 || media.ID != "cloud-agent-media" || media.Version != 3 {
		t.Fatalf("unexpected policy metadata: system=%+v media=%+v", system, media)
	}
	if strings.Contains(system.Text, "id: cloud-agent-system") || !strings.HasPrefix(system.Text, "# 影策 Cloud Agent") {
		t.Fatalf("metadata leaked into compiled policy body: %q", system.Text)
	}
}

func TestParsePolicyDocumentRejectsInvalidMetadata(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
	}{
		{"missing header", "# policy"},
		{"unclosed header", "---\nid: policy\nversion: 1\n# body"},
		{"missing id", "---\nversion: 1\n---\n# body"},
		{"invalid version", "---\nid: policy\nversion: latest\n---\n# body"},
		{"duplicate field", "---\nid: policy\nid: other\nversion: 1\n---\n# body"},
		{"unknown field", "---\nid: policy\nversion: 1\nowner: app\n---\n# body"},
		{"empty body", "---\nid: policy\nversion: 1\n---"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, _, err := parsePolicyDocument(test.raw); err == nil {
				t.Fatal("invalid policy document was accepted")
			}
		})
	}
}
