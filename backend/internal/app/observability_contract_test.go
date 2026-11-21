package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAdminSystemPerformanceResponseContract(t *testing.T) {
	svc, _ := newSystemPerformanceTestService(t)
	if _, err := svc.AdminSystemPerformance(context.Background(), &model.User{ID: "user-1", Role: model.UserRoleUser}); authStatus(err) != 403 {
		t.Fatalf("non-admin error = %#v, want 403", err)
	}

	result, err := svc.AdminSystemPerformance(context.Background(), &model.User{ID: "admin-1", Role: model.UserRoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "healthy" {
		t.Fatalf("status = %q, want healthy", result.Status)
	}
	if got := diagnosticJSONKeys(t, result); !reflect.DeepEqual(got, []string{
		"build", "collectedAt", "database", "disk", "host", "memory", "redis", "status",
	}) {
		t.Fatalf("performance JSON keys = %#v", got)
	}
}

func TestPreviewDiagnosticBundleContract(t *testing.T) {
	svc := newDiagnosticPreviewTestService(t)

	preview, err := svc.PreviewDiagnosticBundle("user-1", DiagnosticExportRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if preview.ClientEventLimit != 500 || preview.TaskCount != 0 || preview.TaskLogCount != 0 || preview.APICallCount != 0 || preview.EstimatedBytes != 2048 || preview.WillTruncate {
		t.Fatalf("empty preview = %#v", preview)
	}
	if got := diagnosticJSONKeys(t, preview); !reflect.DeepEqual(got, []string{
		"apiCallCount", "clientEventLimit", "estimatedBytes", "taskCount", "taskLogCount", "willTruncate",
	}) {
		t.Fatalf("preview JSON keys = %#v", got)
	}

	events := make([]DiagnosticClientEvent, 501)
	for index := range events {
		events[index] = DiagnosticClientEvent{ID: "event", Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Level: "error", Category: "client", Message: "failed"}
	}
	preview, err = svc.PreviewDiagnosticBundle("user-1", DiagnosticExportRequest{ClientEvents: events})
	if err != nil {
		t.Fatal(err)
	}
	if preview.ClientEventLimit != 500 || preview.EstimatedBytes != 2048+500*420 || !preview.WillTruncate {
		t.Fatalf("truncated preview = %#v", preview)
	}
}

func TestExportDiagnosticBundleContract(t *testing.T) {
	svc := newDiagnosticPreviewTestService(t)
	bundle, err := svc.ExportDiagnosticBundle("user-1", DiagnosticExportRequest{})
	if err != nil {
		t.Fatal(err)
	}
	files := readDiagnosticTestZIP(t, bundle.Data)
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal([]byte(files["manifest.json"]), &manifest); err != nil {
		t.Fatal(err)
	}
	if got := diagnosticJSONKeys(t, manifest); !reflect.DeepEqual(got, []string{
		"bundleId", "counts", "generatedAt", "redactionVersion", "schemaVersion", "timeRange",
	}) {
		t.Fatalf("manifest JSON keys = %#v", got)
	}
	var counts map[string]json.RawMessage
	if err := json.Unmarshal(manifest["counts"], &counts); err != nil {
		t.Fatal(err)
	}
	if got := diagnosticJSONKeys(t, counts); !reflect.DeepEqual(got, []string{"clientEvents", "taskLogs", "tasks", "upstreamCalls"}) {
		t.Fatalf("manifest counts JSON keys = %#v", got)
	}
}

func newDiagnosticPreviewTestService(t *testing.T) *Service {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "diagnostic-preview.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Task{}, &model.TaskLog{}, &model.ApiCallLog{}); err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	return &Service{repo: repository.New(db)}
}

func diagnosticJSONKeys(t *testing.T, value any) []string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
