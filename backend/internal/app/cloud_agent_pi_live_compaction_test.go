package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"
)

// Real inference is opt-in. The credential exists only in this process; the
// isolated database uses a loopback relay and a non-secret placeholder key.
func TestPiNativeCompactionLiveGPUStack(t *testing.T) {
	key, base, modelName := os.Getenv("CANVAS_TEST_GPUSTACK_KEY"), os.Getenv("CANVAS_TEST_GPUSTACK_URL"), os.Getenv("CANVAS_TEST_GPUSTACK_MODEL")
	if key == "" || base == "" || modelName == "" {
		t.Skip("temporary GPUStack test credentials not supplied")
	}
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	t.Setenv("REDIS_URL", "")
	for _, split := range []bool{false, true} {
		t.Run(fmt.Sprintf("split=%t", split), func(t *testing.T) {
			var upstreamCalls atomic.Int32
			relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamCalls.Add(1)
				ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
				defer cancel()
				request, err := http.NewRequestWithContext(ctx, r.Method, strings.TrimRight(base, "/")+r.URL.Path, r.Body)
				if err != nil {
					http.Error(w, "relay request invalid", 502)
					return
				}
				request.Header.Set("Authorization", "Bearer "+key)
				request.Header.Set("Content-Type", "application/json")
				response, err := http.DefaultClient.Do(request)
				if err != nil {
					http.Error(w, "upstream request failed", 502)
					return
				}
				defer response.Body.Close()
				if response.StatusCode >= 400 {
					body, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
					var errorBody struct {
						Error struct {
							Message string `json:"message"`
							Type    string `json:"type"`
							Code    any    `json:"code"`
							Param   string `json:"param"`
						} `json:"error"`
					}
					json.Unmarshal(body, &errorBody)
					message := strings.SplitN(errorBody.Error.Message, "'input':", 2)[0]
					t.Logf("upstream error: status=%d type=%s code=%v param=%s message=%s", response.StatusCode, errorBody.Error.Type, errorBody.Error.Code, errorBody.Error.Param, truncateRunes(strings.ReplaceAll(message, key, "[REDACTED]"), 400))
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(response.StatusCode)
					w.Write(body)
					return
				}
				w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
				w.WriteHeader(response.StatusCode)
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
				buffer := make([]byte, 4096)
				for {
					n, e := response.Body.Read(buffer)
					if n > 0 {
						w.Write(buffer[:n])
						if f, ok := w.(http.Flusher); ok {
							f.Flush()
						}
					}
					if e != nil {
						break
					}
				}
			}))
			defer relay.Close()
			s, db, run := piAgentTestLeasedFixture(t)
			db.Model(&model.ModelChannel{}).Where("id = ?", "channel").Updates(map[string]any{"base_url": relay.URL, "api_key": "local-test-only"})
			db.Model(&model.ChannelModel{}).Where("id = ?", "cm").Update("model_key", modelName)
			// This is a fixture budget, not a claim about the remote model's actual window.
			declareTestChannelWindow(t, db, 64000, 1024)
			s = New(s.repo, s.dataDir)
			t.Cleanup(func() { _ = s.Close() })
			_, state := reloadPiRun(t, s, run.ID)
			state.Request.ChannelModelKey, state.Request.Model = modelName, modelName
			state.Request.Prompt = "Continue the synthetic film plan in one short sentence. Keep Mira's scar on the left cheek; no new generation is authorized."
			state.ForceThinkingOff = true
			state.CreativeAnchor = cloudAgentCreativeAnchor{Version: 1, UserPrompt: state.Request.Prompt,
				ReferenceAssets: []cloudAgentReferenceAnchor{{NodeID: "image-node-live", Type: "image", VisualIdentity: strings.Repeat("a", 64), RequiresVisualInspection: true}}}
			state.Decisions["generation-live"] = "denied; no new generation is authorized"
			state.event(run.ID, "generation_task_created", map[string]any{"taskId": "task-live-001", "nodeIds": []string{"image-node-live"}, "status": "running"})
			assistant := func(text string) map[string]any {
				return map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": text}}, "api": "canvas-agent", "provider": "canvas", "model": modelName, "stopReason": "stop", "timestamp": 1,
					"usage": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]int{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}}
			}
			messages := []map[string]any{{"role": "user", "content": "Synthetic story: Mira has a scar on her LEFT cheek. node=image-node-live; task=task-live-001. Constraint: never create a new image without approval. " + strings.Repeat("The film alternates blue evening light and warm interior lamps. Maintain continuity. ", 25), "timestamp": 1}, assistant("Recorded the film constraints and IDs.")}
			if split {
				messages = append(messages, map[string]any{"role": "user", "content": "Current long turn: preserve the LEFT-cheek scar and only inspect existing work. " + strings.Repeat("Keep the established palette and camera direction. ", 25), "timestamp": 2}, assistant("I have reviewed the existing scene and need to finish the continuity check. "+strings.Repeat("No new generation is authorized. ", 20)))
			}
			messages = append(messages, map[string]any{"role": "user", "content": state.Request.Prompt, "timestamp": 3})
			revision, leaf := seedPiCompactionMessages(t, db, run, messages)
			_ = revision
			_ = leaf
			run = saveCloudAgentCompactionState(t, s, run, state)
			expires := time.Now().Add(10 * time.Minute)
			// The bridge validates both the execution lease and the conversation
			// session lease. Keep the isolated fixture alive for the bounded live
			// request sequence; production workers renew both rows every 15 seconds.
			if err := db.Model(run).Update("lease_expires_at", expires).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&model.CloudAgentPiSession{}).Where("id = ?", run.ConversationID).
				Update("lease_expires_at", expires).Error; err != nil {
				t.Fatal(err)
			}
			snapshot, err := s.PiAgentSnapshot("user", run.ID, run.LeaseOwner)
			if err != nil {
				t.Fatal(err)
			}
			var jobs sync.Map
			var workerMu sync.Mutex
			workerErrors := make(chan error, 4)
			startWorker := func(taskID string) {
				if _, loaded := jobs.LoadOrStore(taskID, true); !loaded {
					go func() {
						workerMu.Lock()
						defer workerMu.Unlock()
						if e := s.ProcessNextTask(); e != nil {
							workerErrors <- e
						}
					}()
				}
			}
			local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := strings.TrimPrefix(r.URL.Path, "/internal-agent/runs/"+run.ID)
				var result any
				var e error
				switch {
				case path == "/phase":
					result = map[string]any{}
				case path == "/context-compactions":
					var body PiContextCompactionStart
					json.NewDecoder(r.Body).Decode(&body)
					result, e = s.PiBeginContextCompaction("user", run.ID, run.LeaseOwner, body)
				case strings.HasSuffix(path, "/model"):
					var body PiNativeCompactionModelRequest
					json.NewDecoder(r.Body).Decode(&body)
					operation := strings.Split(path, "/")[2]
					view, err := s.PiNativeContextCompactionModel("user", run.ID, run.LeaseOwner, operation, body)
					result, e = view, err
					if err == nil && view.Status == "queued" {
						startWorker(view.TaskID)
					}
				case strings.HasSuffix(path, "/complete"):
					var body PiNativeCompactionComplete
					json.NewDecoder(r.Body).Decode(&body)
					result, e = s.PiFinishNativeContextCompaction("user", run.ID, run.LeaseOwner, strings.Split(path, "/")[2], body)
				case strings.HasSuffix(path, "/commit"):
					var body PiContextCompactionCommit
					json.NewDecoder(r.Body).Decode(&body)
					var revision int64
					revision, e = s.PiCommitContextCompaction("user", run.ID, run.LeaseOwner, strings.Split(path, "/")[2], body)
					if e == nil {
						result = map[string]any{"committed": true, "sessionRevision": revision}
					}
				default:
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if e != nil {
					w.WriteHeader(400)
					json.NewEncoder(w).Encode(map[string]any{"code": 1, "msg": e.Error()})
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": result})
			}))
			defer local.Close()
			input, _ := json.Marshal(map[string]any{"base": local.URL, "snapshot": snapshot, "split": split})
			ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
			defer cancel()
			driver, _ := filepath.Abs("../../../agent/dist/test/live-compaction-driver.js")
			command := exec.CommandContext(ctx, "node", driver)
			command.Stdin = bytes.NewReader(input)
			// Node sees no upstream key, including its environment.
			for _, item := range os.Environ() {
				if !strings.HasPrefix(item, "CANVAS_TEST_GPUSTACK_KEY=") {
					command.Env = append(command.Env, item)
				}
			}
			output, err := command.CombinedOutput()
			if err != nil {
				var tasks []model.Task
				db.Where("operation = ?", cloudAgentContextCompactionOperation).Find(&tasks)
				for _, task := range tasks {
					var result map[string]any
					json.Unmarshal([]byte(task.ResultJSON), &result)
					t.Logf("summary diagnostic status=%s stop=%v usage=%v error=%s", task.Status, result["stopReasonKind"], result["usage"], strings.ReplaceAll(task.Error, key, "[REDACTED]"))
				}
				t.Fatalf("native SDK driver: %v %s", err, bytes.ReplaceAll(output, []byte(key), []byte("[REDACTED]")))
			}
			t.Logf("SDK native result: %s", output)
			select {
			case err := <-workerErrors:
				t.Fatal("summary task worker failed: ", err)
			default:
			}
			_, after := reloadPiRun(t, s, run.ID)
			if after.ContextCompaction != nil || after.ContextCheckpoint == nil || after.ContextCheckpoint.HistorySummary == "" {
				t.Fatal("native checkpoint was not committed")
			}
			if cloudAgentJSON(after.CreativeAnchor) != cloudAgentJSON(state.CreativeAnchor) || cloudAgentJSON(after.Decisions) != cloudAgentJSON(state.Decisions) {
				t.Fatal("server image identity or authorization changed")
			}
			if !strings.Contains(cloudAgentJSON(after.ContextCheckpoint.PendingTasks), "task-live-001") {
				t.Fatal("pending task identity lost")
			}
			if !strings.Contains(after.ContextCheckpoint.HistorySummary, "Mira") {
				t.Fatal("semantic summary lost the story protagonist")
			}
			canonical := after.Canonical
			canonical.Tools = nil
			canonical.Messages = append(canonical.Messages, map[string]any{"role": "user", "content": "In one sentence name the protagonist, the scar side, and whether generating a new image is authorized. Do not call tools."})
			admission, err := s.PiModelPreflight("user", run.ID, run.LeaseOwner, PiModelStepRequest{Canonical: canonical})
			if err != nil || admission.Decision != "model" {
				t.Fatalf("compacted continuation rejected: %+v %v", admission, err)
			}
			step, err := s.PiModelStep("user", run.ID, run.LeaseOwner, PiModelStepRequest{Canonical: canonical})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.ProcessNextTask(); err != nil {
				t.Fatal(err)
			}
			view, err := s.PiModelStepView("user", run.ID, run.LeaseOwner, step.TaskID)
			if err != nil || view.Status != "succeeded" {
				t.Fatalf("real continuation status=%v error=%v", view, err)
			}
			var result struct {
				Text  string         `json:"text"`
				Stop  string         `json:"stopReasonKind"`
				Usage map[string]int `json:"usage"`
			}
			json.Unmarshal(view.Result, &result)
			if result.Usage["input"] <= 0 || result.Usage["output"] <= 0 || result.Stop != "stop" {
				t.Fatalf("continuation usage/stop invalid: %+v", result)
			}
			if !strings.Contains(strings.ToLower(result.Text), "mira") || !strings.Contains(strings.ToLower(result.Text), "left") {
				t.Fatalf("continuation lost constraints: %s", result.Text)
			}
			summaryCalls := 1
			if split {
				summaryCalls = 2
			}
			if upstreamCalls.Load() != int32(summaryCalls+1) {
				t.Fatalf("replay sent extra requests: %d", upstreamCalls.Load())
			}
			var orders int64
			db.Model(&model.BillingOrder{}).Where("status = ?", model.BillingStatusSettled).Count(&orders)
			if orders != int64(summaryCalls+1) {
				t.Fatalf("settled orders=%d requests=%d", orders, upstreamCalls.Load())
			}
			t.Logf("real model=%s requests=%d settled=%d continuation input=%d output=%d stop=%s text=%s", modelName, upstreamCalls.Load(), orders, result.Usage["input"], result.Usage["output"], result.Stop, result.Text)
		})
	}
}
