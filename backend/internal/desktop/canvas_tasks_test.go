package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/canvas/tasks"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDesktopTaskWorkerCompletesWithoutRendererOrAgent(t *testing.T) {
	for _, scenario := range []string{"success", "uncertain"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer worker-fixture-secret" {
					t.Error("wrong provider configuration")
				}
				if scenario == "uncertain" {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"choices":[{"message":{"content":"host worker answer"}}]}`)
			}))
			defer upstream.Close()
			dir := t.TempDir()
			db, err := database.Open(database.Config{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			sql, _ := db.DB()
			defer sql.Close()
			if err = database.MigrateSchema(db); err != nil {
				t.Fatal(err)
			}
			if err = database.MigrateDesktop(db); err != nil {
				t.Fatal(err)
			}
			repo := repository.New(db)
			_, user, err := repo.BootstrapDesktopOwner(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			svc, err := app.NewDesktop(repo, dir, StaticSecretProvider(make([]byte, 32)))
			if err != nil {
				t.Fatal(err)
			}
			defer svc.Close()
			if err = svc.SetDesktopPrivateHosts(&user, []string{"127.0.0.1"}); err != nil {
				t.Fatal(err)
			}
			channel, err := svc.CreateSystemChannel(&user, app.ChannelRequest{Name: "worker fixture", BaseURL: upstream.URL + "/v1", APIKey: "worker-fixture-secret", Models: []string{"fixture-model"}})
			if err != nil {
				t.Fatal(err)
			}
			summaries, err := svc.DesktopChannels(&user)
			if err != nil {
				t.Fatal(err)
			}
			enabled := true
			_, err = svc.SaveDesktopChannelModel(&user, channel.ID, summaries[0].Models[0].ID, app.ChannelModelRequest{ModelKey: "fixture-model", Capability: "text", Protocol: "chat-completion", Enabled: &enabled, CapabilityConfig: app.DefaultModelCapabilityConfig("chat-completion")})
			if err != nil {
				t.Fatal(err)
			}
			p := identity.Principal{ID: user.ID, OwnerID: user.ID, Role: "owner", Scopes: identity.OwnerScopes()}
			cs := commands.NewService(repo, commands.ServiceOptions{Validation: svc.DesktopCommandValidation})
			ts := tasks.NewService(repo, cs, svc.DesktopTaskSubmitter(), svc.DesktopCanvasTaskOptions())
			svc.AttachDesktopCanvasTasks(ts)
			snap, err := cs.Create(context.Background(), p, commands.CreateCanvasRequest{ClientID: "fixture", OperationID: "create"})
			if err != nil {
				t.Fatal(err)
			}
			x := 0.0
			_, err = cs.Execute(context.Background(), p, commands.Command{Protocol: 1, CanvasID: snap.CanvasID, DocumentEpoch: snap.DocumentEpoch, ClientID: "fixture", OperationID: "node", BaseRevision: snap.Revision, Operations: []commands.Operation{{Type: "add_node", ID: "target", NodeType: "text", X: &x, Y: &x}}})
			if err != nil {
				t.Fatal(err)
			}
			snap, _ = cs.ReadSnapshot(context.Background(), p, snap.CanvasID)
			req := tasks.SubmitRequest{Command: commands.Command{Protocol: 1, CanvasID: snap.CanvasID, DocumentEpoch: snap.DocumentEpoch, ClientID: "fixture", OperationID: "submit", BaseRevision: snap.Revision}, TargetNodeID: "target", Task: tasks.TaskRequest{Mode: "text", Prompt: "hello", Input: map[string]any{"config": map[string]any{"channelId": channel.ID, "model": "fixture-model"}, "textOptions": map[string]any{"stream": false}}}}
			// Prepare may validate but must not persist or contact the upstream.
			prep, err := svc.DesktopTaskSubmitter().Prepare(context.Background(), p, req.Task)
			if err != nil {
				t.Fatal(err)
			}
			if prep.Task.AgentRunID != "" || prep.Task.GenerationID != "" || calls.Load() != 0 {
				t.Fatal("prepare performed an Agent or upstream side effect")
			}
			// Model settings may change after preparation; transaction admission
			// must reject a stopped channel before writing any task/binding.
			if err = db.Model(&model.ModelChannel{}).Where("id = ?", channel.ID).Update("enabled", false).Error; err != nil {
				t.Fatal(err)
			}
			commitErr := repo.CommandTransaction(context.Background(), func(tx *repository.Repository) error {
				return svc.DesktopTaskSubmitter().Commit(context.Background(), tx, prep)
			})
			if commitErr == nil {
				t.Fatal("disabled channel committed after preparation")
			}
			if err = db.Model(&model.ModelChannel{}).Where("id = ?", channel.ID).Update("enabled", true).Error; err != nil {
				t.Fatal(err)
			}
			var count int64
			db.Model(&model.Task{}).Count(&count)
			if count != 0 {
				t.Fatal("prepare persisted task")
			}
			for _, bad := range []map[string]any{{"config": map[string]any{"apiKey": "caller-secret"}}, {"replay": true}, {"agentRequests": map[string]any{}}, {"config": map[string]any{"baseUrl": "http://evil.example"}}} {
				r := req.Task
				r.Input = bad
				if _, err = svc.DesktopTaskSubmitter().Prepare(context.Background(), p, r); err == nil {
					t.Fatal("unsafe task input accepted")
				}
			}
			h, err := ts.Submit(context.Background(), p, req)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = ts.Submit(context.Background(), p, req); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err = svc.StartDesktopWorkers(ctx); err != nil {
				t.Fatal(err)
			}
			defer func() {
				cancel()
				stop, c := context.WithTimeout(context.Background(), 5*time.Second)
				defer c()
				if err := svc.StopWorker(stop); err != nil {
					t.Error(err)
				}
			}()
			deadline := time.Now().Add(12 * time.Second)
			for time.Now().Before(deadline) {
				job, e := repo.CanvasMaterialization(h.TaskID)
				if e == nil && job.State == "done" {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			task, err := repo.Task(h.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(task.InputJSON, "worker-fixture-secret") {
				t.Fatal("persisted task exposed provider credential")
			}
			result, err := cs.ReadSnapshot(context.Background(), p, snap.CanvasID)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(result.Document)
			expected := model.TaskStatusSucceeded
			required := "host worker answer"
			if scenario == "uncertain" {
				expected = model.TaskStatusSubmissionUncertain
				required = "submission_uncertain"
			}
			if task.Status != expected || calls.Load() != 1 || !strings.Contains(string(raw), required) {
				t.Fatalf("worker not complete: task=%s error=%s calls=%d document=%s", task.Status, task.Error, calls.Load(), raw)
			}
			if scenario == "uncertain" {
				if _, err = svc.RetryTask(user.ID, h.TaskID); err == nil {
					t.Fatal("ambiguous consumption resubmitted")
				}
				if calls.Load() != 1 {
					t.Fatal("second provider request")
				}
			}
			db.Model(&model.CloudAgentExecution{}).Count(&count)
			if count != 0 {
				t.Fatal("fake Agent run created")
			}
			receipts := []model.CanvasCommandReceipt{}
			db.Find(&receipts)
			for _, r := range receipts {
				if strings.Contains(r.CanonicalJSON+r.ReceiptJSON, "worker-fixture-secret") {
					t.Fatal("credential in command log")
				}
			}
		})
	}
}
