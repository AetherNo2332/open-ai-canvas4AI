package hostupdate

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type docsRunner struct {
	mu    sync.Mutex
	calls []string
	fail  string
}

func (r *docsRunner) Run(_ context.Context, _ string, args, _ []string, out, _ io.Writer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	call := strings.Join(args, " ")
	r.calls = append(r.calls, call)
	if r.fail != "" && strings.Contains(call, r.fail) {
		return errors.New("fixture failure")
	}
	if strings.Contains(call, "config --services") {
		io.WriteString(out, "backend\nweb\nagent\ndocs\n")
	}
	if strings.Contains(call, "image inspect") {
		if strings.Contains(call, ".RepoDigests") {
			json.NewEncoder(out).Encode([]string{"ghcr.io/aetherno2332/open-ai-canvas-docs@sha256:" + strings.Repeat("b", 64)})
			return nil
		}
		commit := strings.Repeat("b", 40)
		if strings.Contains(call, "sha256:"+strings.Repeat("a", 64)) {
			commit = strings.Repeat("a", 40)
		}
		build := DocsBuild{BuildID: "docs-" + commit, Commit: commit, BuiltAt: "2026-11-13T00:00:00Z", DeploymentContract: 1}
		raw, _ := json.Marshal(build)
		json.NewEncoder(out).Encode(map[string]string{"tv.yingce.docs.build": string(raw)})
	}
	return nil
}
func docsFixture(t *testing.T) (*Manager, *docsRunner, string, string) {
	t.Helper()
	dir := t.TempDir()
	old := "ghcr.io/aetherno2332/open-ai-canvas-docs@sha256:" + strings.Repeat("a", 64)
	target := "ghcr.io/aetherno2332/open-ai-canvas-docs@sha256:" + strings.Repeat("b", 64)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("CANVAS_IMAGE_TAG=1.8.3\nKEEP=untouched\nCANVAS_DOCS_IMAGE="+old+"\n"), 0600)
	os.WriteFile(filepath.Join(dir, "docker-compose.deploy.yml"), []byte("services:\n  docs:\n    image: test\n    labels:\n      tv.yingce.docs.contract: '1'\n"), 0600)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/api/health") {
			values, _ := readEnvFile(filepath.Join(dir, ".env"))
			commit := strings.Repeat("a", 40)
			if values["CANVAS_DOCS_IMAGE"] == target {
				commit = strings.Repeat("b", 40)
			}
			status := "ok"
			if _, err := os.Stat(filepath.Join(dir, "fail-health")); err == nil && values["CANVAS_DOCS_IMAGE"] == target {
				status = "error"
			}
			json.NewEncoder(w).Encode(DocsBuild{Status: status, BuildID: "docs-" + commit, Commit: commit, BuiltAt: "2026-11-13T00:00:00Z", DeploymentContract: 1})
			return
		}
		if strings.Contains(r.URL.Path, "search") {
			io.WriteString(w, "[{\"id\":\"sdk\"}]")
			return
		}
		io.WriteString(w, "<html>影策文档</html>")
	}))
	t.Cleanup(server.Close)
	m, err := NewManager(Config{InstallDir: dir, StateDir: filepath.Join(dir, "state"), HealthURL: server.URL + "/api/health", StepTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	runner := &docsRunner{}
	m.runner = runner
	return m, runner, old, target
}

func waitDocs(t *testing.T, m *Manager) DocsStatus {
	t.Helper()
	for i := 0; i < 500; i++ {
		status := m.DocsSnapshot()
		if !status.Operation.Phase.Active() {
			return status
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("docs update did not finish")
	return DocsStatus{}
}

func TestDocsHealthFailureRestoresSourceOnly(t *testing.T) {
	m, r, old, target := docsFixture(t)
	os.WriteFile(filepath.Join(m.config.InstallDir, "fail-health"), []byte("fixture"), 0600)
	m.StartDocsUpdate(DocsUpdateRequest{TargetDigest: target, ExpectedCurrentDigest: old})
	status := waitDocs(t, m)
	if status.Operation.Phase != PhaseRolledBack || status.Current.Digest != old {
		t.Fatalf("%+v", status)
	}
	values, _ := readEnvFile(m.envPath())
	if values["CANVAS_IMAGE_TAG"] != "1.8.3" || values["CANVAS_DOCS_IMAGE"] != old {
		t.Fatal(values)
	}
	for _, call := range r.calls {
		if strings.Contains(call, "migrate") || strings.Contains(call, "stop") || strings.Contains(call, "--remove-orphans") {
			t.Fatal(call)
		}
	}
}

func TestDocsRollbackUsesPreviousDigest(t *testing.T) {
	m, _, old, target := docsFixture(t)
	m.StartDocsUpdate(DocsUpdateRequest{TargetDigest: target, ExpectedCurrentDigest: old})
	if waitDocs(t, m).Operation.Phase != PhaseSucceeded {
		t.Fatal(m.DocsSnapshot())
	}
	if _, err := m.StartDocsRollback(DocsRollbackRequest{ExpectedCurrentDigest: old, Reason: "stale"}); err == nil {
		t.Fatal("accepted stale rollback")
	}
	if _, err := m.StartDocsRollback(DocsRollbackRequest{ExpectedCurrentDigest: target, Reason: "restore"}); err != nil {
		t.Fatal(err)
	}
	status := waitDocs(t, m)
	if status.Operation.Phase != PhaseSucceeded || status.Current.Digest != old || status.Previous.Digest != target {
		t.Fatalf("%+v", status)
	}
}

func TestDocsRecoveryAfterEnvironmentSwitch(t *testing.T) {
	m, _, old, target := docsFixture(t)
	source, err := m.inspectDocs(context.Background(), old)
	if err != nil {
		t.Fatal(err)
	}
	m.docs.Operation = DocsOperation{Phase: PhaseSwitching, Source: &source, Target: &DocsBuild{Digest: target}, Switched: true}
	if err = m.saveDocsLocked(); err != nil {
		t.Fatal(err)
	}
	setEnvValue(m.envPath(), "CANVAS_DOCS_IMAGE", target)
	if err = m.loadDocsState(); err != nil {
		t.Fatal(err)
	}
	m.recoverDocs()
	status := m.DocsSnapshot()
	if status.Operation.Phase != PhaseRolledBack || status.Current.Digest != old {
		t.Fatal(status)
	}
}
func TestDocsUpdateOnlyRecreatesDocs(t *testing.T) {
	m, r, old, target := docsFixture(t)
	if _, err := m.StartDocsUpdate(DocsUpdateRequest{TargetDigest: target, ExpectedCurrentDigest: old}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		if !m.DocsSnapshot().Operation.Phase.Active() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	status := m.DocsSnapshot()
	if status.Operation.Phase != PhaseSucceeded {
		t.Fatalf("%+v", status)
	}
	env, _ := readEnvFile(m.envPath())
	if env["CANVAS_IMAGE_TAG"] != "1.8.3" || env["KEEP"] != "untouched" || env["CANVAS_DOCS_IMAGE"] != target {
		t.Fatal(env)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.calls {
		if strings.Contains(call, "--remove-orphans") || strings.Contains(call, " migrate") || strings.Contains(call, "stop ") || strings.HasSuffix(call, "backend") || strings.HasSuffix(call, "web") {
			t.Fatal(call)
		}
	}
	if !strings.Contains(strings.Join(r.calls, "\n"), "--no-deps") {
		t.Fatal(r.calls)
	}
	if status.Previous == nil || status.Previous.Digest != old {
		t.Fatal(status)
	}
}
func TestDocsRejectCASForeignImageAndApplicationConflict(t *testing.T) {
	m, _, old, target := docsFixture(t)
	for _, req := range []DocsUpdateRequest{{TargetDigest: target, ExpectedCurrentDigest: "wrong"}, {TargetDigest: "ghcr.io/evil/docs@sha256:" + strings.Repeat("a", 64), ExpectedCurrentDigest: old}} {
		if _, err := m.StartDocsUpdate(req); err == nil {
			t.Fatal("accepted invalid update")
		}
	}
	m.state.Operation.Phase = PhaseMigrating
	if _, err := m.StartDocsUpdate(DocsUpdateRequest{TargetDigest: target, ExpectedCurrentDigest: old}); err == nil {
		t.Fatal("concurrent update")
	}
}
func TestDocsPullFailurePreservesEnvironment(t *testing.T) {
	m, r, old, target := docsFixture(t)
	r.fail = "pull"
	before, _ := os.ReadFile(m.envPath())
	m.StartDocsUpdate(DocsUpdateRequest{TargetDigest: target, ExpectedCurrentDigest: old})
	for i := 0; i < 200; i++ {
		if !m.DocsSnapshot().Operation.Phase.Active() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	after, _ := os.ReadFile(m.envPath())
	if string(before) != string(after) {
		t.Fatal("env changed")
	}
	if m.DocsSnapshot().Operation.Phase != PhaseFailed {
		t.Fatal(m.DocsSnapshot())
	}
}

func TestDocsApplicationKeepsIndependentDigest(t *testing.T) {
	m, _, old, _ := docsFixture(t)
	path := filepath.Join(m.config.InstallDir, "next.yml")
	os.WriteFile(path, []byte("services:\n  docs:\n    labels:\n      tv.yingce.docs.contract: '1'\n"), 0600)
	digest, err := m.applicationDocs(path, "v9.0.0")
	if err != nil || digest != old {
		t.Fatalf("digest=%s err=%v", digest, err)
	}
	os.WriteFile(path, []byte("services:\n  docs:\n    labels:\n      tv.yingce.docs.contract: '2'\n"), 0600)
	if _, err = m.applicationDocs(path, "v9.0.0"); err == nil {
		t.Fatal("accepted incompatible contract")
	}
}

func TestDocsCheckResolvesPinnedCandidateAndCurrentIdentity(t *testing.T) {
	m, _, old, target := docsFixture(t)
	status, err := m.CheckDocs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Current == nil || status.Current.Digest != old || status.Candidate == nil || status.Candidate.Digest != target || status.Operation.Phase != PhaseReady {
		t.Fatalf("%+v", status)
	}
	values, _ := readEnvFile(m.envPath())
	if values["CANVAS_DOCS_IMAGE"] != old {
		t.Fatal("check changed deployment")
	}
}

func TestDocsRecoveryBeforeSwitchDoesNotTouchContainers(t *testing.T) {
	m, r, old, target := docsFixture(t)
	m.docs.Operation = DocsOperation{Phase: PhasePulling, Source: &DocsBuild{Digest: old}, Target: &DocsBuild{Digest: target}}
	if err := m.saveDocsLocked(); err != nil {
		t.Fatal(err)
	}
	m.recoverDocs()
	if m.DocsSnapshot().Operation.Phase != PhaseFailed {
		t.Fatal(m.DocsSnapshot())
	}
	if len(r.calls) != 0 {
		t.Fatal(r.calls)
	}
	values, _ := readEnvFile(m.envPath())
	if values["CANVAS_DOCS_IMAGE"] != old {
		t.Fatal(values)
	}
}

func TestDocsServerRequiresAuthenticationAndStrictBody(t *testing.T) {
	m, _, _, _ := docsFixture(t)
	server, err := NewServer(m, strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/v1/docs/status", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatal(response.Code)
	}
	request = httptest.NewRequest("POST", "/v1/docs/update", strings.NewReader("{\"unknown\":true}"))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 32))
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatal(response.Code)
	}
}
