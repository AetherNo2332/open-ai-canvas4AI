package database_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	_ "yingce/backend/internal/repository" // registers the canvas write projector used by v77

	"gorm.io/gorm"
)

const mediaVersionsMigrationVersion int64 = 77

func openMediaVersionsSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := database.Open(database.Config{Driver: "sqlite", DSN: "file:" + t.Name() + "?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := db.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func openMediaVersionsPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	db := openAgentAdmissionAuditPostgresDB(t)
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	return db
}

type mediaVersionsFixture struct {
	db                   *gorm.DB
	owner, canvas, epoch string
}

func seedMediaVersionsCanvas(t *testing.T, db *gorm.DB, nodes []map[string]any, generations map[string]int64) mediaVersionsFixture {
	t.Helper()
	f := mediaVersionsFixture{db: db, owner: "mv-owner", canvas: "mv-canvas", epoch: "epoch-live"}
	payload, err := json.Marshal(map[string]any{"nodes": nodes, "connections": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CanvasProject{ID: f.canvas, UserID: f.owner, Title: "画布", PayloadJSON: string(payload), Revision: 3}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CanvasDocumentEpoch{CanvasID: f.canvas, OwnerID: f.owner, Epoch: f.epoch, EventSeq: 5, UpdatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	for id, generation := range generations {
		if err := db.Create(&model.CanvasEntityVersion{CanvasID: f.canvas, Epoch: f.epoch, EntityKey: "node:" + id, Version: 3, Generation: generation}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f mediaVersionsFixture) row(t *testing.T, id, node string, generation, sequence int64, status, resource string, created time.Time) {
	t.Helper()
	taskID := "task-" + id
	row := model.CanvasMediaVersion{ID: id, OwnerID: f.owner, CanvasID: f.canvas, Epoch: f.epoch, NodeID: node, Generation: generation, Sequence: sequence, TaskID: &taskID, Mode: "image", Status: status, Adoption: "adopted", CreatedAt: created, UpdatedAt: created}
	row.RequestJSON = `{"prompt":"提示词 ` + id + `","model":"m-1","input":{"config":{"size":"1024x1024","quality":"high","videoSeconds":5}}}`
	if status == "ready" {
		row.ResourceID = resource
		row.MediaJSON = `{"storageKey":"resource:` + resource + `","content":"/api/resources/` + resource + `/file","mimeType":"image/png","naturalWidth":1024,"naturalHeight":768,"producedModel":"m-1-pro","taskDurationMs":1200}`
	}
	if err := f.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
}

func (f mediaVersionsFixture) withEpoch(epoch string) mediaVersionsFixture {
	f.epoch = epoch
	return f
}

func rerunMediaVersionsMigration(db *gorm.DB) error {
	if err := db.Exec("DELETE FROM schema_migrations WHERE version = ?", mediaVersionsMigrationVersion).Error; err != nil {
		return err
	}
	return database.MigrateSchema(db)
}

func (f mediaVersionsFixture) project(t *testing.T) model.CanvasProject {
	t.Helper()
	var project model.CanvasProject
	if err := f.db.First(&project, "id = ?", f.canvas).Error; err != nil {
		t.Fatal(err)
	}
	return project
}

func (f mediaVersionsFixture) metadata(t *testing.T, node string) map[string]any {
	t.Helper()
	var doc struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(f.project(t).PayloadJSON), &doc); err != nil {
		t.Fatal(err)
	}
	for _, value := range doc.Nodes {
		if value["id"] == node {
			meta, _ := value["metadata"].(map[string]any)
			return meta
		}
	}
	t.Fatalf("node %s missing", node)
	return nil
}

func mediaVersionIDs(meta map[string]any) []string {
	list, _ := meta["mediaVersions"].([]any)
	ids := []string{}
	for _, raw := range list {
		version, _ := raw.(map[string]any)
		id, _ := version["id"].(string)
		ids = append(ids, id)
	}
	return ids
}

func imageNode(id string, meta map[string]any) map[string]any {
	return map[string]any{"id": id, "type": "image", "title": "图片", "position": map[string]any{"x": 0, "y": 0}, "width": 320, "height": 240, "metadata": meta}
}

func checkMigrateMediaVersions(t *testing.T, db *gorm.DB) {
	base := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	state := map[string]any{"epoch": "epoch-live", "generation": 2, "currentVersionId": "mv-2", "currentVersion": 2, "versionCount": 3}
	f := seedMediaVersionsCanvas(t, db, []map[string]any{
		imageNode("n1", map[string]any{"storageKey": "resource:r2", "content": "/api/resources/r2/file", "mediaVersion": state}),
		imageNode("n2", map[string]any{"storageKey": "resource:r7"}),
	}, map[string]int64{"n1": 2, "n2": 1})
	f.row(t, "mv-1", "n1", 2, 1, "ready", "r1", base)
	f.row(t, "mv-2", "n1", 2, 2, "ready", "r2", base.Add(time.Minute))
	f.row(t, "mv-3", "n1", 2, 3, "failed", "", base.Add(2*time.Minute))
	f.row(t, "mv-oldgen", "n1", 1, 1, "ready", "r8", base.Add(-time.Hour))
	f.withEpoch("epoch-old").row(t, "mv-oldepoch", "n1", 2, 1, "ready", "r9", base.Add(-2*time.Hour))
	f.row(t, "mv-n2-oldgen", "n2", 0, 1, "ready", "r6", base)

	if err := rerunMediaVersionsMigration(db); err != nil {
		t.Fatal(err)
	}
	meta := f.metadata(t, "n1")
	if got := mediaVersionIDs(meta); fmt.Sprint(got) != "[mv-1 mv-2]" {
		t.Fatalf("migrated ids = %v, want ready rows of the live incarnation only", got)
	}
	if meta["currentMediaVersionId"] != "mv-2" {
		t.Fatalf("currentMediaVersionId = %v", meta["currentMediaVersionId"])
	}
	if meta["mediaVersion"] == nil {
		t.Fatal("fork mediaVersion state must stay until Task 11 retires it")
	}
	first := meta["mediaVersions"].([]any)[0].(map[string]any)
	media, _ := first["media"].(map[string]any)
	generation, _ := first["generation"].(map[string]any)
	if first["number"] != float64(1) || first["source"] != "generate" || first["createdAt"] != "2026-10-01T08:00:00.000Z" {
		t.Fatalf("version header = %v", first)
	}
	if media["storageKey"] != "resource:r1" || media["mimeType"] != "image/png" || media["naturalWidth"] != float64(1024) || media["producedModel"] != nil {
		t.Fatalf("media = %v", media)
	}
	if generation["prompt"] != "提示词 mv-1" || generation["model"] != "m-1" || generation["producedModel"] != "m-1-pro" || generation["taskId"] != "task-mv-1" || generation["size"] != "1024x1024" || generation["quality"] != "high" || generation["seconds"] != "5" || generation["taskDurationMs"] != float64(1200) {
		t.Fatalf("generation = %v", generation)
	}
	if second := meta["mediaVersions"].([]any)[1].(map[string]any); second["number"] != float64(2) {
		t.Fatalf("second number = %v", second["number"])
	}
	if other := f.metadata(t, "n2"); other["mediaVersions"] != nil {
		t.Fatalf("row from an old incarnation leaked into recreated node: %v", other["mediaVersions"])
	}
	project := f.project(t)
	if project.Revision != 4 {
		t.Fatalf("revision = %d, want 4", project.Revision)
	}
	var event model.CanvasCommandEvent
	if err := db.First(&event, "canvas_id = ? AND sequence = ?", f.canvas, 6).Error; err != nil {
		t.Fatalf("projected command event missing: %v", err)
	}
	if event.Epoch != f.epoch || event.OperationID != "legacy:4:6" {
		t.Fatalf("projected event = %+v", event)
	}
	var version model.CanvasEntityVersion
	if err := db.First(&version, "canvas_id = ? AND epoch = ? AND entity_key = ?", f.canvas, f.epoch, "node:n1").Error; err != nil {
		t.Fatal(err)
	}
	if version.Version != 4 || version.Generation != 2 {
		t.Fatalf("node entity version = %+v, want version 4 with generation 2 kept", version)
	}
	var epoch model.CanvasDocumentEpoch
	if err := db.First(&epoch, "canvas_id = ?", f.canvas).Error; err != nil || epoch.EventSeq != 6 || epoch.Epoch != f.epoch {
		t.Fatalf("epoch = %+v %v", epoch, err)
	}
}

func checkMigrateMediaVersionsMergesWithExisting(t *testing.T, db *gorm.DB) {
	base := time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
	existing := []any{map[string]any{"id": "up-a", "number": 1, "createdAt": "2026-09-30T08:00:00.000Z", "source": "upload", "media": map[string]any{"storageKey": "resource:r1"}}}
	state := map[string]any{"epoch": "epoch-live", "generation": 2, "currentVersionId": "mv-2"}
	f := seedMediaVersionsCanvas(t, db, []map[string]any{
		imageNode("n1", map[string]any{"storageKey": "resource:r1", "mediaVersions": existing, "currentMediaVersionId": "up-a", "mediaVersion": state}),
	}, map[string]int64{"n1": 2})
	f.row(t, "mv-1", "n1", 2, 1, "ready", "r1", base)
	f.row(t, "mv-2", "n1", 2, 2, "ready", "r2", base.Add(time.Minute))
	if err := rerunMediaVersionsMigration(db); err != nil {
		t.Fatal(err)
	}
	meta := f.metadata(t, "n1")
	if got := mediaVersionIDs(meta); fmt.Sprint(got) != "[up-a mv-2]" {
		t.Fatalf("merged ids = %v, want resource r1 deduplicated against the existing version", got)
	}
	if meta["currentMediaVersionId"] != "up-a" {
		t.Fatalf("existing currentMediaVersionId changed to %v", meta["currentMediaVersionId"])
	}
	list := meta["mediaVersions"].([]any)
	if list[0].(map[string]any)["number"] != float64(1) || list[1].(map[string]any)["number"] != float64(2) || list[0].(map[string]any)["source"] != "upload" {
		t.Fatalf("merged versions = %v", list)
	}
}

func checkMigrateMediaVersionsKeepsCurrentWhenTrimming(t *testing.T, db *gorm.DB) {
	base := time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC)
	state := map[string]any{"epoch": "epoch-live", "generation": 2, "currentVersionId": "mv-03"}
	f := seedMediaVersionsCanvas(t, db, []map[string]any{imageNode("n1", map[string]any{"storageKey": "resource:r03", "mediaVersion": state})}, map[string]int64{"n1": 2})
	for sequence := int64(1); sequence <= 35; sequence++ {
		id := fmt.Sprintf("mv-%02d", sequence)
		f.row(t, id, "n1", 2, sequence, "ready", fmt.Sprintf("r%02d", sequence), base.Add(time.Duration(sequence)*time.Minute))
	}
	if err := rerunMediaVersionsMigration(db); err != nil {
		t.Fatal(err)
	}
	meta := f.metadata(t, "n1")
	ids := mediaVersionIDs(meta)
	if len(ids) != 30 {
		t.Fatalf("kept %d versions, want 30", len(ids))
	}
	if meta["currentMediaVersionId"] != "mv-03" || ids[0] != "mv-03" || ids[1] != "mv-07" || ids[29] != "mv-35" {
		t.Fatalf("trim kept %v current %v; want current mv-03 plus the newest 29", ids, meta["currentMediaVersionId"])
	}
	if number := meta["mediaVersions"].([]any)[29].(map[string]any)["number"]; number != float64(35) {
		t.Fatalf("trimming renumbered versions: last number = %v", number)
	}
}

func checkMigrateMediaVersionsIdempotent(t *testing.T, db *gorm.DB) {
	state := map[string]any{"epoch": "epoch-live", "generation": 2, "currentVersionId": "mv-1"}
	f := seedMediaVersionsCanvas(t, db, []map[string]any{imageNode("n1", map[string]any{"storageKey": "resource:r1", "mediaVersion": state})}, map[string]int64{"n1": 2})
	f.row(t, "mv-1", "n1", 2, 1, "ready", "r1", time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC))
	if err := rerunMediaVersionsMigration(db); err != nil {
		t.Fatal(err)
	}
	first := f.project(t)
	if err := rerunMediaVersionsMigration(db); err != nil {
		t.Fatal(err)
	}
	second := f.project(t)
	if first.Revision != 4 || second.Revision != first.Revision || second.PayloadJSON != first.PayloadJSON {
		t.Fatalf("second run rewrote the canvas: revision %d -> %d", first.Revision, second.Revision)
	}
	var events int64
	if err := db.Model(&model.CanvasCommandEvent{}).Where("canvas_id = ?", f.canvas).Count(&events).Error; err != nil || events != 1 {
		t.Fatalf("command events = %d %v, want exactly one projection", events, err)
	}
}

func checkMigrateMediaVersionsFailsClosedWithoutProjector(t *testing.T, db *gorm.DB) {
	state := map[string]any{"epoch": "epoch-live", "generation": 2, "currentVersionId": "mv-1"}
	f := seedMediaVersionsCanvas(t, db, []map[string]any{imageNode("n1", map[string]any{"storageKey": "resource:r1", "mediaVersion": state})}, map[string]int64{"n1": 2})
	f.row(t, "mv-1", "n1", 2, 1, "ready", "r1", time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC))
	previous := database.RegisterCanvasWriteProjector(nil)
	t.Cleanup(func() { database.RegisterCanvasWriteProjector(previous) })
	if previous == nil {
		t.Fatal("repository did not register a canvas write projector")
	}
	if err := rerunMediaVersionsMigration(db); err == nil {
		t.Fatal("v77 rewrote a canvas without projection")
	}
	if project := f.project(t); project.Revision != 3 {
		t.Fatalf("failed migration left revision %d", project.Revision)
	}
}

func TestMigrateMediaVersionsToMetadata(t *testing.T) {
	checkMigrateMediaVersions(t, openMediaVersionsSQLite(t))
	status, err := database.ReadSchemaStatus(openMediaVersionsSQLite(t))
	if err != nil || !status.Ready || database.CurrentSchemaVersion != mediaVersionsMigrationVersion {
		t.Fatalf("schema status = %+v %v", status, err)
	}
}

func TestMigrateMediaVersionsMergesWithExisting(t *testing.T) {
	checkMigrateMediaVersionsMergesWithExisting(t, openMediaVersionsSQLite(t))
}

func TestMigrateMediaVersionsKeepsCurrentWhenTrimming(t *testing.T) {
	checkMigrateMediaVersionsKeepsCurrentWhenTrimming(t, openMediaVersionsSQLite(t))
}

func TestMigrateMediaVersionsIdempotent(t *testing.T) {
	checkMigrateMediaVersionsIdempotent(t, openMediaVersionsSQLite(t))
}

func TestMigrateMediaVersionsFailsClosedWithoutProjector(t *testing.T) {
	checkMigrateMediaVersionsFailsClosedWithoutProjector(t, openMediaVersionsSQLite(t))
}

func TestMigrateMediaVersionsPostgresToMetadata(t *testing.T) {
	checkMigrateMediaVersions(t, openMediaVersionsPostgres(t))
}

func TestMigrateMediaVersionsPostgresMergesWithExisting(t *testing.T) {
	checkMigrateMediaVersionsMergesWithExisting(t, openMediaVersionsPostgres(t))
}

func TestMigrateMediaVersionsPostgresKeepsCurrentWhenTrimming(t *testing.T) {
	checkMigrateMediaVersionsKeepsCurrentWhenTrimming(t, openMediaVersionsPostgres(t))
}

func TestMigrateMediaVersionsPostgresIdempotent(t *testing.T) {
	checkMigrateMediaVersionsIdempotent(t, openMediaVersionsPostgres(t))
}

func TestMigrateMediaVersionsPostgresFailsClosedWithoutProjector(t *testing.T) {
	checkMigrateMediaVersionsFailsClosedWithoutProjector(t, openMediaVersionsPostgres(t))
}
