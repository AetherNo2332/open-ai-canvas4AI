package app

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

// taskFailureMessage 的上游地址隐藏归属在 internal/taskruntime；这里覆盖
// app 侧错误包装（url.Error）最终走同一脱敏路径。
func TestTaskFailureMessageHidesUpstreamHost(t *testing.T) {
	postErr := &url.Error{
		Op:  "Post",
		URL: "https://www.kccai.ai/v1/images/generations",
		Err: errors.New("http2: server sent GOAWAY and closed the connection"),
	}
	got := taskFailureMessage(postErr)
	if strings.Contains(got, "kccai.ai") {
		t.Fatalf("task failure message leaked upstream host: %q", got)
	}
	if !strings.Contains(got, upstreamAddressLinkPlaceholder) {
		t.Fatalf("task failure message should mark the hidden link: %q", got)
	}
}
