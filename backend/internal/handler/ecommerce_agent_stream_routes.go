package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
	"github.com/gin-gonic/gin"
	"yingce/backend/internal/ecommerceagent"
	"yingce/backend/internal/service"
)

func registerEcommerceAgentStreamRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/agent/generate/stream", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
		var req ecommerceagent.Request
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		c.Header("Content-Type", "text/event-stream; charset=utf-8")
		c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		c.Status(http.StatusOK)
		var mu sync.Mutex
		started := false
		write := func(event ecommerceagent.Event) error {
			data, err := json.Marshal(event)
			if err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			if event.Event == "workflow_started" {
				started = true
			}
			if _, err = fmt.Fprintf(c.Writer, "data: %s\n\n", data); err != nil {
				return err
			}
			c.Writer.Flush()
			return nil
		}
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-c.Request.Context().Done():
					return
				case <-ticker.C:
					mu.Lock()
					ready := started
					if !ready {
						_, _ = fmt.Fprint(c.Writer, ": preparing\n\n")
						c.Writer.Flush()
					}
					mu.Unlock()
					if ready {
						_ = write(ecommerceagent.Event{Event: "PING"})
					}
				}
			}
		}()
		_, err = svc.GenerateEcommerceAgent(c.Request.Context(), user.ID, req, write)
		if err != nil {
			code := "UPSTREAM_ERROR"
			message := "生成失败"
			var ae *service.EcommerceAgentError
			if errors.As(err, &ae) {
				code, message = ae.Code, ae.Message
			}
			if errors.Is(err, c.Request.Context().Err()) {
				code, message = "CANCELLED", "任务已取消"
			}
			_ = write(ecommerceagent.Event{Event: "message", Answer: "ERROR: " + message, Data: gin.H{"done": true, "error": true, "code": code}})
		}
	})
}
