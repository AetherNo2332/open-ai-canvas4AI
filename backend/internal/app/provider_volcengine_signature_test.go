package app

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/volcengine/volc-sdk-golang/base"
)

// volcengineV4Authorization 按上游的算法重算规范请求并给出 Authorization：
// 上游拿到请求后会用它收到的 Host、X-Date、查询串、请求头和 body 重算签名，
// 所以「客户端到底签了什么」可以在本地用同一个 SDK 精确复现。
func volcengineV4Authorization(t *testing.T, request *http.Request, rawBody []byte, host string, credentials base.Credentials) string {
	t.Helper()
	raw := strings.TrimSpace(request.Header.Get("X-Date"))
	date, err := time.Parse("20060102T150405Z", raw)
	if err != nil {
		t.Fatalf("X-Date = %q 无法解析：%v", raw, err)
	}
	signed := base.GetSignRequest(base.RequestParam{
		Body:      rawBody,
		Method:    request.Method,
		Date:      date,
		Path:      request.URL.Path,
		Host:      host,
		QueryList: request.URL.Query(),
		Headers:   request.Header.Clone(),
	}, credentials)
	return signed.Authorization
}

func newVolcengineV4TestRequest(t *testing.T, target string, body []byte) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, target, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	return request
}

// TestVolcengineV4SignatureCoversRequestHost 固定住火山引擎 V4 签名的 host 契约：
// 参与签名的是 request.Host 字段（不是 URL.Host，也不是 Host 头）。实测把 Host 清空后同一把
// AK/SK 立刻变成 SignatureDoesNotMatch（方舟控制面 100010）；http.NewRequest 会填好
// request.Host，所以 provider 用 http.NewRequest 构造请求就不会签出空 host。
func TestVolcengineV4SignatureCoversRequestHost(t *testing.T) {
	credentials := base.Credentials{AccessKeyID: "test-ak", SecretAccessKey: "test-sk", Region: "cn-beijing", Service: "ark"}
	body := []byte(`{"QueryID":"2026071311010100000"}`)
	request := newVolcengineV4TestRequest(t, "https://ark.cn-beijing.volcengineapi.com/?Action=GetArkOfficialResult&Version=2024-01-01", body)
	if request.Host != "ark.cn-beijing.volcengineapi.com" {
		t.Fatalf("http.NewRequest 未填 request.Host：%q", request.Host)
	}
	credentials.Sign(request)
	if got, want := request.Header.Get("Authorization"), volcengineV4Authorization(t, request, body, request.Host, credentials); got != want {
		t.Fatalf("签名与真实 Host 不一致：%q != %q", got, want)
	}
	if got, want := request.Header.Get("Authorization"), volcengineV4Authorization(t, request, body, "", credentials); got == want {
		t.Fatal("签名与空 Host 的重算结果相同：Host 没有进入签名，上游会报 SignatureDoesNotMatch")
	}
}
