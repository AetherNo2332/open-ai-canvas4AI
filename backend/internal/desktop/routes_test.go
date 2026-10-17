package desktop

import (
	"context"
	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/repository"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopRoutesDenyWebAgent(t *testing.T) {
	db, err := database.Open(database.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := database.MigrateDesktop(db); err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db)
	owner, err := BootstrapOwner(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := app.NewDesktop(repo, t.TempDir(), StaticSecretProvider(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(svc, RouterConfig{Owner: owner, WebDir: t.TempDir(), InstanceID: "test"})
	for _, path := range []string{"/api/agent/runs", "/api/auth/register", "/api/payment/topups", "/api/admin/users", "/api/does-not-exist"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		if w.Code != 404 {
			t.Errorf("%s returned %d", path, w.Code)
		}
	}
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("canvas-shell"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "visible.js"), []byte("static-asset"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(outside, []byte("outside-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	hasSymlink := os.Symlink(outside, filepath.Join(webDir, "linked.txt")) == nil
	router = NewRouter(svc, RouterConfig{Owner: owner, WebDir: webDir, InstanceID: "test", Origin: "http://localhost", BootstrapToken: strings.Repeat("x", 40)})
	request := func(method, path, host, origin, bearer string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://localhost"+path, nil)
		req.Host = host
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	for _, host := range []string{"evil.example", "localhost.evil.example"} {
		if got := request("GET", "/api/health", host, "", "").Code; got != 403 {
			t.Fatalf("untrusted host accepted: %d", got)
		}
	}
	if got := request("POST", "/api/desktop/session", "localhost", "http://evil.example", strings.Repeat("x", 40)).Code; got != 403 {
		t.Fatalf("cross-origin bootstrap: %d", got)
	}
	if got := request("GET", "/api/desktop/bootstrap", "localhost", "", "").Code; got != 401 {
		t.Fatalf("anonymous bootstrap: %d", got)
	}
	first := request("POST", "/api/desktop/session", "localhost", "", strings.Repeat("x", 40))
	if first.Code != 200 || len(first.Result().Cookies()) != 1 || !first.Result().Cookies()[0].HttpOnly {
		t.Fatalf("session exchange: %d", first.Code)
	}
	if got := request("POST", "/api/desktop/session", "localhost", "", strings.Repeat("x", 40)).Code; got != 401 {
		t.Fatalf("reused bootstrap accepted: %d", got)
	}
	if got := request("GET", "/visible.js", "localhost", "", ""); got.Code != 200 || got.Body.String() != "static-asset" {
		t.Fatalf("static asset failed: %d", got.Code)
	}
	paths := []string{"/../visible.js", "/%2e%2e/visible.js", "/assets%5c..%5cvisible.js", "/C:%5cprivate.txt", "/api"}
	if hasSymlink {
		paths = append(paths, "/linked.txt")
	}
	for _, path := range paths {
		if got := request("GET", path, "localhost", "", ""); got.Code != 404 {
			t.Errorf("unsafe static path %s: %d", path, got.Code)
		}
	}
}
