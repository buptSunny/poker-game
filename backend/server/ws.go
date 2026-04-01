package server

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"poker/auth"
	"poker/game"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type client struct {
	conn        *websocket.Conn
	playerID    string
	roomID      string
	userID      string // empty if guest
	send        chan []byte
	isSpectator bool
}

type Hub struct {
	mu      sync.RWMutex
	rooms   map[string]map[string]*client // roomID -> playerID -> client
	manager *game.Manager
	store   *auth.Store
}

func NewHub(manager *game.Manager, store *auth.Store) *Hub {
	return &Hub{
		rooms:   map[string]map[string]*client{},
		manager: manager,
		store:   store,
	}
}

func (h *Hub) Broadcast(roomID string, msg game.Message) {
	data, _ := json.Marshal(msg)
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.rooms[roomID] {
		select {
		case c.send <- data:
		default:
		}
	}
}

func (h *Hub) SendTo(roomID, playerID string, msg game.Message) {
	data, _ := json.Marshal(msg)
	h.mu.RLock()
	defer h.mu.RUnlock()
	if clients, ok := h.rooms[roomID]; ok {
		if c, ok := clients[playerID]; ok {
			select {
			case c.send <- data:
			default:
			}
		}
	}
}

func (h *Hub) addClient(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms[c.roomID] == nil {
		h.rooms[c.roomID] = map[string]*client{}
	}
	h.rooms[c.roomID][c.playerID] = c
}

func (h *Hub) removeClient(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if clients, ok := h.rooms[c.roomID]; ok {
		// Only remove if this is still the current client for this player
		// (prevents a reconnecting client from removing the new connection)
		if clients[c.playerID] == c {
			delete(clients, c.playerID)
		}
	}
}

type inMsg struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func sendWSMsg(conn *websocket.Conn, msg game.Message) {
	data, _ := json.Marshal(msg)
	conn.WriteMessage(websocket.TextMessage, data)
}

func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Query().Get("room")
	playerID := r.URL.Query().Get("player")
	playerName := r.URL.Query().Get("name")
	token := r.URL.Query().Get("token")

	if roomID == "" || playerID == "" || playerName == "" {
		http.Error(w, "missing params", http.StatusBadRequest)
		return
	}

	room, ok := h.manager.GetRoom(roomID)
	if !ok {
		http.Error(w, "room not found", http.StatusNotFound)
		return
	}

	// Upgrade first so we can send a proper error message to the client
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("ws upgrade:", err)
		return
	}

	// Validate token
	chips := 1000
	userID := ""
	if h.store != nil {
		if token == "" {
			sendWSMsg(conn, game.Message{Type: "auth_error", Payload: map[string]string{"message": "请先登录"}})
			conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4001, "auth required"))
			conn.Close()
			return
		}
		user, valid := h.store.ValidateToken(token)
		if !valid {
			sendWSMsg(conn, game.Message{Type: "auth_error", Payload: map[string]string{"message": "登录已过期，请重新登录"}})
			conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4001, "invalid token"))
			conn.Close()
			return
		}
		if user.ID != playerID {
			sendWSMsg(conn, game.Message{Type: "auth_error", Payload: map[string]string{"message": "身份验证失败"}})
			conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4001, "token mismatch"))
			conn.Close()
			return
		}
		chips = user.Chips
		// Use room's starting chips if player has fewer (fresh buy-in)
		if room.StartingChips > 0 && !room.Game.IsPlayerInGame(playerID) {
			chips = room.StartingChips
		}
		userID = user.ID
		playerName = user.Username
	}

	// First non-bot player to join becomes owner
	if room.OwnerID == "" && userID != "" {
		room.OwnerID = playerID
		room.Game.OwnerID = playerID
	}

	c := &client{
		conn:     conn,
		playerID: playerID,
		roomID:   roomID,
		userID:   userID,
		send:     make(chan []byte, 64),
	}
	h.addClient(c)

	// Rejoin: player already in game (e.g. reconnected after disconnect)
	isRejoin := room.Game.IsPlayerInGame(playerID)
	isSpectator := false
	if isRejoin {
		// Clear disconnected flag and re-send current state
		room.Game.SetDisconnected(playerID, false)
		room.Game.SendStateTo(playerID)
	} else if room.Game.GetPhase() != game.PhaseWaiting {
		// Game in progress and player not in it — spectator mode
		isSpectator = true
		sendWSMsg(conn, game.Message{Type: "spectator", Payload: map[string]bool{"spectating": true}})
		room.Game.SendStateTo(playerID) // sends current state (no hole cards since not a player)
	} else {
		if err := room.Game.AddPlayer(playerID, playerName, chips); err != nil {
			sendWSMsg(conn, game.Message{Type: "error", Payload: map[string]string{"message": err.Error()}})
			conn.Close()
			h.removeClient(c)
			return
		}
	}

	c.isSpectator = isSpectator

	go c.writePump()
	c.readPump(h, room)
}

func (c *client) readPump(h *Hub, room *game.Room) {
	defer func() {
		if !c.isSpectator {
			phase := room.Game.GetPhase()

			// Save chips to DB on disconnect
			if c.userID != "" && h.store != nil {
				chips := room.Game.GetPlayerChips(c.playerID)
				if chips < 0 {
					chips = 0
				}
				if err := h.store.UpdateChips(c.userID, chips); err != nil {
					log.Println("save chips error:", err)
				}
			}

			// Mid-game: keep player in game (auto-timeout handles their turns).
			// Waiting phase: remove cleanly.
			if phase == game.PhaseWaiting {
				room.Game.RemovePlayer(c.playerID)
			} else {
				// Mark disconnected so other players can see
				room.Game.SetDisconnected(c.playerID, true)
			}
		}

		h.removeClient(c)
		c.conn.Close()
	}()

	c.conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		return nil
	})

	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
		var msg inMsg
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		// spectators can only chat
		if c.isSpectator && msg.Type != "chat" {
			continue
		}
		switch msg.Type {
		case "action":
			var payload struct {
				Action string `json:"action"`
				Amount int    `json:"amount"`
			}
			json.Unmarshal(msg.Payload, &payload)
			if err := room.Game.HandleAction(c.playerID, payload.Action, payload.Amount); err != nil {
				data, _ := json.Marshal(game.Message{Type: "error", Payload: map[string]string{"message": err.Error()}})
				c.send <- data
			}
		case "ready":
			room.Game.SetReady(c.playerID)
		case "chat":
			var payload struct {
				Message string `json:"message"`
			}
			json.Unmarshal(msg.Payload, &payload)
			room.Game.SendChat(c.playerID, payload.Message)
		case "rebuy":
			if err := room.Game.Rebuy(c.playerID, room.StartingChips); err != nil {
				data, _ := json.Marshal(game.Message{Type: "error", Payload: map[string]string{"message": err.Error()}})
				c.send <- data
			}
		case "kick":
			var payload struct {
				PlayerID string `json:"playerId"`
			}
			json.Unmarshal(msg.Payload, &payload)
			if room.OwnerID != c.playerID {
				data, _ := json.Marshal(game.Message{Type: "error", Payload: map[string]string{"message": "只有房主可以踢人"}})
				c.send <- data
			} else if err := room.Game.KickPlayer(payload.PlayerID); err != nil {
				data, _ := json.Marshal(game.Message{Type: "error", Payload: map[string]string{"message": err.Error()}})
				c.send <- data
			}
		}
	}
}

func (c *client) writePump() {
	ticker := time.NewTicker(50 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case data, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
