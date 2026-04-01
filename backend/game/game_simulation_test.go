package game

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// collectMessages captures broadcast and personal messages for verification.
type messageLog struct {
	mu       sync.Mutex
	messages []Message
	personal map[string][]Message // playerID -> messages
}

func newMessageLog() *messageLog {
	return &messageLog{personal: map[string][]Message{}}
}

func (ml *messageLog) broadcast(msg Message) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	ml.messages = append(ml.messages, msg)
}

func (ml *messageLog) sendTo(playerID string, msg Message) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	ml.personal[playerID] = append(ml.personal[playerID], msg)
}

func (ml *messageLog) lastBroadcast() *Message {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	if len(ml.messages) == 0 {
		return nil
	}
	return &ml.messages[len(ml.messages)-1]
}

func (ml *messageLog) countType(msgType string) int {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	count := 0
	for _, m := range ml.messages {
		if m.Type == msgType {
			count++
		}
	}
	return count
}

func newTestGame(smallBlind int) (*Game, *messageLog) {
	ml := newMessageLog()
	g := NewGame("test", smallBlind, ml.broadcast, ml.sendTo)
	return g, ml
}

// getCurrentPlayer returns the current player's ID. Must be called without holding lock.
func getCurrentPlayer(g *Game) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.CurrentIdx >= len(g.Players) {
		return ""
	}
	return g.Players[g.CurrentIdx].ID
}

func getPhase(g *Game) GamePhase {
	return g.GetPhase()
}

// playUntilPhase drives the game by having all players take simple actions until target phase.
func playUntilPhase(t *testing.T, g *Game, target GamePhase, maxActions int) {
	t.Helper()
	for i := 0; i < maxActions; i++ {
		phase := getPhase(g)
		if phase == target || phase == PhaseWaiting {
			return
		}
		cur := getCurrentPlayer(g)
		if cur == "" {
			return
		}
		g.mu.Lock()
		p := g.Players[g.CurrentIdx]
		callAmt := g.RoundBet - p.Bet
		g.mu.Unlock()
		if callAmt > 0 {
			g.HandleAction(cur, "call", 0)
		} else {
			g.HandleAction(cur, "check", 0)
		}
	}
}

// === Test: Complete 2-player hand (call down to showdown) ===
func TestSimulation_TwoPlayerCallDown(t *testing.T) {
	g, ml := newTestGame(10)
	g.AddPlayer("p1", "Alice", 1000)
	g.AddPlayer("p2", "Bob", 1000)
	g.SetReady("p1")
	g.SetReady("p2")

	if getPhase(g) != PhasePreFlop {
		t.Fatal("game should be in preflop")
	}

	// Play out all streets by calling/checking
	playUntilPhase(t, g, PhaseShowdown, 20)

	phase := getPhase(g)
	if phase != PhaseShowdown {
		t.Errorf("expected showdown, got %s", phase)
	}

	// Verify showdown message was sent
	showdownCount := ml.countType("showdown")
	if showdownCount < 1 {
		t.Error("no showdown message broadcast")
	}

	// Wait for post-hand cleanup
	time.Sleep(5 * time.Second)
	phase = getPhase(g)
	if phase != PhaseWaiting {
		t.Errorf("after cleanup, expected waiting, got %s", phase)
	}

	// Chips should sum to 2000 (zero-sum game)
	g.mu.Lock()
	totalChips := 0
	for _, p := range g.Players {
		totalChips += p.Chips
	}
	g.mu.Unlock()
	if totalChips != 2000 {
		t.Errorf("total chips = %d, want 2000 (zero-sum)", totalChips)
	}
}

// === Test: 3-player game with one fold ===
func TestSimulation_ThreePlayerOneFold(t *testing.T) {
	g, _ := newTestGame(10)
	g.AddPlayer("p1", "Alice", 1000)
	g.AddPlayer("p2", "Bob", 1000)
	g.AddPlayer("p3", "Charlie", 1000)
	g.SetReady("p1")
	g.SetReady("p2")
	g.SetReady("p3")

	if getPhase(g) != PhasePreFlop {
		t.Fatal("game should start")
	}

	// First player folds, rest call down
	cur := getCurrentPlayer(g)
	g.HandleAction(cur, "fold", 0)

	playUntilPhase(t, g, PhaseShowdown, 20)

	if getPhase(g) != PhaseShowdown {
		t.Errorf("expected showdown, got %s", getPhase(g))
	}

	time.Sleep(5 * time.Second)

	// Chips should sum to 3000
	g.mu.Lock()
	totalChips := 0
	for _, p := range g.Players {
		totalChips += p.Chips
	}
	g.mu.Unlock()
	if totalChips != 3000 {
		t.Errorf("total chips = %d, want 3000", totalChips)
	}
}

// === Test: All-in preflop (2 players) — should deal full board ===
func TestSimulation_AllInPreflop(t *testing.T) {
	g, ml := newTestGame(10)
	g.AddPlayer("p1", "Alice", 500)
	g.AddPlayer("p2", "Bob", 500)
	g.SetReady("p1")
	g.SetReady("p2")

	// First player goes all-in
	cur := getCurrentPlayer(g)
	g.HandleAction(cur, "allin", 0)

	// Second player calls
	cur = getCurrentPlayer(g)
	g.HandleAction(cur, "call", 0)

	// Should reach showdown with 5 community cards
	time.Sleep(1 * time.Second)

	g.mu.Lock()
	communityLen := len(g.Community)
	phase := g.Phase
	g.mu.Unlock()

	if phase != PhaseShowdown {
		t.Errorf("expected showdown, got %s", phase)
	}
	if communityLen != 5 {
		t.Errorf("community cards = %d, want 5", communityLen)
	}

	// Check showdown message has 5 community cards
	ml.mu.Lock()
	var showdownComm int
	for _, m := range ml.messages {
		if m.Type == "showdown" {
			if payload, ok := m.Payload.(map[string]interface{}); ok {
				if cards, ok := payload["community"].([]Card); ok {
					showdownComm = len(cards)
				}
			}
		}
	}
	ml.mu.Unlock()
	if showdownComm != 5 {
		t.Errorf("showdown community cards = %d, want 5", showdownComm)
	}
}

// === Test: Everyone folds preflop — board should still have 5 cards ===
func TestSimulation_EveryoneFoldsPreflop(t *testing.T) {
	g, ml := newTestGame(10)
	g.AddPlayer("p1", "Alice", 1000)
	g.AddPlayer("p2", "Bob", 1000)
	g.SetReady("p1")
	g.SetReady("p2")

	// Current player folds
	cur := getCurrentPlayer(g)
	g.HandleAction(cur, "fold", 0)

	// Should be showdown (one player wins)
	phase := getPhase(g)
	if phase != PhaseShowdown {
		t.Errorf("expected showdown after fold, got %s", phase)
	}

	// Community cards should be 5 (we now deal them all)
	g.mu.Lock()
	communityLen := len(g.Community)
	g.mu.Unlock()
	if communityLen != 5 {
		t.Errorf("community cards after fold = %d, want 5", communityLen)
	}

	// Verify showdown message was sent
	if ml.countType("showdown") < 1 {
		t.Error("no showdown message after fold")
	}
}

// === Test: Side pots with 3 players ===
func TestSimulation_SidePots(t *testing.T) {
	g, ml := newTestGame(10)
	// Different chip stacks to force side pots
	g.AddPlayer("p1", "Alice", 100)  // short stack
	g.AddPlayer("p2", "Bob", 500)    // medium
	g.AddPlayer("p3", "Charlie", 1000) // big stack
	g.SetReady("p1")
	g.SetReady("p2")
	g.SetReady("p3")

	// Everyone goes all-in
	for i := 0; i < 3; i++ {
		cur := getCurrentPlayer(g)
		if cur == "" {
			break
		}
		g.HandleAction(cur, "allin", 0)
	}

	// Wait for showdown
	time.Sleep(1 * time.Second)

	phase := getPhase(g)
	if phase != PhaseShowdown {
		t.Errorf("expected showdown, got %s", phase)
	}

	// Verify pot details were sent
	ml.mu.Lock()
	hasPots := false
	for _, m := range ml.messages {
		if m.Type == "showdown" {
			if payload, ok := m.Payload.(map[string]interface{}); ok {
				if pots, ok := payload["pots"]; ok && pots != nil {
					if potList, ok := pots.([]PotDetail); ok && len(potList) > 1 {
						hasPots = true
						// Verify pot labels
						for _, p := range potList {
							t.Logf("Pot: %s, amount=%d, winners=%v, eligible=%v, handRank=%s",
								p.Label, p.Amount, p.Winners, p.Eligible, p.HandRank)
						}
					}
				}
			}
		}
	}
	ml.mu.Unlock()
	if !hasPots {
		t.Error("expected side pot details in showdown message")
	}

	// Total chips should be conserved
	time.Sleep(5 * time.Second)
	g.mu.Lock()
	totalChips := 0
	for _, p := range g.Players {
		totalChips += p.Chips
	}
	g.mu.Unlock()
	if totalChips != 1600 { // 100+500+1000
		t.Errorf("total chips = %d, want 1600", totalChips)
	}
}

// === Test: Bot-only room gets cleaned up ===
func TestSimulation_BotOnlyCleanup(t *testing.T) {
	g, _ := newTestGame(10)
	g.AddPlayer("human1", "Alice", 1000)
	g.AddBotPlayer("bot1", "Bot 1")

	// Remove the human — bots should be cleared
	g.RemovePlayer("human1")

	if g.PlayerCount() != 0 {
		t.Errorf("player count = %d, want 0 (bots should be removed when no humans)", g.PlayerCount())
	}
}

// === Test: Disconnected player removed after hand ===
func TestSimulation_DisconnectedPlayerCleanup(t *testing.T) {
	g, _ := newTestGame(10)
	g.AddPlayer("p1", "Alice", 1000)
	g.AddPlayer("p2", "Bob", 1000)
	g.SetReady("p1")
	g.SetReady("p2")

	// Mark p1 as disconnected
	g.SetDisconnected("p1", true)

	// Play out the hand (both should auto-act via timeout or manual)
	// For testing, just fold p1's turns and play normally
	for i := 0; i < 30; i++ {
		phase := getPhase(g)
		if phase == PhaseShowdown || phase == PhaseWaiting {
			break
		}
		cur := getCurrentPlayer(g)
		if cur == "" {
			break
		}
		g.mu.Lock()
		p := g.Players[g.CurrentIdx]
		callAmt := g.RoundBet - p.Bet
		g.mu.Unlock()

		if cur == "p1" {
			g.HandleAction(cur, "fold", 0)
		} else if callAmt > 0 {
			g.HandleAction(cur, "call", 0)
		} else {
			g.HandleAction(cur, "check", 0)
		}
	}

	// Wait for post-hand cleanup
	time.Sleep(5 * time.Second)

	// p1 was disconnected, should be removed
	g.mu.Lock()
	var foundP1 bool
	for _, p := range g.Players {
		if p.ID == "p1" {
			foundP1 = true
		}
	}
	playerCount := len(g.Players)
	g.mu.Unlock()

	if foundP1 {
		t.Error("disconnected player p1 should be removed after hand ends")
	}
	// Only p2 should remain (or 0 if p2 is also gone for some reason)
	if playerCount > 1 {
		t.Errorf("player count = %d, want 0 or 1", playerCount)
	}
}

// === Test: Multiple hands in sequence ===
func TestSimulation_MultipleHands(t *testing.T) {
	g, _ := newTestGame(10)
	g.AddPlayer("p1", "Alice", 1000)
	g.AddPlayer("p2", "Bob", 1000)

	for hand := 0; hand < 3; hand++ {
		g.SetReady("p1")
		g.SetReady("p2")

		phase := getPhase(g)
		if phase == PhaseWaiting {
			t.Logf("hand %d: still waiting after ready", hand)
			continue
		}

		// Play out the hand
		playUntilPhase(t, g, PhaseShowdown, 20)

		// Wait for cleanup
		time.Sleep(5 * time.Second)

		phase = getPhase(g)
		if phase != PhaseWaiting {
			t.Errorf("hand %d: expected waiting after cleanup, got %s", hand, phase)
		}

		// Verify chip conservation
		g.mu.Lock()
		total := 0
		for _, p := range g.Players {
			total += p.Chips
		}
		g.mu.Unlock()
		if total != 2000 {
			t.Errorf("hand %d: total chips = %d, want 2000", hand, total)
		}
	}
}

// === Test: Raise and re-raise ===
func TestSimulation_RaiseReRaise(t *testing.T) {
	g, _ := newTestGame(10)
	g.AddPlayer("p1", "Alice", 1000)
	g.AddPlayer("p2", "Bob", 1000)
	g.SetReady("p1")
	g.SetReady("p2")

	// p1 (SB/dealer in heads-up) raises
	cur := getCurrentPlayer(g)
	g.HandleAction(cur, "raise", 60)

	// p2 re-raises
	cur = getCurrentPlayer(g)
	err := g.HandleAction(cur, "raise", 120)
	if err != nil {
		t.Fatalf("re-raise error: %v", err)
	}

	// p1 calls
	cur = getCurrentPlayer(g)
	g.HandleAction(cur, "call", 0)

	phase := getPhase(g)
	if phase == PhasePreFlop {
		t.Error("should have advanced past preflop after call")
	}

	// Verify pot size (each put in 120)
	g.mu.Lock()
	pot := g.Pot
	g.mu.Unlock()
	if pot != 240 {
		t.Errorf("pot = %d, want 240", pot)
	}
}

// === Test: 6-player game completes without panic ===
func TestSimulation_SixPlayerFullGame(t *testing.T) {
	g, _ := newTestGame(5)
	for i := 1; i <= 6; i++ {
		g.AddPlayer(fmt.Sprintf("p%d", i), fmt.Sprintf("Player%d", i), 1000)
	}
	for i := 1; i <= 6; i++ {
		g.SetReady(fmt.Sprintf("p%d", i))
	}

	if getPhase(g) != PhasePreFlop {
		t.Fatal("6-player game should start")
	}

	// Play out with mix of actions
	actionCount := 0
	for actionCount < 60 {
		phase := getPhase(g)
		if phase == PhaseShowdown || phase == PhaseWaiting {
			break
		}
		cur := getCurrentPlayer(g)
		if cur == "" {
			break
		}

		g.mu.Lock()
		p := g.Players[g.CurrentIdx]
		callAmt := g.RoundBet - p.Bet
		g.mu.Unlock()

		// Mix of actions
		switch actionCount % 5 {
		case 0:
			if callAmt > 0 {
				g.HandleAction(cur, "call", 0)
			} else {
				g.HandleAction(cur, "check", 0)
			}
		case 1:
			g.HandleAction(cur, "fold", 0)
		default:
			if callAmt > 0 {
				g.HandleAction(cur, "call", 0)
			} else {
				g.HandleAction(cur, "check", 0)
			}
		}
		actionCount++
	}

	phase := getPhase(g)
	if phase != PhaseShowdown && phase != PhaseWaiting {
		t.Errorf("6-player game ended in unexpected phase: %s", phase)
	}

	// Chip conservation
	time.Sleep(5 * time.Second)
	g.mu.Lock()
	total := 0
	for _, p := range g.Players {
		total += p.Chips
	}
	g.mu.Unlock()
	if total != 6000 {
		t.Errorf("total chips = %d, want 6000", total)
	}
}

// === Test: Bot game completes without hanging ===
func TestSimulation_BotGameDoesNotHang(t *testing.T) {
	g, _ := newTestGame(10)
	g.AddPlayer("human1", "Alice", 1000)
	g.AddBotPlayer("bot1", "Bot 1")
	g.AddBotPlayer("bot2", "Bot 2")

	g.SetReady("human1")
	// Bots are auto-ready, game should start

	if getPhase(g) != PhasePreFlop {
		t.Fatal("game with human + bots should start")
	}

	// The human plays, bots act automatically via AfterFunc
	done := make(chan bool, 1)
	go func() {
		for i := 0; i < 200; i++ {
			phase := getPhase(g)
			if phase == PhaseShowdown || phase == PhaseWaiting {
				done <- true
				return
			}
			cur := getCurrentPlayer(g)
			if cur == "human1" {
				g.mu.Lock()
				if g.CurrentIdx < len(g.Players) && g.Players[g.CurrentIdx].ID == "human1" {
					callAmt := g.RoundBet - g.Players[g.CurrentIdx].Bet
					g.mu.Unlock()
					if callAmt > 0 {
						g.HandleAction("human1", "call", 0)
					} else {
						g.HandleAction("human1", "check", 0)
					}
				} else {
					g.mu.Unlock()
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		done <- false
	}()

	select {
	case ok := <-done:
		if !ok {
			t.Error("game with bots did not reach showdown within timeout")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("game with bots hung — timeout after 30s")
	}
}

// === Test: Call that makes player exactly 0 chips ===
func TestSimulation_ExactChipsCall(t *testing.T) {
	g, _ := newTestGame(10)
	g.AddPlayer("p1", "Alice", 100)
	g.AddPlayer("p2", "Bob", 1000)
	g.SetReady("p1")
	g.SetReady("p2")

	// Force an all-in scenario
	cur := getCurrentPlayer(g)
	g.HandleAction(cur, "allin", 0)

	cur = getCurrentPlayer(g)
	g.HandleAction(cur, "call", 0)

	// Should proceed to showdown
	time.Sleep(1 * time.Second)
	phase := getPhase(g)
	if phase != PhaseShowdown {
		t.Errorf("expected showdown, got %s", phase)
	}
}

// === Test: RemovePlayer mid-game doesn't crash ===
func TestSimulation_RemovePlayerMidGame(t *testing.T) {
	g, _ := newTestGame(10)
	g.AddPlayer("p1", "Alice", 1000)
	g.AddPlayer("p2", "Bob", 1000)
	g.AddPlayer("p3", "Charlie", 1000)
	g.SetReady("p1")
	g.SetReady("p2")
	g.SetReady("p3")

	if getPhase(g) != PhasePreFlop {
		t.Fatal("game should start")
	}

	// Remove a non-current player mid-game
	g.mu.Lock()
	curID := g.Players[g.CurrentIdx].ID
	g.mu.Unlock()

	// Find a non-current player to remove
	removeID := "p1"
	if curID == "p1" {
		removeID = "p2"
	}

	// This should not panic
	g.RemovePlayer(removeID)

	// Game should still be playable
	phase := getPhase(g)
	if phase == PhaseWaiting {
		// Game may have ended if removal caused only 1 non-folded player
		return
	}

	// Try to continue playing
	playUntilPhase(t, g, PhaseShowdown, 20)
}

// === Test: Verify DealerIdx wraps correctly after player removal ===
func TestSimulation_DealerIdxAfterRemoval(t *testing.T) {
	g, _ := newTestGame(10)
	g.AddPlayer("p1", "Alice", 1000)
	g.AddPlayer("p2", "Bob", 1000)
	g.AddPlayer("p3", "Charlie", 1000)

	// Play a hand
	g.SetReady("p1")
	g.SetReady("p2")
	g.SetReady("p3")

	// Quick fold to end the hand
	cur := getCurrentPlayer(g)
	g.HandleAction(cur, "fold", 0)
	cur = getCurrentPlayer(g)
	if cur != "" {
		g.HandleAction(cur, "fold", 0)
	}

	time.Sleep(5 * time.Second)

	// DealerIdx should be valid
	g.mu.Lock()
	dealerIdx := g.DealerIdx
	numPlayers := len(g.Players)
	g.mu.Unlock()

	if numPlayers > 0 && dealerIdx >= numPlayers {
		t.Errorf("DealerIdx = %d, but only %d players (out of bounds!)", dealerIdx, numPlayers)
	}
}

// === Test: Check that player names appear correctly in results ===
func TestSimulation_ShowdownResultNames(t *testing.T) {
	g, ml := newTestGame(10)
	g.AddPlayer("p1", "Alice", 1000)
	g.AddPlayer("p2", "Bob", 1000)
	g.SetReady("p1")
	g.SetReady("p2")

	playUntilPhase(t, g, PhaseShowdown, 20)

	ml.mu.Lock()
	defer ml.mu.Unlock()
	for _, m := range ml.messages {
		if m.Type == "showdown" {
			payload, ok := m.Payload.(map[string]interface{})
			if !ok {
				continue
			}
			results, ok := payload["results"].([]ShowdownResult)
			if !ok {
				continue
			}
			names := []string{}
			for _, r := range results {
				names = append(names, r.Name)
				if r.HandRank == "" {
					t.Error("showdown result has empty handRank")
				}
			}
			if len(names) != 2 {
				t.Errorf("expected 2 results, got %d", len(names))
			}
			namesStr := strings.Join(names, ",")
			if !strings.Contains(namesStr, "Alice") || !strings.Contains(namesStr, "Bob") {
				t.Errorf("expected Alice and Bob in results, got: %s", namesStr)
			}
		}
	}
}
