package websocket

import (
	"context"
	"net/http"
	"sync"

	httpws "github.com/gorilla/websocket"
	apperr "github.com/shadowofcards/go-toolkit/errors"
	"github.com/shadowofcards/go-toolkit/metrics"
)

type ManagerOption func(*manager)

func WithManagerMetrics(rc metrics.Recorder) ManagerOption {
	return func(m *manager) {
		m.metrics = rc
	}
}

type Manager interface {
	Register(ctx context.Context, id string, raw *httpws.Conn) error
	Unregister(ctx context.Context, id string)
	JoinRoom(id, room string)
	LeaveRoom(id, room string)
	SendTo(id string, mt int, msg []byte) error
	SendToRoom(room string, mt int, msg []byte)
	Broadcast(mt int, msg []byte)
	Refresh(ctx context.Context, id string)
	ActiveCount(ctx context.Context) int
}

type manager struct {
	conns   map[string]*SafeConn
	rooms   map[string]map[string]struct{}
	ctxs    map[string]context.Context
	mu      sync.RWMutex
	metrics metrics.Recorder
}

func NewManager(opts ...ManagerOption) Manager {
	m := &manager{
		conns:   make(map[string]*SafeConn),
		rooms:   make(map[string]map[string]struct{}),
		ctxs:    make(map[string]context.Context),
		metrics: nil,
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

func (m *manager) Register(ctx context.Context, id string, raw *httpws.Conn) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.conns[id]; ok {
		if m.metrics != nil {
			m.metrics.IncWithTags(ctx, "errors_total", 1, map[string]string{"player_id": id, "stage": "register"})
		}
		return apperr.New().
			WithHTTPStatus(http.StatusConflict).
			WithCode("ALREADY_CONNECTED").
			WithMessage("connection exists")
	}

	m.conns[id] = &SafeConn{Conn: raw}
	m.ctxs[id] = ctx

	if m.metrics != nil {
		m.metrics.GaugeWithTags(ctx, "connections_active", float64(len(m.conns)), map[string]string{"player_id": id})
	}
	return nil
}

func (m *manager) Unregister(ctx context.Context, id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if c, ok := m.conns[id]; ok {
		_ = c.Close()
		delete(m.conns, id)
	}
	delete(m.ctxs, id)

	if m.metrics != nil {
		m.metrics.GaugeWithTags(ctx, "connections_active", float64(len(m.conns)), map[string]string{"player_id": id})
	}

	for room, set := range m.rooms {
		delete(set, id)
		if len(set) == 0 {
			delete(m.rooms, room)
		}
	}
}

func (m *manager) JoinRoom(id, room string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.rooms[room]; !ok {
		m.rooms[room] = make(map[string]struct{})
	}
	m.rooms[room][id] = struct{}{}

	if ctx, ok := m.ctxs[id]; ok && m.metrics != nil {
		m.metrics.IncWithTags(ctx, "room_joins_total", 1, map[string]string{"room": room, "player_id": id})
	}
}

func (m *manager) LeaveRoom(id, room string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if set, ok := m.rooms[room]; ok {
		delete(set, id)
		if len(set) == 0 {
			delete(m.rooms, room)
		}
	}

	if ctx, ok := m.ctxs[id]; ok && m.metrics != nil {
		m.metrics.IncWithTags(ctx, "room_leaves_total", 1, map[string]string{"room": room, "player_id": id})
	}
}

func (m *manager) SendTo(id string, mt int, msg []byte) error {
	m.mu.RLock()
	c, ok := m.conns[id]
	ctx := m.ctxs[id]
	m.mu.RUnlock()

	if !ok {
		if m.metrics != nil {
			m.metrics.IncWithTags(ctx, "errors_total", 1, map[string]string{"stage": "send_to", "player_id": id})
		}
		return apperr.New().
			WithHTTPStatus(http.StatusNotFound).
			WithCode("NOT_CONNECTED").
			WithMessage("player not online")
	}

	err := c.WriteMessage(mt, msg)
	if err != nil && m.metrics != nil {
		m.metrics.IncWithTags(ctx, "errors_total", 1, map[string]string{"stage": "write", "player_id": id})
	}
	return err
}

func (m *manager) SendToRoom(room string, mt int, msg []byte) {
	m.mu.RLock()
	set, ok := m.rooms[room]
	m.mu.RUnlock()
	if !ok {
		return
	}

	m.mu.RLock()
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	var ctx context.Context
	if len(ids) > 0 {
		ctx = m.ctxs[ids[0]]
	}

	if m.metrics != nil {
		m.metrics.IncWithTags(ctx, "broadcasts_total", 1, map[string]string{"room": room, "type": "room"})
	}

	for _, id := range ids {
		_ = m.SendTo(id, mt, msg)
	}
}

func (m *manager) Broadcast(mt int, msg []byte) {
	m.mu.RLock()
	ids := make([]string, 0, len(m.conns))
	for id := range m.conns {
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	var ctx context.Context
	if len(ids) > 0 {
		ctx = m.ctxs[ids[0]]
	}

	if m.metrics != nil {
		m.metrics.IncWithTags(ctx, "broadcasts_total", 1, map[string]string{"type": "global"})
	}

	for _, id := range ids {
		_ = m.SendTo(id, mt, msg)
	}
}

func (m *manager) Refresh(ctx context.Context, id string) {
	// no-op or update last-seen timestamp if needed
}

func (m *manager) ActiveCount(ctx context.Context) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.conns)
}
