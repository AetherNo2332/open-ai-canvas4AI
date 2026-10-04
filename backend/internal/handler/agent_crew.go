package handler

import (
	"errors"
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/service"
)

func RegisterAgentCrewRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/agent/workspaces/:canvasId/crews", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		view, err := svc.ListCrews(user.ID, c.Param("canvasId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.POST("/agent/workspaces/:canvasId/crews", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req service.CreateCrewInput
		if !decodeAgentConfiguration(c, &req) {
			return
		}
		view, err := svc.CreateCrew(user.ID, c.Param("canvasId"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.GET("/agent/crews/:crewId", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		view, err := svc.GetCrew(user.ID, c.Param("crewId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.PATCH("/agent/crews/:crewId", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req struct {
			Revision *int64 `json:"revision"`
			service.UpdateCrewInput
		}
		if !decodeAgentConfiguration(c, &req) {
			return
		}
		if req.Revision == nil {
			fail(c, 400, errors.New("revision 必填"))
			return
		}
		view, err := svc.UpdateCrew(user.ID, c.Param("crewId"), *req.Revision, req.UpdateCrewInput)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.DELETE("/agent/crews/:crewId", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req struct {
			Revision *int64 `json:"revision"`
		}
		if !decodeAgentConfiguration(c, &req) {
			return
		}
		if req.Revision == nil {
			fail(c, 400, errors.New("revision 必填"))
			return
		}
		revision, err := svc.DeleteCrew(user.ID, c.Param("crewId"), *req.Revision)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"revision": revision})
	})
	saveMember := func(c *gin.Context, create bool) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req struct {
			Revision *int64 `json:"revision"`
			Enabled  *bool  `json:"enabled"`
			Position *int   `json:"position"`
			service.CrewMemberInput
		}
		if !decodeAgentConfiguration(c, &req) {
			return
		}
		if req.Revision == nil || req.Enabled == nil || req.Position == nil {
			fail(c, 400, errors.New("revision、enabled、position 必填"))
			return
		}
		req.CrewMemberInput.Enabled = *req.Enabled
		req.CrewMemberInput.Position = *req.Position
		var view *service.CrewView
		if create {
			view, err = svc.AddCrewMember(user.ID, c.Param("crewId"), *req.Revision, req.CrewMemberInput)
		} else {
			view, err = svc.UpdateCrewMember(user.ID, c.Param("memberId"), *req.Revision, req.CrewMemberInput)
		}
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	}
	r.POST("/agent/crews/:crewId/members", func(c *gin.Context) { saveMember(c, true) })
	r.PATCH("/agent/crew-members/:memberId", func(c *gin.Context) { saveMember(c, false) })
	r.DELETE("/agent/crew-members/:memberId", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req struct {
			Revision *int64 `json:"revision"`
		}
		if !decodeAgentConfiguration(c, &req) {
			return
		}
		if req.Revision == nil {
			fail(c, 400, errors.New("revision 必填"))
			return
		}
		view, err := svc.DeleteCrewMember(user.ID, c.Param("memberId"), *req.Revision)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.PUT("/agent/crew-members/:memberId/skills", func(c *gin.Context) {
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
			fail(c, 400, errors.New("revision 和 skills 必填"))
			return
		}
		view, err := svc.ReplaceCrewMemberSkills(user.ID, c.Param("memberId"), *req.Revision, *req.Skills)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
}
