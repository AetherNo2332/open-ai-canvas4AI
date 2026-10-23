package handler

import (
	"bufio"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"infinite-canvas/backend/internal/assets"
	"infinite-canvas/backend/internal/service"

	"github.com/gin-gonic/gin"
)

func resourceAccessOptions(c *gin.Context) service.ResourceAccessOptions {
	purpose := assets.AccessPurpose(strings.TrimSpace(c.Query("purpose")))
	if purpose == "" && c.Query("download") == "1" {
		purpose = assets.PurposeDownload
	}
	variant := assets.ResourceVariant(strings.TrimSpace(c.Query("variant")))
	return service.ResourceAccessOptions{
		Purpose:      purpose,
		Variant:      variant,
		DownloadName: strings.TrimSpace(c.Query("downloadName")),
		DescribeOnly: c.Query("access") == "1",
	}
}

// serveResourceDelivery is the only HTTP byte/redirect executor. Authorization and
// delivery selection happen in app; this function only applies the already-resolved
// contract to an HTTP response.
func serveResourceDelivery(c *gin.Context, delivery *service.ResourceDelivery, cacheControl string, disposition string) {
	if delivery == nil || delivery.Access == nil {
		fail(c, http.StatusServiceUnavailable, fmt.Errorf("资源分发结果无效"))
		return
	}
	access := delivery.Access
	if access.URL == "" && delivery.Stream == nil {
		fail(c, http.StatusServiceUnavailable, fmt.Errorf("资源分发结果无效"))
		return
	}
	if c.Query("access") == "1" {
		c.Header("Cache-Control", "private, no-store")
		c.Header("Referrer-Policy", "no-referrer")
		ok(c, gin.H{"access": access})
		return
	}
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Accept-Ranges", "bytes")
	if access.Delivery == assets.DeliveryCDN || access.Delivery == assets.DeliveryOrigin {
		c.Header("Cache-Control", "private, no-store")
		// Do not let Gin generate its default HTML redirect body. A media request
		// should transfer only the redirect metadata; the actual bytes must be
		// fetched from the CDN/origin selected by the access contract.
		c.Header("Location", access.URL)
		c.Status(http.StatusTemporaryRedirect)
		return
	}
	stream := delivery.Stream
	if stream == nil || stream.Body == nil {
		fail(c, http.StatusServiceUnavailable, fmt.Errorf("资源流不可用"))
		return
	}
	defer stream.Body.Close()
	if cacheControl != "" {
		c.Header("Cache-Control", cacheControl)
	}
	if disposition == "" && strings.TrimSpace(c.Query("downloadName")) != "" && resourceAccessOptions(c).Purpose == assets.PurposeDownload {
		disposition = mime.FormatMediaType("attachment", map[string]string{"filename": strings.TrimSpace(c.Query("downloadName"))})
	}
	resource := stream.Resource
	mimeType := resource.MimeType
	var body io.Reader = stream.Body
	// Full local streams can contain historical mislabelled content. Peek
	// without consuming bytes; never attempt to sniff a middle byte range.
	if stream.ContentRange == "" && stream.StatusCode != http.StatusPartialContent {
		if seeker, ok := stream.Body.(io.ReadSeeker); ok {
			position, err := seeker.Seek(0, io.SeekCurrent)
			if err == nil && position == 0 {
				prefix := make([]byte, 512)
				n, _ := seeker.Read(prefix)
				if _, err := seeker.Seek(position, io.SeekStart); err != nil {
					fail(c, http.StatusInternalServerError, fmt.Errorf("资源流无法重置"))
					return
				}
				mimeType = assets.DetectedContentType(prefix[:n], mimeType, resource.ObjectKey)
			}
		} else {
			reader := bufio.NewReader(stream.Body)
			prefix, _ := reader.Peek(512)
			mimeType = assets.DetectedContentType(prefix, mimeType, resource.ObjectKey)
			body = reader
		}
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	if !assets.InlineMediaType(mimeType) {
		// Preserve a requested filename, but an explicit inline disposition may
		// never override the safety boundary for active/document/unknown bytes.
		_, params, _ := mime.ParseMediaType(disposition)
		if params == nil {
			params = map[string]string{}
		}
		if params["filename"] == "" {
			params["filename"] = resource.ID
		}
		disposition = mime.FormatMediaType("attachment", params)
		c.Header("Content-Security-Policy", "sandbox; default-src 'none'")
	}
	if disposition != "" {
		c.Header("Content-Disposition", disposition)
	}
	if stream.ContentRange != "" {
		c.Header("Content-Range", stream.ContentRange)
	}
	if stream.AcceptRanges != "" {
		c.Header("Accept-Ranges", stream.AcceptRanges)
	}
	if seeker, ok := stream.Body.(io.ReadSeeker); ok {
		c.Header("Content-Type", mimeType)
		http.ServeContent(c.Writer, c.Request, resource.ID, resource.UpdatedAt, seeker)
		return
	}
	c.DataFromReader(stream.StatusCode, stream.ContentLength, mimeType, body, nil)
}
