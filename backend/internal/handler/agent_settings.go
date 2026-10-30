package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterAgentMemoryRoutes 注册用户级 Agent 设置：全局自定义系统提示词与长期记忆文件。
func RegisterAgentMemoryRoutes(r *gin.RouterGroup, svc *service.Service) {
	registerAgentMemoryDocumentRoutes(r, svc)
	r.GET("/agent/settings", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		view, err := svc.UserAgentSystemPrompt(user.ID)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.PATCH("/agent/settings", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req struct {
			SystemPrompt *string `json:"systemPrompt"`
			Revision     *int64  `json:"revision"`
		}
		if err := decodeAgentJSON(c, &req, 80<<10); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		if req.SystemPrompt == nil || req.Revision == nil {
			fail(c, http.StatusBadRequest, errors.New("缺少 systemPrompt 或 revision"))
			return
		}
		view, err := svc.UpdateUserAgentSystemPrompt(user.ID, service.AgentSystemPromptRequest{SystemPrompt: *req.SystemPrompt, Revision: *req.Revision})
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
}

// RegisterAgentMemoryAdminRoutes 注册管理员只读查看记忆文件的接口（权限在 service 校验）。
func RegisterAgentMemoryAdminRoutes(r *gin.RouterGroup, svc *service.Service) {
	registerAgentMemoryDocumentAdminRoutes(r, svc)
}

func decodeAgentJSON(c *gin.Context, dest any, maxBytes int64) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("请求必须只包含一个 JSON 对象")
	}
	return nil
}
