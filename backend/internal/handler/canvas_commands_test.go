package handler

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"infinite-canvas/backend/internal/canvas/commands"
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

func TestCanvasCommandHTTPDoesNotTrustActor(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "http.db")+"?_synchronous=FULL"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	if err = db.AutoMigrate(&model.User{}, &model.CanvasProject{}, &model.Resource{}); err != nil {
		t.Fatal(err)
	}
	if err = database.MigrateCanvasCommands(db); err != nil {
		t.Fatal(err)
	}
	db.Create(&model.User{ID: "owner", Status: model.UserStatusActive})
	svc := commands.NewService(repository.New(db), commands.ServiceOptions{})
	p := identity.Principal{ID: "owner", OwnerID: "owner", Role: "owner", Scopes: identity.OwnerScopes()}
	snapshot, err := svc.Create(context.Background(), p, commands.CreateCanvasRequest{OperationID: "create", ClientID: "client", Title: "HTTP"})
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterCanvasCommandRoutes(router.Group("/api"), svc, func(*gin.Context) (identity.Principal, error) { return p, nil })
	request := commands.Command{Protocol: 1, CanvasID: snapshot.CanvasID, DocumentEpoch: snapshot.DocumentEpoch, OperationID: "edit1", ClientID: "client", BaseRevision: snapshot.Revision, Operations: []commands.Operation{{Type: "add_node", ID: "text", NodeType: "text"}}}
	raw, _ := json.Marshal(request)
	path := "/api/desktop/canvases/" + snapshot.CanvasID + "/commands"
	for _, body := range []string{strings.TrimSuffix(string(raw), "}") + `,"actorId":"forged"}`, string(raw) + " {}"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("invalid envelope accepted: %d %s", w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw))))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"actorId":"owner"`) {
		t.Fatalf("not persisted as authenticated actor: %d %s", w.Code, w.Body.String())
	}
	first := w.Body.String()
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw))))
	if first != w.Body.String() {
		t.Fatal("HTTP retry changed receipt")
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/desktop/capabilities", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "edit_drawing") {
		t.Fatalf("capability registry did not serialize: %d %s", w.Code, w.Body.String())
	}
}
