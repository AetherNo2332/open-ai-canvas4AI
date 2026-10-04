package handler

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/service"
	"time"
)

func RegisterAgentCrewRunRoutes(r *gin.RouterGroup, svc *service.Service) {
	crewRoutes := r.Group("")
	crewRoutes.Use(RequireFeature(svc, service.FeatureAgentCrew))
	r = crewRoutes
	r.POST("/agent/crew-runs/:runId/cancel", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.CancelCrewRun(c.Request.Context(), user.ID, c.Param("runId")); err != nil {
			failService(c, err)
			return
		}
		run, err := svc.GetCrewRun(user.ID, c.Param("runId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, run)
	})
	r.POST("/agent/crew-runs/:runId/approvals/:approvalId", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		}
		if !decodeAgentConfiguration(c, &req) {
			return
		}
		if err := svc.DecideCrewApproval(user.ID, c.Param("runId"), c.Param("approvalId"), req.Decision, req.Reason); err != nil {
			failService(c, err)
			return
		}
		run, err := svc.GetCrewRun(user.ID, c.Param("runId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, run)
	})
	r.POST("/agent/crew-runs/:runId/commit", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req struct {
			ApprovalID           string `json:"approvalId"`
			ExpectedSnapshotHash string `json:"expectedSnapshotHash"`
			IdempotencyKey       string `json:"idempotencyKey"`
		}
		if !decodeAgentConfiguration(c, &req) {
			return
		}
		result, err := svc.CommitCrewProposal(user.ID, c.Param("runId"), req.ApprovalID, req.ExpectedSnapshotHash, req.IdempotencyKey)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.GET("/agent/workspaces/:canvasId/crew-runs", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		view, err := svc.ListCrewRuns(user.ID, c.Param("canvasId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.POST("/agent/crews/:crewId/runs", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req service.CreateCrewRunInput
		if !decodeAgentConfiguration(c, &req) {
			return
		}
		run, err := svc.CreateCrewRun(user.ID, c.Param("crewId"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, run)
	})
	r.GET("/agent/crew-runs/:runId", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		run, err := svc.GetCrewRun(user.ID, c.Param("runId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, run)
	})
	r.GET("/agent/crew-runs/:runId/events", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		after, err := taskTextEventCursor(c)
		if err != nil {
			fail(c, 400, err)
			return
		}
		run, err := svc.GetCrewRun(user.ID, c.Param("runId"))
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("X-Accel-Buffering", "no")
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		lastWrite := time.Now()
		revision := int64(-1)
		for {
			events, err := svc.CrewRunEvents(user.ID, run.ID, after)
			if err != nil {
				c.SSEvent("error", gin.H{"message": "Crew 状态读取失败"})
				c.Writer.Flush()
				return
			}
			for _, event := range events {
				writeAgentSSE(c, "crew_event", event.Sequence, event)
				after = event.Sequence
				lastWrite = time.Now()
			}
			if revision != run.Revision {
				writeAgentSSE(c, "crew_snapshot", 0, run)
				revision = run.Revision
				lastWrite = time.Now()
			}
			if after >= run.LatestSequence && (run.Status == "completed" || run.Status == "failed" || run.Status == "cancelled") {
				return
			}
			if time.Since(lastWrite) > 15*time.Second {
				_, _ = fmt.Fprint(c.Writer, ": heartbeat\n\n")
				c.Writer.Flush()
				lastWrite = time.Now()
			}
			select {
			case <-c.Request.Context().Done():
				return
			case <-ticker.C:
			}
			run, err = svc.GetCrewRun(user.ID, run.ID)
			if err != nil {
				c.SSEvent("error", gin.H{"message": "Crew 状态读取失败"})
				c.Writer.Flush()
				return
			}
		}
	})
}
