package architecture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testBackend(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files["go.mod"] = "module yingce/backend\n\ngo 1.25.0\n"
	for name, body := range files {
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func hasRule(findings []Finding, rule string) bool {
	for _, finding := range findings {
		if finding.Rule == rule {
			return true
		}
	}
	return false
}

func TestAgentScopeMatchesForkLayout(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"internal/app/cloud_agent.go", true},
		{"internal/app/cloud_agent_runtime.go", true},
		{"internal/app/agent_subagent_runtime.go", true},
		{"internal/app/cloud_agent_runtime_test.go", true},
		{"internal/app/provider_text.go", false},
		{"internal/app/canvas_storyboard_split.go", false},
		{"internal/handler/agent.go", true},
		{"internal/handler/internal_agent.go", true},
		{"internal/handler/user_data.go", false},
		{"internal/agentcontext/checkpoint.go", true},
		{"internal/agentmemory/document.go", true},
		{"internal/agentmemoryx/x.go", false},
		{"internal/canvas/commands/service.go", false},
	} {
		if got := InAgentScope(tc.path); got != tc.want {
			t.Errorf("InAgentScope(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestRejectsAgentDomainImportsOfAppAndService(t *testing.T) {
	root := testBackend(t, map[string]string{"internal/agentcontext/bad.go": `package agentcontext
import (
 _ "yingce/backend/internal/app"
 _ "yingce/backend/internal/service/nested"
 _ "yingce/backend/internal/apps"
)
`})
	findings, err := Check(root, Baseline{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 || !hasRule(findings, "dependency") {
		t.Fatalf("dependency imports not caught exactly: %+v", findings)
	}
}

func TestHandlerAndAppMayImportServiceAndApp(t *testing.T) {
	root := testBackend(t, map[string]string{
		"internal/handler/agent.go":     "package handler\nimport _ \"yingce/backend/internal/service\"\n",
		"internal/app/cloud_agent_x.go": "package app\nimport _ \"yingce/backend/internal/app/sub\"\n",
	})
	findings, err := Check(root, Baseline{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if hasRule(findings, "dependency") {
		t.Fatalf("handler/app imports are allowed by the layering: %+v", findings)
	}
}

func TestRejectsAliasedProtocolMapsAndAllowsRawMessage(t *testing.T) {
	root := testBackend(t, map[string]string{"internal/agentcontext/dto.go": `package agentcontext
import "encoding/json"
type hidden = map[string]interface{}
type Response struct { Payload hidden }
func Send(value hidden) {}
type Request struct { Raw json.RawMessage; Fields map[string]json.RawMessage }
func Accept(value Request) json.RawMessage { return value.Raw }
`})
	findings, err := Check(root, Baseline{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 || !hasRule(findings, "untyped_protocol_map") {
		t.Fatalf("want only Response.Payload and Send to fail: %+v", findings)
	}
}

func TestSizeAndComplexityGuardCatchesNewDebt(t *testing.T) {
	root := testBackend(t, map[string]string{"internal/app/cloud_agent_bad.go": "package app\nfunc long(x bool) {\n" + strings.Repeat("if x {}\n", 125) + "}\n" + strings.Repeat("\n", 480)})
	findings, err := Check(root, Baseline{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{"file_lines", "function_lines", "complexity"} {
		if !hasRule(findings, rule) {
			t.Errorf("missed %s: %+v", rule, findings)
		}
	}
}

// 把逻辑塞进闭包不能绕过上限：闭包单独度量，ID 按外层函数名和闭包序号稳定生成。
func TestClosuresAreMeasuredSeparately(t *testing.T) {
	body := "package app\nfunc outer(x bool) {\n\tf := func() {\n" + strings.Repeat("\t\tif x {}\n", 25) + "\t}\n\tf()\n}\n"
	root := testBackend(t, map[string]string{"internal/app/cloud_agent_closure.go": body})
	findings, err := Check(root, Baseline{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].ID != "internal/app/cloud_agent_closure.go:func:outer.func1:complexity" {
		t.Fatalf("closure complexity must be reported on its own ID: %+v", findings)
	}
}

func TestPackageLevelFunctionLiteralsAreMeasured(t *testing.T) {
	body := "package app\nvar handler = func(x bool) {\n" + strings.Repeat("\tif x {}\n", 125) + "}\n"
	root := testBackend(t, map[string]string{"internal/app/cloud_agent_var.go": body})
	findings, err := Check(root, Baseline{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"internal/app/cloud_agent_var.go:func:handler.func1:lines":      true,
		"internal/app/cloud_agent_var.go:func:handler.func1:complexity": true,
	}
	if len(findings) != len(want) || !want[findings[0].ID] || !want[findings[1].ID] {
		t.Fatalf("package-level function literal must be measured: %+v", findings)
	}
}

func TestBaselineCannotHideGrowthAndMustRatchetDown(t *testing.T) {
	root := testBackend(t, map[string]string{"internal/app/cloud_agent_big.go": "package app\n" + strings.Repeat("\n", 610)})
	metrics, err := Scan(root)
	if err != nil || len(metrics) != 1 {
		t.Fatalf("metrics=%+v err=%v", metrics, err)
	}
	baseline := Baseline{Version: 1, Allowances: []Allowance{{ID: metrics[0].ID, Max: metrics[0].Value, Reason: "existing file, reviewed"}}}
	if findings, err := Check(root, baseline); err != nil || len(findings) != 0 {
		t.Fatalf("reviewed unchanged baseline should pass: %+v (%v)", findings, err)
	}
	baseline.Allowances[0].Max--
	if findings, _ := Check(root, baseline); len(findings) == 0 {
		t.Fatal("growth passed ratchet")
	}
	baseline.Allowances[0].Max += 2
	if findings, _ := Check(root, baseline); !hasRule(findings, "stale_baseline") {
		t.Fatalf("reduction must lower baseline: %+v", findings)
	}
}

func TestBaselineRejectsBlanketOrDuplicateExceptions(t *testing.T) {
	for _, body := range []string{
		`{"version":0}`,
		`{"version":1,"allowances":[{"id":"*","max":1,"reason":"r"}]}`,
		`{"version":1,"allowances":[{"id":"internal/app/x.go:file_lines","max":0,"reason":"r"}]}`,
		`{"version":1,"allowances":[{"id":"internal/app/x.go:file_lines","max":1,"reason":" "}]}`,
		`{"version":1,"allowances":[{"id":"same","max":1,"reason":"r"},{"id":"same","max":1,"reason":"r"}]}`,
	} {
		if _, err := DecodeBaseline([]byte(body)); err == nil {
			t.Errorf("invalid baseline accepted: %s", body)
		}
	}
	if _, err := json.Marshal(Baseline{Version: 1}); err != nil {
		t.Fatal(err)
	}
}

// 工作区检查：新增或增长的超标、已改善却没下调的基线都会失败。没有重新生成基线的开关：
// 存量只能下调或删除，新代码必须满足上限。
func TestWorkspaceArchitecture(t *testing.T) {
	body, err := os.ReadFile("baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := DecodeBaseline(body)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := Check(filepath.Join("..", ".."), baseline)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) > 0 {
		t.Fatal("Agent architecture check failed:\n" + FormatFindings(findings))
	}
}
