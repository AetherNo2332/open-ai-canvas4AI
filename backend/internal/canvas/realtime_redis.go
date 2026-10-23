package canvas

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

type realtimeEnvelope struct {
	Source string          `json:"source"`
	Kind   string          `json:"kind"`
	Key    RealtimeRoomKey `json:"room"`
	Peer   RealtimePeer    `json:"peer"`
}

var realtimeClaimScript = redis.NewScript(`
local expired=redis.call('ZRANGEBYSCORE',KEYS[1],'-inf',ARGV[1])
for _,id in ipairs(expired) do redis.call('HDEL',KEYS[2],id) end
redis.call('ZREMRANGEBYSCORE',KEYS[1],'-inf',ARGV[1])
local number=redis.call('HGET',KEYS[2],ARGV[3])
if not number then
 if redis.call('ZCARD',KEYS[1])>=64 then return 0 end
 local used={};for _,n in ipairs(redis.call('HVALS',KEYS[2])) do used[tonumber(n)]=true end
 for n=1,64 do if not used[n] then number=n;break end end
 redis.call('HSET',KEYS[2],ARGV[3],number)
end
redis.call('ZADD',KEYS[1],ARGV[2],ARGV[3])
redis.call('PEXPIRE',KEYS[1],120000);redis.call('PEXPIRE',KEYS[2],120000)
return tonumber(number)
`)
var realtimeReleaseScript = redis.NewScript(`redis.call('ZREM',KEYS[1],ARGV[1]);redis.call('HDEL',KEYS[2],ARGV[1]);return 1`)
var realtimeOwnsSlotScript = redis.NewScript(`
local number=redis.call('HGET',KEYS[2],ARGV[1])
local expiry=redis.call('ZSCORE',KEYS[1],ARGV[1])
if number==ARGV[2] and expiry and tonumber(expiry)>tonumber(ARGV[3]) then return 1 end
return 0
`)

func removeRemoteSlotLocked(room *realtimeRoom, number int, sessionID string) {
	for id, peer := range room.remote {
		if id != sessionID && peer.DeviceNumber == number {
			delete(room.remote, id)
			room.dirty = true
		}
	}
}

func (h *RealtimeHub) redisKeys(key RealtimeRoomKey) []string {
	encode := base64.RawURLEncoding.EncodeToString
	room := h.options.Namespace + ":" + encode([]byte(key.OwnerID)) + ":" + encode([]byte(key.CanvasID))
	return []string{room + ":leases", room + ":numbers"}
}
func (h *RealtimeHub) claimRedis(ctx context.Context, s *RealtimeSession) (int, error) {
	bounded, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	now := time.Now().UTC()
	h.mu.Lock()
	expires := s.peer.ExpiresAt
	h.mu.Unlock()
	if !expires.After(now) {
		return 0, nil
	}
	return realtimeClaimScript.Run(bounded, h.options.Redis, h.redisKeys(s.key), now.UnixMilli(), expires.UnixMilli(), s.id).Int()
}
func (h *RealtimeHub) publish(env realtimeEnvelope) {
	if h.options.Redis == nil {
		return
	}
	env.Source = h.instanceID
	select {
	case h.outbound <- env:
	default:
	} /* bounded ephemeral hints; observer/heartbeats recover drops */
}
func (h *RealtimeHub) setCoordination(mode string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.mode == mode || h.closed {
		return
	}
	h.mode = mode
	for _, room := range h.rooms {
		if mode == "degraded" || mode == "recovering" {
			room.remote = map[string]RealtimePeer{}
		}
		for _, s := range room.members {
			s.peer.Coordination = mode
			if mode != "shared" {
				s.peer.DeviceNumber = 0
			}
			h.sendLocked(s, RealtimeMessage{Type: "coordination", Protocol: 1, Coordination: mode})
		}
		room.dirty = true
	}
}
func (h *RealtimeHub) redisWriteLoop() {
	heartbeat := time.NewTicker(h.options.RedisHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-heartbeat.C:
			h.reconcileRedis()
		case env := <-h.outbound:
			ctx, cancel := context.WithTimeout(h.ctx, 500*time.Millisecond)
			if env.Kind == "leave" {
				_ = realtimeReleaseScript.Run(ctx, h.options.Redis, h.redisKeys(env.Key), env.Peer.SessionID).Err()
			}
			raw, _ := json.Marshal(env)
			err := h.options.Redis.Publish(ctx, h.options.Namespace+":events", raw).Err()
			cancel()
			if err != nil && h.ctx.Err() == nil {
				h.setCoordination("degraded")
			}
		}
	}
}
func (h *RealtimeHub) reconcileRedis() {
	ctx, cancel := context.WithTimeout(h.ctx, 500*time.Millisecond)
	err := h.options.Redis.Ping(ctx).Err()
	cancel()
	if err != nil {
		h.setCoordination("degraded")
		return
	}
	h.mu.Lock()
	mode := h.mode
	sessions := make([]*RealtimeSession, 0, h.count)
	for _, room := range h.rooms {
		for _, s := range room.members {
			sessions = append(sessions, s)
		}
	}
	h.mu.Unlock()
	if mode == "degraded" {
		h.setCoordination("recovering")
	}
	for _, s := range sessions {
		number, err := h.claimRedis(h.ctx, s)
		if err != nil {
			h.setCoordination("degraded")
			return
		}
		if number == 0 {
			s.Close()
			continue
		}
		h.mu.Lock()
		s.peer.DeviceNumber = number
		s.peer.Coordination = "shared"
		if room := h.rooms[s.key]; room != nil {
			removeRemoteSlotLocked(room, number, s.id)
			room.dirty = true
		}
		peer := s.peer
		h.mu.Unlock()
		h.publish(realtimeEnvelope{Kind: "join", Key: s.key, Peer: peer})
	}
	h.setCoordination("shared")
}
func (h *RealtimeHub) redisReadLoop() {
	for h.ctx.Err() == nil {
		sub := h.options.Redis.Subscribe(h.ctx, h.options.Namespace+":events")
		h.redisMu.Lock()
		h.subscription = sub
		h.redisMu.Unlock()
		ctx, cancel := context.WithTimeout(h.ctx, time.Second)
		_, err := sub.Receive(ctx)
		cancel()
		if err == nil {
			h.reconcileRedis()
			for h.ctx.Err() == nil {
				message, readErr := sub.ReceiveMessage(h.ctx)
				if readErr != nil {
					err = readErr
					break
				}
				if len(message.Payload) > 32<<10 {
					continue
				}
				var env realtimeEnvelope
				if json.Unmarshal([]byte(message.Payload), &env) != nil || env.Source == h.instanceID || env.Source == "" {
					continue
				}
				h.receiveRemote(env)
			}
		}
		_ = sub.Close()
		h.redisMu.Lock()
		if h.subscription == sub {
			h.subscription = nil
		}
		h.redisMu.Unlock()
		if h.ctx.Err() != nil {
			return
		}
		h.setCoordination("degraded")
		select {
		case <-h.ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
func (h *RealtimeHub) receiveRemote(env realtimeEnvelope) {
	if !realtimeID(env.Key.OwnerID) || !realtimeID(env.Key.CanvasID) {
		return
	}
	h.mu.Lock()
	room := h.rooms[env.Key]
	if room == nil || h.mode != "shared" {
		h.mu.Unlock()
		return
	}
	if env.Kind == "invalidate" {
		h.nudge()
		h.mu.Unlock()
		return
	}
	if env.Kind == "leave" {
		delete(room.remote, env.Peer.SessionID)
		room.dirty = true
		h.mu.Unlock()
		return
	}
	if env.Kind != "presence" && env.Kind != "join" {
		h.mu.Unlock()
		return
	}
	peer := env.Peer
	now := time.Now().UTC()
	if !realtimeID(peer.SessionID) || peer.DeviceNumber < 1 || peer.DeviceNumber > 64 || peer.Coordination != "shared" || !peer.ExpiresAt.After(now) || peer.ExpiresAt.After(now.Add(h.options.SessionTTL+time.Second)) || !validRealtimePresence(realtimeClientMessage{Type: "presence", Cursor: peer.Cursor, Drag: peer.Drag, DeviceLabel: peer.DeviceLabel}) {
		h.mu.Unlock()
		return
	}
	if room.members[peer.SessionID] != nil || room.pending[peer.SessionID] != nil {
		h.mu.Unlock()
		return
	}
	previous, exists := room.remote[peer.SessionID]
	verify := !exists || previous.DeviceNumber != peer.DeviceNumber
	h.mu.Unlock()
	if verify {
		// Pub/sub ordering and TTL are insufficient after a released slot is
		// reused: only its current Redis owner may replace a remote identity.
		ctx, cancel := context.WithTimeout(h.ctx, 500*time.Millisecond)
		valid, err := realtimeOwnsSlotScript.Run(ctx, h.options.Redis, h.redisKeys(env.Key), peer.SessionID, peer.DeviceNumber, now.UnixMilli()).Int()
		cancel()
		if err != nil {
			h.setCoordination("degraded")
			return
		}
		if valid != 1 {
			return
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	room = h.rooms[env.Key]
	if room == nil || h.mode != "shared" || room.members[peer.SessionID] != nil || room.pending[peer.SessionID] != nil {
		return
	}
	for _, member := range room.members {
		if member.peer.DeviceNumber == peer.DeviceNumber {
			return // a local live claim wins over stale remote hints
		}
	}
	removeRemoteSlotLocked(room, peer.DeviceNumber, peer.SessionID)
	if _, exists := room.remote[peer.SessionID]; !exists && len(room.members)+len(room.remote) >= 64 {
		return
	}
	room.remote[peer.SessionID] = peer
	room.dirty = true
	if env.Kind == "join" {
		for _, s := range room.members {
			h.publish(realtimeEnvelope{Kind: "presence", Key: env.Key, Peer: s.peer})
		}
	}
}
