package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRedactDiagnosticHeadersReplacesCompleteValues(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "mixed case bearer",
			input: "AUTHORIZATION: Bearer fixture-auth-marker",
			want:  "AUTHORIZATION: [REDACTED]",
		},
		{
			name:  "proxy basic",
			input: "proxy-authorization=Basic fixture-basic-marker",
			want:  "proxy-authorization=[REDACTED]",
		},
		{
			name:  "digest parameters",
			input: `Authorization: Digest username="fixture-user", realm="fixture-realm", nonce="fixture-nonce", response="fixture-digest-marker"`,
			want:  "Authorization: [REDACTED]",
		},
		{
			name:  "multi value cookie",
			input: "Cookie: id=fixture-cookie-one; sid=fixture-cookie-two",
			want:  "Cookie: [REDACTED]",
		},
		{
			name:  "multi value set cookie",
			input: "Set-Cookie: first=fixture-cookie-one; second=fixture-cookie-two",
			want:  "Set-Cookie: [REDACTED]",
		},
		{
			name:  "json fields preserve neighbors",
			input: `{"Authorization":"Bearer fixture-auth-marker","Proxy-Authorization":"Basic fixture-basic-marker","Cookie":"id=fixture-cookie-one; sid=fixture-cookie-two","Set-Cookie":["first=fixture-cookie-one","second=fixture-cookie-two"],"stage":"fixture-safe"}`,
			want:  `{"Authorization":"[REDACTED]","Proxy-Authorization":"[REDACTED]","Cookie":"[REDACTED]","Set-Cookie":"[REDACTED]","stage":"fixture-safe"}`,
		},
		{
			name:  "json whitespace preserves neighbors",
			input: `{"Authorization": "Digest username=\"fixture-user\", response=\"fixture-digest-marker\"", "stage": "fixture-safe"}`,
			want:  `{"Authorization": "[REDACTED]", "stage": "fixture-safe"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactDiagnosticHeaders(tc.input); got != tc.want {
				t.Fatalf("redactDiagnosticHeaders() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExportDiagnosticBundleRedactsSensitiveValues(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:diagnostics-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Task{}, &model.TaskLog{}, &model.ApiCallLog{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&model.Task{
		ID: "task-1", UserID: "user-1", TraceID: "trace-1", RequestID: "req-1", ProjectID: "project-1", Type: "video_generate",
		Status: model.TaskStatusFailed, Error: "authorization: task-secret", Prompt: "secret prompt", CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.TaskLog{ID: "task-log-1", UserID: "user-1", TaskID: "task-1", TraceID: "trace-1", Message: "任务失败", Payload: "apiKey=log-secret", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ApiCallLog{ID: "api-log-1", UserID: "user-1", TraceID: "trace-1", RequestID: "req-1", TaskID: "task-1", Path: "/v1/generations", RequestBody: "apiKey=body-secret", ResponseBody: "response-secret", UpstreamURL: "https://provider.example/result?signature=secret", Error: "token=api-secret", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}

	svc := &Service{repo: repository.New(db)}
	request := DiagnosticExportRequest{
		From: now.Add(-2 * time.Minute).Format(time.RFC3339Nano), To: now.Add(time.Minute).Format(time.RFC3339Nano), TaskID: "task-1",
		Description: "页面报错 https://fixture-user:fixture-pass@example.org/path?q=query-secret#fragment-secret", ClientEvents: []DiagnosticClientEvent{{ID: "event-1", Message: "request failed\nAuthorization: Bearer fixture-auth\nProxy-Authorization: Basic fixture-basic\nCookie: id=client-secret; sid=fixture-session\n" + `{"Set-Cookie":["fixture-cookie-one", "fixture-cookie-two"],"AuThOrIzAtIoN":"Digest fixture-digest", "stage":"fixture-safe"}`, TraceID: "trace-1"}},
	}
	preview, err := svc.PreviewDiagnosticBundle("user-1", request)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := svc.ExportDiagnosticBundle("user-1", request)
	if err != nil {
		t.Fatal(err)
	}
	files := readDiagnosticTestZIP(t, bundle.Data)
	if len(files) != 7 {
		t.Fatalf("diagnostic ZIP entries = %d, want 7: %v", len(files), files)
	}
	manifest := files["manifest.json"]
	if !strings.Contains(manifest, `"bundleId": "`+bundle.BundleID+`"`) {
		t.Fatalf("manifest = %s", manifest)
	}
	var decodedManifest struct {
		RedactionVersion int `json:"redactionVersion"`
		Counts           struct {
			ClientEvents int `json:"clientEvents"`
			Tasks        int `json:"tasks"`
			TaskLogs     int `json:"taskLogs"`
			Upstream     int `json:"upstreamCalls"`
		} `json:"counts"`
	}
	if err := json.Unmarshal([]byte(manifest), &decodedManifest); err != nil {
		t.Fatal(err)
	}
	if decodedManifest.RedactionVersion != 2 {
		t.Fatalf("redactionVersion = %d, want 2", decodedManifest.RedactionVersion)
	}
	if decodedManifest.Counts.ClientEvents != len(request.ClientEvents) {
		t.Fatalf("manifest client event count = %d, request = %d", decodedManifest.Counts.ClientEvents, len(request.ClientEvents))
	}
	if decodedManifest.Counts.ClientEvents > preview.ClientEventLimit || decodedManifest.Counts.Tasks != preview.TaskCount ||
		decodedManifest.Counts.TaskLogs != preview.TaskLogCount || decodedManifest.Counts.Upstream != preview.APICallCount {
		t.Fatalf("manifest counts = %+v, preview = %+v", decodedManifest.Counts, preview)
	}
	for file, count := range map[string]int{
		"client/events.jsonl":          decodedManifest.Counts.ClientEvents,
		"backend/tasks.jsonl":          preview.TaskCount,
		"backend/task-logs.jsonl":      preview.TaskLogCount,
		"backend/upstream-calls.jsonl": preview.APICallCount,
	} {
		if got := diagnosticJSONLines(files[file]); got != count {
			t.Fatalf("%s lines = %d, preview = %d", file, got, count)
		}
	}
	for name, content := range files {
		for _, secret := range []string{"task-secret", "secret prompt", "log-secret", "body-secret", "response-secret", "api-secret", "signature=secret", "client-secret", "fixture-auth", "fixture-basic", "fixture-session", "fixture-cookie-one", "fixture-cookie-two", "fixture-digest", "fixture-user", "fixture-pass", "query-secret", "fragment-secret"} {
			if strings.Contains(content, secret) {
				t.Fatalf("diagnostic entry %s contains a synthetic sensitive value", name)
			}
		}
	}
	if !strings.Contains(files["client/events.jsonl"], "fixture-safe") || !strings.Contains(files["client/events.jsonl"], "request failed") {
		t.Fatal("safe event context missing")
	}
	if strings.Contains(files["backend/upstream-calls.jsonl"], "requestBody") || strings.Contains(files["backend/upstream-calls.jsonl"], "result?signature") {
		t.Fatalf("upstream call contains excluded payload or query: %s", files["backend/upstream-calls.jsonl"])
	}
}

func diagnosticJSONLines(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	return strings.Count(value, "\n") + 1
}

func TestExportDiagnosticBundleRejectsForeignTask(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:diagnostics-ownership-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Task{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Task{ID: "foreign-task", UserID: "user-2", CreatedAt: time.Now(), UpdatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	_, err = (&Service{repo: repository.New(db)}).ExportDiagnosticBundle("user-1", DiagnosticExportRequest{TaskID: "foreign-task"})
	if err == nil || !strings.Contains(err.Error(), "任务不存在或无权访问") {
		t.Fatalf("foreign task error = %v", err)
	}
}

func readDiagnosticTestZIP(t *testing.T, data []byte) map[string]string {
	t.Helper()
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]string, len(archive.File))
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		files[file.Name] = string(content)
	}
	return files
}
