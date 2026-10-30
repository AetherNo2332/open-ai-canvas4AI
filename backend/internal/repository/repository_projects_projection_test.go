package repository

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"yingce/backend/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 项目关联写路径（删除项目、归属、解除归属）同样改了画布 revision 与 payload，
// 必须与整份保存一样在同一事务里投影为 Agent 命令事件与实体版本。
func projectProjectionTestDB(t *testing.T) (*gorm.DB, *Repository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "project-projection.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&model.CanvasEditLease{}, &model.CanvasProject{}, &model.CanvasUnitLink{}, &model.CanvasShare{},
		&model.Project{}, &model.ProjectUnit{}, &model.ProjectAssetLink{}, &model.ProjectAssetFolder{}, &model.ProjectAssetCandidate{},
		&model.Task{}, &model.Shot{}, &model.ShotArtifact{}, &model.ShotRevision{}, &model.ShotAssetReference{},
		&model.ProductionTaskLink{}, &model.WorkflowInstance{}, &model.WorkflowStepInstance{}, &model.WorkflowStepTask{},
		&model.CanvasDocumentEpoch{}, &model.CanvasEntityVersion{}, &model.CanvasCommandReceipt{}, &model.CanvasCommandEvent{},
		&model.CanvasActorHistory{}, &model.CanvasCommandResource{}, &model.CanvasCreateReceipt{},
	); err != nil {
		t.Fatal(err)
	}
	return db, New(db)
}

func seedProjectionCanvas(t *testing.T, repo *Repository, projectID string) *model.CanvasProject {
	t.Helper()
	payload := `{"id":"canvas","title":"分镜","nodes":[{"id":"n1"}]}`
	if projectID != "" {
		payload = `{"id":"canvas","title":"分镜","projectId":"` + projectID + `","nodes":[{"id":"n1"}]}`
	}
	canvas := &model.CanvasProject{ID: "canvas", UserID: "owner", ProjectID: projectID, Title: "分镜", PayloadJSON: payload}
	if err := repo.UpsertCanvasProject(canvas); err != nil {
		t.Fatal(err)
	}
	return canvas
}

func assertProjectionAt(t *testing.T, db *gorm.DB, repo *Repository, wantProjectID string) {
	t.Helper()
	stored, err := repo.CanvasProjectForUser("owner", "canvas")
	if err != nil {
		t.Fatal(err)
	}
	events, err := repo.CommandEvents("owner", "canvas", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("no command events")
	}
	var last struct {
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal([]byte(events[len(events)-1].EventJSON), &last); err != nil {
		t.Fatal(err)
	}
	if last.Revision != stored.Revision {
		t.Fatalf("last event revision = %d, canvas revision = %d", last.Revision, stored.Revision)
	}
	var version model.CanvasEntityVersion
	if err := db.Where("canvas_id = ? AND entity_key = ?", "canvas", "project:projectId").First(&version).Error; err != nil {
		t.Fatalf("project:projectId version: %v", err)
	}
	if version.Version != stored.Revision {
		t.Fatalf("project:projectId version = %d, canvas revision = %d", version.Version, stored.Revision)
	}
	if version.Deleted != (wantProjectID == "") {
		t.Fatalf("project:projectId deleted = %v, want projectId %q", version.Deleted, wantProjectID)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(stored.PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	got, _ := payload["projectId"].(string)
	if stored.ProjectID != wantProjectID || got != wantProjectID {
		t.Fatalf("project_id column = %q, payload projectId = %q, want %q", stored.ProjectID, got, wantProjectID)
	}
}

func TestAssignCanvasProjectsWrite(t *testing.T) {
	db, repo := projectProjectionTestDB(t)
	seedProjectionCanvas(t, repo, "")
	if err := repo.AssignCanvasToProject("owner", "canvas", "project"); err != nil {
		t.Fatal(err)
	}
	assertProjectionAt(t, db, repo, "project")
}

func TestUnassignCanvasProjectsWrite(t *testing.T) {
	db, repo := projectProjectionTestDB(t)
	canvas := seedProjectionCanvas(t, repo, "project")
	if err := db.Create(&model.Project{ID: "project", UserID: "owner", Name: "项目"}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	payload := `{"id":"canvas","title":"分镜","nodes":[{"id":"n1"}],"updatedAt":"` + now.Format(time.RFC3339Nano) + `"}`
	if err := repo.UnassignCanvasFromProject("owner", "project", canvas.ID, payload, now, canvas.Revision); err != nil {
		t.Fatal(err)
	}
	assertProjectionAt(t, db, repo, "")
}

func TestDeleteProjectProjectsCanvasWrite(t *testing.T) {
	db, repo := projectProjectionTestDB(t)
	canvas := seedProjectionCanvas(t, repo, "project")
	if err := db.Create(&model.Project{ID: "project", UserID: "owner", Name: "项目"}).Error; err != nil {
		t.Fatal(err)
	}
	update := *canvas
	update.UpdatedAt = time.Now()
	update.PayloadJSON = `{"id":"canvas","title":"分镜","nodes":[{"id":"n1"}],"updatedAt":"` + update.UpdatedAt.Format(time.RFC3339Nano) + `"}`
	if err := repo.DeleteProject("owner", "project", []model.CanvasProject{update}); err != nil {
		t.Fatal(err)
	}
	assertProjectionAt(t, db, repo, "")
}
