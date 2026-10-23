package handler

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/service"
)

func realtimeOriginAllowed(r *http.Request) bool {
	raw := strings.TrimSpace(r.Header.Get("Origin"))
	origin, err := url.Parse(raw)
	if err != nil || origin.Host == "" || origin.User != nil || origin.Scheme != "http" && origin.Scheme != "https" || origin.Path != "" && origin.Path != "/" || origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}
	host := r.Host
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); forwarded != "" {
		host = strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	if strings.EqualFold(origin.Host, host) {
		return true
	}
	configured := strings.TrimSpace(os.Getenv("CANVAS_CORS_ORIGINS"))
	for _, entry := range strings.Split(configured, ",") {
		entry = strings.TrimSuffix(strings.TrimSpace(entry), "/")
		if entry == "*" || strings.EqualFold(entry, origin.Scheme+"://"+origin.Host) {
			return true
		}
	}
	if configured == "" {
		name := strings.ToLower(origin.Hostname())
		return name == "localhost" || name == "127.0.0.1" || name == "::1"
	}
	return false
}
func RegisterCanvasRealtimeRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/canvas-projects/:id/realtime", func(c *gin.Context) { serveCanvasRealtime(c, svc, 15*time.Second) })
}
func serveCanvasRealtime(c *gin.Context, svc *service.Service, interval time.Duration) {
	if c.Request.URL.RawQuery != "" {
		failService(c, service.NewAppError(400, "实时画布不接受查询参数"))
		return
	}
	if !realtimeOriginAllowed(c.Request) {
		failService(c, service.Forbidden("实时画布来源不被允许"))
		return
	}
	user, err := currentUser(c, svc)
	if err != nil {
		failService(c, err)
		return
	}
	cookie, canvasID := sessionCookie(c), c.Param("id")
	session, err := svc.OpenCanvasRealtime(c.Request.Context(), cookie, canvasID)
	if err != nil {
		failService(c, err)
		return
	}
	defer session.Close()
	conn, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{OriginPatterns: []string{c.GetHeader("Origin")}, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(32 << 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		defer cancel()
		defer conn.CloseNow()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-session.Done():
				_ = conn.Close(websocket.StatusCode(session.CloseStatus()), "reconnect and synchronize")
				return
			case data := <-session.Messages():
				session.Consumed(data)
				writeCtx, stop := context.WithTimeout(ctx, 5*time.Second)
				err := conn.Write(writeCtx, websocket.MessageText, data)
				stop()
				if err != nil {
					return
				}
			case <-ticker.C:
				checkCtx, stop := context.WithTimeout(ctx, 5*time.Second)
				err := svc.AuthorizeCanvasRealtime(checkCtx, cookie, user.ID, canvasID)
				stop()
				if err != nil {
					_ = conn.Close(websocket.StatusPolicyViolation, "session or canvas authorization revoked")
					return
				}
				pingCtx, stop := context.WithTimeout(ctx, 5*time.Second)
				err = conn.Ping(pingCtx)
				stop()
				if err != nil {
					return
				}
			}
		}
	}()
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		if kind != websocket.MessageText || session.Receive(data) != nil {
			_ = conn.Close(websocket.StatusPolicyViolation, "invalid realtime message or rate")
			break
		}
	}
	cancel()
	_ = conn.CloseNow()
	<-writerDone
}
