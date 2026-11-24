package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

// Agent orchestration is durable; the browser stream never drives execution.
func RegisterAgentRoutes(r *gin.RouterGroup, svc *service.Service) {
	scoped := r.Group("", agentTargetScopeGuard(svc))
	RegisterAgentMessageQueueRoutes(scoped, svc)
	registerAgentConversationRoutes(scoped, svc)
	registerAgentSettingsRoutes(r, svc)
	registerAgentRunRoutes(scoped, svc)
	registerAgentApprovalRoutes(scoped, svc)
	registerAgentEventStreamRoute(scoped, svc)
}

// registerAgentConversationRoutes 会话归档、列表与按会话列出运行。
func registerAgentConversationRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.DELETE("/agent/conversations/:id", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.ArchiveCloudAgentConversation(user.ID, c.Param("id")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"archived": true})
	})
	r.GET("/agent/conversations", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		limit, err := agentConversationLimit(c)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		selector, provided, err := normalizedAgentTargetScope(c, svc, user.ID)
		if err != nil {
			failService(c, err)
			return
		}
		if !provided {
			selector.Scope = service.AgentBusinessScope{Type: "canvas", ID: c.Query("canvasId")}
		} else if canvasID := c.Query("canvasId"); canvasID != "" && (selector.Scope.Type != "canvas" || selector.Scope.ID != canvasID) {
			fail(c, http.StatusBadRequest, errors.New("画布 ID 与业务范围不一致"))
			return
		}
		page, err := svc.ScopedCloudAgentConversations(user.ID, selector, c.Query("before"), limit)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, page)
	})
	r.GET("/agent/director-previews/pending", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		page, err := svc.PendingCloudAgentDirectorPreviews(user.ID)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, page)
	})
	r.GET("/agent/conversations/:id/runs", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		limit, err := agentConversationLimit(c)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		page, err := svc.CloudAgentConversationRuns(user.ID, c.Param("id"), c.Query("after"), limit)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, page)
	})
}

// registerAgentSettingsRoutes 子代理策略、技能默认值、能力与技能用量等只读或用户级设置。
func registerAgentSettingsRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/agent/subagent-policy", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		canvasID := strings.TrimSpace(c.Query("canvasId"))
		view, err := svc.AgentSubagentPolicy(user.ID, canvasID)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.PUT("/agent/subagent-policy", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		var req service.AgentSubagentPolicyRequest
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		view, err := svc.UpdateAgentSubagentPolicy(user.ID, req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.GET("/agent/runs/:id/subagents", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		view, err := svc.ListAgentSubagents(user.ID, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.GET("/agent/skill-defaults", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		view, err := svc.AgentSkillDefaultsForBusiness(user.ID, c.Query("scopeType"), c.Query("agentDefinitionId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.GET("/agent/capabilities", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		selector, provided := agentTargetScope(c)
		if provided {
			if selector.Scope.Type == "" || selector.Scope.ID == "" {
				fail(c, http.StatusBadRequest, errors.New("scoped capabilities 需要 scopeType 和 scopeId"))
				return
			}
			view, err := svc.AgentCapabilities(user.ID, selector.Scope, selector.AgentDefinitionID)
			if err != nil {
				failService(c, err)
				return
			}
			ok(c, view)
			return
		}
		capabilities := service.CloudAgentCapabilitySetInfo()
		availability, err := svc.AgentAvailability()
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"version": 2, "permissionModes": []string{"read_only", "request_approval", "auto"}, "contextScopes": []string{"canvas"}, "skills": true, "writeTools": true, "billing": "fixed_request", "maxHistoryPairs": 10, "maxHistoryBytes": 64000, "maxSteps": 0, "tools": service.CloudAgentSupportedToolNames(), "capabilitySetVersion": capabilities.Version, "capabilitySetHash": capabilities.Hash, "nodeTypes": capabilities.Nodes, "availability": availability})
	})
	// Skill usage is derived from the caller's own journal receipts, so it stays
	// read-only and never exposes another user's runs.
	r.GET("/agent/skills/usage", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		view, err := svc.CloudAgentSkillUsage(user.ID)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
}

// registerAgentRunRoutes 创建运行与追加消息、插话、切换权限、读取、取消与撤销。
func registerAgentRunRoutes(r *gin.RouterGroup, svc *service.Service) {
	create := createAgentRunHandler(svc)
	r.POST("/agent/runs", create)
	// Each additional message starts a new immutable turn and returns its ID.
	r.POST("/agent/runs/:id/messages", create)
	registerAgentRunControlRoutes(r, svc)
	registerAgentRunStateRoutes(r, svc)
}

// createAgentRunHandler 新建运行和追加消息共用：限流、严格解码单个 JSON 对象，并检查 Agent 是否可用。
func createAgentRunHandler(svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		policy, available := loadRuntimePolicy(c, svc)
		if !available || !enforceRateLimit(c, "tasks:"+user.ID, policy.Request.TaskCreatePerMinute, time.Minute) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
		var req service.CloudAgentRequest
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		if err := normalizeAgentRequestScopeFromQuery(c, svc, user.ID, &req); err != nil {
			failService(c, err)
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			fail(c, http.StatusBadRequest, errors.New("请求必须只包含一个 JSON 对象"))
			return
		}
		availability, err := svc.AgentAvailability()
		if err != nil {
			failService(c, err)
			return
		}
		if !availability.Configured {
			fail(c, http.StatusServiceUnavailable, errors.New(availability.Reason))
			return
		}
		run, err := svc.CreateCloudAgentRun(user.ID, req, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"run": run})
	}
}

// registerAgentRunControlRoutes 运行中的插话与执行权限切换。
func registerAgentRunControlRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/agent/runs/:id/interjections", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		policy, available := loadRuntimePolicy(c, svc)
		if !available || !enforceRateLimit(c, "tasks:"+user.ID, policy.Request.TaskCreatePerMinute, time.Minute) {
			return
		}
		if _, err = svc.CloudAgentRun(user.ID, c.Param("id")); err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		var req struct {
			Text      string `json:"text"`
			MessageID string `json:"messageId"`
		}
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		if req.Text != "" && (!utf8.ValidString(req.Text) || utf8.RuneCountInString(req.Text) > 4000 || strings.ContainsRune(req.Text, 0)) {
			fail(c, http.StatusBadRequest, errors.New("text 无效"))
			return
		}
		if req.MessageID != "" && (!utf8.ValidString(req.MessageID) || utf8.RuneCountInString(req.MessageID) > 160) {
			fail(c, http.StatusBadRequest, errors.New("messageId 无效"))
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			fail(c, http.StatusBadRequest, errors.New("请求必须只包含一个 JSON 对象"))
			return
		}
		pending, err := svc.InterjectCloudAgent(user.ID, c.Param("id"), req.MessageID, req.Text)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"accepted": true, "pending": pending})
	})
	// 运行中切换执行权限：下一次工具调用生效，正在等待的审批卡保持原样。
	r.POST("/agent/runs/:id/permission", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		policy, available := loadRuntimePolicy(c, svc)
		if !available || !enforceRateLimit(c, "agent-permission:"+user.ID, policy.Request.CanvasWritePerMinute, time.Minute) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
		var req struct {
			PermissionMode string `json:"permissionMode"`
			RequestID      string `json:"requestId"`
		}
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			fail(c, http.StatusBadRequest, errors.New("请求必须只包含一个 JSON 对象"))
			return
		}
		result, err := svc.ChangeCloudAgentPermission(user.ID, c.Param("id"), req.PermissionMode, req.RequestID)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
}

// registerAgentRunStateRoutes 读取运行状态、取消与逐步撤销。
func registerAgentRunStateRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/agent/runs/:id", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		// 运行详情只返回一页事件，分页参数越界或非法一律拒绝：静默夹取会让客户端
		// 以为拿到的是它请求的那一页，从而把缺口当成"没有更多记录"。
		options, err := agentRunViewOptions(c)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		run, err := svc.CloudAgentRun(user.ID, c.Param("id"), options)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"run": run})
	})
	r.POST("/agent/runs/:id/cancel", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err = svc.CancelCloudAgent(c.Request.Context(), user.ID, c.Param("id")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"accepted": true})
	})
	r.POST("/agent/runs/:id/undo", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		var req struct {
			Reason string `json:"reason"`
		}
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		// 请求体可省略；撤销范围由运行本身决定，不接受客户端指定的步骤或快照。
		if err := decoder.Decode(&req); err != nil && err != io.EOF {
			fail(c, http.StatusBadRequest, err)
			return
		}
		if req.Reason != "" && (!utf8.ValidString(req.Reason) || utf8.RuneCountInString(req.Reason) > 2000 || strings.ContainsAny(req.Reason, "\x00\r\n")) {
			fail(c, http.StatusBadRequest, errors.New("reason 无效"))
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			fail(c, http.StatusBadRequest, errors.New("请求必须只包含一个 JSON 对象"))
			return
		}
		result, err := svc.UndoCloudAgentRun(user.ID, c.Param("id"), req.Reason)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
}

// registerAgentApprovalRoutes 审批决策与导演台预览回传。
func registerAgentApprovalRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/agent/runs/:id/approvals/:approvalId/decision", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if _, err = svc.CloudAgentRun(user.ID, c.Param("id")); err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		var req struct {
			Decision      string                           `json:"decision"`
			Reason        string                           `json:"reason"`
			MediaSettings *service.CloudAgentMediaSettings `json:"mediaSettings,omitempty"`
		}
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		if req.Decision != "approve" && req.Decision != "reject" {
			fail(c, http.StatusBadRequest, errors.New("decision 无效"))
			return
		}
		if req.Reason != "" && (!utf8.ValidString(req.Reason) || utf8.RuneCountInString(req.Reason) > 2000 || strings.ContainsAny(req.Reason, "\x00\r\n")) {
			fail(c, http.StatusBadRequest, errors.New("reason 无效"))
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			fail(c, http.StatusBadRequest, errors.New("请求必须只包含一个 JSON 对象"))
			return
		}
		if err := svc.DecideCloudAgentApproval(user.ID, c.Param("id"), c.Param("approvalId"), req.Decision, req.Reason, req.MediaSettings); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"accepted": true})
	})
	// 导演台浏览器视口录制完成（或失败）后回传结果，恢复 Agent 挂起的 director preview。
	r.POST("/agent/runs/:id/director-preview", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		var req struct {
			RequestID       string `json:"requestId"`
			VideoResourceID string `json:"videoResourceId"`
			Error           string `json:"error"`
		}
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			fail(c, http.StatusBadRequest, errors.New("请求必须只包含一个 JSON 对象"))
			return
		}
		if !utf8.ValidString(req.RequestID) || !utf8.ValidString(req.VideoResourceID) || !utf8.ValidString(req.Error) {
			fail(c, http.StatusBadRequest, errors.New("请求包含无效字符"))
			return
		}
		if err := svc.SubmitCloudAgentDirectorPreview(user.ID, c.Param("id"), req.RequestID, req.VideoResourceID, req.Error); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"accepted": true})
	})
}

// registerAgentEventStreamRoute 运行事件 SSE：游标为事件 seq，断线用 Last-Event-ID 或 ?after= 续传。
func registerAgentEventStreamRoute(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/agent/runs/:id/events", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		// 游标就是事件 seq：先解析它，才能只把 after 之后的增量交给视图，
		// 断线重连不必重新载入前面已经发过的事件。
		// Last-Event-ID（或 ?after=）用的就是这个游标。
		after, err := taskTextEventCursor(c)
		if err != nil {
			fail(c, 400, err)
			return
		}
		options := service.CloudAgentRunViewOptions{SinceSeq: int(after)}
		run, err := svc.CloudAgentRun(user.ID, c.Param("id"), options)
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("X-Accel-Buffering", "no")
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		revision := run.Revision
		lastWrite := time.Now()
		for {
			if err != nil {
				c.SSEvent("error", gin.H{"message": "Agent 状态读取失败"})
				c.Writer.Flush()
				return
			}
			if run != nil {
				for _, event := range run.Events {
					if int64(event.Seq) > after {
						writeAgentSSE(c, "agent_event", int64(event.Seq), event)
						after = int64(event.Seq)
					}
				}
				// Snapshots are state observations, not replayable Agent events; they
				// intentionally have no SSE id and therefore never advance the cursor.
				snapshot := *run
				snapshot.Events = nil // Events were sent once above, never in every snapshot too.
				writeAgentSSE(c, "run_snapshot", 0, &snapshot)
				revision, lastWrite = run.Revision, time.Now()
				if !run.CleanupPending && (run.Status == "completed" || run.Status == "failed" || run.Status == "cancelled" || run.Status == "rejected") {
					return
				}
			} else if time.Since(lastWrite) >= 15*time.Second {
				_, _ = fmt.Fprint(c.Writer, ": heartbeat\n\n")
				c.Writer.Flush()
				lastWrite = time.Now()
			}
			select {
			case <-c.Request.Context().Done():
				return
			case <-ticker.C:
				run, err = svc.CloudAgentRunIfChanged(user.ID, c.Param("id"), revision, options)
			}
		}
	})
}

func writeAgentSSE(c *gin.Context, event string, id int64, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	if id > 0 {
		_, _ = fmt.Fprintf(c.Writer, "id: %d\n", id)
	}
	_, _ = fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, data)
	c.Writer.Flush()
}
