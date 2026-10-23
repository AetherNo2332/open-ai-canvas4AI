package canvas

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"infinite-canvas/backend/internal/repository"
)

type RealtimeRoomKey = repository.CanvasRealtimeKey
type RealtimeCommittedState = repository.CanvasCommittedState
type RealtimePoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type RealtimeDrag struct {
	NodeID string  `json:"nodeId"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
}
type RealtimePeer struct {
	SessionID    string         `json:"sessionId"`
	DeviceNumber int            `json:"deviceNumber"`
	Coordination string         `json:"coordination"`
	DeviceLabel  string         `json:"deviceLabel"`
	Cursor       *RealtimePoint `json:"cursor"`
	Drag         []RealtimeDrag `json:"drag"`
	ExpiresAt    time.Time      `json:"expiresAt"`
}
type RealtimeMessage struct {
	Type             string         `json:"type"`
	Protocol         int            `json:"protocolVersion"`
	SessionID        string         `json:"sessionId,omitempty"`
	DeviceNumber     int            `json:"deviceNumber"`
	Coordination     string         `json:"coordination,omitempty"`
	HeartbeatSeconds int            `json:"heartbeatSeconds,omitempty"`
	LeaseSeconds     int            `json:"leaseSeconds,omitempty"`
	DocumentEpoch    string         `json:"documentEpoch,omitempty"`
	Revision         int64          `json:"revision,omitempty"`
	EventSeq         int64          `json:"eventSeq,omitempty"`
	Deleted          bool           `json:"deleted,omitempty"`
	Peers            []RealtimePeer `json:"peers,omitempty"`
}
type realtimeClientMessage struct {
	Type        string         `json:"type"`
	Protocol    int            `json:"protocolVersion"`
	Cursor      *RealtimePoint `json:"cursor,omitempty"`
	Drag        []RealtimeDrag `json:"drag,omitempty"`
	DeviceLabel string         `json:"deviceLabel,omitempty"`
}
type RealtimeOptions struct {
	ReadCommitted   func(context.Context, []RealtimeRoomKey) ([]RealtimeCommittedState, error)
	Redis           *redis.Client
	Namespace       string
	ObserveInterval time.Duration
	FlushInterval   time.Duration
	SessionTTL      time.Duration
	RedisHeartbeat  time.Duration
	MaxSessions     int
}
type realtimeRoom struct {
	members    map[string]*RealtimeSession
	pending    map[string]*RealtimeSession // capacity reservations, never public peers
	remote     map[string]RealtimePeer
	dirty      bool
	localDirty bool
	seen       bool
	state      RealtimeCommittedState
}
type RealtimeSession struct {
	hub         *RealtimeHub
	key         RealtimeRoomKey
	id          string
	peer        RealtimePeer
	done        chan struct{}
	stop        sync.Once
	queue       chan []byte
	queueMu     sync.Mutex
	queuedBytes int
	ready       bool // protected by hub.mu; ready/sync precede broadcasts
	rateStart   time.Time
	rateCount   int
	closeStatus atomic.Int32
}
type RealtimeHub struct {
	mu           sync.Mutex
	rooms        map[RealtimeRoomKey]*realtimeRoom
	count        int
	closed       bool
	mode         string
	options      RealtimeOptions
	ctx          context.Context
	cancel       context.CancelFunc
	start        sync.Once
	wg           sync.WaitGroup
	wake         chan struct{}
	outbound     chan realtimeEnvelope
	instanceID   string
	redisMu      sync.Mutex
	subscription *redis.PubSub
}

func NewRealtimeHub(options RealtimeOptions) *RealtimeHub {
	if options.ObserveInterval <= 0 {
		options.ObserveInterval = 500 * time.Millisecond
	}
	if options.FlushInterval <= 0 {
		options.FlushInterval = 50 * time.Millisecond
	}
	if options.SessionTTL <= 0 {
		options.SessionTTL = 60 * time.Second
	}
	if options.RedisHeartbeat <= 0 {
		options.RedisHeartbeat = 15 * time.Second
	}
	if options.MaxSessions <= 0 || options.MaxSessions > 1024 {
		options.MaxSessions = 1024
	}
	if options.Namespace == "" {
		options.Namespace = "canvas:realtime:v1"
	}
	ctx, cancel := context.WithCancel(context.Background())
	mode := "local"
	if options.Redis != nil {
		mode = "recovering"
	}
	return &RealtimeHub{rooms: map[RealtimeRoomKey]*realtimeRoom{}, options: options, ctx: ctx, cancel: cancel, mode: mode, wake: make(chan struct{}, 1), outbound: make(chan realtimeEnvelope, 256), instanceID: uuid.NewString()}
}
func (h *RealtimeHub) startLoops() {
	h.start.Do(func() {
		h.wg.Add(2)
		go func() { defer h.wg.Done(); h.observeLoop() }()
		go func() { defer h.wg.Done(); h.presenceLoop() }()
		if h.options.Redis != nil {
			h.wg.Add(2)
			go func() { defer h.wg.Done(); h.redisReadLoop() }()
			go func() { defer h.wg.Done(); h.redisWriteLoop() }()
		}
	})
}
func (h *RealtimeHub) Join(ctx context.Context, key RealtimeRoomKey) (*RealtimeSession, error) {
	if !realtimeID(key.OwnerID) || !realtimeID(key.CanvasID) {
		return nil, fmt.Errorf("invalid realtime room")
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, fmt.Errorf("realtime hub is closed")
	}
	h.pruneLocked(time.Now().UTC())
	room := h.rooms[key]
	if h.count >= h.options.MaxSessions || room != nil && len(room.members)+len(room.pending) >= 64 || room == nil && len(h.rooms) >= 1024 {
		h.mu.Unlock()
		return nil, fmt.Errorf("realtime capacity reached")
	}
	if room == nil {
		room = &realtimeRoom{members: map[string]*RealtimeSession{}, pending: map[string]*RealtimeSession{}, remote: map[string]RealtimePeer{}}
		h.rooms[key] = room
	}
	used := map[int]bool{}
	for _, s := range room.members {
		used[s.peer.DeviceNumber] = true
	}
	for _, s := range room.pending {
		used[s.peer.DeviceNumber] = true
	}
	number := 0
	if h.options.Redis == nil {
		for i := 1; i <= 64; i++ {
			if !used[i] {
				number = i
				break
			}
		}
	}
	s := &RealtimeSession{hub: h, key: key, id: uuid.NewString(), done: make(chan struct{}), queue: make(chan []byte, 8), peer: RealtimePeer{DeviceNumber: number, Coordination: h.mode, Drag: []RealtimeDrag{}, ExpiresAt: time.Now().UTC().Add(h.options.SessionTTL)}}
	s.peer.SessionID = s.id
	room.pending[s.id] = s
	h.count++
	h.startLoops()
	h.mu.Unlock()
	if h.options.Redis != nil {
		number, err := h.claimRedis(ctx, s)
		if err == nil && number == 0 {
			s.Close()
			return nil, fmt.Errorf("realtime room has 64 devices")
		}
		h.mu.Lock()
		if err != nil {
			s.peer.DeviceNumber = 0
			s.peer.Coordination = "degraded"
		} else {
			s.peer.DeviceNumber = number
			s.peer.Coordination = "shared"
		}
		h.mu.Unlock()
		if err != nil {
			h.setCoordination("degraded")
		}
	}
	h.mu.Lock()
	room = h.rooms[key]
	if h.closed || room == nil || room.pending[s.id] != s {
		h.mu.Unlock()
		s.end(1001)
		return nil, fmt.Errorf("realtime hub is closed")
	}
	delete(room.pending, s.id)
	// A successful local claim is authoritative even if the prior owner's
	// release succeeded but its independent leave hint was lost.
	if s.peer.DeviceNumber > 0 {
		removeRemoteSlotLocked(room, s.peer.DeviceNumber, s.id)
	}
	room.members[s.id] = s
	room.dirty, room.localDirty = true, true
	s.ready = true
	h.sendLocked(s, RealtimeMessage{Type: "ready", Protocol: 1, SessionID: s.id, DeviceNumber: s.peer.DeviceNumber, Coordination: s.peer.Coordination, HeartbeatSeconds: 15, LeaseSeconds: 60})
	h.sendLocked(s, RealtimeMessage{Type: "sync", Protocol: 1})
	peer := s.peer
	h.mu.Unlock()
	h.nudge()
	h.publish(realtimeEnvelope{Kind: "join", Key: key, Peer: peer})
	return s, nil
}
func (s *RealtimeSession) Messages() <-chan []byte { return s.queue }
func (s *RealtimeSession) Done() <-chan struct{}   { return s.done }
func (s *RealtimeSession) CloseStatus() int {
	code := s.closeStatus.Load()
	if code == 0 {
		return 1001
	}
	return int(code)
}
func (s *RealtimeSession) end(code int32) {
	s.stop.Do(func() { s.closeStatus.Store(code); close(s.done) })
}
func (s *RealtimeSession) DeviceNumber() int {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	return s.peer.DeviceNumber
}

// Consumed releases the outbound byte budget before a bounded transport write.
func (s *RealtimeSession) Consumed(data []byte) {
	s.queueMu.Lock()
	s.queuedBytes -= len(data)
	if s.queuedBytes < 0 {
		s.queuedBytes = 0
	}
	s.queueMu.Unlock()
}
func (s *RealtimeSession) Close() {
	s.end(1000)
	s.hub.mu.Lock()
	s.hub.removeLocked(s)
	s.hub.mu.Unlock()
}
func (h *RealtimeHub) removeLocked(s *RealtimeSession) {
	room := h.rooms[s.key]
	if room == nil || room.members[s.id] != s && room.pending[s.id] != s {
		return
	}
	delete(room.members, s.id)
	delete(room.pending, s.id)
	h.count--
	room.dirty = true
	h.publish(realtimeEnvelope{Kind: "leave", Key: s.key, Peer: RealtimePeer{SessionID: s.id}})
	if len(room.members)+len(room.pending) == 0 {
		delete(h.rooms, s.key)
	}
}
func (s *RealtimeSession) Receive(raw []byte) error {
	if len(raw) > 32<<10 {
		return fmt.Errorf("realtime message too large")
	}
	var msg realtimeClientMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&msg); err != nil {
		return fmt.Errorf("invalid realtime message")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || msg.Protocol != 1 || msg.Type != "heartbeat" && msg.Type != "presence" {
		return fmt.Errorf("invalid realtime protocol")
	}
	if !validRealtimePresence(msg) {
		return fmt.Errorf("invalid realtime presence")
	}
	h := s.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	select {
	case <-s.done:
		return fmt.Errorf("realtime session closed")
	default:
	}
	now := time.Now().UTC()
	if !s.peer.ExpiresAt.After(now) {
		s.end(1008)
		return fmt.Errorf("realtime heartbeat expired")
	}
	if now.Sub(s.rateStart) >= time.Second {
		s.rateStart = now
		s.rateCount = 0
	}
	s.rateCount++
	if s.rateCount > 80 {
		return fmt.Errorf("realtime rate exceeded")
	}
	s.peer.ExpiresAt = now.Add(h.options.SessionTTL)
	if msg.Type == "presence" {
		s.peer.Cursor = msg.Cursor
		s.peer.Drag = append([]RealtimeDrag{}, msg.Drag...)
		s.peer.DeviceLabel = msg.DeviceLabel
	}
	if room := h.rooms[s.key]; room != nil {
		room.dirty = true
		room.localDirty = true
	}
	return nil
}
func realtimeID(value string) bool {
	return len(value) > 0 && len(value) <= 128 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}
func validRealtimePresence(msg realtimeClientMessage) bool {
	valid := func(x, y float64) bool {
		return !math.IsNaN(x) && !math.IsNaN(y) && !math.IsInf(x, 0) && !math.IsInf(y, 0) && math.Abs(x) <= 1e7 && math.Abs(y) <= 1e7
	}
	if utf8.RuneCountInString(msg.DeviceLabel) > 64 || !utf8.ValidString(msg.DeviceLabel) || strings.ContainsAny(msg.DeviceLabel, "\x00\r\n") || len(msg.Drag) > 32 {
		return false
	}
	if msg.Cursor != nil && !valid(msg.Cursor.X, msg.Cursor.Y) {
		return false
	}
	ids := map[string]bool{}
	for _, drag := range msg.Drag {
		if !realtimeID(drag.NodeID) || ids[drag.NodeID] || !valid(drag.X, drag.Y) {
			return false
		}
		ids[drag.NodeID] = true
	}
	return msg.Type != "heartbeat" || msg.Cursor == nil && len(msg.Drag) == 0 && msg.DeviceLabel == ""
}
func (h *RealtimeHub) sendLocked(s *RealtimeSession, msg RealtimeMessage) {
	raw := realtimeFrame(msg)
	h.sendBytes(s, raw)
}
func (h *RealtimeHub) sendBytes(s *RealtimeSession, data []byte) {
	if !s.ready {
		return
	}
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	select {
	case <-s.done:
		return
	default:
	}
	if s.queuedBytes+len(data) > 1<<20 {
		s.end(1013)
		return
	}
	select {
	case s.queue <- data:
		s.queuedBytes += len(data)
	default:
		s.end(1013)
	}
}
func (h *RealtimeHub) enqueue(s *RealtimeSession, msg RealtimeMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sendLocked(s, msg)
}
func (h *RealtimeHub) broadcastLocked(room *realtimeRoom, msg RealtimeMessage) {
	raw := realtimeFrame(msg)
	for _, s := range room.members {
		h.sendBytes(s, raw)
	}
}

// Client input is limited to 32KiB. A full 64-peer snapshot has a separate
// 512KiB output budget; optional motion is discarded if escaping expands it.
func realtimeFrame(msg RealtimeMessage) []byte {
	raw, _ := json.Marshal(msg)
	if len(raw) <= 512<<10 {
		return raw
	}
	msg.Peers = append([]RealtimePeer(nil), msg.Peers...)
	for i := range msg.Peers {
		msg.Peers[i].Cursor = nil
		msg.Peers[i].Drag = []RealtimeDrag{}
	}
	raw, _ = json.Marshal(msg)
	return raw
}
func (h *RealtimeHub) pruneLocked(now time.Time) {
	for _, room := range h.rooms {
		for _, s := range room.pending {
			if !s.peer.ExpiresAt.After(now) {
				s.end(1008)
				h.removeLocked(s)
			}
		}
		for _, s := range room.members {
			select {
			case <-s.done:
				h.removeLocked(s)
			default:
				if !s.peer.ExpiresAt.After(now) {
					s.end(1008)
					h.removeLocked(s)
				}
			}
		}
		for id, peer := range room.remote {
			if !peer.ExpiresAt.After(now) {
				delete(room.remote, id)
				room.dirty = true
			}
		}
	}
}
func (h *RealtimeHub) presenceLoop() {
	ticker := time.NewTicker(h.options.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			h.mu.Lock()
			h.pruneLocked(time.Now().UTC())
			for key, room := range h.rooms {
				if !room.dirty {
					continue
				}
				room.dirty = false
				peers := []RealtimePeer{}
				for _, s := range room.members {
					peers = append(peers, s.peer)
					if room.localDirty {
						h.publish(realtimeEnvelope{Kind: "presence", Key: key, Peer: s.peer})
					}
				}
				for _, peer := range room.remote {
					peers = append(peers, peer)
				}
				sort.Slice(peers, func(i, j int) bool { return peers[i].SessionID < peers[j].SessionID })
				h.broadcastLocked(room, RealtimeMessage{Type: "peers", Protocol: 1, Peers: peers})
				room.localDirty = false
			}
			h.mu.Unlock()
		}
	}
}
func (h *RealtimeHub) nudge() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}
func (h *RealtimeHub) observeLoop() {
	ticker := time.NewTicker(h.options.ObserveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
		case <-h.wake:
		}
		h.observe()
	}
}
func (h *RealtimeHub) observe() {
	if h.options.ReadCommitted == nil {
		return
	}
	h.mu.Lock()
	keys := make([]RealtimeRoomKey, 0, len(h.rooms))
	for key := range h.rooms {
		keys = append(keys, key)
	}
	h.mu.Unlock()
	for i := 0; i < len(keys); i += 128 {
		end := i + 128
		if end > len(keys) {
			end = len(keys)
		}
		ctx, cancel := context.WithTimeout(h.ctx, 2*time.Second)
		states, err := h.options.ReadCommitted(ctx, keys[i:end])
		cancel()
		if err != nil {
			continue
		}
		h.mu.Lock()
		for _, state := range states {
			key := RealtimeRoomKey{OwnerID: state.OwnerID, CanvasID: state.CanvasID}
			room := h.rooms[key]
			if room == nil {
				continue
			}
			ready := false
			for _, session := range room.members {
				ready = ready || session.ready
			}
			if !ready {
				continue // a pending Redis claim must not consume the initial observation
			}
			if room.seen && room.state == state {
				continue
			}
			room.seen = true
			room.state = state
			h.broadcastLocked(room, RealtimeMessage{Type: "invalidate", Protocol: 1, DocumentEpoch: state.DocumentEpoch, EventSeq: state.EventSeq, Revision: state.Revision, Deleted: state.Deleted})
			h.publish(realtimeEnvelope{Kind: "invalidate", Key: key})
		}
		h.mu.Unlock()
	}
}
func (h *RealtimeHub) Close() error {
	h.mu.Lock()
	if !h.closed {
		h.closed = true
		h.cancel()
		for _, room := range h.rooms {
			for _, s := range room.members {
				s.end(1001)
			}
			for _, s := range room.pending {
				s.end(1001)
			}
		}
		h.rooms = map[RealtimeRoomKey]*realtimeRoom{}
		h.count = 0
	}
	h.mu.Unlock()
	h.redisMu.Lock()
	if h.subscription != nil {
		_ = h.subscription.Close()
	}
	h.redisMu.Unlock()
	done := make(chan struct{})
	go func() { h.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("realtime shutdown timed out")
	}
}
