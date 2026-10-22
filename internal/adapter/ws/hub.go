// Package ws pushes room events to browsers over WebSocket and tracks which
// devices are online.
package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

// Presence is told when a device goes online or offline.
type Presence interface {
	SetPresence(ctx context.Context, id domain.DeviceID, online bool) error
}

type message struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

type client struct {
	device  domain.DeviceID
	room    domain.RoomID
	pending bool
	canDL   bool
	canChat bool
	send    chan []byte
}

// Hub implements port.EventPublisher.
type Hub struct {
	presence Presence
	log      *slog.Logger

	mu      sync.Mutex
	clients map[domain.RoomID]map[*client]struct{}
	conns   map[domain.DeviceID]int
}

func NewHub(presence Presence, log *slog.Logger) *Hub {
	return &Hub{
		presence: presence,
		log:      log,
		clients:  make(map[domain.RoomID]map[*client]struct{}),
		conns:    make(map[domain.DeviceID]int),
	}
}

// SetPresence lets main wire the presence use case after construction
// (the hub is needed to build the use cases that publish events).
func (h *Hub) SetPresence(p Presence) { h.presence = p }

// Publish sends the event to every connection of the event's room.
// A connection that cannot keep up is dropped instead of blocking the sender.
func (h *Hub) Publish(e port.Event) {
	payload, err := json.Marshal(message{Type: e.Type, Data: e.Data})
	if err != nil {
		h.log.Error("marshal event", "type", e.Type, "err", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients[e.RoomID] {
		if !eventVisible(c, e.Type) {
			continue
		}
		select {
		case c.send <- payload:
		default:
			h.log.Warn("slow websocket client dropped", "device", c.device)
			h.removeLocked(c)
		}
	}
}

// eventVisible keeps pending devices to presence events only, and hides
// file/transfer/chat traffic from roles that cannot see that data.
func eventVisible(c *client, typ string) bool {
	if c.pending {
		return typ == port.EventDevices
	}
	switch typ {
	case port.EventFileAdded, port.EventFileDeleted, port.EventTransfer:
		return c.canDL
	case port.EventMessage, port.EventClipboard:
		return c.canChat
	default:
		return true
	}
}

func (h *Hub) removeLocked(c *client) {
	if set, ok := h.clients[c.room]; ok {
		if _, in := set[c]; in {
			delete(set, c)
			close(c.send)
		}
	}
}

// Serve upgrades the request and runs until the browser disconnects.
// The caller must already have authenticated the device.
func (h *Hub) Serve(w http.ResponseWriter, r *http.Request, device domain.Device) {
	conn, err := websocket.Accept(w, r, nil) // rejects cross-origin by default
	if err != nil {
		return // Accept already replied
	}
	defer conn.CloseNow()

	ctx := conn.CloseRead(r.Context())
	c := &client{
		device: device.ID, room: device.RoomID, pending: device.Pending,
		canDL: device.CanDownload(), canChat: device.CanChat(),
		send: make(chan []byte, 64),
	}

	h.mu.Lock()
	if h.clients[c.room] == nil {
		h.clients[c.room] = make(map[*client]struct{})
	}
	h.clients[c.room][c] = struct{}{}
	h.conns[c.device]++
	first := h.conns[c.device] == 1
	h.mu.Unlock()

	if first {
		h.setPresence(c.device, true)
	}
	defer func() {
		h.mu.Lock()
		h.removeLocked(c)
		h.conns[c.device]--
		last := h.conns[c.device] <= 0
		if last {
			delete(h.conns, c.device)
		}
		h.mu.Unlock()
		if last {
			h.setPresence(c.device, false)
		}
	}()

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-c.send:
			if !ok {
				conn.Close(websocket.StatusPolicyViolation, "too slow")
				return
			}
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func (h *Hub) setPresence(id domain.DeviceID, online bool) {
	if h.presence == nil {
		return
	}
	if err := h.presence.SetPresence(context.Background(), id, online); err != nil {
		h.log.Warn("set presence", "device", id, "online", online, "err", err)
	}
}
