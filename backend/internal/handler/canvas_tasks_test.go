package handler

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/canvas/tasks"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type canvasHTTPSubmitter struct{ calls int }

func (s *canvasHTTPSubmitter) Prepare(_ context.Context, p identity.Principal, req tasks.TaskRequest) (tasks.PreparedTask, error) {
	s.calls++
	return tasks.PreparedTask{Task: model.Task{ID: "http-task", UserID: p.OwnerID, Status: model.TaskStatusQueued, Type: "canvas_" + req.Mode, Prompt: req.Prompt, InputJSON: `{}`}}, nil
}
func (s *canvasHTTPSubmitter) Commit(_ context.Context, tx *repository.Repository, p tasks.PreparedTask) error {
	return tx.InsertPreparedCanvasTask(&p.Task)
}
func (s *canvasHTTPSubmitter) Wake() {}
func TestCanvasTaskHTTPStrictEnvelopeAndSubmissionReceipt(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "http-tasks.db")+"?_busy_timeout=5000"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	if err = db.AutoMigrate(&model.User{}, &model.CanvasProject{}, &model.Resource{}, &model.Asset{}, &model.Task{}); err != nil {
		t.Fatal(err)
	}
	if err = database.MigrateCanvasCommands(db); err != nil {
		t.Fatal(err)
	}
	if err = database.MigrateCanvasTasks(db); err != nil {
		t.Fatal(err)
	}
	db.Create(&model.User{ID: "owner", Status: model.UserStatusActive})
	repo := repository.New(db)
	cs := commands.NewService(repo, commands.ServiceOptions{})
	p := identity.Principal{ID: "owner", OwnerID: "owner", Role: "owner", ClientID: "client", Scopes: identity.OwnerScopes()}
	snap, err := cs.Create(context.Background(), p, commands.CreateCanvasRequest{ClientID: "client", OperationID: "create"})
	if err != nil {
		t.Fatal(err)
	}
	header := commands.Command{Protocol: 1, CanvasID: snap.CanvasID, DocumentEpoch: snap.DocumentEpoch, ClientID: "client", OperationID: "node", BaseRevision: snap.Revision, Operations: []commands.Operation{{Type: "add_node", ID: "target", NodeType: "text"}}}
	if _, err = cs.Execute(context.Background(), p, header); err != nil {
		t.Fatal(err)
	}
	snap, _ = cs.ReadSnapshot(context.Background(), p, snap.CanvasID)
	header.OperationID = "generate"
	header.BaseRevision = snap.Revision
	header.Operations = nil
	req := tasks.SubmitRequest{Command: header, TargetNodeID: "target", Task: tasks.TaskRequest{Mode: "text", Prompt: "fixture"}}
	raw, _ := json.Marshal(req)
	stub := &canvasHTTPSubmitter{}
	ts := tasks.NewService(repo, cs, stub)
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterCanvasTaskRoutes(router.Group("/api"), ts, func(*gin.Context) (identity.Principal, error) { return p, nil })
	path := "/api/desktop/canvases/" + snap.CanvasID + "/tasks"
	for _, body := range []string{strings.TrimSuffix(string(raw), "}") + `,"actorId":"forged"}`, string(raw) + ` {}`} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		if w.Code != 400 || stub.calls != 0 {
			t.Fatal("untrusted envelope reached submitter", w.Code)
		}
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw))))
	if w.Code != 200 || stub.calls != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	first := w.Body.String()
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/desktop/canvases/"+snap.CanvasID+"/task-submissions/generate?clientId=client&documentEpoch="+snap.DocumentEpoch, nil))
	if w.Code != 200 || w.Body.String() != first {
		t.Fatal("task handle replay differs", w.Code, w.Body.String())
	}
	p.ClientID = "different-client"
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/desktop/canvases/"+snap.CanvasID+"/task-submissions/generate?clientId=client&documentEpoch="+snap.DocumentEpoch, nil))
	if w.Code != 403 {
		t.Fatal("bound client receipt crossed", w.Code, w.Body.String())
	}
	p.ClientID = "client"
	p.Role = "viewer"
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw))))
	if w.Code != 403 || stub.calls != 1 {
		t.Fatal("viewer submitted task")
	}
}
