package storage

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDisplaySigningUsesStablePrivateCacheWindowForAllProviders(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	signedAt := time.Now().UTC().Truncate(time.Hour)
	expires := signedAt.Add(2 * time.Hour)
	cases := map[string]Settings{
		"s3":      {Provider: "s3", Region: "us-east-1", Endpoint: server.URL, PathStyle: true, Bucket: "bucket", AccessKeyID: "id", AccessKeySecret: "secret", SessionToken: "session"},
		"qiniu":   {Provider: "qiniu", Region: "z0", Bucket: "bucket", AccessKeyID: "id", AccessKeySecret: "secret"},
		"tencent": {Provider: "tencent", Endpoint: "https://cos.ap-guangzhou.myqcloud.com", Bucket: "bucket-1250000000", AccessKeyID: "id", AccessKeySecret: "secret"},
		"aliyun":  {Provider: "aliyun", Endpoint: "https://oss-cn-hangzhou.aliyuncs.com", Bucket: "bucket", AccessKeyID: "id", AccessKeySecret: "secret"},
	}
	for name, setting := range cases {
		t.Run(name, func(t *testing.T) {
			first, err := SignedOriginObjectDisplayURL(setting, "users/u/a b.png", signedAt, expires, 90*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			// Cross the SDK second boundary to catch a hidden time.Now in any signer.
			time.Sleep(1100 * time.Millisecond)
			second, err := SignedOriginObjectDisplayURL(setting, "users/u/a b.png", signedAt, expires, 90*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if first != second {
				t.Fatalf("URL changes within signing window: %s / %s", first, second)
			}
			parsed, _ := url.Parse(first)
			query := parsed.Query()
			if query.Get("response-cache-control") != "private, max-age=5400, immutable" {
				t.Fatalf("cache policy = %q", query.Get("response-cache-control"))
			}
			switch name {
			case "s3", "qiniu":
				if query.Get("X-Amz-Date") != signedAt.Format("20060102T150405Z") || query.Get("X-Amz-Expires") != "7200" {
					t.Fatalf("wrong SigV4 window: %s", first)
				}
			case "tencent":
				if query.Get("q-sign-time") != strconv.FormatInt(signedAt.Unix(), 10)+";"+strconv.FormatInt(expires.Unix(), 10) {
					t.Fatalf("wrong COS window: %s", first)
				}
			}
			if name == "s3" && (query.Get("X-Amz-Security-Token") != "session" || parsed.Path != "/bucket/users/u/a b.png") {
				t.Fatalf("explicit path style/session lost: %s", first)
			}
			download, err := SignedOriginObjectDownloadURL(setting, "users/u/a b.png", time.Now().Add(time.Hour), "镜头.png")
			if err != nil {
				t.Fatal(err)
			}
			oneShot, _ := url.Parse(download)
			if oneShot.Query().Get("response-cache-control") != "" || !strings.HasPrefix(oneShot.Query().Get("response-content-disposition"), "attachment") {
				t.Fatalf("download contract changed: %s", download)
			}
		})
	}
}

func TestPublicCDNDownloadBuilderRejectsMissingForwardingContract(t *testing.T) {
	setting := Settings{CDNBaseURL: "https://cdn.example.com", Delivery: DeliverySettings{CDNAuthMode: "public"}}
	if _, err := PublicCDNObjectDownloadURL(setting, "users/u/file.png", "file.png"); err == nil {
		t.Fatal("missing contract accepted")
	}
	setting.Delivery.CDNAttachmentQueryForwarding = true
	value, err := PublicCDNObjectDownloadURL(setting, "users/u/a b.png", "镜头.png")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(value)
	if parsed.Path != "/users/u/a b.png" || !strings.HasPrefix(parsed.Query().Get("response-content-disposition"), "attachment") {
		t.Fatalf("bad CDN download: %s", value)
	}
	setting.Delivery.CDNAuthMode = "qiniu"
	if _, err := PublicCDNObjectDownloadURL(setting, "a.png", ""); err == nil {
		t.Fatal("private CDN must not use public download builder")
	}
}
