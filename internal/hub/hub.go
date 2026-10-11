// Package hub is the WebSocket hub linking the queue page (controllers)
// and the player page (screens). PostgreSQL is the source of truth; the hub
// only relays commands and notifications.
package hub

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/BrightDN/karakoek/internal/queue"
	"github.com/gorilla/websocket"
)

const (
	RoleController = "controller"
	RoleScreen     = "screen"

	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 30 * time.Second
	maxMsgSize = 4096
)

type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type client struct {
	hub  *Hub
	conn *websocket.Conn
	role string
	send chan []byte
}

type Hub struct {
	q *queue.Service

	mu      sync.Mutex // guards clients and state
	clients map[*client]bool
	state   string // last player state: cued/playing/paused

	cmdMu sync.Mutex // serialises commands that change the queue

	upgrader websocket.Upgrader
}

func New(q *queue.Service, baseURL *url.URL) *Hub {
	return &Hub{
		q:       q,
		clients: map[*client]bool{},
		state:   "cued",
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				o := r.Header.Get("Origin")
				if o == "" {
					return false
				}
				ou, err := url.Parse(o)
				return err == nil && ou.Host == baseURL.Host
			},
		},
	}
}

// ServeWS upgrades the connection. The caller has already authenticated the admin.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	role := r.URL.Query().Get("role")
	if role != RoleController && role != RoleScreen {
		http.Error(w, "bad role", http.StatusBadRequest)
		return
	}
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already replied
	}
	c := &client{hub: h, conn: conn, role: role, send: make(chan []byte, 32)}
	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()

	go c.writePump()
	h.onConnect(c)
	go c.readPump()
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	if h.clients[c] {
		delete(h.clients, c)
		close(c.send)
	}
	h.mu.Unlock()
	_ = c.conn.Close()
	h.broadcastScreenStatus()
}

// ---------- sending ----------

func encode(t string, payload interface{}) []byte {
	var raw json.RawMessage
	if payload != nil {
		raw, _ = json.Marshal(payload)
	}
	b, _ := json.Marshal(Message{Type: t, Payload: raw})
	return b
}

func (c *client) enqueue(b []byte) {
	select {
	case c.send <- b:
	default: // slow client: drop; it will resync on reconnect
	}
}

func (h *Hub) sendRole(role string, b []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.role == role {
			c.enqueue(b)
		}
	}
}

func (h *Hub) countScreens() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for c := range h.clients {
		if c.role == RoleScreen {
			n++
		}
	}
	return n
}

func (h *Hub) broadcastScreenStatus() {
	h.sendRole(RoleController, encode("screen_status", map[string]int{"connected": h.countScreens()}))
}

type snapshot struct {
	Queue   []queue.Request `json:"queue"`
	Current *queue.Request  `json:"current"`
	State   string          `json:"state"`
}

func (h *Hub) snapshot(ctx context.Context) (snapshot, error) {
	qs, err := h.q.Queued(ctx)
	if err != nil {
		return snapshot{}, err
	}
	cur, err := h.q.Current(ctx)
	if err != nil {
		return snapshot{}, err
	}
	h.mu.Lock()
	st := h.state
	h.mu.Unlock()
	return snapshot{Queue: qs, Current: cur, State: st}, nil
}

// BroadcastQueue pushes the full queue + current song to every controller.
func (h *Hub) BroadcastQueue() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := h.snapshot(ctx)
	if err != nil {
		log.Printf("hub: snapshot: %v", err)
		return
	}
	h.sendRole(RoleController, encode("queue_updated", s))
}

func (h *Hub) setState(state string, requestID int64) {
	h.mu.Lock()
	h.state = state
	h.mu.Unlock()
	h.sendRole(RoleController, encode("player_state", map[string]interface{}{"state": state, "requestId": requestID}))
}

func (h *Hub) onConnect(c *client) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if c.role == RoleController {
		if s, err := h.snapshot(ctx); err == nil {
			c.enqueue(encode("queue_updated", s))
		}
		c.enqueue(encode("screen_status", map[string]int{"connected": h.countScreens()}))
		return
	}

	// Screen: restore the current song cued and paused; never auto-start.
	cur, err := h.q.Current(ctx)
	if err == nil && cur != nil {
		_ = h.q.ResetPlaying(ctx)
		c.enqueue(encode("GetVideo", map[string]interface{}{"requestId": cur.ID, "videoId": cur.VideoID}))
		h.setState("cued", cur.ID)
		h.BroadcastQueue()
	}
	h.broadcastScreenStatus()
}

// ---------- receiving ----------

func (c *client) readPump() {
	defer c.hub.remove(c)
	c.conn.SetReadLimit(maxMsgSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var m Message
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if c.role == RoleController {
			c.hub.handleCommand(c, m)
		} else {
			c.hub.handleScreen(m)
		}
	}
}

func (c *client) writePump() {
	t := time.NewTicker(pingPeriod)
	defer func() { t.Stop(); _ = c.conn.Close() }()
	for {
		select {
		case b, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, b); err != nil {
				return
			}
		case <-t.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (h *Hub) handleCommand(c *client, m Message) {
	h.cmdMu.Lock()
	defer h.cmdMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fail := func(msg string) { c.enqueue(encode("error", map[string]string{"message": msg})) }
	toScreens := func(t string, p interface{}) { h.sendRole(RoleScreen, encode(t, p)) }

	switch m.Type {
	case "GetVideo":
		var p struct {
			RequestID int64 `json:"requestId"`
		}
		if json.Unmarshal(m.Payload, &p) != nil || p.RequestID <= 0 {
			fail("Invalid request")
			return
		}
		if h.countScreens() == 0 {
			fail("No player screen connected")
			return
		}
		req, err := h.q.Load(ctx, p.RequestID)
		switch {
		case errors.Is(err, queue.ErrBusy):
			fail("Another song is already loaded")
			return
		case errors.Is(err, queue.ErrNoLink):
			fail("Add a valid link first")
			return
		case errors.Is(err, queue.ErrNotFound):
			fail("Request no longer exists")
			h.BroadcastQueue()
			return
		case err != nil:
			log.Printf("hub: load: %v", err)
			fail("Database error")
			return
		}
		// videoId always comes from the database, never from the client.
		toScreens("GetVideo", map[string]interface{}{"requestId": req.ID, "videoId": req.VideoID})
		h.setState("cued", req.ID)
		h.BroadcastQueue()

	case "PlayVideo", "RestartVideo":
		cur, err := h.q.MarkPlaying(ctx)
		if err != nil || cur == nil {
			if err != nil {
				log.Printf("hub: play: %v", err)
			}
			return
		}
		toScreens(m.Type, nil)
		h.setState("playing", cur.ID)
		h.BroadcastQueue()

	case "PauseVideo":
		cur, err := h.q.Current(ctx)
		if err != nil || cur == nil {
			return
		}
		toScreens("PauseVideo", nil)
		h.setState("paused", cur.ID)

	case "FinishVideo":
		cur, err := h.q.Finish(ctx)
		if err != nil || cur == nil {
			return
		}
		toScreens("FinishVideo", nil)
		h.setState("cued", 0)
		h.BroadcastQueue()

	case "UndoVideo":
		cur, err := h.q.Undo(ctx)
		if err != nil || cur == nil {
			return
		}
		toScreens("UndoVideo", nil)
		h.setState("cued", 0)
		h.BroadcastQueue()
	}
}

// handleScreen processes state reports from the player page.
func (h *Hub) handleScreen(m Message) {
	if m.Type != "player_state" {
		return
	}
	var p struct {
		State     string `json:"state"`
		RequestID int64  `json:"requestId"`
	}
	if json.Unmarshal(m.Payload, &p) != nil || p.RequestID <= 0 {
		return
	}
	h.cmdMu.Lock()
	defer h.cmdMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cur, err := h.q.Current(ctx)
	if err != nil || cur == nil || cur.ID != p.RequestID {
		return // stale report
	}
	switch p.State {
	case "ended":
		if _, err := h.q.Finish(ctx); err != nil {
			log.Printf("hub: finish: %v", err)
			return
		}
		h.setState("cued", 0)
		h.BroadcastQueue()
	case "playing", "paused", "cued":
		h.setState(p.State, cur.ID)
	}
}
