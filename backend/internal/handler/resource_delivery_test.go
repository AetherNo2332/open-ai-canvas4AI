package handler

import (
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/assets"
	"yingce/backend/internal/model"
	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
)

func TestServeResourceDeliveryRedirectsCloudMediaWithoutWritingBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/resource", func(c *gin.Context) {
		serveResourceDelivery(c, &service.ResourceDelivery{Access: &assets.ResourceAccess{
			ResourceID: "resource-1",
			URL:        "https://cdn.example.com/users/user-1/image/test.png?signature=redacted",
			Delivery:   assets.DeliveryCDN,
		}}, "private, no-cache", "")
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/resource", nil))

	if recorder.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307", recorder.Code)
	}
	if got := recorder.Header().Get("Location"); got != "https://cdn.example.com/users/user-1/image/test.png?signature=redacted" {
		t.Fatalf("location = %q", got)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("redirect wrote %d body bytes; platform must not send cloud media", recorder.Body.Len())
	}
}

func TestServeResourceDeliveryReturnsAccessDescriptorWithoutRedirect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/resource", func(c *gin.Context) {
		serveResourceDelivery(c, &service.ResourceDelivery{Access: &assets.ResourceAccess{
			ResourceID: "resource-1",
			URL:        "https://cdn.example.com/test.png",
			Delivery:   assets.DeliveryCDN,
		}}, "private, no-cache", "")
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/resource?access=1", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("cache-control = %q", got)
	}
	if !strings.Contains(recorder.Body.String(), `"url":"https://cdn.example.com/test.png"`) {
		t.Fatalf("access descriptor missing from body: %s", recorder.Body.String())
	}
}

func TestServeResourceDeliveryStreamsPlatformBytesAndRangeMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resource := &model.Resource{ID: "resource-1", MimeType: "video/mp4", UpdatedAt: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)}
	router := gin.New()
	router.GET("/resource", func(c *gin.Context) {
		serveResourceDelivery(c, &service.ResourceDelivery{
			Resource: resource,
			Access:   &assets.ResourceAccess{ResourceID: resource.ID, URL: "/api/resources/resource-1/file", Delivery: assets.DeliveryProxy},
			Stream:   &assets.ResourceStream{Resource: resource, Body: io.NopCloser(strings.NewReader("partial-media")), StatusCode: http.StatusPartialContent, ContentLength: 13, ContentRange: "bytes 10-22/100", AcceptRanges: "bytes"},
		}, "private, no-cache", `inline; filename="video.mp4"`)
	})

	recorder := httptest.NewRecorder()
	recorder.HeaderMap = make(http.Header)
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/resource", nil))

	if recorder.Code != http.StatusPartialContent || recorder.Body.String() != "partial-media" {
		t.Fatalf("response = status %d body %q", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Content-Range") != "bytes 10-22/100" || recorder.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("range headers missing: %#v", recorder.Header())
	}
	if recorder.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("content-type = %q", recorder.Header().Get("Content-Type"))
	}
	if recorder.Header().Get("Content-Disposition") != `inline; filename="video.mp4"` {
		t.Fatalf("content-disposition = %q", recorder.Header().Get("Content-Disposition"))
	}
}

func TestResourceAccessOptionsCarriesDownloadNameForPlatformDelivery(t *testing.T) {

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/resource?download=1&downloadName=%E7%89%87%E5%A4%B4%E9%95%9C%E5%A4%B4.mp4", nil)

	options := resourceAccessOptions(c)
	if options.Purpose != assets.PurposeDownload {
		t.Fatalf("purpose = %q, want download", options.Purpose)
	}
	if options.DownloadName != "片头镜头.mp4" {
		t.Fatalf("download name = %q, want node title", options.DownloadName)
	}
}

func TestServeResourceDeliveryUsesRequestedDownloadName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resource := &model.Resource{ID: "resource-1", MimeType: "video/mp4", UpdatedAt: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)}
	router := gin.New()
	router.GET("/resource", func(c *gin.Context) {
		options := resourceAccessOptions(c)
		disposition := mime.FormatMediaType("attachment", map[string]string{"filename": options.DownloadName})
		serveResourceDelivery(c, &service.ResourceDelivery{
			Resource: resource,
			Access:   &assets.ResourceAccess{ResourceID: resource.ID, URL: "/api/resources/resource-1/file", Delivery: assets.DeliveryProxy},
			Stream:   &assets.ResourceStream{Resource: resource, Body: io.NopCloser(strings.NewReader("media")), StatusCode: http.StatusOK, ContentLength: 5},
		}, "private, no-cache", disposition)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/resource?download=1&downloadName=%E7%89%87%E5%A4%B4%E9%95%9C%E5%A4%B4.mp4", nil))
	// mime.FormatMediaType 对非 ASCII 文件名输出 RFC 2231 扩展参数
	// （filename*=utf-8''...），因此解析参数校验而不是比对原始报文。
	dispositionType, params, err := mime.ParseMediaType(recorder.Header().Get("Content-Disposition"))
	if err != nil || dispositionType != "attachment" || params["filename"] != "片头镜头.mp4" {
		t.Fatalf("content-disposition = %q (err = %v)", recorder.Header().Get("Content-Disposition"), err)
	}
}

func TestServeResourceDeliveryForcesAttachmentAndSandboxForNonMedia(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mediaType := range []string{"image/svg+xml", "application/xml", "application/pdf", "application/octet-stream", "image/png"} {
		t.Run(mediaType, func(t *testing.T) {
			resource := &model.Resource{ID: "unsafe", MimeType: mediaType, ObjectKey: "users/u/fake.png"}
			router := gin.New()
			router.GET("/resource", func(c *gin.Context) {
				serveResourceDelivery(c, &service.ResourceDelivery{Access: &assets.ResourceAccess{Delivery: assets.DeliveryLocal}, Stream: &assets.ResourceStream{Resource: resource, Body: io.NopCloser(strings.NewReader("<html><script>alert(1)</script></html>")), StatusCode: 200, ContentLength: 36}}, "private, no-cache", `inline; filename="fake.png"`)
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/resource", nil))
			if recorder.Code != 200 || !strings.HasPrefix(recorder.Header().Get("Content-Disposition"), "attachment") || recorder.Header().Get("Content-Security-Policy") != "sandbox; default-src 'none'" || recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("unsafe response: %d %#v", recorder.Code, recorder.Header())
			}
			if recorder.Header().Get("Content-Type") != "text/html" {
				t.Fatalf("untrusted MIME survived: %q", recorder.Header().Get("Content-Type"))
			}
		})
	}
}
