package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func diagnosticActivityService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Task{}, &model.TaskLog{}, &model.ApiCallLog{}, &model.CanvasProject{}, &model.CanvasSnapshot{}, &model.CloudAgentCanvasMutation{}, &model.CloudAgentExecution{}, &model.CloudAgentEventRecord{}, &model.CloudAgentMessageRecord{}, &model.CloudAgentPiEntry{}, &model.SystemSetting{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { sqlDB.Close() })
	return &Service{repo: repository.New(db)}, db
}

func TestExportDiagnosticBundleIncludesRecentCanvasAndAgentActivity(t *testing.T) {
	svc, db := diagnosticActivityService(t)
	now := time.Now().UTC().Add(-time.Minute)
	create := func(v any) {
		t.Helper()
		if err := db.Create(v).Error; err != nil {
			t.Fatal(err)
		}
	}
	create(&model.CanvasProject{ID: "canvas-1", UserID: "user-1", Title: "最近画布", Revision: 3, UpdatedAt: now})
	create(&model.CanvasSnapshot{ID: "snapshot-1", CanvasID: "canvas-1", UserID: "user-1", Revision: 2, NodeCount: 4, Reason: "autosave", PayloadJSON: `{"prompt":"snapshot-private"}`, CreatedAt: now})
	create(&model.CloudAgentCanvasMutation{ID: "mutation-1", CanvasID: "canvas-1", RunID: "run-1", UserID: "user-1", Operation: "update_nodes", Status: "applied", BeforeJSON: "mutation-private", CreatedAt: now})
	create(&model.CloudAgentExecution{ID: "run-1", UserID: "user-1", CanvasID: "canvas-1", Status: "failed", ActiveTaskID: "model-step", RuntimePhase: "waiting", WaitKind: "tool", CreatedAt: now, UpdatedAt: now,
		StateJSON: `{"step":3,"activeTaskId":"model-step","lastStepTaskId":"step-2","piToolBatchTaskId":"step-2","callIndex":0,"calls":[{"id":"call-1","function":{"name":"update_nodes","arguments":"tool-arguments-private"}}],"request":{"prompt":"harness-private"}}`})
	create(&model.CloudAgentEventRecord{RunID: "run-1", UserID: "user-1", Sequence: 1, CreatedAt: now, EventJSON: `{"type":"tool_failed","payload":{"callId":"call-1","taskId":"step-2","tool":"update_nodes","error":"authorization: Bearer event-secret","arguments":"event-arguments-private"}}`})
	create(&model.CloudAgentPiEntry{SessionID: "session-1", EntryID: "entry-1", Sequence: 1, UserID: "user-1", RunID: "run-1", CreatedAt: now, EntryJSON: `{"type":"message","message":{"role":"user","content":"请调整节点。apiKey=message-secret https://name:url-secret@example.com/file?signature=signed-secret"}}`})
	create(&model.CloudAgentPiEntry{SessionID: "session-1", EntryID: "entry-2", Sequence: 2, UserID: "user-1", RunID: "run-1", CreatedAt: now, EntryJSON: `{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"正在调整节点"},{"type":"thinking","thinking":"thinking-private"},{"type":"image","data":"media-private"},{"type":"toolCall","id":"call-1","name":"update_nodes","arguments":{"prompt":"call-private"}}]}}`})
	for i, scope := range []struct {
		user, canvas string
		at           time.Time
	}{{"user-2", "canvas-1", now}, {"user-1", "canvas-2", now}, {"user-1", "canvas-1", now.Add(-time.Hour)}} {
		id := fmt.Sprintf("excluded-%d", i)
		create(&model.CloudAgentExecution{ID: id, UserID: scope.user, CanvasID: scope.canvas, UpdatedAt: scope.at, CreatedAt: scope.at})
		create(&model.CloudAgentEventRecord{RunID: id, UserID: scope.user, Sequence: 1, CreatedAt: scope.at, EventJSON: `{"type":"run_failed"}`})
		create(&model.CanvasSnapshot{ID: id, UserID: scope.user, CanvasID: scope.canvas, Revision: int64(i + 10), PayloadJSON: "{}", CreatedAt: scope.at})
	}
	var req DiagnosticExportRequest
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"from":%q,"to":%q,"canvasId":"canvas-1"}`, now.Add(-10*time.Minute).Format(time.RFC3339Nano), now.Add(time.Minute).Format(time.RFC3339Nano))), &req); err != nil {
		t.Fatal(err)
	}
	bundle, err := svc.ExportDiagnosticBundle("user-1", req)
	if err != nil {
		t.Fatal(err)
	}
	files := readDiagnosticTestZIP(t, bundle.Data)
	for name, want := range map[string]string{
		"canvas/current.jsonl": "canvas-1", "canvas/history.jsonl": "snapshot-1", "canvas/agent-mutations.jsonl": "mutation-1",
		"agent/runs.jsonl": `"pendingCallCount":1`, "agent/events.jsonl": "tool_failed", "agent/messages.jsonl": "请调整节点",
	} {
		if !strings.Contains(files[name], want) {
			t.Fatalf("%s missing %q: %s", name, want, files[name])
		}
	}
	all := ""
	for _, content := range files {
		all += content
	}
	for _, excluded := range []string{"excluded-", "snapshot-private", "mutation-private", "tool-arguments-private", "harness-private", "event-secret", "event-arguments-private", "message-secret", "url-secret", "signed-secret", "thinking-private", "media-private", "call-private"} {
		if strings.Contains(all, excluded) {
			t.Errorf("bundle leaked %q", excluded)
		}
	}
	preview, err := svc.PreviewDiagnosticBundle("user-1", req)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(preview)
	for _, want := range []string{`"canvasChangeCount":2`, `"agentRunCount":1`, `"agentMessageCount":2`, `"agentEventCount":1`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("preview missing %s: %s", want, encoded)
		}
	}
}

func TestExportDiagnosticBundleRejectsForeignCanvas(t *testing.T) {
	svc, db := diagnosticActivityService(t)
	if err := db.Create(&model.CanvasProject{ID: "foreign-canvas", UserID: "user-2"}).Error; err != nil {
		t.Fatal(err)
	}
	var req DiagnosticExportRequest
	json.Unmarshal([]byte(`{"canvasId":"foreign-canvas"}`), &req)
	if _, err := svc.ExportDiagnosticBundle("user-1", req); err == nil {
		t.Fatal("foreign canvas was accepted")
	}
}

func TestExportDiagnosticBundleOlderTranscriptsAndNoStaleFallback(t *testing.T) {
	svc, db := diagnosticActivityService(t)
	now := time.Now().UTC().Add(-time.Minute)
	for _, run := range []model.CloudAgentExecution{
		{ID: "legacy-run", UserID: "user-1", CreatedAt: now, UpdatedAt: now, StateJSON: "broken"},
		{ID: "old-pi-run", UserID: "user-1", Engine: "pi", CreatedAt: now, UpdatedAt: now, StateJSON: "{}"},
		{ID: "new-pi-run", UserID: "user-1", Engine: "pi", CreatedAt: now.Add(-time.Hour), UpdatedAt: now, StateJSON: "{}"},
	} {
		if err := db.Create(&run).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []model.CloudAgentMessageRecord{
		{RunID: "legacy-run", UserID: "user-1", Kind: "canonical", Sequence: 1, MessageJSON: `{"role":"user","content":"旧会话文字"}`},
		{RunID: "legacy-run", UserID: "user-1", Kind: "canonical", Sequence: 2, MessageJSON: `{"role":"system","content":"system-private"}`},
		{RunID: "old-pi-run", UserID: "user-1", Kind: "pi", Sequence: 1, MessageJSON: `{"role":"assistant","content":[{"type":"text","text":"旧 Pi 会话文字"}]}`},
		{RunID: "new-pi-run", UserID: "user-1", Kind: "pi", Sequence: 1, MessageJSON: `{"role":"user","content":"stale-private"}`},
		{RunID: "legacy-run", UserID: "user-2", Kind: "canonical", Sequence: 3, MessageJSON: `{"role":"user","content":"foreign-private"}`},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&model.CloudAgentPiEntry{RunID: "new-pi-run", UserID: "user-1", SessionID: "new-session", EntryID: "old-entry", Sequence: 1, CreatedAt: now.Add(-time.Hour), EntryJSON: `{"type":"message","message":{"role":"user","content":"stale-private"}}`}).Error; err != nil {
		t.Fatal(err)
	}
	bundle, err := svc.ExportDiagnosticBundle("user-1", DiagnosticExportRequest{From: now.Add(-10 * time.Minute).Format(time.RFC3339Nano), To: now.Add(time.Minute).Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	files := readDiagnosticTestZIP(t, bundle.Data)
	for _, want := range []string{"旧会话文字", "旧 Pi 会话文字", "active_run_transcript_snapshot"} {
		if !strings.Contains(files["agent/messages.jsonl"], want) {
			t.Errorf("messages missing %s", want)
		}
	}
	for _, secret := range []string{"system-private", "stale-private", "foreign-private"} {
		if strings.Contains(files["agent/messages.jsonl"], secret) {
			t.Errorf("messages leaked %s", secret)
		}
	}
	if !strings.Contains(files["agent/runs.jsonl"], `"checkpointReadable":false`) {
		t.Fatal("unreadable checkpoint not marked")
	}
}

func TestExportDiagnosticBundleKeepsLatestHistoryAndReportsTruncation(t *testing.T) {
	svc, db := diagnosticActivityService(t)
	now := time.Now().UTC().Add(-time.Minute)
	rows := make([]model.CanvasSnapshot, 201)
	for i := range rows {
		rows[i] = model.CanvasSnapshot{ID: fmt.Sprintf("snapshot-%03d", i), CanvasID: "canvas-1", UserID: "user-1", Revision: int64(i + 1), PayloadJSON: "{}", CreatedAt: now.Add(time.Duration(i) * time.Millisecond)}
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	request := DiagnosticExportRequest{From: now.Add(-time.Minute).Format(time.RFC3339Nano), To: now.Add(time.Minute).Format(time.RFC3339Nano)}
	bundle, err := svc.ExportDiagnosticBundle("user-1", request)
	if err != nil {
		t.Fatal(err)
	}
	files := readDiagnosticTestZIP(t, bundle.Data)
	history := files["canvas/history.jsonl"]
	if strings.Contains(history, "snapshot-000") || !strings.Contains(history, "snapshot-200") || strings.Count(history, "\n") != 200 {
		t.Fatal("history did not retain latest 200 records")
	}
	if strings.Index(history, "snapshot-001") > strings.Index(history, "snapshot-200") {
		t.Fatal("history is not chronological")
	}
	if !strings.Contains(files["manifest.json"], `"truncated": true`) {
		t.Fatal("missing manifest truncation warning")
	}
	preview, err := svc.PreviewDiagnosticBundle("user-1", request)
	if err != nil || !preview.WillTruncate || preview.CanvasChangeCount != 200 {
		t.Fatalf("preview = %+v, error = %v", preview, err)
	}
}

func TestDiagnosticConversationRedaction(t *testing.T) {
	for _, input := range []string{
		`{"apiKey":"sensitive-value","password":"sensitive-value"}`,
		"Authorization: Bearer sensitive-value", "Cookie: a=sensitive-value; b=sensitive-value",
		"Bearer sensitive-value", "https://user:sensitive-value@example.com/file?signature=sensitive-value#sensitive-value",
		"data:image/png;base64,sensitive-value", "sk-sensitive-value",
	} {
		if result := redactDiagnosticText(input, 4000); strings.Contains(result, "sensitive-value") {
			t.Errorf("redaction leaked: %s", result)
		}
	}
}

func TestExportDiagnosticBundleBoundsLargeMessageBodies(t *testing.T) {
	svc, db := diagnosticActivityService(t)
	now := time.Now().UTC().Add(-time.Minute)
	if err := db.Create(&model.CloudAgentExecution{ID: "pi-run", UserID: "user-1", Engine: "pi", CreatedAt: now, UpdatedAt: now, StateJSON: "{}"}).Error; err != nil {
		t.Fatal(err)
	}
	for i, content := range []string{strings.Repeat("字", 5000), strings.Repeat("A", 100000)} {
		body, _ := json.Marshal(map[string]any{"type": "message", "message": map[string]any{"role": "user", "content": content}})
		if err := db.Create(&model.CloudAgentPiEntry{SessionID: "session", RunID: "pi-run", UserID: "user-1", EntryID: fmt.Sprintf("entry-%d", i), Sequence: i + 1, CreatedAt: now, EntryJSON: string(body)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	bundle, err := svc.ExportDiagnosticBundle("user-1", DiagnosticExportRequest{})
	if err != nil {
		t.Fatal(err)
	}
	files := readDiagnosticTestZIP(t, bundle.Data)
	if !strings.Contains(files["manifest.json"], `"truncated": true`) || !strings.Contains(files["agent/messages.jsonl"], "unreadable_message") {
		t.Fatal("oversized body was not omitted and reported")
	}
	if strings.Count(files["agent/messages.jsonl"], "字") != 4000 {
		t.Fatal("message text not bounded to 4000 characters")
	}
}
