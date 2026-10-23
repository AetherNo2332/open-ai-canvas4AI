package canvas

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// A test-owned TCP gate exercises real Redis disconnect/reconnect without
// changing the Redis process, configuration, or any unrelated keys.
type realtimeRedisGate struct {
	mu       sync.Mutex
	listener net.Listener
	target   string
	paused   bool
	conns    map[net.Conn]bool
}

func newRealtimeRedisGate(t *testing.T, target string) *realtimeRedisGate {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g := &realtimeRedisGate{listener: l, target: target, conns: map[net.Conn]bool{}}
	t.Cleanup(func() { g.setPaused(true); _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go g.forward(c)
		}
	}()
	return g
}
func (g *realtimeRedisGate) forward(c net.Conn) {
	g.mu.Lock()
	if g.paused {
		g.mu.Unlock()
		c.Close()
		return
	}
	g.conns[c] = true
	g.mu.Unlock()
	upstream, err := net.DialTimeout("tcp", g.target, time.Second)
	if err != nil {
		c.Close()
		return
	}
	g.mu.Lock()
	if g.paused {
		g.mu.Unlock()
		c.Close()
		upstream.Close()
		return
	}
	g.conns[upstream] = true
	g.mu.Unlock()
	done := make(chan struct{})
	go func() { _, _ = io.Copy(upstream, c); upstream.Close(); close(done) }()
	_, _ = io.Copy(c, upstream)
	c.Close()
	<-done
	g.mu.Lock()
	delete(g.conns, c)
	delete(g.conns, upstream)
	g.mu.Unlock()
}
func (g *realtimeRedisGate) setPaused(value bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.paused = value
	if value {
		for c := range g.conns {
			_ = c.Close()
		}
	}
}
func TestRealtimeRedisTwoHubsRecoveryAndIsolation(t *testing.T) {
	addr := os.Getenv("CANVAS_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("isolated Redis address not configured")
	}
	ctx := context.Background()
	prefix := "canvas:test:realtime:" + uuid.NewString()
	admin := redis.NewClient(&redis.Options{Addr: addr})
	defer admin.Close()
	if err := admin.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		var cursor uint64
		for {
			keys, next, err := admin.Scan(ctx, cursor, prefix+"*", 100).Result()
			if err != nil {
				t.Error(err)
				return
			}
			if len(keys) > 0 {
				if err = admin.Del(ctx, keys...).Err(); err != nil {
					t.Error(err)
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}()
	gate := newRealtimeRedisGate(t, addr)
	makeHub := func() *RealtimeHub {
		client := redis.NewClient(&redis.Options{Addr: gate.listener.Addr().String(), DialTimeout: 50 * time.Millisecond, ReadTimeout: 100 * time.Millisecond, WriteTimeout: 100 * time.Millisecond, MaxRetries: -1, ContextTimeoutEnabled: true})
		t.Cleanup(func() { client.Close() })
		h := NewRealtimeHub(RealtimeOptions{Redis: client, Namespace: prefix, RedisHeartbeat: 100 * time.Millisecond, FlushInterval: 10 * time.Millisecond, SessionTTL: 3 * time.Second})
		t.Cleanup(func() { h.Close() })
		return h
	}
	a, b := makeHub(), makeHub()
	key := RealtimeRoomKey{OwnerID: "one", CanvasID: "canvas"}
	sa, err := a.Join(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := b.Join(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if sa.DeviceNumber() == sb.DeviceNumber() || sa.DeviceNumber() == 0 || sb.DeviceNumber() == 0 {
		t.Fatal("cross-instance number collision")
	}
	isolated, err := a.Join(ctx, RealtimeRoomKey{OwnerID: "two", CanvasID: "canvas"})
	if err != nil {
		t.Fatal(err)
	}
	// Continuously drain all sockets just as transports do, avoiding backpressure
	// while the proxy is intentionally offline and the retry loops run.
	stop := make(chan struct{})
	defer close(stop)
	for _, s := range []*RealtimeSession{sa, sb, isolated} {
		go func(s *RealtimeSession) {
			for {
				select {
				case raw := <-s.Messages():
					s.Consumed(raw)
				case <-s.Done():
					return
				case <-stop:
					return
				}
			}
		}(s)
	}
	wait := func(label string, check func() bool) {
		t.Helper()
		until := time.Now().Add(5 * time.Second)
		for time.Now().Before(until) {
			if check() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal(label)
	}
	raw, _ := json.Marshal(realtimeClientMessage{Type: "presence", Protocol: 1, Cursor: &RealtimePoint{X: 3, Y: 4}})
	if err = sa.Receive(raw); err != nil {
		t.Fatal(err)
	}
	wait("remote presence not delivered", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		peer, ok := b.rooms[key].remote[sa.id]
		return ok && peer.Cursor != nil && peer.Cursor.X == 3
	})
	a.mu.Lock()
	if len(a.rooms[isolated.key].remote) != 0 {
		t.Error("owner isolation failed")
	}
	a.mu.Unlock()
	gate.setPaused(true)
	wait("degraded peers were not cleared", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.mode == "degraded" && sa.peer.DeviceNumber == 0 && len(a.rooms[key].remote) == 0
	})
	// Live browser heartbeats retain the session during Redis outage.
	for _, s := range []*RealtimeSession{sa, sb, isolated} {
		if err = s.Receive([]byte(`{"type":"heartbeat","protocolVersion":1}`)); err != nil {
			t.Fatal(err)
		}
	}
	gate.setPaused(false)
	wait("shared coordination did not recover", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.mode == "shared" && sa.peer.DeviceNumber > 0 && len(a.rooms[key].remote) == 1
	})
	if sa.DeviceNumber() == sb.DeviceNumber() {
		t.Fatal("recovery reused an active device number")
	}
	sb.Close()
	wait("left remote peer retained", func() bool { a.mu.Lock(); defer a.mu.Unlock(); return len(a.rooms[key].remote) == 0 })
}
