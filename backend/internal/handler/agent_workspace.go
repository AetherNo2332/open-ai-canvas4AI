package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/service"
)

// Agent configuration writes reject missing revision, unknown fields and trailing JSON.
func decodeAgentConfiguration(c *gin.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		fail(c, http.StatusBadRequest, err)
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		fail(c, http.StatusBadRequest, errors.New("只接受一个 JSON 对象"))
		return false
	}
	return true
}

func RegisterAgentWorkspaceRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/agent/workspaces/:canvasId", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		view, err := svc.GetAgentWorkspace(user.ID, c.Param("canvasId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.PATCH("/agent/workspaces/:canvasId", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req struct {
			Revision *int64  `json:"revision"`
			AgentsMD *string `json:"agentsMd"`
		}
		if !decodeAgentConfiguration(c, &req) {
			return
		}
		if req.Revision == nil || req.AgentsMD == nil {
			fail(c, 400, errors.New("revision 和 agentsMd 必填"))
			return
		}
		view, err := svc.UpdateAgentWorkspace(user.ID, c.Param("canvasId"), *req.Revision, *req.AgentsMD)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.GET("/agent/workspaces/:canvasId/skills", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		view, err := svc.ListWorkspaceSkills(user.ID, c.Param("canvasId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.PUT("/agent/workspaces/:canvasId/skills", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req struct {
			Revision *int64                    `json:"revision"`
			Skills   *[]service.SkillSelection `json:"skills"`
		}
		if !decodeAgentConfiguration(c, &req) {
			return
		}
		if req.Revision == nil || req.Skills == nil {
			fail(c, 400, errors.New("revision 和 skills 必填（清空请传 []）"))
			return
		}
		view, err := svc.ReplaceWorkspaceSkills(user.ID, c.Param("canvasId"), *req.Revision, *req.Skills)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
}
