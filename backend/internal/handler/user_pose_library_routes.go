package handler

import (
	"net/http"
	"time"
	"yingce/backend/internal/service"
	"github.com/gin-gonic/gin"
)

func registerUserPoseLibraryRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/pose-library/poses", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		page, pageSize, err := parsePaginationQuery(c, 24)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		catalog, err := svc.PoseLibraryCatalog(user, service.PoseLibraryCatalogQuery{
			Keyword: c.Query("keyword"), System: c.Query("system"), Category: c.Query("category"), Angle: c.Query("angle"), Page: page, PageSize: pageSize,
		})
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, catalog)
	})
	r.GET("/pose-library/items/:id/thumb", func(c *gin.Context) { servePoseLibraryFile(c, svc, service.PoseLibraryFileThumb) })
	r.GET("/pose-library/items/:id/image", func(c *gin.Context) { servePoseLibraryFile(c, svc, service.PoseLibraryFileImage) })
	r.POST("/pose-library/use", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		policy, available := loadRuntimePolicy(c, svc)
		if !available || !enforceRateLimit(c, "pose-library-use:"+user.ID, policy.Request.ResourceImportPerMinute, time.Minute) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		var req struct {
			IDs []string `json:"ids"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		items, err := svc.UsePoseLibraryItems(user, req.IDs)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"items": items})
	})
}
