package handler

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"
)

func TestAgentWakeClaimRacePostgres(t *testing.T) {
	if os.Getenv("TEST_AGENT_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	for _, mode := range []string{"poll", "notify"} {
		t.Run(mode, func(t *testing.T) { runWakeClaimRecovery(t, mode) })
	}
}

func runWakeClaimRecovery(t *testing.T, mode string) {
	t.Helper()
	t.Setenv("CANVAS_AGENT_WAKE_MODE", mode)
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	t.Setenv("CANVAS_AGENT_HARNESS_DIR", "")
	t.Setenv("REDIS_URL", "")
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", strings.Repeat("i", 48))
	t.Setenv("CANVAS_AGENT_WORKER_USER_ID", "claim-user")
	db := agentSDKOpenDatabase(t)
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	svc := service.New(repository.New(db), t.TempDir())
	svc.StartWorker()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := svc.StopWorker(ctx); err != nil {
			t.Error(err)
		}
		_ = svc.Close()
	})
	const canvasJSON = `{"connections":[],"nodes":[{"id":"source","metadata":{"content":"Wake source"},"type":"text"}]}`
	var calls, resumes atomic.Int32
	upstream := httptest.NewServer(wakeClaimModel(&calls, &resumes, fmt.Sprintf("%x", sha256.Sum256([]byte(canvasJSON)))))
	defer upstream.Close()
	seedFourCapabilityChannels(t, db, upstream.URL)
	for _, row := range []any{
		&model.User{ID: "claim-user", Username: "claim-user", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.CreditAccount{UserID: "claim-user", AvailableMicrocredits: 100_000_000},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	canvas := model.CanvasProject{ID: "claim-canvas", UserID: "claim-user", PayloadJSON: canvasJSON}
	if err := repository.New(db).UpsertCanvasProject(&canvas); err != nil {
		t.Fatal(err)
	}
	workers := []string{"claim-a", "claim-b"}
	for _, worker := range workers {
		t.Setenv("CANVAS_AGENT_WORKER_ID", worker)
		t.Setenv("CANVAS_AGENT_WORKER_TOKEN", strings.Repeat(worker, 8))
		if err := svc.SeedAgentWorker(); err != nil {
			t.Fatal(err)
		}
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if strings.HasSuffix(c.Request.URL.Path, "/model-preflight") {
			body, _ := io.ReadAll(io.LimitReader(c.Request.Body, 2<<20))
			c.Request.Body = io.NopCloser(bytes.NewReader(body))
			var input service.PiModelStepRequest
			var execution model.CloudAgentExecution
			var state struct{ Canonical struct{ SystemPrompt string } }
			if json.Unmarshal(body, &input) == nil && db.First(&execution, "id = ?", c.Param("id")).Error == nil && json.Unmarshal([]byte(execution.StateJSON), &state) == nil {
				expected, actual := strings.TrimSpace(state.Canonical.SystemPrompt), input.Canonical.SystemPrompt
				prefix := 0
				for prefix < min(len(expected), len(actual)) && expected[prefix] == actual[prefix] {
					prefix++
				}
				t.Logf("preflight common prefix=%d replacement characters stored=%d sent=%d CRLF stored=%d sent=%d", prefix, strings.Count(expected, "\ufffd"), strings.Count(actual, "\ufffd"), strings.Count(expected, "\r\n"), strings.Count(actual, "\r\n"))
				t.Logf("preflight policy bytes stored=%d sent=%d hash stored=%x sent=%x included=%t after LF normalization=%t", len(expected), len(actual), sha256.Sum256([]byte(expected)), sha256.Sum256([]byte(actual)), strings.Contains(actual, expected), strings.Contains(strings.ReplaceAll(actual, "\r\n", "\n"), strings.ReplaceAll(expected, "\r\n", "\n")))
			}
		}
		c.Next()
	})
	RegisterInternalAgentRoutes(router, svc)
	server := httptest.NewServer(router)
	defer server.Close()
	request := service.CloudAgentRequest{CanvasID: "claim-canvas", Prompt: "Add the authorized recovery test note.", Model: "text-e2e", ChannelID: "sdk-text", ChannelModelKey: "text-e2e", PermissionMode: "auto", ContextScope: []string{"canvas"}, IdempotencyKey: "wake-claim-recovery"}
	request.Budget.MaxCredits = 1
	run, err := svc.CreateCloudAgentRun("claim-user", request, "")
	if err != nil {
		t.Fatal(err)
	}
	winner := raceAgentWakeClaims(t, server, workers, run.ID, "slot")
	var admitted model.CloudAgentExecution
	if err := db.First(&admitted, "id = ?", run.ID).Error; err != nil {
		t.Fatal(err)
	}
	var stored struct{ Canonical struct{ SystemPrompt string } }
	if err := json.Unmarshal([]byte(admitted.StateJSON), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Canonical.SystemPrompt != winner.snapshot.Canonical.SystemPrompt {
		t.Fatalf("claim changed frozen policy: stored=%x snapshot=%x", sha256.Sum256([]byte(stored.Canonical.SystemPrompt)), sha256.Sum256([]byte(winner.snapshot.Canonical.SystemPrompt)))
	}
	t.Logf("claim policy equals persisted policy: bytes=%d sha256=%x", len(stored.Canonical.SystemPrompt), sha256.Sum256([]byte(stored.Canonical.SystemPrompt)))
	assertWakeClaimLease(t, db, winner, 1)
	worker, lines := startWakeClaimWorker(t, server, winner, "checkpoint")
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		var execution model.CloudAgentExecution
		_ = db.First(&execution, "id = ?", run.ID).Error
		var tasks []model.Task
		_ = db.Select("id", "status", "error").Where("project_id = ?", "claim-canvas").Find(&tasks).Error
		t.Logf("run status=%s failure=%q model requests=%d", execution.Status, execution.FailureMessage, calls.Load())
		for _, task := range tasks {
			t.Logf("task %s status=%s error=%q", task.ID, task.Status, task.Error)
		}
	})
	agentSDKAwaitLine(t, lines, "CHECKPOINTED")
	receipt := wakeClaimToolEntry(t, db, run.ID)
	mutation := assertWakeMutation(t, db, run.ID, canvas.Revision+1)
	if !strings.Contains(receipt.EntryJSON, `"toolName":"canvas_apply_ops"`) || !strings.Contains(receipt.EntryJSON, `"isError":false`) || calls.Load() != 1 {
		t.Fatalf("expected one model call and a durable successful canvas receipt, calls=%d", calls.Load())
	}
	if err := worker.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = worker.Wait()
	var interrupted model.CloudAgentExecution
	if err := db.First(&interrupted, "id = ?", run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if interrupted.Status != "running" || interrupted.LeaseExpiresAt == nil {
		t.Fatalf("interruption did not leave a live unfinished run: %s", interrupted.Status)
	}
	t.Logf("durable checkpoint=%s mutation=%s; awaiting natural lease expiry", receipt.EntryID, mutation.ID)
	time.Sleep(max(0, time.Until(*interrupted.LeaseExpiresAt)+100*time.Millisecond))
	replacement := raceAgentWakeClaims(t, server, workers, run.ID, "resume")
	assertWakeClaimLease(t, db, replacement, 2)
	if replacement.snapshot.PiSessionID != winner.snapshot.PiSessionID || replacement.snapshot.PiActiveLeafID != receipt.EntryID {
		t.Fatal("replacement did not recover the original Pi checkpoint")
	}
	assertWakeOldLeaseDenied(t, server, winner)
	resumed, resumedLines := startWakeClaimWorker(t, server, replacement, "resume")
	agentSDKAwaitLine(t, resumedLines, "FINISHED")
	if err := resumed.Wait(); err != nil {
		t.Fatal(err)
	}
	var execution model.CloudAgentExecution
	if err := db.First(&execution, "id = ?", run.ID).Error; err != nil {
		t.Fatal(err)
	}
	finalReceipt := wakeClaimToolEntry(t, db, run.ID)
	finalMutation := assertWakeMutation(t, db, run.ID, canvas.Revision+1)
	if finalMutation.ID != mutation.ID || finalReceipt.EntryID != receipt.EntryID || finalReceipt.EntryJSON != receipt.EntryJSON || calls.Load() != 2 || resumes.Load() != 1 || execution.Status != "completed" {
		t.Fatalf("recovery assertions: model calls=%d saw original tool result=%d status=%s same mutation=%t same Pi receipt=%t", calls.Load(), resumes.Load(), execution.Status, finalMutation.ID == mutation.ID, finalReceipt.EntryID == receipt.EntryID && finalReceipt.EntryJSON == receipt.EntryJSON)
	}
	t.Logf("same run=%s session=%s checkpoint=%s; old lease rejected; second model request carries original tool result; mutation=%s and canvas revision=%d unchanged", run.ID, winner.snapshot.PiSessionID, receipt.EntryID, mutation.ID, canvas.Revision+1)
}

func wakeClaimModel(calls, resumes *atomic.Int32, snapshot string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []struct {
				Role       string
				ToolCallID string `json:"tool_call_id"`
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "invalid test model request", http.StatusBadRequest)
			return
		}
		var delta map[string]any
		finish := "stop"
		if calls.Add(1) == 1 {
			delta = agentSDKToolDelta("canvas_apply_ops", fmt.Sprintf(`{"snapshotHash":%q,"ops":[{"type":"add_node","id":"wake-note","nodeType":"text","title":"Wake recovery note","content":"Written once"}]}`, snapshot))
			finish = "tool_calls"
		} else {
			for _, message := range input.Messages {
				if message.Role == "tool" && message.ToolCallID == "sdk-canvas_apply_ops" {
					resumes.Add(1)
					break
				}
			}
			delta = map[string]any{"content": "The recovery note was written."}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}})
		end, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 30, "completion_tokens": 10, "total_tokens": 40}})
		fmt.Fprintf(w, "data: %s\n\ndata: %s\n\ndata: [DONE]\n\n", chunk, end)
	})
}

func assertWakeMutation(t *testing.T, db *gorm.DB, runID string, revision int64) model.CloudAgentCanvasMutation {
	t.Helper()
	var canvas model.CanvasProject
	var mutations []model.CloudAgentCanvasMutation
	if err := db.First(&canvas, "id = ?", "claim-canvas").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("run_id = ? AND operation = ?", runID, "canvas_apply_ops").Find(&mutations).Error; err != nil {
		t.Fatal(err)
	}
	if canvas.Revision != revision || strings.Count(canvas.PayloadJSON, `"id":"wake-note"`) != 1 || len(mutations) != 1 {
		t.Fatalf("expected exactly one canvas write: revision=%d want=%d mutation rows=%d", canvas.Revision, revision, len(mutations))
	}
	return mutations[0]
}

type wakeClaim struct {
	owner, authorization string
	snapshot             *service.PiAgentSnapshot
}

func assertWakeClaimLease(t *testing.T, db *gorm.DB, winner wakeClaim, epoch int64) {
	t.Helper()
	var run model.CloudAgentExecution
	var session model.CloudAgentPiSession
	if err := db.First(&run, "id = ?", winner.snapshot.RunID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&session, "active_run_id = ?", run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if run.LeaseOwner != winner.owner || session.LeaseOwner != winner.owner || session.LeaseEpoch != epoch || winner.snapshot.PiSessionLeaseEpoch != epoch ||
		run.LeaseExpiresAt == nil || session.LeaseExpiresAt == nil || !run.LeaseExpiresAt.Equal(*session.LeaseExpiresAt) {
		t.Fatalf("lease mismatch: Run=%s Session=%s epoch=%d, want %s@%d", run.LeaseOwner, session.LeaseOwner, session.LeaseEpoch, winner.owner, epoch)
	}
	t.Logf("two HTTP 2xx; one snapshot, one null; Run and Session leases agree with %s@%d", winner.owner, epoch)
}

func wakeClaimToolEntry(t *testing.T, db *gorm.DB, runID string) model.CloudAgentPiEntry {
	t.Helper()
	var entries []model.CloudAgentPiEntry
	if err := db.Where("run_id = ? AND entry_json LIKE ?", runID, `%"role":"toolResult"%`).Find(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one durable Pi tool result, got %d", len(entries))
	}
	return entries[0]
}

func assertWakeOldLeaseDenied(t *testing.T, server *httptest.Server, claim wakeClaim) {
	t.Helper()
	assertWakeOldLeaseRequestDenied(t, server, claim, "renew", `{}`)
	assertWakeOldLeaseRequestDenied(t, server, claim, "messages", `{"sequence":99,"message":{"role":"assistant","content":[{"type":"text","text":"Stale worker must not advance the run"}]}}`)
}

func assertWakeOldLeaseRequestDenied(t *testing.T, server *httptest.Server, claim wakeClaim, route, body string) {
	t.Helper()
	identity, _, _ := strings.Cut(claim.owner, ":")
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/internal-agent/runs/"+claim.snapshot.RunID+"/"+route, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	setWakeWorkerHeaders(request, identity, claim.authorization)
	request.Header.Set("X-Agent-User-ID", claim.snapshot.UserID)
	request.Header.Set("X-Agent-Worker-ID", claim.owner)
	request.Header.Set("X-Agent-Session-Epoch", fmt.Sprint(claim.snapshot.PiSessionLeaseEpoch))
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var failure struct{ Reason string }
	if err := json.NewDecoder(response.Body).Decode(&failure); err != nil || response.StatusCode != http.StatusForbidden || failure.Reason != "agent_lease_lost" {
		t.Fatalf("expected old lease rejection for /%s, got HTTP %d reason=%q err=%v", route, response.StatusCode, failure.Reason, err)
	}
	t.Logf("stale HTTP /%s rejected: %d %s", route, response.StatusCode, failure.Reason)
}

func startWakeClaimWorker(t *testing.T, server *httptest.Server, claim wakeClaim, phase string) (*exec.Cmd, <-chan string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node.js required for PostgreSQL claim acceptance")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(root, "agent")
	payload, err := json.Marshal(struct {
		Run           *service.PiAgentSnapshot `json:"run"`
		Authorization string                   `json:"runAuthorization"`
	}{claim.snapshot, claim.authorization})
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), node, "test/agent-wake-driver.mjs", server.URL, claim.owner, phase)
	command.Dir = agentDir
	command.Stdin = strings.NewReader(string(payload))
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	lines := make(chan string, 16)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-t.Context().Done():
				return
			}
		}
	}()
	return command, lines
}

// raceAgentWakeClaims 让两个 worker 并发认领同一个运行。运行刚提交时两次认领
// 可能都读到「暂无」，因此用有界重试等到运行可认领；每一轮仍只允许一个赢家，
// 出现双认领会立刻失败（断言强度不变）。
func raceAgentWakeClaims(t *testing.T, server *httptest.Server, workers []string, runID, slot string) wakeClaim {
	t.Helper()
	for attempt := 0; attempt < 50; attempt++ {
		winner, nulls, claimable := raceAgentWakeClaimsOnce(t, server, workers, runID, slot)
		if claimable {
			if nulls != 1 {
				t.Fatalf("winner=%q, null responses=%d", winner.owner, nulls)
			}
			return winner
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("run %s never became claimable within bounded retries", runID)
	return wakeClaim{}
}

func raceAgentWakeClaimsOnce(t *testing.T, server *httptest.Server, workers []string, runID, slot string) (wakeClaim, int, bool) {
	t.Helper()
	type result struct {
		owner    string
		response *http.Response
		err      error
	}
	start := make(chan struct{})
	replies := make(chan result, len(workers))
	for _, worker := range workers {
		go func() {
			owner := worker + ":" + slot
			request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/internal-agent/claim", strings.NewReader(`{"owner":"`+owner+`"}`))
			request.Header.Set("Content-Type", "application/json")
			setWakeWorkerHeaders(request, worker, strings.Repeat(worker, 8))
			<-start
			response, err := server.Client().Do(request)
			replies <- result{owner, response, err}
		}()
	}
	close(start)
	var winner wakeClaim
	nulls := 0
	for range workers {
		reply := <-replies
		if reply.err != nil {
			t.Fatal(reply.err)
		}
		var envelope struct {
			Code int
			Msg  string
			Data struct {
				Run              *service.PiAgentSnapshot
				RunAuthorization string
			}
		}
		err := json.NewDecoder(reply.response.Body).Decode(&envelope)
		_ = reply.response.Body.Close()
		if err != nil || reply.response.StatusCode/100 != 2 || envelope.Code != 0 {
			t.Fatalf("claim %s status=%d code=%d: %s (%v)", reply.owner, reply.response.StatusCode, envelope.Code, envelope.Msg, err)
		}
		snapshot := envelope.Data.Run
		if snapshot == nil {
			nulls++
			continue
		}
		var header struct {
			Type    string
			ID      string
			Version int
		}
		if winner.snapshot != nil || snapshot.RunID != runID || snapshot.PiSessionID == "" || envelope.Data.RunAuthorization == "" ||
			json.Unmarshal(snapshot.PiSessionHeader, &header) != nil || header.Type != "session" || header.ID != snapshot.PiSessionID || header.Version != 3 || len(snapshot.Tools) == 0 {
			t.Fatalf("invalid or second claim snapshot: id=%s, header=%+v", snapshot.RunID, header)
		}
		winner = wakeClaim{reply.owner, envelope.Data.RunAuthorization, snapshot}
	}
	if winner.snapshot == nil {
		return winner, nulls, false
	}
	return winner, nulls, true
}

func setWakeWorkerHeaders(request *http.Request, worker, token string) {
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Agent-Identity", worker)
	request.Header.Set("X-Agent-Protocol-Version", internalAgentWireVersion)
	request.Header.Set("X-Pi-SDK-Version", internalAgentPiSDKVersion)
	request.Header.Set("X-Pi-Session-Format", internalAgentSessionFormat)
}
