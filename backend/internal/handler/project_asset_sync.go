package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/service"
)

func registerProjectAssetSyncRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/projects/:id/assets/:assetId/sync", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		state, err := svc.GetProjectAssetSync(user.ID, c.Param("id"), c.Param("assetId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, state)
	})
	r.PUT("/projects/:id/assets/:assetId/sync", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		var body struct {
			ExpectedProjectRevision *int64  `json:"expectedProjectRevision" binding:"required"`
			Category                *string `json:"category"`
			FolderID                *string `json:"folderId"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		state, err := svc.SyncProjectAsset(user.ID, c.Param("id"), c.Param("assetId"), service.SyncProjectAssetRequest{ExpectedProjectRevision: *body.ExpectedProjectRevision, Category: body.Category, FolderID: body.FolderID})
		if err != nil {
			// This endpoint promises an explicit retry flag. Other handlers keep
			// their existing error projection.
			if appErr, ok := err.(*service.AppError); ok && appErr.Reason == "project_asset_sync_conflict" {
				c.JSON(appErr.Status, gin.H{"code": appErr.Code, "data": nil, "msg": appErr.Message, "reason": appErr.Reason, "retryable": appErr.Retryable})
				return
			}
			failService(c, err)
			return
		}
		ok(c, state)
	})
}
