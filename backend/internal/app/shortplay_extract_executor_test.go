package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfigureShortplayExtractExecutorFromEnv(t *testing.T) {
	t.Setenv("TEXT_BASE_URL", "")
	t.Setenv("TEXT_MODEL", "")
	svc := &Service{}
	if svc.ConfigureShortplayExtractExecutorFromEnv() {
		t.Fatal("empty env must not configure an executor")
	}
	if svc.shortplayExtractExecutor() != nil {
		t.Fatal("executor must stay nil")
	}

	var gotPath, gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"characters\":[]}"}}]}`))
	}))
	defer server.Close()
	t.Setenv("TEXT_BASE_URL", server.URL+"/v1/")
	t.Setenv("TEXT_MODEL", "extract-model")
	t.Setenv("TEXT_API_KEY", "env-key")
	svc = &Service{}
	if !svc.ConfigureShortplayExtractExecutorFromEnv() {
		t.Fatal("env should configure executor")
	}
	content, err := svc.shortplayExtractExecutor()(context.Background(), "系统提示", "正文提示")
	if err != nil {
		t.Fatal(err)
	}
	if content != `{"characters":[]}` {
		t.Fatalf("content = %q", content)
	}
	if gotPath != "/v1/chat/completions" || gotAuth != "Bearer env-key" {
		t.Fatalf("path=%q auth=%q", gotPath, gotAuth)
	}
	var payload struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Model != "extract-model" || len(payload.Messages) != 2 ||
		payload.Messages[0].Role != "system" || payload.Messages[1].Role != "user" ||
		payload.Messages[1].Content != "正文提示" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestConfigureShortplayExtractExecutorReportsHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream down"))
	}))
	defer server.Close()
	t.Setenv("TEXT_BASE_URL", server.URL)
	t.Setenv("TEXT_MODEL", "m")
	t.Setenv("TEXT_API_KEY", "")
	svc := &Service{}
	if !svc.ConfigureShortplayExtractExecutorFromEnv() {
		t.Fatal("executor should be configured")
	}
	_, err := svc.shortplayExtractExecutor()(context.Background(), "s", "u")
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") || !strings.Contains(err.Error(), "upstream down") {
		t.Fatalf("error = %v", err)
	}
}
