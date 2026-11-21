package protocol

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// AppendQuery adds repeated query values while preserving the parsed URL's
// existing query parameters.
func AppendQuery(rawURL string, values map[string][]string) (string, error) {
	if len(values) == 0 {
		return rawURL, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	for key, items := range values {
		for _, item := range items {
			query.Add(key, item)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// TextResultOutput builds the text result contract without performing any
// persistence or streaming side effects.
func TextResultOutput(result *Result) map[string]any {
	output := map[string]any{"mode": "text", "text": result.Text}
	if strings.TrimSpace(result.Reasoning) != "" {
		output["reasoning"] = result.Reasoning
	}
	return output
}

// ResultMediaReferences selects and validates the media list for a mode.
func ResultMediaReferences(mode string, result *Result) ([]MediaReference, error) {
	var references []MediaReference
	switch mode {
	case "image":
		references = result.Images
	case "video":
		references = result.Videos
	case "audio":
		references = result.Audios
	default:
		return nil, fmt.Errorf("声明式协议不支持生成模式 %s", mode)
	}
	if len(references) == 0 {
		return nil, errors.New("声明式协议已完成但没有返回媒体地址")
	}
	return references, nil
}

// DownloadedMedia is already fetched by the host. Encoding it into the public
// result contract is deterministic protocol behavior.
type DownloadedMedia struct {
	Data     []byte
	MIMEType string
}

// MediaResultOutput builds image/video/audio output from downloaded bytes and
// attaches provider usage where the public contract permits it.
func MediaResultOutput(mode string, result *Result, items []DownloadedMedia) (map[string]any, error) {
	if len(items) == 0 {
		return nil, errors.New("声明式协议已完成但没有返回媒体地址")
	}
	encoded := make([]any, 0, len(items))
	for _, item := range items {
		encoded = append(encoded, map[string]any{
			"dataUrl":  protocolDataURL(item.MIMEType, item.Data),
			"mimeType": item.MIMEType,
		})
	}
	var output map[string]any
	switch mode {
	case "image":
		output = map[string]any{"mode": "image", "images": encoded}
	case "video":
		output = map[string]any{"mode": "video", "video": encoded[0]}
	case "audio":
		output = map[string]any{"mode": "audio", "audio": encoded[0]}
	default:
		return nil, fmt.Errorf("声明式协议不支持生成模式 %s", mode)
	}
	AttachResultUsage(output, mode, result)
	return output, nil
}

// AttachResultUsage preserves video output usage for duration-based billing.
func AttachResultUsage(output map[string]any, mode string, result *Result) {
	if output == nil || result == nil || mode != "video" || len(result.Usage) == 0 {
		return
	}
	output["usage"] = result.Usage
}

// ResultEndpoint returns the independent result endpoint only when the adapter
// explicitly declares and implements it.
func ResultEndpoint(adapter Adapter) (ResultAdapter, bool) {
	resultAdapter, ok := adapter.(ResultAdapter)
	capability, hasCapability := adapter.(ResultCapability)
	return resultAdapter, ok && hasCapability && capability.ResultAvailable()
}

// ResultReady reports whether a completed result can be finalized.
func ResultReady(mode string, result *Result, adapter Adapter) bool {
	if mode != "video" && mode != "audio" && mode != "image" {
		return true
	}
	if ResultHasOutput(mode, result) {
		return true
	}
	_, ok := ResultEndpoint(adapter)
	return ok
}

// ResultHasOutput checks whether the selected mode has a usable inline result.
func ResultHasOutput(mode string, result *Result) bool {
	if result == nil {
		return false
	}
	switch mode {
	case "text":
		return strings.TrimSpace(result.Text) != ""
	case "image":
		return len(result.Images) > 0
	case "video":
		return len(result.Videos) > 0
	case "audio":
		return len(result.Audios) > 0
	default:
		return false
	}
}

const (
	LateResultGrace    = 2 * time.Minute
	LateResultMaxPolls = 24
	LateResultStage    = "上游已完成，等待结果地址"
)

// LateResultWait bounds how long a completed task may wait for a media URL.
type LateResultWait struct {
	since time.Time
	polls int
}

// KeepWaiting records one completed-but-empty poll and reports whether the
// grace window remains open.
func (w *LateResultWait) KeepWaiting(now time.Time) bool {
	if w.since.IsZero() {
		w.since = now
	}
	w.polls++
	return w.polls <= LateResultMaxPolls && now.Sub(w.since) < LateResultGrace
}

// ResolveResultMediaURLs resolves relative media references against the
// already-validated manifest URL.
func ResolveResultMediaURLs(manifestURL string, result *Result) error {
	base, err := url.Parse(manifestURL)
	if err != nil {
		return err
	}
	for _, media := range [][]MediaReference{result.Images, result.Videos, result.Audios} {
		for index := range media {
			if media[index].URL == "" {
				continue
			}
			reference, err := url.Parse(media[index].URL)
			if err != nil {
				return err
			}
			media[index].URL = base.ResolveReference(reference).String()
		}
	}
	return nil
}

// RetryableMediaDownload classifies transient download failures. Cancellation,
// deadline expiration and opaque application errors are not retryable.
func RetryableMediaDownload(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var networkError net.Error
	if errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary()) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"tls handshake timeout", "connection reset", "unexpected eof", "broken pipe"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func protocolDataURL(mimeType string, data []byte) string {
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return "data:" + strings.Split(mimeType, ";")[0] + ";base64," + base64.StdEncoding.EncodeToString(data)
}
