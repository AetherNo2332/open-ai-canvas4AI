package handler

import (
	"errors"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"yingce/backend/internal/service"
)

// RegisterPoseLibraryRoutes 注册站点姿势库：登录用户浏览、预览和选用，管理员导入、同步和维护。
func RegisterPoseLibraryRoutes(r *gin.RouterGroup, svc *service.Service) {
	registerUserPoseLibraryRoutes(r, svc)
	registerAdminPoseLibraryImportRoutes(r, svc)
	registerAdminPoseLibraryMutationRoutes(r, svc)
}

// servePoseLibraryFile 直接输出本站数据目录里的姿势图。URL 带 ?v=同步时间，文件更新后地址随之变化，可以放心让浏览器缓存。
func servePoseLibraryFile(c *gin.Context, svc *service.Service, variant service.PoseLibraryFileVariant) {
	user, err := currentUser(c, svc)
	if err != nil {
		failService(c, err)
		return
	}
	opened, err := svc.OpenPoseLibraryFile(user, c.Param("id"), variant)
	if err != nil {
		failService(c, err)
		return
	}
	defer opened.File.Close()
	c.Header("Cache-Control", "private, max-age=604800")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	if opened.MimeType != "" {
		c.Header("Content-Type", opened.MimeType)
	}
	http.ServeContent(c.Writer, c.Request, "", opened.Info.ModTime(), opened.File)
}

func readMultipartFile(c *gin.Context, field string, maxBytes int64) ([]byte, error) {
	header, err := c.FormFile(field)
	if err != nil {
		return nil, errors.New("请选择要上传的文件")
	}
	if header.Size > maxBytes {
		return nil, errors.New("文件超过大小上限")
	}
	file, err := header.Open()
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("文件超过大小上限")
	}
	return data, nil
}

func cleanupMultipartForm(c *gin.Context) {
	if c.Request.MultipartForm != nil {
		_ = c.Request.MultipartForm.RemoveAll()
	}
}
