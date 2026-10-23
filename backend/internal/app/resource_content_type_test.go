package app

import (
	"bytes"
	"encoding/base64"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUploadMimeSniffingRejectsDeclaredInlineDisguises(t *testing.T) {
	for _, item := range []struct{ body, filename, declared, want string }{
		{"<html><script>alert(1)</script></html>", "fake.png", "image/png", "text/html"},
		{"<svg xmlns=\"http://www.w3.org/2000/svg\"><script/></svg>", "fake.jpg", "image/jpeg", "text/plain"},
		{"unknown binary\x00payload", "fake.png", "image/png", "application/octet-stream"},
		{"\x89PNG\r\n\x1a\n", "fake.html", "text/html", "image/png"},
	} {
		t.Run(item.filename+item.want, func(t *testing.T) {
			file := bytes.NewReader([]byte(item.body))
			got := detectUploadedMimeType(file, item.filename, item.declared)
			if got != item.want {
				t.Fatalf("mime = %q, want %q", got, item.want)
			}
			if pos, _ := file.Seek(0, 1); pos != 0 {
				t.Fatalf("sniff consumed upload: %d", pos)
			}
		})
	}
}

func TestRemoteResourceMimeSniffingRejectsDeclaredInlineDisguise(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("<html><script>alert(1)</script></html>"))
	}))
	defer server.Close()
	payload, err := downloadRemoteResource(server.URL+"/fake.png", 4096)
	if err != nil {
		t.Fatal(err)
	}
	if payload.mimeType != "text/html" {
		t.Fatalf("remote mime = %q", payload.mimeType)
	}
}

func TestGeneratedDataURLMimeSniffingRejectsInlineDisguise(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.SystemSetting{}); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.New(db), t.TempDir())
	body := []byte("<html><script>alert(1)</script></html>")
	mediaType, data, err := svc.decodeDataURL("data:image/png;base64," + base64.StdEncoding.EncodeToString(body))
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "text/html" || !bytes.Equal(data, body) {
		t.Fatalf("generated MIME = %q", mediaType)
	}
}
