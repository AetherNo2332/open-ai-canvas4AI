package handler

import (
	"errors"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"yingce/backend/internal/service"
)

func registerAdminPoseLibraryMutationRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/admin/pose-library/sync", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req service.PoseLibrarySyncRequest
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			fail(c, http.StatusBadRequest, err)
			return
		}
		result, err := svc.SyncPoseLibraryImages(c.Request.Context(), user, req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.POST("/admin/pose-library/items", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.PoseLibraryMaxImageBytes+(1<<20))
		defer cleanupMultipartForm(c)
		data, err := readMultipartFile(c, "file", service.PoseLibraryMaxImageBytes)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		item, err := svc.CreatePoseLibraryItem(user, service.PoseLibraryCreateItemRequest{
			Name: c.PostForm("name"), System: c.PostForm("system"), Category: c.PostForm("category"), Angle: c.PostForm("angle"), Code: c.PostForm("code"), Data: data,
		})
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"item": item})
	})
	r.PATCH("/admin/pose-library/items/:id", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req service.PoseLibraryUpdateItemRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		item, err := svc.UpdatePoseLibraryItem(user, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"item": item})
	})
	r.DELETE("/admin/pose-library/items/:id", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.DeletePoseLibraryItem(user, c.Param("id")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"ok": true})
	})
	r.POST("/admin/pose-library/bulk", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req service.PoseLibraryBulkRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		result, err := svc.BulkPoseLibraryItems(user, req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
}
