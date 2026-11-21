package handler

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/model"
	"yingce/backend/internal/service"
)

type agentWakeSSE struct {
	ctx        *gin.Context
	svc        *service.Service
	after      int64
	scope      string
	heartbeat  *time.Ticker
	controller *http.ResponseController
}

func agentWakeStream(svc *service.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		after, err := strconv.ParseInt(firstAgentCursor(ctx), 10, 64)
		if err != nil || after < 0 {
			ctx.AbortWithStatus(http.StatusBadRequest)
			return
		}
		ctx.Header("Content-Type", "text/event-stream")
		ctx.Header("Cache-Control", "no-cache")
		ctx.Header("X-Accel-Buffering", "no")
		ctx.Writer.WriteHeader(http.StatusOK)
		ctx.Writer.Flush()
		stream := agentWakeSSE{ctx: ctx, svc: svc, after: after, scope: ctx.GetString("agentWorkerUserID"),
			heartbeat: time.NewTicker(25 * time.Second), controller: http.NewResponseController(ctx.Writer)}
		defer stream.heartbeat.Stop()
		for ctx.Request.Context().Err() == nil {
			events, boundary, ready, unsubscribe := svc.AgentWakeSubscription(stream.scope)
			if events == nil {
				if !stream.poll(ready) {
					return
				}
				continue
			}
			retry := stream.live(events, boundary)
			unsubscribe()
			if !retry {
				return
			}
		}
	}
}

func (stream *agentWakeSSE) poll(ready <-chan struct{}) bool {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := stream.page(math.MaxInt64); err != nil {
			return false
		}
		select {
		case <-stream.ctx.Request.Context().Done():
			return false
		case <-ready:
			return true
		case <-ticker.C:
		case <-stream.heartbeat.C:
			if stream.sendHeartbeat() != nil {
				return false
			}
		}
	}
}

func (stream *agentWakeSSE) live(events <-chan model.AgentWakeEvent, boundary int64) bool {
	for stream.after < boundary && stream.ctx.Request.Context().Err() == nil {
		full, err := stream.page(boundary)
		if err != nil {
			return false
		}
		if !full {
			break
		}
	}
	for {
		select {
		case <-stream.ctx.Request.Context().Done():
			return false
		case event, ok := <-events:
			if !ok {
				return true
			}
			if event.Sequence > stream.after {
				if err := stream.write(event); err != nil {
					return false
				}
				stream.flush()
			}
		case <-stream.heartbeat.C:
			if stream.sendHeartbeat() != nil {
				return false
			}
		}
	}
}

func (stream *agentWakeSSE) page(boundary int64) (bool, error) {
	events, err := stream.svc.PiWakeEventsForWorker(stream.after, stream.scope)
	if err != nil {
		return false, err
	}
	for _, event := range events {
		if event.Sequence > boundary {
			break
		}
		if err := stream.write(event); err != nil {
			return false, err
		}
	}
	if len(events) > 0 {
		stream.flush()
	}
	return len(events) == 128 && events[127].Sequence < boundary, nil
}

func (stream *agentWakeSSE) write(event model.AgentWakeEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_ = stream.controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err = stream.ctx.Writer.Write([]byte("id: " + strconv.FormatInt(event.Sequence, 10) + "\ndata: " + string(body) + "\n\n"))
	if err == nil {
		stream.after = event.Sequence
	}
	return err
}

func (stream *agentWakeSSE) sendHeartbeat() error {
	_ = stream.controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := stream.ctx.Writer.Write([]byte(": heartbeat\n\n"))
	if err == nil {
		stream.flush()
	}
	return err
}

func (stream *agentWakeSSE) flush() {
	stream.ctx.Writer.Flush()
	_ = stream.controller.SetWriteDeadline(time.Time{})
}
