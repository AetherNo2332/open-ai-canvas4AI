package handler

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"yingce/backend/internal/service"
)

// The public acceptance chain must use the shipped Node/Pi loop. Build agent/
// before running this test; a missing runtime is a failure, never Go fallback.
func startChainPiWorker(t *testing.T, router *gin.Engine, svc *service.Service) {
	t.Helper()
	workerDir, err := filepath.Abs("../../../agent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workerDir, "dist/src/server.js")); err != nil {
		if os.Getenv("CANVAS_PI_EVIDENCE_DIR") != "" {
			t.Fatal("required Pi runtime missing: ", err)
		}
		t.Skip("HTTP acceptance requires built agent/: npm ci --ignore-scripts && npm run build")
	}
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", strings.Repeat("i", 48))
	t.Setenv("CANVAS_AGENT_WORKER_ID", "chain-pi-worker")
	t.Setenv("CANVAS_AGENT_WORKER_TOKEN", strings.Repeat("w", 48))
	router.Use(func(c *gin.Context) {
		w := &chainDiagnosticWriter{ResponseWriter: c.Writer}
		c.Writer = w
		c.Next()
		if c.Writer.Status() >= 400 {
			t.Logf("bridge %s %s: %d %s", c.Request.Method, c.Request.URL.Path, c.Writer.Status(), w.body.String())
		}
	})
	RegisterInternalAgentRoutes(router, svc)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	logDir := os.Getenv("CANVAS_PI_EVIDENCE_DIR")
	if logDir == "" {
		logDir = t.TempDir()
	}
	if err := os.MkdirAll(logDir, 0700); err != nil {
		t.Fatal(err)
	}
	log, err := os.CreateTemp(logDir, "node-worker-*.log")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "dist/src/server.js")
	cmd.Dir = workerDir
	cmd.Env = append(os.Environ(), "CANVAS_BACKEND_INTERNAL_URL="+server.URL,
		"CANVAS_AGENT_HARNESS_DIR="+filepath.Join(workerDir, "harness"),
		"CANVAS_AGENT_HEALTH_FILE="+filepath.Join(logDir, "health.json"))
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait(); _ = log.Close() })
	t.Logf("real Pi worker pid=%d transport=%s execution=%s log=%s", cmd.Process.Pid, internalAgentWireVersion, internalAgentExecutionVersion, log.Name())
}

func writeChainPiEvidence(t *testing.T, db *gorm.DB) {
	t.Helper()
	dir := os.Getenv("CANVAS_PI_EVIDENCE_DIR")
	if dir == "" {
		return
	}
	evidence := map[string]any{"transport": internalAgentWireVersion, "execution": internalAgentExecutionVersion, "recordedAt": time.Now().UTC().Format(time.RFC3339)}
	// Explicit columns prevent model credentials, request payloads or run grants
	// from leaking into a shareable acceptance artifact.
	for table, columns := range map[string]string{
		"cloud_agent_executions":  "id,user_id,conversation_id,status,engine,lease_owner,revision",
		"cloud_agent_pi_sessions": "id,user_id,conversation_id,active_run_id,lease_epoch,revision",
		"agent_sessions":          "id,owner_id,definition_id,application,space_type,space_id,work_item_id",
		"agent_assignments":       "id,work_item_id,executor_session_id,epoch,status",
		"agent_work_items":        "id,root_work_id,status,budget_grant_id,authorization_version",
		"agent_approvals":         "id,status,work_item_id,authorization_version",
		"agent_operations":        "operation_key,work_item_id,name,status,operation_ref",
		"billing_orders":          "id,user_id,task_id,status,capability,actual_amount_microcredits,reserved_amount_microcredits,refunded_amount_microcredits",
		"platform_budget_charges": "id,owner_id,root_id,grant_id,billing_order_id,task_id",
	} {
		var rows []map[string]any
		if err := db.Table(table).Select(columns).Find(&rows).Error; err != nil {
			t.Errorf("evidence %s: %v", table, err)
			continue
		}
		evidence[table] = rows
	}
	var entries int64
	if err := db.Table("cloud_agent_pi_entries").Count(&entries).Error; err != nil {
		t.Error(err)
	}
	evidence["piEntryCount"] = entries
	var runs []struct {
		ID        string
		StateJSON string
	}
	if err := db.Table("cloud_agent_executions").Select("id,state_json").Find(&runs).Error; err != nil {
		t.Error(err)
	}
	bindings := map[string]json.RawMessage{}
	for _, run := range runs {
		var state struct {
			Execution json.RawMessage `json:"execution"`
		}
		if err := json.Unmarshal([]byte(run.StateJSON), &state); err != nil {
			t.Error(err)
			continue
		}
		bindings[run.ID] = state.Execution
	}
	evidence["executionBindings"] = bindings
	evidence["passed"] = !t.Failed()
	raw, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "chain-evidence.json"), raw, 0600); err != nil {
		t.Error(err)
	}
}

type chainDiagnosticWriter struct {
	gin.ResponseWriter
	body bytes.Buffer
}

func (w *chainDiagnosticWriter) Write(b []byte) (int, error) {
	if w.Status() >= 400 {
		w.body.Write(b)
	}
	return w.ResponseWriter.Write(b)
}
