package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type resultCapableAdapter struct {
	UnavailableAdapter
	available bool
}

func (a resultCapableAdapter) BuildResult(context.Context, PollContext) (RequestSpec, error) {
	return RequestSpec{Method: "GET", Path: "/result"}, nil
}

func (a resultCapableAdapter) ResultAvailable() bool { return a.available }

func TestResolveResultMediaURLsFromJSONManifest(t *testing.T) {
	var result Result
	if err := json.Unmarshal([]byte(`{
		"images":[{"url":"images/still.png","mimeType":"image/png"}],
		"videos":[{"url":"/files/result.mp4","mimeType":"video/mp4"}],
		"audios":[{"url":"https://cdn.example/result.mp3","mimeType":"audio/mpeg"}]
	}`), &result); err != nil {
		t.Fatal(err)
	}
	if err := ResolveResultMediaURLs("https://api.example/v1/jobs/job-1/outputs", &result); err != nil {
		t.Fatal(err)
	}
	if got := result.Images[0].URL; got != "https://api.example/v1/jobs/job-1/images/still.png" {
		t.Fatalf("image URL = %q", got)
	}
	if got := result.Videos[0].URL; got != "https://api.example/files/result.mp4" {
		t.Fatalf("video URL = %q", got)
	}
	if got := result.Audios[0].URL; got != "https://cdn.example/result.mp3" {
		t.Fatalf("audio URL = %q", got)
	}
}

func TestResultOutputsPreserveTextAndMediaContract(t *testing.T) {
	text := TextResultOutput(&Result{Text: "answer", Reasoning: "reason"})
	if text["mode"] != "text" || text["text"] != "answer" || text["reasoning"] != "reason" {
		t.Fatalf("text output = %#v", text)
	}

	image, err := MediaResultOutput("image", &Result{Usage: map[string]any{"ignored": true}}, []DownloadedMedia{
		{Data: []byte("png"), MIMEType: "image/png"},
		{Data: []byte("jpg"), MIMEType: "image/jpeg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	images := image["images"].([]any)
	first := images[0].(map[string]any)
	if image["mode"] != "image" || first["dataUrl"] != "data:image/png;base64,cG5n" || first["mimeType"] != "image/png" {
		t.Fatalf("image output = %#v", image)
	}
	if _, ok := image["usage"]; ok {
		t.Fatal("image usage was attached")
	}

	usage := map[string]any{"durationSeconds": float64(5)}
	video, err := MediaResultOutput("video", &Result{Usage: usage}, []DownloadedMedia{{Data: []byte("video"), MIMEType: "video/mp4"}})
	if err != nil {
		t.Fatal(err)
	}
	if video["mode"] != "video" || video["usage"] == nil {
		t.Fatalf("video output = %#v", video)
	}
	if got := video["video"].(map[string]any)["dataUrl"]; got != "data:video/mp4;base64,dmlkZW8=" {
		t.Fatalf("video data URL = %v", got)
	}
}

func TestResultManifestReadinessAndLateWait(t *testing.T) {
	if _, err := ResultMediaReferences("video", &Result{}); err == nil || err.Error() != "声明式协议已完成但没有返回媒体地址" {
		t.Fatalf("empty manifest error = %v", err)
	}
	if _, err := ResultMediaReferences("unknown", &Result{}); err == nil {
		t.Fatal("unsupported mode was accepted")
	}
	adapter := resultCapableAdapter{available: true}
	if !ResultReady("video", &Result{}, adapter) {
		t.Fatal("declared result endpoint was not considered ready")
	}
	now := time.Unix(1000, 0)
	var wait LateResultWait
	for index := 0; index < LateResultMaxPolls; index++ {
		if !wait.KeepWaiting(now) {
			t.Fatalf("poll %d ended before poll limit", index+1)
		}
	}
	if wait.KeepWaiting(now) {
		t.Fatal("poll limit was ignored")
	}
}

func TestProtocolQueryAndMediaRetryClassification(t *testing.T) {
	got, err := AppendQuery("https://api.example/jobs?existing=1", map[string][]string{"tag": {"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://api.example/jobs?existing=1&tag=a&tag=b" {
		t.Fatalf("query = %q", got)
	}
	if !RetryableMediaDownload(errors.New("unexpected EOF")) {
		t.Fatal("transient EOF was not retryable")
	}
	if RetryableMediaDownload(context.Canceled) {
		t.Fatal("cancellation was retryable")
	}
}

func TestMediaResultOutputRejectsEmptyMedia(t *testing.T) {
	if _, err := MediaResultOutput("audio", &Result{}, nil); err == nil || err.Error() != "声明式协议已完成但没有返回媒体地址" {
		t.Fatalf("empty output error = %v", err)
	}
}
