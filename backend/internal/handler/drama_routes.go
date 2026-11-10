package handler

import (
	"log/slog"
	"mime"
	"net/http"
	"yingce/backend/internal/service"
	"github.com/gin-gonic/gin"
)

// dramaRoute 统一短剧接口的鉴权、请求体上限和响应封装；业务校验与归属检查全部在 service 内完成。
func dramaRoute(svc *service.Service, bodyLimit int64, fn func(c *gin.Context, userID string) (any, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if bodyLimit > 0 {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, bodyLimit)
		}
		data, err := fn(c, user.ID)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, data)
	}
}

// streamDramaArchive 在校验通过后才写响应头；写出中断（用户取消、存储读取失败）只记录日志，响应已开始无法再改状态码。
func streamDramaArchive(c *gin.Context, svc *service.Service, userID string, archive *service.DramaArchive) {
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": archive.FileName}))
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	c.Writer.Flush()
	if err := svc.WriteDramaArchive(c.Request.Context(), userID, archive, c.Writer); err != nil {
		slog.Warn("drama archive download interrupted", "error", err)
	}
}

func bindDramaJSON(c *gin.Context, target any) error {
	if err := c.ShouldBindJSON(target); err != nil {
		return service.BadAuthRequest("请求格式错误")
	}
	return nil
}

func registerDramaRoutes(r *gin.RouterGroup, svc *service.Service) {
	registerDramaProjectRoutes(r, svc)
	registerDramaLifecycleRoutes(r, svc)
	registerDramaChapterRoutes(r, svc)
	registerDramaEpisodeRoutes(r, svc)
	registerDramaSegmentRoutes(r, svc)
	registerDramaCanvasRoutes(r, svc)
	registerDramaExportRoutes(r, svc)
	registerDramaCastRoutes(r, svc)
	registerDramaVoiceRoutes(r, svc)
}
