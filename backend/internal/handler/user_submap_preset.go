package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

// RegisterUserSubmapPresetRoutes exposes user-private submap presets. Ownership
// is enforced from the authenticated user on every operation; no route accepts
// a user id from the body or path.
func RegisterUserSubmapPresetRoutes(r *gin.RouterGroup, svc *service.Service) {
	// manifestJson embeds the JSON document as a string, so quote-heavy snapshots
	// can roughly double in transport. Keep the accepted document limit at 512 KiB
	// while allowing that envelope overhead.
	const maxBodyBytes = 2 << 20

	r.GET("/user-submap-presets", func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store")
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		presets, err := svc.ListUserSubmapPresets(user.ID)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"presets": presets})
	})
	r.POST("/user-submap-presets", func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store")
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		var req service.UserSubmapPresetRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, service.BadAuthRequest("预制请求格式错误"))
			return
		}
		preset, err := svc.CreateUserSubmapPreset(user.ID, req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"preset": preset})
	})
	r.PATCH("/user-submap-presets/:id", func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store")
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		var req service.UserSubmapPresetRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, service.BadAuthRequest("预制请求格式错误"))
			return
		}
		preset, err := svc.UpdateUserSubmapPreset(user.ID, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"preset": preset})
	})
	r.DELETE("/user-submap-presets/:id", func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store")
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.DeleteUserSubmapPreset(user.ID, c.Param("id")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"id": c.Param("id")})
	})
}
