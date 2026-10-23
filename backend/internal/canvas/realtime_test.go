package canvas

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func realtimeMessage(t *testing.T, s *RealtimeSession, kind string) RealtimeMessage {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case data := <-s.Messages():
			s.Consumed(data)
			var msg RealtimeMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				t.Fatal(err)
			}
			if msg.Type == kind {
				return msg
			}
		case <-s.Done():
			t.Fatal("session closed")
		case <-timer.C:
			t.Fatalf("no %s message", kind)
		}
	}
}

func TestRealtimeFullRoomFrameBudgetAndLeaseExpiry(t *testing.T) {
	hub := NewRealtimeHub(RealtimeOptions{FlushInterval: time.Hour})
	defer hub.Close()
	key := RealtimeRoomKey{OwnerID: "owner", CanvasID: "canvas"}
	var sessions []*RealtimeSession
	for i := 0; i < 64; i++ {
		s, err := hub.Join(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, s)
	}
	if _, err := hub.Join(context.Background(), key); err == nil {
		t.Fatal("65th device accepted")
	}
	peers := make([]RealtimePeer, 64)
	for i, s := range sessions {
		peers[i] = s.peer
		peers[i].DeviceLabel = strings.Repeat("界", 64)
		peers[i].Cursor = &RealtimePoint{X: 1e7, Y: -1e7}
		for j := 0; j < 32; j++ {
			peers[i].Drag = append(peers[i].Drag, RealtimeDrag{NodeID: fmt.Sprintf("%03d", j) + strings.Repeat("<", 125), X: 1e7, Y: -1e7})
		}
	}
	raw := realtimeFrame(RealtimeMessage{Type: "peers", Protocol: 1, Peers: peers})
	if len(raw) > 512<<10 {
		t.Fatalf("unbounded server frame %d", len(raw))
	}
	var msg RealtimeMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatal(err)
	}
	if len(msg.Peers) != 64 || msg.Peers[0].Cursor != nil || len(msg.Peers[0].Drag) != 0 {
		t.Fatal("motion fallback lost membership or retained oversized motion")
	}
	// Encoding must not mutate the live presence used by the next frame.
	if len(peers[0].Drag) != 32 || peers[0].Cursor == nil {
		t.Fatal("encoding mutated presence")
	}
	hub.mu.Lock()
	sessions[0].peer.ExpiresAt = time.Now().Add(-time.Second)
	hub.pruneLocked(time.Now())
	hub.mu.Unlock()
	if sessions[0].CloseStatus() != 1008 {
		t.Fatal("expired lease retained")
	}
	replacement, err := hub.Join(context.Background(), key)
	if err != nil || replacement.DeviceNumber() != 1 {
		t.Fatalf("expired number not reusable: %v", err)
	}
	if err = replacement.Receive([]byte(`{"type":"heartbeat","protocolVersion":1}`)); err != nil {
		t.Fatal(err)
	}
}
func TestRealtimeHubBoundsPresenceAndShutdown(t *testing.T) {
	hub := NewRealtimeHub(RealtimeOptions{ObserveInterval: 10 * time.Millisecond, FlushInterval: time.Millisecond, SessionTTL: 50 * time.Millisecond, MaxSessions: 2})
	defer hub.Close()
	key := RealtimeRoomKey{OwnerID: "one", CanvasID: "canvas"}
	a, err := hub.Join(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	b, err := hub.Join(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if a.DeviceNumber() == b.DeviceNumber() || a.DeviceNumber() < 1 || a.DeviceNumber() > 64 {
		t.Fatal("duplicate local device numbers")
	}
	if _, err = hub.Join(context.Background(), RealtimeRoomKey{OwnerID: "other", CanvasID: "other"}); err == nil {
		t.Fatal("process session limit ignored")
	}
	raw := `{"type":"presence","protocolVersion":1,"cursor":{"x":1,"y":2},"drag":[{"nodeId":"a","x":2,"y":3}],"deviceLabel":"Chrome"}`
	if err = a.Receive([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	peers := realtimeMessage(t, b, "peers")
	for len(peers.Peers) != 2 || peers.Peers[0].Cursor == nil && peers.Peers[1].Cursor == nil {
		peers = realtimeMessage(t, b, "peers")
	}
	for _, invalid := range []string{`{"type":"heartbeat"}`, `{"type":"heartbeat","protocolVersion":2}`, `{"type":"presence","protocolVersion":1,"cursor":{"x":10000001,"y":0}}`, `{"type":"presence","protocolVersion":1,"unexpected":true}`, strings.Repeat("a", 32769)} {
		if err = a.Receive([]byte(invalid)); err == nil {
			t.Fatalf("invalid message accepted: %s", invalid)
		}
	}
	b.Close()
	a.Close()
	next, err := hub.Join(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-next.Done():
		t.Fatal("new lease immediately closed")
	default:
	}
	hub.Close()
	select {
	case <-next.Done():
	case <-time.After(time.Second):
		t.Fatal("shutdown left session open")
	}
	if _, err = hub.Join(context.Background(), key); err == nil {
		t.Fatal("join after shutdown")
	}
}

func TestRealtimeHubRejectsRateAndSlowConsumer(t *testing.T) {
	hub := NewRealtimeHub(RealtimeOptions{})
	defer hub.Close()
	s, err := hub.Join(context.Background(), RealtimeRoomKey{OwnerID: "owner", CanvasID: "canvas"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 80; i++ {
		if err = s.Receive([]byte(`{"type":"heartbeat","protocolVersion":1}`)); err != nil {
			t.Fatalf("early limit %d: %v", i, err)
		}
	}
	if err = s.Receive([]byte(`{"type":"heartbeat","protocolVersion":1}`)); err == nil {
		t.Fatal("message rate not bounded")
	}
	// Outbound queue is bounded; slow readers are disconnected and must resync.
	for i := 0; i < 20; i++ {
		hub.enqueue(s, RealtimeMessage{Type: "sync", Protocol: 1})
	}
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("slow consumer retained indefinitely")
	}
}
