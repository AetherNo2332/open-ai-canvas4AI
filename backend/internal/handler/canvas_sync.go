package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"yingce/backend/internal/service"
)

func canvasEditProofMiddleware(c *gin.Context) {
	ctx, err := service.ParseCanvasEditProofHeader(c.Request.Context(), c.GetHeader(service.CanvasEditProofHeader))
	if err != nil {
		failService(c, err)
		c.Abort()
		return
	}
	c.Request = c.Request.WithContext(ctx)
	c.Next()
}
func RegisterCanvasSyncRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/canvas-projects/:id/sync-status", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		out, err := svc.CanvasSyncStatus(c.Request.Context(), user.ID, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, out)
	})
	for _, action := range []string{"acquire", "renew", "release"} {
		r.POST("/canvas-projects/:id/edit-leases/"+action, func(c *gin.Context) {
			user, err := currentUser(c, svc)
			if err != nil {
				failService(c, err)
				return
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2048)
			var proof service.CanvasEditProof
			if !decodeCanvasCommand(c, &proof) {
				return
			}
			if proof.CanvasID != "" && proof.CanvasID != c.Param("id") {
				failService(c, service.BadAuthRequest("编辑证明画布与路径不一致"))
				return
			}
			var out service.CanvasEditLeaseGrant
			switch action {
			case "acquire":
				out, err = svc.AcquireCanvasEditLease(c.Request.Context(), user.ID, c.Param("id"), proof)
			case "renew":
				out, err = svc.RenewCanvasEditLease(c.Request.Context(), user.ID, c.Param("id"), proof)
			case "release":
				err = svc.ReleaseCanvasEditLease(c.Request.Context(), user.ID, c.Param("id"), proof)
			}
			if err != nil {
				failService(c, err)
				return
			}
			if action == "release" {
				ok(c, gin.H{"released": true})
				return
			}
			c.Header("Cache-Control", "no-store")
			ok(c, out)
		})
	}
}
