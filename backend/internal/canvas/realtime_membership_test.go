package canvas

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestRealtimeRedisLostLeaveSlotReuseKeepsAuthoritativeMembers(t *testing.T) {
	addr := os.Getenv("CANVAS_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("isolated Redis address not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: addr, DialTimeout: 200 * time.Millisecond, ReadTimeout: 300 * time.Millisecond, WriteTimeout: 300 * time.Millisecond, MaxRetries: -1, ContextTimeoutEnabled: true})
	defer client.Close()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	prefix := "canvas:test:membership:" + uuid.NewString()
	defer func() {
		var cursor uint64
		for {
			keys, next, err := client.Scan(context.Background(), cursor, prefix+":*", 100).Result()
			if err != nil {
				t.Error(err)
				return
			}
			if len(keys) > 0 {
				if err = client.Del(context.Background(), keys...).Err(); err != nil {
					t.Error(err)
				}
			}
			cursor = next
			if cursor == 0 {
				return
			}
		}
	}()
	options := RealtimeOptions{Redis: client, Namespace: prefix, RedisHeartbeat: time.Hour, FlushInterval: time.Hour}
	a, b := NewRealtimeHub(options), NewRealtimeHub(options)
	defer a.Close()
	defer b.Close()
	key := RealtimeRoomKey{OwnerID: "owner", CanvasID: "canvas"}
	first, err := a.Join(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	wait := func(label string, check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatalf("%s: %v", label, ctx.Err())
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	wait("first subscription", func() bool {
		n, _ := client.PubSubNumSub(ctx, prefix+":events").Result()
		a.mu.Lock()
		defer a.mu.Unlock()
		return n[prefix+":events"] >= 1 && a.mode == "shared"
	})
	var victim *RealtimeSession
	for i := 0; i < 63; i++ {
		s, err := b.Join(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			victim = s
		}
	}
	wait("second subscription", func() bool {
		n, _ := client.PubSubNumSub(ctx, prefix+":events").Result()
		return n[prefix+":events"] == 2
	})
	b.reconcileRedis()
	wait("full remote snapshot", func() bool { a.mu.Lock(); defer a.mu.Unlock(); return len(a.rooms[key].remote) == 63 })
	// Retire the local connection and commit the normal release, deliberately
	// dropping only the subsequent ephemeral leave publish. All other 63 leases
	// and their live connections remain intact on the two hubs.
	lostLeave := func(s *RealtimeSession) RealtimePeer {
		s.hub.mu.Lock()
		peer := s.peer
		s.end(1000)
		delete(s.hub.rooms[key].members, s.id)
		s.hub.count--
		s.hub.mu.Unlock()
		if err := realtimeReleaseScript.Run(ctx, client, a.redisKeys(key), s.id).Err(); err != nil {
			t.Fatal(err)
		}
		return peer
	}
	assertFull := func(h *RealtimeHub, oldID, newID string) {
		t.Helper()
		claims, err := client.HGetAll(ctx, a.redisKeys(key)[1]).Result()
		if err != nil {
			t.Fatal(err)
		}
		h.mu.Lock()
		room := h.rooms[key]
		peers := []RealtimePeer{}
		for _, s := range room.members {
			peers = append(peers, s.peer)
		}
		for _, peer := range room.remote {
			peers = append(peers, peer)
		}
		h.mu.Unlock()
		var frame RealtimeMessage
		if err = json.Unmarshal(realtimeFrame(RealtimeMessage{Type: "peers", Protocol: 1, Peers: peers}), &frame); err != nil {
			t.Fatal(err)
		}
		if len(claims) != 64 || len(peers) != 64 || len(frame.Peers) != 64 {
			t.Fatalf("claims=%d membership=%d frame=%d (old=%s new=%s)", len(claims), len(peers), len(frame.Peers), oldID, newID)
		}
		seen := map[string]bool{}
		slots := map[int]bool{}
		for _, peer := range frame.Peers {
			if peer.SessionID == oldID || claims[peer.SessionID] != strconv.Itoa(peer.DeviceNumber) || slots[peer.DeviceNumber] {
				t.Fatalf("ghost/duplicate active member: %+v", peer)
			}
			seen[peer.SessionID] = true
			slots[peer.DeviceNumber] = true
		}
		if !seen[first.id] || !seen[newID] {
			t.Fatal("legitimate local/new member omitted")
		}
	}
	for cycle := 0; cycle < 4; cycle++ {
		oldPeer := lostLeave(victim)
		// Alternate which hub reuses the free slot, exercising both local claim
		// cleanup and new remote hint replacement in full 64-member views.
		nextHub := a
		if victim.hub == a {
			nextHub = b
		}
		next, err := nextHub.Join(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		other := a
		if nextHub == a {
			other = b
		}
		wait("replacement delivered", func() bool {
			other.mu.Lock()
			defer other.mu.Unlock()
			_, ok := other.rooms[key].remote[next.id]
			return ok
		})
		assertFull(a, victim.id, next.id)
		assertFull(b, victim.id, next.id)
		// Old join/presence, and a delayed leave for the retired identity, must
		// neither resurrect it nor remove the replacement sharing its number.
		for _, h := range []*RealtimeHub{a, b} {
			h.receiveRemote(realtimeEnvelope{Kind: "join", Key: key, Peer: oldPeer})
			h.receiveRemote(realtimeEnvelope{Kind: "presence", Key: key, Peer: oldPeer})
			h.receiveRemote(realtimeEnvelope{Kind: "leave", Key: key, Peer: oldPeer})
			assertFull(h, victim.id, next.id)
		}
		victim = next
	}
}
