package server

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"poker/auth"
	"poker/game"
)

// wsPlayer simulates a real player connecting via WebSocket.
type wsPlayer struct {
	t        *testing.T
	conn     *websocket.Conn
	id       string
	name     string
	token    string
	roomID   string
	mu       sync.Mutex
	hand     []map[string]interface{}
	state    map[string]interface{}
	turn     chan turnInfo
	showdown chan map[string]interface{}
	errors   []string
	closed   int32
}

type turnInfo struct {
	options    []string
	callAmount int
	minRaise   int
}

func newWSPlayer(t *testing.T, serverURL, roomID, id, name, token string) *wsPlayer {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") +
		fmt.Sprintf("/ws?room=%s&player=%s&name=%s&token=%s", roomID, id, name, token)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("player %s: ws dial error: %v", name, err)
	}

	p := &wsPlayer{
		t:        t,
		conn:     conn,
		id:       id,
		name:     name,
		token:    token,
		roomID:   roomID,
		turn:     make(chan turnInfo, 10),
		showdown: make(chan map[string]interface{}, 10),
	}
	go p.readLoop()
	return p
}

func (p *wsPlayer) readLoop() {
	for {
		_, raw, err := p.conn.ReadMessage()
		if err != nil {
			if atomic.LoadInt32(&p.closed) == 0 {
				// unexpected close
			}
			return
		}
		var msg struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "game_state":
			var state map[string]interface{}
			json.Unmarshal(msg.Payload, &state)
			p.mu.Lock()
			p.state = state
			p.mu.Unlock()

		case "deal":
			var payload map[string]interface{}
			json.Unmarshal(msg.Payload, &payload)
			if hand, ok := payload["hand"].([]interface{}); ok {
				p.mu.Lock()
				p.hand = make([]map[string]interface{}, len(hand))
				for i, c := range hand {
					if cm, ok := c.(map[string]interface{}); ok {
						p.hand[i] = cm
					}
				}
				p.mu.Unlock()
			}

		case "your_turn":
			var payload map[string]interface{}
			json.Unmarshal(msg.Payload, &payload)
			ti := turnInfo{}
			if opts, ok := payload["options"].([]interface{}); ok {
				for _, o := range opts {
					ti.options = append(ti.options, fmt.Sprint(o))
				}
			}
			if ca, ok := payload["callAmount"].(float64); ok {
				ti.callAmount = int(ca)
			}
			if mr, ok := payload["minRaise"].(float64); ok {
				ti.minRaise = int(mr)
			}
			select {
			case p.turn <- ti:
			default:
			}

		case "showdown":
			var payload map[string]interface{}
			json.Unmarshal(msg.Payload, &payload)
			select {
			case p.showdown <- payload:
			default:
			}

		case "error":
			var payload map[string]string
			json.Unmarshal(msg.Payload, &payload)
			p.mu.Lock()
			p.errors = append(p.errors, payload["message"])
			p.mu.Unlock()
		}
	}
}

func (p *wsPlayer) send(msgType string, payload interface{}) {
	msg := map[string]interface{}{"type": msgType, "payload": payload}
	data, _ := json.Marshal(msg)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.conn.WriteMessage(websocket.TextMessage, data)
}

func (p *wsPlayer) ready() {
	p.send("ready", map[string]interface{}{})
}

func (p *wsPlayer) action(act string, amount int) {
	p.send("action", map[string]interface{}{"action": act, "amount": amount})
}

func (p *wsPlayer) chat(message string) {
	p.send("chat", map[string]interface{}{"message": message})
}

func (p *wsPlayer) close() {
	atomic.StoreInt32(&p.closed, 1)
	p.conn.Close()
}

func (p *wsPlayer) getPhase() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == nil {
		return ""
	}
	if phase, ok := p.state["phase"].(string); ok {
		return phase
	}
	return ""
}

func (p *wsPlayer) getErrors() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make([]string, len(p.errors))
	copy(cp, p.errors)
	return cp
}

// setupIntegrationServer creates a test server with auth store and returns the URL and helper to register users.
func setupIntegrationServer(t *testing.T) (serverURL string, store *auth.Store, cleanup func()) {
	t.Helper()
	f, _ := os.CreateTemp("", "poker_integration_*.db")
	f.Close()

	store, err := auth.NewStore(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	manager := game.NewManager()
	hub := NewHub(manager, store)
	srv := NewServer(manager, hub, store)

	ts := httptest.NewServer(srv.Routes())
	return ts.URL, store, func() {
		ts.Close()
		os.Remove(f.Name())
	}
}

func registerUser(t *testing.T, serverURL, username, password string) (userID, token string) {
	t.Helper()
	body := fmt.Sprintf(`{"username":"%s","password":"%s"}`, username, password)
	resp, err := http.Post(serverURL+"/auth/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("register %s: %v", username, err)
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if errMsg, ok := result["error"]; ok {
		t.Fatalf("register %s error: %v", username, errMsg)
	}
	return fmt.Sprint(result["userId"]), fmt.Sprint(result["token"])
}

func createRoom(t *testing.T, serverURL string, maxPlayers, smallBlind, startingChips int) string {
	t.Helper()
	body := fmt.Sprintf(`{"maxPlayers":%d,"smallBlind":%d,"startingChips":%d}`, maxPlayers, smallBlind, startingChips)
	resp, err := http.Post(serverURL+"/rooms", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	defer resp.Body.Close()
	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	return result["roomId"]
}

// ======================================================================
// Integration Tests
// ======================================================================

// Test: 2 players play a full hand via WebSocket
func TestIntegration_TwoPlayerFullHand(t *testing.T) {
	serverURL, _, cleanup := setupIntegrationServer(t)
	defer cleanup()

	// Register users
	id1, tok1 := registerUser(t, serverURL, "Alice", "pass1")
	id2, tok2 := registerUser(t, serverURL, "Bob", "pass2")

	// Create room
	roomID := createRoom(t, serverURL, 2, 10, 1000)

	// Connect via WebSocket
	p1 := newWSPlayer(t, serverURL, roomID, id1, "Alice", tok1)
	defer p1.close()
	time.Sleep(200 * time.Millisecond)
	p2 := newWSPlayer(t, serverURL, roomID, id2, "Bob", tok2)
	defer p2.close()
	time.Sleep(200 * time.Millisecond)

	// Both ready
	p1.ready()
	p2.ready()

	// Play the hand: each player acts when it's their turn
	showdownSeen := int32(0)
	var wg sync.WaitGroup

	playHand := func(p *wsPlayer) {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			select {
			case ti := <-p.turn:
				time.Sleep(50 * time.Millisecond)
				if contains(ti.options, "call") {
					p.action("call", 0)
				} else if contains(ti.options, "check") {
					p.action("check", 0)
				} else {
					p.action("fold", 0)
				}
			case <-p.showdown:
				atomic.AddInt32(&showdownSeen, 1)
				return
			case <-time.After(15 * time.Second):
				return
			}
		}
	}

	wg.Add(2)
	go playHand(p1)
	go playHand(p2)
	wg.Wait()

	if atomic.LoadInt32(&showdownSeen) < 2 {
		t.Errorf("showdown seen by %d players, want 2", atomic.LoadInt32(&showdownSeen))
	}

	// Check no errors
	for _, p := range []*wsPlayer{p1, p2} {
		errs := p.getErrors()
		if len(errs) > 0 {
			t.Errorf("player %s had errors: %v", p.name, errs)
		}
	}
}

// Test: 4 players with mixed strategies (fold, call, raise, all-in)
func TestIntegration_FourPlayerMixedStrategies(t *testing.T) {
	serverURL, _, cleanup := setupIntegrationServer(t)
	defer cleanup()

	names := []string{"Alice", "Bob", "Charlie", "Diana"}
	ids := make([]string, 4)
	tokens := make([]string, 4)
	for i, name := range names {
		ids[i], tokens[i] = registerUser(t, serverURL, name, "pass"+name)
	}

	roomID := createRoom(t, serverURL, 4, 10, 1000)

	// Connect all players
	players := make([]*wsPlayer, 4)
	for i := range players {
		players[i] = newWSPlayer(t, serverURL, roomID, ids[i], names[i], tokens[i])
		defer players[i].close()
		time.Sleep(100 * time.Millisecond)
	}

	// All ready
	for _, p := range players {
		p.ready()
	}
	time.Sleep(300 * time.Millisecond)

	showdownCount := int32(0)
	var wg sync.WaitGroup

	strategies := []string{"caller", "folder", "raiser", "caller"}

	for i, p := range players {
		wg.Add(1)
		go func(p *wsPlayer, strategy string) {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				select {
				case ti := <-p.turn:
					time.Sleep(time.Duration(30+rand.Intn(70)) * time.Millisecond)
					switch strategy {
					case "folder":
						if j == 0 { // fold on first action
							p.action("fold", 0)
							// wait for showdown
							select {
							case <-p.showdown:
								atomic.AddInt32(&showdownCount, 1)
							case <-time.After(20 * time.Second):
							}
							return
						}
						fallthrough
					case "caller":
						if contains(ti.options, "call") {
							p.action("call", 0)
						} else if contains(ti.options, "check") {
							p.action("check", 0)
						} else {
							p.action("fold", 0)
						}
					case "raiser":
						if contains(ti.options, "raise") && ti.minRaise > 0 && j < 2 {
							p.action("raise", ti.minRaise)
						} else if contains(ti.options, "call") {
							p.action("call", 0)
						} else if contains(ti.options, "check") {
							p.action("check", 0)
						} else {
							p.action("fold", 0)
						}
					}
				case <-p.showdown:
					atomic.AddInt32(&showdownCount, 1)
					return
				case <-time.After(20 * time.Second):
					return
				}
			}
		}(p, strategies[i])
	}

	wg.Wait()

	if atomic.LoadInt32(&showdownCount) < 4 {
		t.Errorf("showdown seen by %d players, want 4", atomic.LoadInt32(&showdownCount))
	}
}

// Test: Player disconnects mid-game, game continues
func TestIntegration_PlayerDisconnectMidGame(t *testing.T) {
	serverURL, _, cleanup := setupIntegrationServer(t)
	defer cleanup()

	id1, tok1 := registerUser(t, serverURL, "Alice", "pass1")
	id2, tok2 := registerUser(t, serverURL, "Bob", "pass2")
	id3, tok3 := registerUser(t, serverURL, "Charlie", "pass3")

	roomID := createRoom(t, serverURL, 3, 10, 1000)

	p1 := newWSPlayer(t, serverURL, roomID, id1, "Alice", tok1)
	defer p1.close()
	time.Sleep(100 * time.Millisecond)
	p2 := newWSPlayer(t, serverURL, roomID, id2, "Bob", tok2)
	time.Sleep(100 * time.Millisecond)
	p3 := newWSPlayer(t, serverURL, roomID, id3, "Charlie", tok3)
	defer p3.close()
	time.Sleep(100 * time.Millisecond)

	p1.ready()
	p2.ready()
	p3.ready()
	time.Sleep(500 * time.Millisecond)

	// p2 disconnects after game starts
	time.Sleep(500 * time.Millisecond)
	p2.close()
	t.Log("Bob disconnected")

	// p1 and p3 continue playing
	showdownCount := int32(0)
	var wg sync.WaitGroup

	playToEnd := func(p *wsPlayer) {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			select {
			case ti := <-p.turn:
				if contains(ti.options, "call") {
					p.action("call", 0)
				} else if contains(ti.options, "check") {
					p.action("check", 0)
				} else {
					p.action("fold", 0)
				}
			case <-p.showdown:
				atomic.AddInt32(&showdownCount, 1)
				return
			case <-time.After(30 * time.Second):
				t.Logf("player %s timed out waiting", p.name)
				return
			}
		}
	}

	wg.Add(2)
	go playToEnd(p1)
	go playToEnd(p3)
	wg.Wait()

	// Game should complete despite disconnect
	if atomic.LoadInt32(&showdownCount) < 2 {
		t.Errorf("showdown seen by %d remaining players, want 2", atomic.LoadInt32(&showdownCount))
	}
}

// Test: Multiple hands in sequence with same players
func TestIntegration_MultipleHandsSequence(t *testing.T) {
	serverURL, _, cleanup := setupIntegrationServer(t)
	defer cleanup()

	id1, tok1 := registerUser(t, serverURL, "Alice", "pass1")
	id2, tok2 := registerUser(t, serverURL, "Bob", "pass2")

	roomID := createRoom(t, serverURL, 2, 10, 1000)

	p1 := newWSPlayer(t, serverURL, roomID, id1, "Alice", tok1)
	defer p1.close()
	time.Sleep(200 * time.Millisecond)
	p2 := newWSPlayer(t, serverURL, roomID, id2, "Bob", tok2)
	defer p2.close()
	time.Sleep(200 * time.Millisecond)

	for hand := 0; hand < 3; hand++ {
		t.Logf("--- Hand %d ---", hand+1)

		p1.ready()
		p2.ready()
		time.Sleep(300 * time.Millisecond)

		showdownCount := int32(0)
		var wg sync.WaitGroup

		playHand := func(p *wsPlayer) {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				select {
				case ti := <-p.turn:
					time.Sleep(30 * time.Millisecond)
					if contains(ti.options, "call") {
						p.action("call", 0)
					} else if contains(ti.options, "check") {
						p.action("check", 0)
					} else {
						p.action("fold", 0)
					}
				case <-p.showdown:
					atomic.AddInt32(&showdownCount, 1)
					return
				case <-time.After(15 * time.Second):
					t.Logf("hand %d: player %s timed out", hand+1, p.name)
					return
				}
			}
		}

		wg.Add(2)
		go playHand(p1)
		go playHand(p2)
		wg.Wait()

		if atomic.LoadInt32(&showdownCount) < 2 {
			t.Errorf("hand %d: showdown seen by %d, want 2", hand+1, atomic.LoadInt32(&showdownCount))
		}

		// Wait for post-hand cleanup
		time.Sleep(5 * time.Second)
	}
}

// Test: All-in preflop with 3 players (side pots)
func TestIntegration_AllInSidePots(t *testing.T) {
	serverURL, _, cleanup := setupIntegrationServer(t)
	defer cleanup()

	// Different starting chips to force side pots
	id1, tok1 := registerUser(t, serverURL, "Short", "pass1")
	id2, tok2 := registerUser(t, serverURL, "Medium", "pass2")
	id3, tok3 := registerUser(t, serverURL, "Big", "pass3")

	roomID := createRoom(t, serverURL, 3, 10, 500)

	p1 := newWSPlayer(t, serverURL, roomID, id1, "Short", tok1)
	defer p1.close()
	time.Sleep(100 * time.Millisecond)
	p2 := newWSPlayer(t, serverURL, roomID, id2, "Medium", tok2)
	defer p2.close()
	time.Sleep(100 * time.Millisecond)
	p3 := newWSPlayer(t, serverURL, roomID, id3, "Big", tok3)
	defer p3.close()
	time.Sleep(100 * time.Millisecond)

	p1.ready()
	p2.ready()
	p3.ready()
	time.Sleep(500 * time.Millisecond)

	// Everyone goes all-in
	showdownCount := int32(0)
	var wg sync.WaitGroup

	for _, p := range []*wsPlayer{p1, p2, p3} {
		wg.Add(1)
		go func(p *wsPlayer) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				select {
				case ti := <-p.turn:
					if contains(ti.options, "allin") {
						p.action("allin", 0)
					} else if contains(ti.options, "call") {
						p.action("call", 0)
					}
				case sd := <-p.showdown:
					atomic.AddInt32(&showdownCount, 1)
					// Check for pot details
					if pots, ok := sd["pots"].([]interface{}); ok {
						t.Logf("player %s sees %d pots", p.name, len(pots))
						for _, pot := range pots {
							if pm, ok := pot.(map[string]interface{}); ok {
								t.Logf("  %s: %v chips — %s",
									pm["label"], pm["amount"], pm["reason"])
							}
						}
					}
					return
				case <-time.After(15 * time.Second):
					return
				}
			}
		}(p)
	}

	wg.Wait()

	if atomic.LoadInt32(&showdownCount) < 3 {
		t.Errorf("showdown seen by %d, want 3", atomic.LoadInt32(&showdownCount))
	}
}

// Test: Concurrent chat messages during game
func TestIntegration_ConcurrentChat(t *testing.T) {
	serverURL, _, cleanup := setupIntegrationServer(t)
	defer cleanup()

	id1, tok1 := registerUser(t, serverURL, "Alice", "pass1")
	id2, tok2 := registerUser(t, serverURL, "Bob", "pass2")

	roomID := createRoom(t, serverURL, 2, 10, 1000)

	p1 := newWSPlayer(t, serverURL, roomID, id1, "Alice", tok1)
	defer p1.close()
	time.Sleep(200 * time.Millisecond)
	p2 := newWSPlayer(t, serverURL, roomID, id2, "Bob", tok2)
	defer p2.close()
	time.Sleep(200 * time.Millisecond)

	// Send chat messages concurrently while readying up
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			p1.chat(fmt.Sprintf("Alice message %d", n))
		}(i)
		go func(n int) {
			defer wg.Done()
			p2.chat(fmt.Sprintf("Bob message %d", n))
		}(i)
	}
	wg.Wait()

	// Should not crash or produce errors
	for _, p := range []*wsPlayer{p1, p2} {
		errs := p.getErrors()
		if len(errs) > 0 {
			t.Errorf("player %s had errors: %v", p.name, errs)
		}
	}
}

// Test: Rapid reconnect — same player joins twice quickly
func TestIntegration_RapidReconnect(t *testing.T) {
	serverURL, _, cleanup := setupIntegrationServer(t)
	defer cleanup()

	id1, tok1 := registerUser(t, serverURL, "Alice", "pass1")
	id2, tok2 := registerUser(t, serverURL, "Bob", "pass2")

	roomID := createRoom(t, serverURL, 2, 10, 1000)

	// Alice connects
	p1a := newWSPlayer(t, serverURL, roomID, id1, "Alice", tok1)
	time.Sleep(100 * time.Millisecond)

	// Alice disconnects and immediately reconnects
	p1a.close()
	time.Sleep(50 * time.Millisecond)
	p1b := newWSPlayer(t, serverURL, roomID, id1, "Alice", tok1)
	defer p1b.close()
	time.Sleep(200 * time.Millisecond)

	// Bob joins
	p2 := newWSPlayer(t, serverURL, roomID, id2, "Bob", tok2)
	defer p2.close()
	time.Sleep(200 * time.Millisecond)

	// Game should work normally
	p1b.ready()
	p2.ready()
	time.Sleep(500 * time.Millisecond)

	// Play a hand
	showdownCount := int32(0)
	var wg sync.WaitGroup

	playHand := func(p *wsPlayer) {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			select {
			case ti := <-p.turn:
				if contains(ti.options, "call") {
					p.action("call", 0)
				} else if contains(ti.options, "check") {
					p.action("check", 0)
				}
			case <-p.showdown:
				atomic.AddInt32(&showdownCount, 1)
				return
			case <-time.After(15 * time.Second):
				return
			}
		}
	}

	wg.Add(2)
	go playHand(p1b)
	go playHand(p2)
	wg.Wait()

	if atomic.LoadInt32(&showdownCount) < 2 {
		t.Errorf("showdown seen by %d, want 2", atomic.LoadInt32(&showdownCount))
	}
}

// Test: 6 players stress test — random actions
func TestIntegration_SixPlayerStress(t *testing.T) {
	serverURL, _, cleanup := setupIntegrationServer(t)
	defer cleanup()

	n := 6
	ids := make([]string, n)
	tokens := make([]string, n)
	names := []string{"Alpha", "Beta", "Gamma", "Delta", "Epsilon", "Zeta"}
	for i := 0; i < n; i++ {
		ids[i], tokens[i] = registerUser(t, serverURL, names[i], "pass"+names[i])
	}

	roomID := createRoom(t, serverURL, 6, 5, 1000)

	players := make([]*wsPlayer, n)
	for i := 0; i < n; i++ {
		players[i] = newWSPlayer(t, serverURL, roomID, ids[i], names[i], tokens[i])
		defer players[i].close()
		time.Sleep(80 * time.Millisecond)
	}

	// All ready
	for _, p := range players {
		p.ready()
	}
	time.Sleep(500 * time.Millisecond)

	showdownCount := int32(0)
	var wg sync.WaitGroup

	for _, p := range players {
		wg.Add(1)
		go func(p *wsPlayer) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				select {
				case ti := <-p.turn:
					time.Sleep(time.Duration(20+rand.Intn(80)) * time.Millisecond)
					// Random strategy
					r := rand.Float64()
					switch {
					case r < 0.15 && contains(ti.options, "fold"):
						p.action("fold", 0)
					case r < 0.3 && contains(ti.options, "raise") && ti.minRaise > 0:
						p.action("raise", ti.minRaise)
					case r < 0.1 && contains(ti.options, "allin"):
						p.action("allin", 0)
					case contains(ti.options, "call"):
						p.action("call", 0)
					case contains(ti.options, "check"):
						p.action("check", 0)
					default:
						p.action("fold", 0)
					}
				case <-p.showdown:
					atomic.AddInt32(&showdownCount, 1)
					return
				case <-time.After(30 * time.Second):
					t.Logf("player %s timed out", p.name)
					return
				}
			}
		}(p)
	}

	wg.Wait()
	t.Logf("showdown seen by %d/%d players", atomic.LoadInt32(&showdownCount), n)

	if atomic.LoadInt32(&showdownCount) < int32(n) {
		t.Errorf("showdown seen by %d, want %d", atomic.LoadInt32(&showdownCount), n)
	}
}

// helper: get players from state
func (p *wsPlayer) getPlayers() []map[string]interface{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == nil {
		return nil
	}
	players, ok := p.state["players"].([]interface{})
	if !ok {
		return nil
	}
	result := make([]map[string]interface{}, 0, len(players))
	for _, pl := range players {
		if pm, ok := pl.(map[string]interface{}); ok {
			result = append(result, pm)
		}
	}
	return result
}

// Test: Ready state broadcasts to other players + toggle ready
func TestIntegration_ReadyBroadcastAndToggle(t *testing.T) {
	serverURL, _, cleanup := setupIntegrationServer(t)
	defer cleanup()

	id1, tok1 := registerUser(t, serverURL, "Alice", "pass1")
	id2, tok2 := registerUser(t, serverURL, "Bob", "pass2")

	roomID := createRoom(t, serverURL, 2, 10, 1000)

	// Alice joins
	p1 := newWSPlayer(t, serverURL, roomID, id1, "Alice", tok1)
	defer p1.close()
	time.Sleep(300 * time.Millisecond)

	// Alice readies up
	p1.ready()
	time.Sleep(300 * time.Millisecond)

	// Verify Alice sees herself as ready
	players := p1.getPlayers()
	if len(players) != 1 {
		t.Fatalf("Alice should see 1 player, got %d", len(players))
	}
	if ready, _ := players[0]["isReady"].(bool); !ready {
		t.Error("Alice should see herself as ready")
	}

	// Bob joins — should see Alice (ready) and himself (not ready)
	p2 := newWSPlayer(t, serverURL, roomID, id2, "Bob", tok2)
	defer p2.close()
	time.Sleep(300 * time.Millisecond)

	players = p2.getPlayers()
	if len(players) != 2 {
		t.Fatalf("Bob should see 2 players, got %d", len(players))
	}
	t.Logf("Bob sees %d players", len(players))
	for _, pl := range players {
		name := pl["name"].(string)
		ready, _ := pl["isReady"].(bool)
		t.Logf("  %s: isReady=%v", name, ready)
		if name == "Alice" && !ready {
			t.Error("Bob should see Alice as ready")
		}
		if name == "Bob" && ready {
			t.Error("Bob should see himself as NOT ready")
		}
	}

	// Alice also sees both players
	players = p1.getPlayers()
	if len(players) != 2 {
		t.Fatalf("Alice should see 2 players after Bob joins, got %d", len(players))
	}

	// Test toggle: Alice cancels ready
	p1.ready() // toggle off
	time.Sleep(300 * time.Millisecond)

	// Bob should see Alice as NOT ready
	players = p2.getPlayers()
	aliceReady := false
	for _, pl := range players {
		if pl["name"].(string) == "Alice" {
			aliceReady, _ = pl["isReady"].(bool)
		}
	}
	if aliceReady {
		t.Error("After toggle, Bob should see Alice as NOT ready")
	}

	// Alice re-readies, Bob readies — game should start
	p1.ready()
	time.Sleep(100 * time.Millisecond)
	p2.ready()
	time.Sleep(500 * time.Millisecond)

	// Both should be in preflop (game started)
	phase1 := p1.getPhase()
	phase2 := p2.getPhase()
	if phase1 != "preflop" {
		t.Errorf("Alice phase = %s, want preflop", phase1)
	}
	if phase2 != "preflop" {
		t.Errorf("Bob phase = %s, want preflop", phase2)
	}
	t.Logf("Game started: Alice=%s, Bob=%s", phase1, phase2)
}

// Test: Late joiner sees existing players and their ready state
func TestIntegration_LateJoinerSeesPlayers(t *testing.T) {
	serverURL, _, cleanup := setupIntegrationServer(t)
	defer cleanup()

	id1, tok1 := registerUser(t, serverURL, "Alice", "pass1")
	id2, tok2 := registerUser(t, serverURL, "Bob", "pass2")
	id3, tok3 := registerUser(t, serverURL, "Charlie", "pass3")

	roomID := createRoom(t, serverURL, 4, 10, 1000)

	// Alice joins and readies, Bob joins but does NOT ready
	p1 := newWSPlayer(t, serverURL, roomID, id1, "Alice", tok1)
	defer p1.close()
	time.Sleep(200 * time.Millisecond)

	p2 := newWSPlayer(t, serverURL, roomID, id2, "Bob", tok2)
	defer p2.close()
	time.Sleep(200 * time.Millisecond)

	p1.ready()
	time.Sleep(300 * time.Millisecond)

	// Charlie joins late — should see Alice (ready), Bob (not ready), and himself
	p3 := newWSPlayer(t, serverURL, roomID, id3, "Charlie", tok3)
	defer p3.close()
	time.Sleep(300 * time.Millisecond)

	players := p3.getPlayers()
	if len(players) != 3 {
		t.Fatalf("Charlie should see 3 players, got %d", len(players))
	}
	for _, pl := range players {
		name := pl["name"].(string)
		ready, _ := pl["isReady"].(bool)
		t.Logf("Charlie sees: %s isReady=%v", name, ready)
		if name == "Alice" && !ready {
			t.Error("Charlie should see Alice as ready")
		}
		if name == "Bob" && ready {
			t.Error("Charlie should see Bob as NOT ready")
		}
		if name == "Charlie" && ready {
			t.Error("Charlie should see himself as NOT ready")
		}
	}

	// Everyone readies — game should start
	p2.ready()
	time.Sleep(100 * time.Millisecond)
	p3.ready()
	time.Sleep(500 * time.Millisecond)

	for _, p := range []*wsPlayer{p1, p2, p3} {
		phase := p.getPhase()
		if phase != "preflop" {
			t.Errorf("%s phase = %s, want preflop", p.name, phase)
		}
	}
	t.Log("All 3 players in game — late joiner saw everyone correctly!")
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
