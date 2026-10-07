package app

import (
	"encoding/json"
	"infinite-canvas/backend/internal/model"
	"os"
	"path/filepath"
	"testing"
)

func TestAgentParityDatabaseDrawingReadTemplate(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	records := map[string]map[string]any{
		"document:document": {"id": "document:document", "typeName": "document", "name": "Blank", "gridSize": 10.0, "meta": map[string]any{}},
		"page:aaa":          {"id": "page:aaa", "typeName": "page", "name": "Second", "index": "a2", "meta": map[string]any{}},
		"page:zzz":          {"id": "page:zzz", "typeName": "page", "name": "First", "index": "a1", "meta": map[string]any{}},
	}
	store := map[string]any{}
	for id, record := range records {
		store[id] = record
	}
	meta := map[string]any{"drawingEngine": "tldraw", "drawingDocument": map[string]any{"engine": "tldraw", "snapshot": map[string]any{"document": map[string]any{"schema": map[string]any{"schemaVersion": 2}, "store": store}}}}
	raw, _ := json.Marshal(map[string]any{"nodes": []any{map[string]any{"id": "draw", "type": "drawing", "metadata": meta}}, "connections": []any{}})
	canvas := model.CanvasProject{ID: "native-read-canvas", UserID: "user", PayloadJSON: string(raw)}
	if err := db.Create(&canvas).Error; err != nil {
		t.Fatal(err)
	}
	result, err := cloudAgentReadDrawing(s.repo, "user", canvas.ID, parityCall("canvas_read_drawing", map[string]any{"nodeId": "draw"}))
	if err != nil {
		t.Fatal(err)
	}
	fields := result.(map[string]any)
	template, ok := fields["exampleRecord"].(map[string]any)
	if !ok || template["parentId"] != "page:zzz" || fields["editingHint"] == "" {
		t.Fatalf("missing usable native-page template: %#v", fields)
	}
	records[stringValue(template["id"])] = template
	if err := validateCloudAgentTldrawRecords(records); err != nil {
		t.Fatalf("read template cannot be upserted: %v", err)
	}
	stored, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
	if stored.PayloadJSON != canvas.PayloadJSON {
		t.Fatal("read template changed canvas")
	}
}

func TestAgentParityTldrawNativeRecords(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "test", "fixtures", "canvas-native-tldraw-records.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Records      map[string]map[string]any `json:"records"`
		InvalidCases []struct {
			Name    string                    `json:"name"`
			Records map[string]map[string]any `json:"records"`
		} `json:"invalidCases"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if err = validateCloudAgentTldrawRecords(fixture.Records); err != nil {
		t.Fatalf("valid native fixture: %v", err)
	}
	for _, invalid := range fixture.InvalidCases {
		t.Run(invalid.Name, func(t *testing.T) {
			if err := validateCloudAgentTldrawRecords(invalid.Records); err == nil {
				t.Fatal("malformed or dangling native record accepted")
			}
		})
	}
	// Every built-in shape's required props come from the installed native engine,
	// so dropping one cannot silently succeed on the server.
	for id, record := range fixture.Records {
		if stringValue(record["typeName"]) != "shape" {
			continue
		}
		for key := range record["props"].(map[string]any) {
			t.Run(id+"/missing-"+key, func(t *testing.T) {
				raw, _ := json.Marshal(fixture.Records)
				var next map[string]map[string]any
				_ = json.Unmarshal(raw, &next)
				delete(next[id]["props"].(map[string]any), key)
				if validateCloudAgentTldrawRecords(next) == nil {
					t.Fatal("missing required native prop accepted")
				}
			})
		}
	}
}
