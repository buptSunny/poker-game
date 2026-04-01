package game

import (
	"testing"
)

// helpers

func noop(_ Message)            {}
func noopTo(_ string, _ Message) {}

func newGame() *Game {
	return NewGame("test", 10, noop, noopTo)
}

func addPlayers(t *testing.T, g *Game, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := g.AddPlayer(n, n, 1000); err != nil {
			t.Fatalf("AddPlayer(%q): %v", n, err)
		}
	}
}

// ===== AddPlayer =====

func TestAddPlayer_Basic(t *testing.T) {
	g := newGame()
	if err := g.AddPlayer("p1", "Alice", 1000); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g.PlayerCount() != 1 {
		t.Errorf("player count = %d, want 1", g.PlayerCount())
	}
}

func TestAddPlayer_DefaultChips(t *testing.T) {
	g := newGame()
	g.AddPlayer("p1", "Alice", 0) // 0 chips → should default to 1000
	g.mu.Lock()
	chips := g.Players[0].Chips
	g.mu.Unlock()
	if chips != 1000 {
		t.Errorf("chips = %d, want 1000 (default)", chips)
	}
}

func TestAddPlayer_Duplicate(t *testing.T) {
	g := newGame()
	g.AddPlayer("p1", "Alice", 1000)
	err := g.AddPlayer("p1", "Alice", 1000)
	if err == nil {
		t.Error("expected error for duplicate player")
	}
}

func TestAddPlayer_GameInProgress(t *testing.T) {
	g := newGame()
	addPlayers(t, g, "p1", "p2")
	g.SetReady("p1")
	g.SetReady("p2")
	// game should now be running (preflop)
	if g.GetPhase() == PhaseWaiting {
		t.Skip("game did not start")
	}
	err := g.AddPlayer("p3", "Charlie", 1000)
	if err == nil {
		t.Error("expected error adding player to game in progress")
	}
}

// ===== Ready / Start =====

func TestMaybeStart_NeedsAtLeastTwo(t *testing.T) {
	g := newGame()
	g.AddPlayer("p1", "Alice", 1000)
	g.SetReady("p1")
	if g.GetPhase() != PhaseWaiting {
		t.Error("game should not start with only 1 player")
	}
}

func TestMaybeStart_StartsWhenAllReady(t *testing.T) {
	g := newGame()
	addPlayers(t, g, "p1", "p2")
	g.SetReady("p1")
	if g.GetPhase() != PhaseWaiting {
		t.Error("game should not start until all players are ready")
	}
	g.SetReady("p2")
	if g.GetPhase() == PhaseWaiting {
		t.Error("game should have started after all players are ready")
	}
}

func TestMaybeStart_NotReadyPlayerBlocks(t *testing.T) {
	g := newGame()
	addPlayers(t, g, "p1", "p2", "p3")
	g.SetReady("p1")
	g.SetReady("p2")
	// p3 not ready
	if g.GetPhase() != PhaseWaiting {
		t.Error("game should not start while p3 is not ready")
	}
}

// ===== GetPlayerChips =====

func TestGetPlayerChips(t *testing.T) {
	g := newGame()
	g.AddPlayer("p1", "Alice", 1500)
	chips := g.GetPlayerChips("p1")
	if chips != 1500 {
		t.Errorf("chips = %d, want 1500", chips)
	}
}

func TestGetPlayerChips_NotFound(t *testing.T) {
	g := newGame()
	if g.GetPlayerChips("nonexistent") != -1 {
		t.Error("expected -1 for unknown player")
	}
}

// ===== RemovePlayer =====

func TestRemovePlayer(t *testing.T) {
	g := newGame()
	addPlayers(t, g, "p1", "p2")
	g.RemovePlayer("p1")
	if g.PlayerCount() != 1 {
		t.Errorf("player count = %d, want 1", g.PlayerCount())
	}
	if g.GetPlayerChips("p1") != -1 {
		t.Error("removed player should not be found")
	}
}

// ===== HandleAction =====

func TestHandleAction_NotYourTurn(t *testing.T) {
	g := newGame()
	addPlayers(t, g, "p1", "p2")
	g.SetReady("p1")
	g.SetReady("p2")

	g.mu.Lock()
	currentPlayer := g.Players[g.CurrentIdx].ID
	g.mu.Unlock()

	// find the OTHER player
	other := "p1"
	if currentPlayer == "p1" {
		other = "p2"
	}

	err := g.HandleAction(other, "fold", 0)
	if err == nil {
		t.Error("expected error when acting out of turn")
	}
}

func TestHandleAction_Fold(t *testing.T) {
	g := newGame()
	addPlayers(t, g, "p1", "p2")
	g.SetReady("p1")
	g.SetReady("p2")

	g.mu.Lock()
	currentID := g.Players[g.CurrentIdx].ID
	g.mu.Unlock()

	if err := g.HandleAction(currentID, "fold", 0); err != nil {
		t.Fatalf("fold error: %v", err)
	}
}

func TestHandleAction_Check_WhenMustCall(t *testing.T) {
	g := newGame()
	addPlayers(t, g, "p1", "p2")
	g.SetReady("p1")
	g.SetReady("p2")

	g.mu.Lock()
	currentID := g.Players[g.CurrentIdx].ID
	roundBet := g.RoundBet
	currentBet := g.Players[g.CurrentIdx].Bet
	g.mu.Unlock()

	if roundBet > currentBet {
		// must call, check should fail
		err := g.HandleAction(currentID, "check", 0)
		if err == nil {
			t.Error("expected error: cannot check when must call")
		}
	}
}

func TestHandleAction_Call(t *testing.T) {
	g := newGame()
	addPlayers(t, g, "p1", "p2")
	g.SetReady("p1")
	g.SetReady("p2")

	g.mu.Lock()
	currentID := g.Players[g.CurrentIdx].ID
	callAmt := g.RoundBet - g.Players[g.CurrentIdx].Bet
	g.mu.Unlock()

	if callAmt > 0 {
		if err := g.HandleAction(currentID, "call", 0); err != nil {
			t.Fatalf("call error: %v", err)
		}
	}
}

// ===== BB Option (大盲特权) =====

func TestBBOption_BBCanActWhenNobodyRaised(t *testing.T) {
	g := newGame()
	addPlayers(t, g, "p1", "p2", "p3")
	g.SetReady("p1")
	g.SetReady("p2")
	g.SetReady("p3")
	// Game is now in preflop. p3=BB, p1=SB, p0=dealer
	// preflop order: p1(after BB) → p2 → p3(BB has option)

	// Figure out BB player
	g.mu.Lock()
	n := len(g.Players)
	bbIdx := (g.DealerIdx + 2) % n
	bbID := g.Players[bbIdx].ID
	g.mu.Unlock()

	// Have everyone call until it's BB's turn
	for i := 0; i < 10; i++ {
		g.mu.Lock()
		phase := g.Phase
		curID := g.Players[g.CurrentIdx].ID
		g.mu.Unlock()

		if phase != PhasePreFlop {
			break
		}
		if curID == bbID {
			// BB should be able to check (no raise happened)
			if err := g.HandleAction(bbID, "check", 0); err != nil {
				t.Errorf("BB should be able to check when nobody raised, got: %v", err)
			}
			return
		}
		// call for others
		g.HandleAction(curID, "call", 0)
	}
	t.Error("BB never got a chance to act")
}

func TestHeadsUp_DealerIsSB(t *testing.T) {
	g := newGame()
	addPlayers(t, g, "p1", "p2")
	g.SetReady("p1")
	g.SetReady("p2")

	g.mu.Lock()
	defer g.mu.Unlock()
	dealerIdx := g.DealerIdx
	// In heads-up, dealer = SB (smaller bet), other = BB (larger bet)
	sbPlayer := g.Players[dealerIdx]
	bbPlayer := g.Players[(dealerIdx+1)%2]
	// SB should have bet SmallBlind, BB should have bet BigBlind
	if sbPlayer.TotalBet != g.SmallBlind {
		t.Errorf("dealer (SB) TotalBet = %d, want %d", sbPlayer.TotalBet, g.SmallBlind)
	}
	if bbPlayer.TotalBet != g.BigBlind {
		t.Errorf("non-dealer (BB) TotalBet = %d, want %d", bbPlayer.TotalBet, g.BigBlind)
	}
	// In heads-up preflop, dealer (SB) acts first
	if g.CurrentIdx != dealerIdx {
		t.Errorf("CurrentIdx = %d, want dealer %d (SB acts first in heads-up)", g.CurrentIdx, dealerIdx)
	}
}

func TestHandEval_RoyalFlush(t *testing.T) {
	cards := []Card{
		{Rank: 14, Suit: "s"}, {Rank: 13, Suit: "s"}, {Rank: 12, Suit: "s"},
		{Rank: 11, Suit: "s"}, {Rank: 10, Suit: "s"},
		{Rank: 2, Suit: "h"}, {Rank: 3, Suit: "d"},
	}
	r := Best5(cards)
	if r.Rank != RoyalFlush {
		t.Errorf("rank = %d (%s), want RoyalFlush", r.Rank, r.RankName)
	}
}

func TestHandEval_StraightFlush(t *testing.T) {
	cards := []Card{
		{Rank: 9, Suit: "h"}, {Rank: 8, Suit: "h"}, {Rank: 7, Suit: "h"},
		{Rank: 6, Suit: "h"}, {Rank: 5, Suit: "h"},
		{Rank: 2, Suit: "s"}, {Rank: 3, Suit: "d"},
	}
	r := Best5(cards)
	if r.Rank != StraightFlush {
		t.Errorf("rank = %d (%s), want StraightFlush", r.Rank, r.RankName)
	}
}

func TestHandEval_FourOfAKind(t *testing.T) {
	cards := []Card{
		{Rank: 7, Suit: "s"}, {Rank: 7, Suit: "h"}, {Rank: 7, Suit: "d"}, {Rank: 7, Suit: "c"},
		{Rank: 2, Suit: "s"}, {Rank: 3, Suit: "h"}, {Rank: 4, Suit: "d"},
	}
	r := Best5(cards)
	if r.Rank != FourOfAKind {
		t.Errorf("rank = %d (%s), want FourOfAKind", r.Rank, r.RankName)
	}
}

func TestHandEval_FullHouse(t *testing.T) {
	cards := []Card{
		{Rank: 7, Suit: "s"}, {Rank: 7, Suit: "h"}, {Rank: 7, Suit: "d"},
		{Rank: 3, Suit: "s"}, {Rank: 3, Suit: "h"},
		{Rank: 9, Suit: "c"}, {Rank: 2, Suit: "d"},
	}
	r := Best5(cards)
	if r.Rank != FullHouse {
		t.Errorf("rank = %d (%s), want FullHouse", r.Rank, r.RankName)
	}
}

func TestHandEval_Flush(t *testing.T) {
	cards := []Card{
		{Rank: 14, Suit: "h"}, {Rank: 10, Suit: "h"}, {Rank: 8, Suit: "h"},
		{Rank: 5, Suit: "h"}, {Rank: 3, Suit: "h"},
		{Rank: 2, Suit: "s"}, {Rank: 4, Suit: "d"},
	}
	r := Best5(cards)
	if r.Rank != Flush {
		t.Errorf("rank = %d (%s), want Flush", r.Rank, r.RankName)
	}
}

func TestHandEval_Straight(t *testing.T) {
	cards := []Card{
		{Rank: 9, Suit: "s"}, {Rank: 8, Suit: "h"}, {Rank: 7, Suit: "d"},
		{Rank: 6, Suit: "c"}, {Rank: 5, Suit: "s"},
		{Rank: 2, Suit: "h"}, {Rank: 4, Suit: "d"},
	}
	r := Best5(cards)
	if r.Rank != Straight {
		t.Errorf("rank = %d (%s), want Straight", r.Rank, r.RankName)
	}
}

func TestHandEval_WheelStraight(t *testing.T) {
	// A-2-3-4-5 straight (wheel)
	cards := []Card{
		{Rank: 14, Suit: "s"}, {Rank: 2, Suit: "h"}, {Rank: 3, Suit: "d"},
		{Rank: 4, Suit: "c"}, {Rank: 5, Suit: "s"},
		{Rank: 9, Suit: "h"}, {Rank: 7, Suit: "d"},
	}
	r := Best5(cards)
	if r.Rank != Straight {
		t.Errorf("rank = %d (%s), want Straight (wheel)", r.Rank, r.RankName)
	}
}

func TestHandEval_ThreeOfAKind(t *testing.T) {
	cards := []Card{
		{Rank: 7, Suit: "s"}, {Rank: 7, Suit: "h"}, {Rank: 7, Suit: "d"},
		{Rank: 2, Suit: "c"}, {Rank: 3, Suit: "s"},
		{Rank: 9, Suit: "h"}, {Rank: 4, Suit: "d"},
	}
	r := Best5(cards)
	if r.Rank != ThreeOfAKind {
		t.Errorf("rank = %d (%s), want ThreeOfAKind", r.Rank, r.RankName)
	}
}

func TestHandEval_TwoPair(t *testing.T) {
	cards := []Card{
		{Rank: 7, Suit: "s"}, {Rank: 7, Suit: "h"},
		{Rank: 3, Suit: "d"}, {Rank: 3, Suit: "c"},
		{Rank: 9, Suit: "s"}, {Rank: 2, Suit: "h"}, {Rank: 4, Suit: "d"},
	}
	r := Best5(cards)
	if r.Rank != TwoPair {
		t.Errorf("rank = %d (%s), want TwoPair", r.Rank, r.RankName)
	}
}

func TestHandEval_OnePair(t *testing.T) {
	cards := []Card{
		{Rank: 7, Suit: "s"}, {Rank: 7, Suit: "h"},
		{Rank: 2, Suit: "d"}, {Rank: 3, Suit: "c"},
		{Rank: 9, Suit: "s"}, {Rank: 4, Suit: "h"}, {Rank: 6, Suit: "d"},
	}
	r := Best5(cards)
	if r.Rank != OnePair {
		t.Errorf("rank = %d (%s), want OnePair", r.Rank, r.RankName)
	}
}

func TestHandEval_HighCard(t *testing.T) {
	cards := []Card{
		{Rank: 14, Suit: "s"}, {Rank: 10, Suit: "h"}, {Rank: 7, Suit: "d"},
		{Rank: 5, Suit: "c"}, {Rank: 3, Suit: "s"},
		{Rank: 2, Suit: "h"}, {Rank: 9, Suit: "d"},
	}
	r := Best5(cards)
	if r.Rank != HighCard {
		t.Errorf("rank = %d (%s), want HighCard", r.Rank, r.RankName)
	}
}

func TestHandEval_BetterHandWins(t *testing.T) {
	community := []Card{
		{Rank: 7, Suit: "s"}, {Rank: 8, Suit: "h"}, {Rank: 9, Suit: "d"},
		{Rank: 2, Suit: "c"}, {Rank: 3, Suit: "s"},
	}
	// p1: straight (5-9)
	p1 := Best5(append([]Card{{Rank: 5, Suit: "h"}, {Rank: 6, Suit: "d"}}, community...))
	// p2: one pair
	p2 := Best5(append([]Card{{Rank: 2, Suit: "h"}, {Rank: 4, Suit: "d"}}, community...))

	if p1.Rank <= p2.Rank {
		t.Errorf("straight (%d) should beat pair (%d)", p1.Rank, p2.Rank)
	}
}
