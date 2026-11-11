package handler

import (
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"net/http"
	"strings"
	"yingce/backend/internal/service"
)

func registerAdminPoseLibraryImportRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/admin/pose-library/items", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		page, pageSize, err := parsePaginationQuery(c, 50)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		result, err := svc.AdminPoseLibraryItems(user, service.PoseLibraryAdminQuery{
			Keyword: c.Query("keyword"), SourceKey: c.Query("source"), System: c.Query("system"), Status: c.Query("status"), Page: page, PageSize: pageSize,
		})
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.POST("/admin/pose-library/import", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.PoseLibraryMaxImportBytes+(1<<20))
		defer cleanupMultipartForm(c)
		data, err := readMultipartFile(c, "file", service.PoseLibraryMaxImportBytes)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		var systems []string
		if raw := strings.TrimSpace(c.PostForm("systems")); raw != "" {
			if err := json.Unmarshal([]byte(raw), &systems); err != nil {
				fail(c, http.StatusBadRequest, errors.New("systems 须为 JSON 字符串数组"))
				return
			}
		}
		result, err := svc.ImportPoseLibrary(user, service.PoseLibraryImportRequest{Data: data, BaseURL: c.PostForm("baseUrl"), Systems: systems})
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
}
