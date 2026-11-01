package architecture

import (
	"strings"
	"testing"
)

func TestScopeCoversBackendProductionCode(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"internal/app/provider_text.go", true},
		{"internal/canvas/commands/service.go", true},
		{"cmd/server/main.go", true},
		{"payment-plugins/main.go", true},
		{"internal/app/provider_text_test.go", false},
		{"internal/canvas/references/testdata/bad.go", false},
		{"third_party/go-sms-sender/sender.go", false},
		{"internal/app/notes.md", false},
	} {
		if got := InScope(tc.path); got != tc.want {
			t.Errorf("InScope(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// 分层规则沿用 AGENTS.md 第 3 节的调用链：域包与 repository 不得回头依赖上层，handler 不直连 app/repository/database。
func TestLayerRulesFollowCallChain(t *testing.T) {
	root := testBackend(t, map[string]string{
		"internal/handler/user.go":      "package handler\nimport (\n _ \"yingce/backend/internal/service\"\n _ \"yingce/backend/internal/repository\"\n)\n",
		"internal/repository/user.go":   "package repository\nimport _ \"yingce/backend/internal/app\"\n",
		"internal/model/user.go":        "package model\nimport _ \"yingce/backend/internal/database\"\n",
		"internal/canvas/tasks/task.go": "package tasks\nimport (\n _ \"yingce/backend/internal/handler\"\n _ \"yingce/backend/internal/repository\"\n _ \"yingce/backend/internal/model\"\n)\n",
		"internal/service/aliases.go":   "package service\nimport _ \"yingce/backend/internal/app\"\n",
		"internal/app/root.go":          "package app\nimport _ \"yingce/backend/internal/canvas/tasks\"\n",
		"cmd/server/main.go":            "package main\nimport (\n _ \"yingce/backend/internal/handler\"\n _ \"yingce/backend/internal/database\"\n)\n",
	})
	findings, err := Check(root, Baseline{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"internal/handler/user.go:import:yingce/backend/internal/repository":   true,
		"internal/repository/user.go:import:yingce/backend/internal/app":       true,
		"internal/model/user.go:import:yingce/backend/internal/database":       true,
		"internal/canvas/tasks/task.go:import:yingce/backend/internal/handler": true,
	}
	got := map[string]bool{}
	for _, finding := range findings {
		if finding.Rule == "dependency" {
			got[finding.ID] = true
		}
	}
	if len(got) != len(want) {
		t.Fatalf("dependency findings = %v, want %v", got, want)
	}
	for id := range want {
		if !got[id] {
			t.Fatalf("missing dependency finding %s in %v", id, got)
		}
	}
}

func TestDependencyFindingsCannotBeBaselined(t *testing.T) {
	root := testBackend(t, map[string]string{"internal/repository/user.go": "package repository\nimport _ \"yingce/backend/internal/app\"\n"})
	id := "internal/repository/user.go:import:yingce/backend/internal/app"
	findings, err := Check(root, Baseline{Version: 1, Allowances: []Allowance{{ID: id, Max: 1, Reason: "legacy"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].ID != id || findings[0].Rule != "dependency" {
		t.Fatalf("dependency allowance must not hide the import: %+v", findings)
	}
}

// 导出 map 规则只约束云端 Agent 协议；其余后端代码只受规模、复杂度与分层约束。
func TestUntypedMapRuleOnlyAppliesToAgentCode(t *testing.T) {
	root := testBackend(t, map[string]string{
		"internal/app/provider_text.go":   "package app\nfunc Export(value map[string]any) {}\n",
		"internal/app/cloud_agent_api.go": "package app\nfunc ExportAgent(value map[string]any) {}\n",
	})
	findings, err := Check(root, Baseline{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].ID != "internal/app/cloud_agent_api.go:signature:ExportAgent" {
		t.Fatalf("only the Agent export should fail: %+v", findings)
	}
}

func TestSizeRulesApplyOutsideAgentCode(t *testing.T) {
	root := testBackend(t, map[string]string{"internal/repository/big.go": "package repository\nfunc long(x bool) {\n" + strings.Repeat("if x {}\n", 125) + "}\n"})
	findings, err := Check(root, Baseline{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !hasRule(findings, "function_lines") || !hasRule(findings, "complexity") {
		t.Fatalf("backend-wide size and complexity rules missed: %+v", findings)
	}
}
