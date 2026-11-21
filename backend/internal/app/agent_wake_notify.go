package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

const wakeRecoveryInterval = 5 * time.Second

type agentWakeSubscriber struct {
	scope  string
	events chan model.AgentWakeEvent
}

type agentWakeHub struct {
	mu                    sync.Mutex
	repo                  *repository.Repository
	subscribers           map[*agentWakeSubscriber]struct{}
	ready                 chan struct{}
	online                bool
	watermark             int64
	rowsRead, deliveries  int64
	disconnects, overflow int64
	cancel                context.CancelFunc
	done                  chan struct{}
}

func (s *Service) StartAgentWakeListener() {
	mode := os.Getenv("CANVAS_AGENT_WAKE_MODE")
	if s.repo == nil || s.repo.Dialect() != "postgres" || (mode != "" && mode != "notify") {
		slog.Info("agent wake reader ready", "mode", "poll", "listeners", 0)
		return
	}
	s.agentWakeOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.agentWake = &agentWakeHub{repo: s.repo, subscribers: make(map[*agentWakeSubscriber]struct{}), ready: make(chan struct{}), cancel: cancel, done: make(chan struct{})}
		go s.agentWake.run(ctx)
	})
}

// AgentWakeSubscription captures the live queue and the historical boundary atomically.
// A nil queue means poll until ready closes (or forever if ready is nil).
func (s *Service) AgentWakeSubscription(scope string) (<-chan model.AgentWakeEvent, int64, <-chan struct{}, func()) {
	hub := s.agentWake
	if hub == nil {
		return nil, 0, nil, nil
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if !hub.online {
		return nil, 0, hub.ready, nil
	}
	sub := &agentWakeSubscriber{scope: scope, events: make(chan model.AgentWakeEvent, 512)}
	hub.subscribers[sub] = struct{}{}
	return sub.events, hub.watermark, nil, func() {
		hub.mu.Lock()
		delete(hub.subscribers, sub)
		hub.mu.Unlock()
	}
}

func (hub *agentWakeHub) run(ctx context.Context) {
	defer close(hub.done)
	initialized := false
	for ctx.Err() == nil {
		err := hub.listen(ctx, &initialized)
		hub.setOffline()
		if ctx.Err() != nil {
			return
		}
		slog.Warn("agent wake listener fallback", "mode", "poll", "listeners", 0, "disconnects", hub.disconnects, "overflow", hub.overflow, "error_type", fmt.Sprintf("%T", err))
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

func (hub *agentWakeHub) listen(ctx context.Context, initialized *bool) error {
	openCtx, cancel := context.WithTimeout(ctx, wakeRecoveryInterval)
	conn, err := hub.repo.OpenAgentWakeListener(openCtx)
	cancel()
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	if !*initialized {
		readCtx, cancel := context.WithTimeout(ctx, wakeRecoveryInterval)
		hub.watermark, err = hub.repo.AgentWakeHighWater(readCtx)
		cancel()
		if err != nil {
			return err
		}
		*initialized = true
	}
	rows, err := hub.read(ctx)
	if err != nil {
		return err
	}
	hub.mu.Lock()
	hub.online = true
	close(hub.ready)
	hub.mu.Unlock()
	slog.Info("agent wake listener ready", "mode", "notify", "listeners", 1, "watermark", hub.watermark, "catchup_rows", rows)
	nextMetrics := time.Now().Add(wakeRecoveryInterval)
	for ctx.Err() == nil {
		waitCtx, cancel := context.WithTimeout(ctx, wakeRecoveryInterval)
		_, err := conn.WaitForNotification(waitCtx)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			pingCtx, stop := context.WithTimeout(ctx, time.Second)
			err = conn.Ping(pingCtx)
			stop()
		}
		if err != nil {
			return err
		}
		rows, err := hub.read(ctx)
		if err != nil {
			return err
		}
		if rows == 0 {
			if time.Now().After(nextMetrics) {
				hub.logMetrics()
				nextMetrics = time.Now().Add(wakeRecoveryInterval)
			}
		}
	}
	return ctx.Err()
}

func (hub *agentWakeHub) read(ctx context.Context) (int64, error) {
	var count int64
	for {
		readCtx, cancel := context.WithTimeout(ctx, wakeRecoveryInterval)
		events, err := hub.repo.AgentWakeDeliveries(readCtx, hub.watermark)
		cancel()
		if err != nil {
			return count, err
		}
		hub.publish(events)
		count += int64(len(events))
		hub.mu.Lock()
		hub.rowsRead += int64(len(events))
		hub.mu.Unlock()
		if len(events) < 128 {
			return count, nil
		}
	}
}

func (hub *agentWakeHub) publish(events []repository.AgentWakeDelivery) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for _, event := range events {
		for sub := range hub.subscribers {
			if sub.scope != "" && event.UserID != sub.scope && !event.Broadcast {
				continue
			}
			select {
			case sub.events <- event.AgentWakeEvent:
				hub.deliveries++
			default:
				close(sub.events)
				delete(hub.subscribers, sub)
				hub.overflow++
				if hub.overflow == 1 || hub.overflow%100 == 0 {
					slog.Warn("agent wake subscriber backlog", "overflow", hub.overflow, "watermark", hub.watermark)
				}
			}
		}
		hub.watermark = event.Sequence
	}
}

func (hub *agentWakeHub) logMetrics() {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	queued := 0
	for sub := range hub.subscribers {
		queued += len(sub.events)
	}
	slog.Info("agent wake listener metrics", "mode", "notify", "listeners", 1, "subscribers", len(hub.subscribers),
		"watermark", hub.watermark, "rows_read_total", hub.rowsRead, "deliveries_total", hub.deliveries,
		"queued_events", queued, "disconnects", hub.disconnects, "overflow", hub.overflow)
}

func (hub *agentWakeHub) setOffline() {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.online {
		hub.online = false
		hub.ready = make(chan struct{})
		hub.disconnects++
	}
	for sub := range hub.subscribers {
		close(sub.events)
		delete(hub.subscribers, sub)
	}
}
