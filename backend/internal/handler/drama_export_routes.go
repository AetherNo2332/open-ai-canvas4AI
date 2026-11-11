package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"yingce/backend/internal/service"
)

func registerDramaExportRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/dramas/:id/episodes/:episodeId/jianying-draft", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		archive, err := svc.PrepareDramaJianyingDraft(user.ID, c.Param("id"), c.Param("episodeId"), service.DramaJianyingDraftRequest{DraftRoot: c.PostForm("draftRoot"), Captions: c.PostForm("captions") == "1"})
		if err != nil {
			failService(c, err)
			return
		}
		streamDramaArchive(c, svc, user.ID, archive)
	})
	// 打包资产：表单字段 episodeId 可重复，media=1 打包媒体文件，prompts=1 打包提示词。
	r.POST("/dramas/:id/asset-package", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		archive, err := svc.PrepareDramaAssetPackage(user.ID, c.Param("id"), service.DramaAssetPackageRequest{EpisodeIDs: c.PostFormArray("episodeId"), Media: c.PostForm("media") == "1", Prompts: c.PostForm("prompts") == "1"})
		if err != nil {
			failService(c, err)
			return
		}
		streamDramaArchive(c, svc, user.ID, archive)
	})
}
