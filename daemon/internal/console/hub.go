// Package console implements the wings-compatible WebSocket hub.
package console

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"ptero-native/internal/api"
	"ptero-native/internal/auth"
	"ptero-native/internal/server"
	"ptero-native/internal/util"
)

// client is one connected websocket session.
type client struct {
	conn    *websocket.Conn
	uuid    string
	writeMu sync.Mutex
	permUID string
	authed  bool
	sendCh  chan outMsg
	closed  bool
}

type outMsg struct {
	event string
	data  interface{}
}

// Hub manages websocket clients and broadcasts events.
type Hub struct {
	mu      sync.RWMutex
	clients map[string]map[*client]struct{} // uuid -> clients
	app     *api.App
}

// BuildHub creates the console hub and wires it into the app.
func BuildHub(app *api.App) *Hub {
	return &Hub{clients: map[string]map[*client]struct{}{}, app: app}
}

// ensure Hub implements server.Hub
var _ server.Hub = (*Hub)(nil)

// ConsoleLine broadcasts a console line for a server.
func (h *Hub) ConsoleLine(uuid, line string) {
	h.emit(uuid, "console output", map[string]string{"line": line})
}

// StatusChange broadcasts a power state change.
func (h *Hub) StatusChange(uuid, state string) {
	h.emit(uuid, "status", map[string]string{"state": state})
}

// InstallOutput broadcasts install output.
func (h *Hub) InstallOutput(uuid, line string) {
	h.emit(uuid, "install output", map[string]string{"line": line})
}

// InstallStatus broadcasts install status changes.
func (h *Hub) InstallStatus(uuid, status string) {
	h.emit(uuid, "install status", map[string]interface{}{
		"status":   status,
		"progress": nil,
	})
}

// Stats broadcasts a stats payload.
func (h *Hub) Stats(uuid string, data interface{}) {
	h.emit(uuid, "stats", map[string]interface{}{"data": data})
}

// emit sends an event to all clients of a server.
func (h *Hub) emit(uuid, event string, data interface{}) {
	h.mu.RLock()
	clients := h.clients[uuid]
	h.mu.RUnlock()
	if len(clients) == 0 {
		return
	}
	for c := range clients {
		c.send(outMsg{event: event, data: data})
	}
}

// send queues a message to a client (non-blocking on closed channel).
func (c *client) send(m outMsg) {
	defer func() { _ = recover() }() // channel closed race
	payload, err := json.Marshal(map[string]interface{}{
		"event": m.event,
		"args":  []interface{}{m.data},
	})
	if err != nil {
		return
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed {
		return
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		// trigger close asynchronously
		go c.close()
	}
}

// close tears down a client.
func (c *client) close() {
	if c.closed {
		return
	}
	c.closed = true
	_ = c.conn.Close()
}

// ServeWS handles GET /api/servers/{uuid}/ws?token=JWT.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	a := h.app
	uuid := r.PathValue("uuid")
	s, ok := a.Registry.Get(uuid)
	if !ok {
		util.WriteError(w, util.ErrNotFound("server"))
		return
	}

	token := r.URL.Query().Get("token")
	if token == "" {
		// wings also accepts "Authorization: Bearer" for the ws route
		token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	if token == "" {
		util.WriteJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"errors": []interface{}{util.NewErr(http.StatusUnauthorized, "UnauthorizedAccessException", "missing websocket token")},
		})
		return
	}
	// v1.15: panel signs ws tokens with the node daemon token (node->getDecryptedKey()).
	secret := a.Cfg.Daemon.Token
	claims, err := auth.ParseJWT(token, secret)
	if err != nil {
		util.WriteJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"errors": []interface{}{util.NewErr(http.StatusUnauthorized, "UnauthorizedAccessException", "invalid websocket token: "+err.Error())},
		})
		return
	}
	// v1.15 claim: server_uuid (fall back to sub)
	claimServer := claims.ServerUUID
	if claimServer == "" {
		claimServer = claims.Sub
	}
	if claimServer != "" && claimServer != uuid {
		util.WriteJSON(w, http.StatusForbidden, map[string]interface{}{
			"errors": []interface{}{util.NewErr(http.StatusForbidden, "ForbiddenAccessException", "token does not match server")},
		})
		return
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 4096,
		CheckOrigin:     func(r *http.Request) bool { return true },
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	c := &client{conn: ws, uuid: uuid, authed: true, permUID: claims.UniqueID}
	h.mu.Lock()
	if h.clients[uuid] == nil {
		h.clients[uuid] = map[*client]struct{}{}
	}
	h.clients[uuid][c] = struct{}{}
	h.mu.Unlock()

	// initial state + logs replay
	c.send(outMsg{event: "status", data: map[string]string{"state": s.State()}})
	for _, line := range s.ConsoleLines(100) {
		c.send(outMsg{event: "console output", data: map[string]string{"line": line}})
	}

	// token expiry watch (panel JWT exp)
	if claims.Exp > 0 {
		go func() {
			secs := claims.Exp - time.Now().Unix()
			if secs > 60 {
				time.Sleep(time.Duration(secs-60) * time.Second)
				c.send(outMsg{event: "token expiring", data: map[string]int{"seconds_left": 60}})
			}
			if secs > 0 {
				time.Sleep(time.Duration(secs) * time.Second)
				c.send(outMsg{event: "token expired", data: map[string]interface{}{}})
			}
		}()
	}

	// stats ticker: 1s while connected & running
	stopStats := make(chan struct{})
	go func() {
		t := time.NewTicker(1 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				st := s.Snapshot()
				if st.State == server.StateRunning || st.State == server.StateStarting || st.State == server.StateStopping {
					if stats := s.CollectStats(s.Quota()); stats != nil {
						h.emit(uuid, "stats", map[string]interface{}{"data": stats})
					}
				}
			case <-stopStats:
				return
			}
		}
	}()

	defer func() {
		close(stopStats)
		h.mu.Lock()
		if set, ok := h.clients[uuid]; ok {
			delete(set, c)
			if len(set) == 0 {
				delete(h.clients, uuid)
			}
		}
		h.mu.Unlock()
		_ = ws.Close()
	}()

	// read loop
	for {
		_, payload, err := ws.ReadMessage()
		if err != nil {
			return
		}
		var msg struct {
			Event string   `json:"event"`
			Args  []string `json:"args"`
		}
		if err := json.Unmarshal(payload, &msg); err != nil {
			continue
		}
		switch msg.Event {
		case "send commands":
			for _, cmd := range msg.Args {
				_ = s.SendCommand(cmd)
			}
		case "send logs":
			n := 100
			if len(msg.Args) > 0 {
				if v, err := strconv.Atoi(msg.Args[0]); err == nil && v != 0 {
					if v < 0 {
						v = -v
					}
					n = v
				}
			}
			for _, line := range s.ConsoleLines(n) {
				c.send(outMsg{event: "console output", data: map[string]string{"line": line}})
			}
		case "send stats":
			if stats := s.CollectStats(s.Quota()); stats != nil {
				c.send(outMsg{event: "stats", data: map[string]interface{}{"data": stats}})
			}
		case "send install logs":
			// replay from install tracker is wired via api package buffer
			for _, line := range a.InstallLogReplay(uuid) {
				c.send(outMsg{event: "install output", data: map[string]string{"line": line}})
			}
		case "set state":
			// panel clients never send this; ignore politely
		case "auth":
			// re-auth no-op (token from query is already validated)
		default:
			// unknown events ignored (wings parity)
		}
	}
}
