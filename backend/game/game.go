package game

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"
)

type GamePhase string

const (
	PhaseWaiting  GamePhase = "waiting"
	PhasePreFlop  GamePhase = "preflop"
	PhaseFlop     GamePhase = "flop"
	PhaseTurn     GamePhase = "turn"
	PhaseRiver    GamePhase = "river"
	PhaseShowdown GamePhase = "showdown"
)

type PlayerState struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Chips        int    `json:"chips"`
	Bet          int    `json:"bet"`
	TotalBet     int    `json:"totalBet"`
	ChipsBefore  int    `json:"-"`
	Disconnected bool   `json:"disconnected"`
	Folded       bool   `json:"folded"`
	AllIn        bool   `json:"allIn"`
	HasActed     bool   `json:"hasActed"`
	CanRaise     bool   `json:"-"`
	Hand         []Card `json:"hand,omitempty"`
	IsDealer     bool   `json:"isDealer"`
	IsReady      bool   `json:"isReady"`
	SeatIdx      int    `json:"seatIdx"`
	IsBot        bool   `json:"isBot"`
}

type PublicPlayerState struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Chips        int    `json:"chips"`
	Bet          int    `json:"bet"`
	TotalBet     int    `json:"totalBet"`
	Disconnected bool   `json:"disconnected"`
	Folded       bool   `json:"folded"`
	AllIn        bool   `json:"allIn"`
	IsDealer     bool   `json:"isDealer"`
	IsReady      bool   `json:"isReady"`
	SeatIdx      int    `json:"seatIdx"`
	CardCount    int    `json:"cardCount"`
	Hand         []Card `json:"hand,omitempty"` // revealed at showdown
	IsBot        bool   `json:"isBot"`
}

type Game struct {
	mu          sync.Mutex
	RoomID      string
	Phase       GamePhase
	Players     []*PlayerState
	Community   []Card
	Deck        []Card
	Pot         int
	SidePots    []SidePot
	CurrentIdx  int // whose turn
	DealerIdx   int
	SmallBlind  int
	BigBlind    int
	MinRaise    int
	LastRaise   int
	RoundBet    int // highest bet in current round
	Broadcast   func(msg Message)
	SendTo      func(playerID string, msg Message)
	// Called after each hand ends with all players' updated chip counts (skip bots).
	SaveChips func(playerID string, chips int)
	// Called after each hand ends with results for history/stats recording.
	OnHandEnd func(roomID, community string, pot int, results []ShowdownResult)
	OwnerID   string // set by ws.go when first human player joins
	turnTimer   *time.Timer
}

type SidePot struct {
	Amount  int      `json:"amount"`
	Players []string `json:"players"`
}

type Message struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

type GameStatePayload struct {
	Phase      GamePhase           `json:"phase"`
	Community  []Card              `json:"community"`
	Pot        int                 `json:"pot"`
	Players    []PublicPlayerState `json:"players"`
	CurrentIdx int                 `json:"currentIdx"`
	DealerIdx  int                 `json:"dealerIdx"`
	SmallBlind int                 `json:"smallBlind"`
	BigBlind   int                 `json:"bigBlind"`
	RoundBet   int                 `json:"roundBet"`
	OwnerID    string              `json:"ownerId"`
}

func NewGame(roomID string, smallBlind int, broadcast func(Message), sendTo func(string, Message)) *Game {
	return &Game{
		RoomID:     roomID,
		Phase:      PhaseWaiting,
		SmallBlind: smallBlind,
		BigBlind:   smallBlind * 2,
		Broadcast:  broadcast,
		SendTo:     sendTo,
	}
}

func (g *Game) AddPlayer(id, name string, chips int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.Players) >= 9 {
		return fmt.Errorf("room full")
	}
	if g.Phase != PhaseWaiting {
		return fmt.Errorf("game in progress")
	}
	for _, p := range g.Players {
		if p.ID == id {
			return fmt.Errorf("already in room")
		}
	}
	if chips <= 0 {
		chips = 1000
	}
	g.Players = append(g.Players, &PlayerState{
		ID:      id,
		Name:    name,
		Chips:   chips,
		SeatIdx: len(g.Players),
	})
	g.broadcastState()
	return nil
}

// GetPlayerChips returns the current chip count of a player, or -1 if not found.
func (g *Game) GetPlayerChips(id string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, p := range g.Players {
		if p.ID == id {
			return p.Chips
		}
	}
	return -1
}

func (g *Game) AddBotPlayer(id, name string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.Players) >= 9 {
		return fmt.Errorf("room full")
	}
	if g.Phase != PhaseWaiting {
		return fmt.Errorf("game in progress")
	}
	for _, p := range g.Players {
		if p.ID == id {
			return fmt.Errorf("bot already in room")
		}
	}
	g.Players = append(g.Players, &PlayerState{
		ID:      id,
		Name:    name,
		Chips:   1000,
		SeatIdx: len(g.Players),
		IsBot:   true,
		IsReady: true, // bots are always ready
	})
	g.broadcastState()
	// check if all players are now ready
	g.maybeStart()
	return nil
}

// maybeStart starts a hand if all players >= 2 and all ready. Must be called with mu held.
func (g *Game) maybeStart() {
	if len(g.Players) < 2 {
		return
	}
	for _, p := range g.Players {
		if !p.IsReady {
			return
		}
	}
	g.startHand()
}

func (g *Game) RemovePlayer(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i, p := range g.Players {
		if p.ID == id {
			g.Players = append(g.Players[:i], g.Players[i+1:]...)
			// re-index seats
			for j, pp := range g.Players {
				pp.SeatIdx = j
			}
			break
		}
	}

	// If no human players remain, remove all bots
	hasHuman := false
	for _, p := range g.Players {
		if !p.IsBot {
			hasHuman = true
			break
		}
	}
	if !hasHuman {
		g.Players = nil
	}

	if g.Phase != PhaseWaiting {
		// mark as folded so game can continue
		g.checkAdvance()
	}
	g.broadcastState()
}

func (g *Game) SetReady(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, p := range g.Players {
		if p.ID == id {
			p.IsReady = !p.IsReady
		}
	}
	g.broadcastState()
	g.maybeStart()
}

func (g *Game) startHand() {
	// reset player state
	for _, p := range g.Players {
		p.ChipsBefore = p.Chips // snapshot before any bets this hand
		p.Hand = nil
		p.Bet = 0
		p.TotalBet = 0
		p.Folded = false
		p.AllIn = false
		p.IsDealer = false
		p.IsReady = false
		p.HasActed = false
		p.CanRaise = true
	}
	g.Community = nil
	g.Pot = 0
	g.SidePots = nil

	// advance dealer
	n := len(g.Players)
	g.DealerIdx = (g.DealerIdx + 1) % n
	g.Players[g.DealerIdx].IsDealer = true

	// deal cards
	deck := newDeck()
	shuffle(deck)
	g.Deck = deck
	for i := 0; i < 2; i++ {
		for _, p := range g.Players {
			p.Hand = append(p.Hand, g.Deck[0])
			g.Deck = g.Deck[1:]
		}
	}

	// post blinds — heads-up rule: dealer = SB and acts first preflop
	var sbIdx, bbIdx int
	if n == 2 {
		sbIdx = g.DealerIdx
		bbIdx = (g.DealerIdx + 1) % n
	} else {
		sbIdx = (g.DealerIdx + 1) % n
		bbIdx = (g.DealerIdx + 2) % n
	}
	g.postBlind(sbIdx, g.SmallBlind)
	g.postBlind(bbIdx, g.BigBlind)
	g.RoundBet = g.BigBlind
	g.MinRaise = g.BigBlind
	g.LastRaise = g.BigBlind

	// preflop first to act: left of BB (heads-up: dealer/SB acts first)
	if n == 2 {
		g.CurrentIdx = g.DealerIdx
	} else {
		g.CurrentIdx = (bbIdx + 1) % n
	}
	g.Phase = PhasePreFlop

	g.broadcastState()
	g.sendHands()
	g.broadcastYourTurn()
}

func (g *Game) postBlind(idx, amount int) {
	p := g.Players[idx]
	if p.Chips <= amount {
		amount = p.Chips
		p.AllIn = true
	}
	p.Chips -= amount
	p.Bet += amount
	p.TotalBet += amount
	g.Pot += amount
}

func (g *Game) HandleAction(playerID, action string, amount int) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.Phase == PhaseWaiting || g.Phase == PhaseShowdown {
		return fmt.Errorf("not in game")
	}
	p := g.Players[g.CurrentIdx]
	if p.ID != playerID {
		return fmt.Errorf("not your turn")
	}

	if g.turnTimer != nil {
		g.turnTimer.Stop()
	}

	if err := g.applyAction(p, action, amount); err != nil {
		return err
	}
	g.broadcastState()
	g.checkAdvance()
	return nil
}

// reopenRaise updates CanRaise for active players.
// full=true: everyone (re-open after a real raise); false: only unacted players (sub-raise all-in).
func (g *Game) reopenRaise(full bool) {
	for _, pp := range g.Players {
		if pp.Folded || pp.AllIn {
			continue
		}
		if full || !pp.HasActed {
			pp.CanRaise = true
		}
	}
}

// applyAction executes a poker action for player p. Must be called with mu held.
func (g *Game) applyAction(p *PlayerState, action string, amount int) error {
	callAmount := g.RoundBet - p.Bet

	switch action {
	case "fold":
		p.Folded = true
	case "check":
		if callAmount > 0 {
			return fmt.Errorf("cannot check, must call %d", callAmount)
		}
	case "call":
		if callAmount <= 0 {
			return fmt.Errorf("nothing to call")
		}
		actual := callAmount
		if actual >= p.Chips {
			actual = p.Chips
			p.AllIn = true
		}
		p.Chips -= actual
		p.Bet += actual
		p.TotalBet += actual
		g.Pot += actual
	case "raise", "bet":
		prevMinRaise := g.MinRaise
		prevRoundBet := g.RoundBet
		if amount < g.RoundBet+g.MinRaise {
			amount = g.RoundBet + g.MinRaise
		}
		raiseTotal := amount
		if raiseTotal >= p.Chips+p.Bet {
			raiseTotal = p.Chips + p.Bet
			p.AllIn = true
		}
		added := raiseTotal - p.Bet
		g.LastRaise = raiseTotal - prevRoundBet
		g.MinRaise = g.LastRaise
		g.RoundBet = raiseTotal
		p.Chips -= added
		p.Bet += added
		p.TotalBet += added
		g.Pot += added
		// only reopen raise rights for all if this is a full raise
		isFullRaise := g.LastRaise >= prevMinRaise
		g.reopenRaise(isFullRaise)
	case "allin":
		prevMinRaise := g.MinRaise
		added := p.Chips
		p.AllIn = true
		isFullRaise := false
		if p.Bet+added > g.RoundBet {
			raiseBy := p.Bet + added - g.RoundBet
			isFullRaise = raiseBy >= prevMinRaise
			g.LastRaise = raiseBy
			g.MinRaise = raiseBy
			g.RoundBet = p.Bet + added
		}
		p.Bet += added
		p.TotalBet += added
		g.Pot += added
		p.Chips = 0
		// full raise reopens for all; sub-raise only reopens for unacted players
		g.reopenRaise(isFullRaise)
	default:
		return fmt.Errorf("unknown action: %s", action)
	}

	p.HasActed = true
	return nil
}

func (g *Game) activePlayers() []*PlayerState {
	var active []*PlayerState
	for _, p := range g.Players {
		if !p.Folded && !p.AllIn {
			active = append(active, p)
		}
	}
	return active
}

func (g *Game) nonFolded() []*PlayerState {
	var active []*PlayerState
	for _, p := range g.Players {
		if !p.Folded {
			active = append(active, p)
		}
	}
	return active
}

func (g *Game) roundComplete() bool {
	for _, p := range g.Players {
		if p.Folded || p.AllIn {
			continue
		}
		// Player must have acted AND matched the current bet
		if !p.HasActed || p.Bet < g.RoundBet {
			return false
		}
	}
	return true
}

func (g *Game) checkAdvance() {
	nf := g.nonFolded()
	// only one left
	if len(nf) == 1 {
		g.awardPot(nf)
		return
	}
	// all remaining all-in or round complete
	if !g.roundComplete() {
		g.advanceTurn()
		return
	}
	// advance phase
	g.nextPhase()
}

func (g *Game) advanceTurn() {
	n := len(g.Players)
	for i := 1; i <= n; i++ {
		next := (g.CurrentIdx + i) % n
		p := g.Players[next]
		if !p.Folded && !p.AllIn {
			g.CurrentIdx = next
			g.broadcastState()
			g.broadcastYourTurn()
			return
		}
	}
	// everyone all-in or folded
	g.nextPhase()
}

func (g *Game) nextPhase() {
	// reset bets and acted flag for new betting round
	for _, p := range g.Players {
		p.Bet = 0
		p.HasActed = false
		p.CanRaise = !p.Folded && !p.AllIn // fresh raise rights for new street
	}
	g.RoundBet = 0
	g.MinRaise = g.BigBlind

	// find first active after dealer
	n := len(g.Players)
	startIdx := -1
	for i := 1; i <= n; i++ {
		idx := (g.DealerIdx + i) % n
		if !g.Players[idx].Folded && !g.Players[idx].AllIn {
			startIdx = idx
			break
		}
	}

	switch g.Phase {
	case PhasePreFlop:
		g.Phase = PhaseFlop
		for i := 0; i < 3; i++ {
			g.Community = append(g.Community, g.Deck[0])
			g.Deck = g.Deck[1:]
		}
	case PhaseFlop:
		g.Phase = PhaseTurn
		g.Community = append(g.Community, g.Deck[0])
		g.Deck = g.Deck[1:]
	case PhaseTurn:
		g.Phase = PhaseRiver
		g.Community = append(g.Community, g.Deck[0])
		g.Deck = g.Deck[1:]
	case PhaseRiver:
		g.Phase = PhaseShowdown
		g.broadcastState()
		g.doShowdown()
		return
	}

	g.broadcastState()

	if startIdx == -1 {
		// all-in runout, deal remaining community cards
		g.runoutToShowdown()
		return
	}
	g.CurrentIdx = startIdx
	g.broadcastYourTurn()
}

func (g *Game) runoutToShowdown() {
	for g.Phase != PhaseShowdown {
		switch g.Phase {
		case PhaseFlop:
			g.Phase = PhaseTurn
			g.Community = append(g.Community, g.Deck[0])
			g.Deck = g.Deck[1:]
		case PhaseTurn:
			g.Phase = PhaseRiver
			g.Community = append(g.Community, g.Deck[0])
			g.Deck = g.Deck[1:]
		case PhaseRiver:
			g.Phase = PhaseShowdown
		}
	}
	g.broadcastState()
	g.doShowdown()
}

type sidePot struct {
	Amount   int
	Players  []*PlayerState
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// computePots splits the pot into main pot + side pots based on all-in levels.
// allPlayers must include ALL players (folded and non-folded) so that folded
// players' chips are counted in the pot totals; only non-folded players are
// eligible to win any pot.
func computePots(allPlayers []*PlayerState) []sidePot {
	// collect unique all-in total-bet levels from non-folded all-in players
	levelSet := map[int]bool{}
	for _, p := range allPlayers {
		if p.AllIn && !p.Folded {
			levelSet[p.TotalBet] = true
		}
	}
	levels := make([]int, 0, len(levelSet))
	for l := range levelSet {
		levels = append(levels, l)
	}
	sort.Ints(levels)

	var pots []sidePot
	prev := 0
	for _, level := range levels {
		// amount: sum from ALL players (including folded) up to this level
		amount := 0
		for _, p := range allPlayers {
			amount += minInt(p.TotalBet, level) - minInt(p.TotalBet, prev)
		}
		// eligible: only non-folded players who contributed at this level
		var eligible []*PlayerState
		for _, p := range allPlayers {
			if !p.Folded && p.TotalBet >= level {
				eligible = append(eligible, p)
			}
		}
		if amount > 0 {
			pots = append(pots, sidePot{amount, eligible})
		}
		prev = level
	}
	// final pot: everything above the last all-in level (from ALL players)
	finalAmount := 0
	for _, p := range allPlayers {
		finalAmount += p.TotalBet - minInt(p.TotalBet, prev)
	}
	if finalAmount > 0 {
		var eligible []*PlayerState
		for _, p := range allPlayers {
			if !p.Folded && p.TotalBet > prev {
				eligible = append(eligible, p)
			}
		}
		if len(eligible) > 0 {
			pots = append(pots, sidePot{finalAmount, eligible})
		}
	}
	return pots
}

type ShowdownResult struct {
	PlayerID string `json:"playerId"`
	Name     string `json:"name"`
	Hand     []Card `json:"hand"`
	HandRank string `json:"handRank"`
	HandDesc string `json:"handDesc"` // detailed, e.g. "一对K (Q J 9踢脚)"
	Won      int    `json:"won"`
	Bet      int    `json:"bet"`
	Net      int    `json:"net"` // chips_after - chips_before
	IsWinner bool   `json:"isWinner"`
}

// PotDetail describes one pot (main or side) and who won it.
type PotDetail struct {
	Label    string   `json:"label"`    // e.g. "主池", "边池1"
	Amount   int      `json:"amount"`
	Winners  []string `json:"winners"`  // winner names
	WinnerIDs []string `json:"winnerIds"`
	HandRank string   `json:"handRank"` // winning hand description
	Eligible []string `json:"eligible"` // all eligible player names
	Reason   string   `json:"reason"`   // detailed explanation of why winner won
}

func (g *Game) doShowdown() {
	nf := g.nonFolded()
	type eval struct {
		p      *PlayerState
		result HandResult
	}
	evals := make([]eval, len(nf))
	for i, p := range nf {
		cards := append(p.Hand, g.Community...)
		evals[i] = eval{p: p, result: Best5(cards)}
	}

	// compute side pots — pass ALL players so folded chips are counted
	pots := computePots(g.Players)
	// fallback: no all-ins, single pot
	if len(pots) == 0 {
		pots = []sidePot{{Amount: g.Pot, Players: nf}}
	}

	wonMap := map[string]int{}
	var potDetails []PotDetail

	for potIdx, pot := range pots {
		// evaluate eligible players for this pot
		type potEval struct {
			p      *PlayerState
			result HandResult
		}
		var pe []potEval
		for _, e := range evals {
			for _, ep := range pot.Players {
				if ep.ID == e.p.ID {
					pe = append(pe, potEval{e.p, e.result})
					break
				}
			}
		}
		if len(pe) == 0 {
			continue
		}

		// find best among eligible
		best := pe[0].result
		for _, e := range pe[1:] {
			if e.result.Rank > best.Rank ||
				(e.result.Rank == best.Rank && compareTiebreak(e.result.Tiebreak, best.Tiebreak) > 0) {
				best = e.result
			}
		}
		var potWinners []*PlayerState
		for _, e := range pe {
			if e.result.Rank == best.Rank && compareTiebreak(e.result.Tiebreak, best.Tiebreak) == 0 {
				potWinners = append(potWinners, e.p)
			}
		}

		share := pot.Amount / len(potWinners)
		remainder := pot.Amount % len(potWinners)
		for _, w := range potWinners {
			w.Chips += share
			wonMap[w.ID] += share
		}
		if remainder > 0 {
			potWinners[0].Chips += remainder
			wonMap[potWinners[0].ID] += remainder
		}

		// Build pot detail for frontend explanation
		label := "主池"
		if potIdx > 0 {
			label = fmt.Sprintf("边池%d", potIdx)
		}
		winnerNames := make([]string, len(potWinners))
		winnerIDs := make([]string, len(potWinners))
		for i, w := range potWinners {
			winnerNames[i] = w.Name
			winnerIDs[i] = w.ID
		}
		eligibleNames := make([]string, len(pe))
		for i, e := range pe {
			eligibleNames[i] = e.p.Name
		}

		// Build detailed reason
		reason := ""
		if len(potWinners) > 1 {
			// Split pot
			reason = fmt.Sprintf("平分 — 均为%s", best.RankDesc)
		} else if len(pe) == 1 {
			// Only one eligible player
			reason = fmt.Sprintf("%s 为唯一参与者，自动获得", potWinners[0].Name)
		} else {
			// Winner explanation
			winnerEval := pe[0]
			for _, e := range pe {
				if e.p.ID == potWinners[0].ID {
					winnerEval = e
					break
				}
			}
			reason = fmt.Sprintf("%s 以 %s 胜出", potWinners[0].Name, winnerEval.result.RankDesc)
		}

		potDetails = append(potDetails, PotDetail{
			Label:     label,
			Amount:    pot.Amount,
			Winners:   winnerNames,
			WinnerIDs: winnerIDs,
			HandRank:  best.RankDesc,
			Eligible:  eligibleNames,
			Reason:    reason,
		})
	}

	// build results
	results := make([]ShowdownResult, 0, len(evals))
	for _, e := range evals {
		isWinner := wonMap[e.p.ID] > 0
		results = append(results, ShowdownResult{
			PlayerID: e.p.ID,
			Name:     e.p.Name,
			Hand:     e.p.Hand,
			HandRank: e.result.RankName,
			HandDesc: e.result.RankDesc,
			Won:      wonMap[e.p.ID],
			Bet:      e.p.TotalBet,
			Net:      e.p.Chips - e.p.ChipsBefore,
			IsWinner: isWinner,
		})
	}

	// also include folded players with 0
	for _, p := range g.Players {
		if p.Folded {
			results = append(results, ShowdownResult{
				PlayerID: p.ID,
				Name:     p.Name,
				HandRank: "弃牌",
				Bet:      p.TotalBet,
				Net:      p.Chips - p.ChipsBefore,
			})
		}
	}

	g.Broadcast(Message{Type: "showdown", Payload: map[string]interface{}{
		"results":   results,
		"community": g.Community,
		"pots":      potDetails,
	}})

	// snapshot for callbacks (captured before AfterFunc closure)
	pot := g.Pot
	communitySnap := communityJSON(g.Community)
	resultsSnap := results

	// schedule next hand after delay
	time.AfterFunc(4*time.Second, func() {
		g.mu.Lock()
		defer g.mu.Unlock()

		// fire callbacks before removing players
		if g.SaveChips != nil {
			for _, p := range g.Players {
				if !p.IsBot {
					g.SaveChips(p.ID, p.Chips)
				}
			}
		}
		if g.OnHandEnd != nil {
			g.OnHandEnd(g.RoomID, communitySnap, pot, resultsSnap)
		}

		g.postHandCleanup()
	})
}

func (g *Game) awardPot(winners []*PlayerState) {
	share := g.Pot / len(winners)
	remainder := g.Pot % len(winners)
	for _, w := range winners {
		w.Chips += share
	}
	// give remainder to first winner
	if remainder > 0 {
		winners[0].Chips += remainder
	}
	g.Phase = PhaseShowdown

	// Deal remaining community cards so the board is always complete
	for len(g.Community) < 5 && len(g.Deck) > 0 {
		g.Community = append(g.Community, g.Deck[0])
		g.Deck = g.Deck[1:]
	}

	results := []ShowdownResult{}
	for _, p := range g.Players {
		won := 0
		isWinner := false
		for i, w := range winners {
			if w.ID == p.ID {
				won = share
				if i == 0 {
					won += remainder
				}
				isWinner = true
			}
		}
		results = append(results, ShowdownResult{
			PlayerID: p.ID,
			Name:     p.Name,
			Hand:     p.Hand,
			HandRank: "赢得底池",
			Won:      won,
			Bet:      p.TotalBet,
			Net:      p.Chips - p.ChipsBefore,
			IsWinner: isWinner,
		})
	}
	g.Broadcast(Message{Type: "showdown", Payload: map[string]interface{}{
		"results":   results,
		"community": g.Community,
	}})
	pot := g.Pot
	communitySnap := communityJSON(g.Community)
	resultsSnap := results
	time.AfterFunc(4*time.Second, func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.SaveChips != nil {
			for _, p := range g.Players {
				if !p.IsBot {
					g.SaveChips(p.ID, p.Chips)
				}
			}
		}
		if g.OnHandEnd != nil {
			g.OnHandEnd(g.RoomID, communitySnap, pot, resultsSnap)
		}

		g.postHandCleanup()
	})
}

func (g *Game) broadcastState() {
	pub := g.publicState()
	g.Broadcast(Message{Type: "game_state", Payload: pub})
}

func (g *Game) publicState() GameStatePayload {
	pubs := make([]PublicPlayerState, len(g.Players))
	for i, p := range g.Players {
		pubs[i] = PublicPlayerState{
			ID: p.ID, Name: p.Name, Chips: p.Chips, Bet: p.Bet,
			TotalBet: p.TotalBet, Folded: p.Folded, AllIn: p.AllIn,
			Disconnected: p.Disconnected,
			IsDealer: p.IsDealer, IsReady: p.IsReady, SeatIdx: p.SeatIdx,
			CardCount: len(p.Hand), IsBot: p.IsBot,
		}
		// reveal hands at showdown
		if g.Phase == PhaseShowdown && !p.Folded {
			pubs[i].Hand = p.Hand
		}
	}
	return GameStatePayload{
		Phase:      g.Phase,
		Community:  g.Community,
		Pot:        g.Pot,
		Players:    pubs,
		CurrentIdx: g.CurrentIdx,
		DealerIdx:  g.DealerIdx,
		SmallBlind: g.SmallBlind,
		BigBlind:   g.BigBlind,
		RoundBet:   g.RoundBet,
		OwnerID:    g.OwnerID,
	}
}

func (g *Game) sendHands() {
	for _, p := range g.Players {
		g.SendTo(p.ID, Message{Type: "deal", Payload: map[string]interface{}{
			"hand": p.Hand,
		}})
	}
}

func (g *Game) broadcastYourTurn() {
	if g.CurrentIdx < 0 || g.CurrentIdx >= len(g.Players) {
		return
	}
	p := g.Players[g.CurrentIdx]
	callAmt := g.RoundBet - p.Bet
	options := []string{"fold", "allin"}
	if callAmt == 0 {
		options = append([]string{"check"}, options...)
	} else {
		options = append([]string{"call"}, options...)
	}
	// raise right: player must have CanRaise AND enough chips for a full raise
	// (sub-raise all-ins go through the "allin" button instead)
	canFullRaise := p.CanRaise && p.Bet+p.Chips >= g.RoundBet+g.MinRaise
	if canFullRaise {
		if g.RoundBet == 0 {
			options = append(options, "bet")
		} else {
			options = append(options, "raise")
		}
	}

	g.SendTo(p.ID, Message{Type: "your_turn", Payload: map[string]interface{}{
		"timeout":    30,
		"options":    options,
		"callAmount": callAmt,
		"minRaise":   g.RoundBet + g.MinRaise,
	}})

	// bot: auto-act after a short think delay
	if p.IsBot {
		botID := p.ID
		botName := p.Name
		expectedIdx := g.CurrentIdx
		log.Printf("bot %s (idx=%d) scheduling action in %v", botName, expectedIdx, botThinkDelay())
		time.AfterFunc(botThinkDelay(), func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			log.Printf("bot %s callback: currentIdx=%d, len(players)=%d, phase=%s",
				botName, g.CurrentIdx, len(g.Players), g.Phase)
			if g.CurrentIdx >= len(g.Players) {
				log.Printf("bot %s: currentIdx out of range", botName)
				return
			}
			cur := g.Players[g.CurrentIdx]
			if cur.ID != botID {
				log.Printf("bot %s: not my turn (current is %s)", botName, cur.Name)
				return
			}
			if cur.Folded {
				log.Printf("bot %s: already folded", botName)
				return
			}
			if g.turnTimer != nil {
				g.turnTimer.Stop()
				g.turnTimer = nil
			}
			action, amount := decideBotAction(g, cur)
			log.Printf("bot %s decided: %s %d", botName, action, amount)
			if err := g.applyAction(cur, action, amount); err != nil {
				log.Printf("bot %s action %s failed: %v, trying fallback", botName, action, err)
				if err2 := g.applyAction(cur, "call", 0); err2 != nil {
					if err3 := g.applyAction(cur, "check", 0); err3 != nil {
						g.applyAction(cur, "fold", 0)
					}
				}
			}
			g.broadcastState()
			g.checkAdvance()
		})
		return
	}

	// human: auto-fold on timeout
	g.turnTimer = time.AfterFunc(30*time.Second, func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.Players[g.CurrentIdx].ID == p.ID && !p.Folded {
			p.Folded = true
			p.HasActed = true
			g.broadcastState()
			g.checkAdvance()
		}
	})
}

func (g *Game) SendChat(from, message string) {
	g.Broadcast(Message{Type: "chat", Payload: map[string]string{
		"from":    from,
		"message": message,
	}})
}

func (g *Game) SendEmoji(from, emoji string) {
	g.Broadcast(Message{Type: "emoji", Payload: map[string]string{
		"from":  from,
		"emoji": emoji,
	}})
}

func (g *Game) PlayerCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.Players)
}

// HumanCount returns the number of non-bot players currently in the game.
func (g *Game) HumanCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	count := 0
	for _, p := range g.Players {
		if !p.IsBot {
			count++
		}
	}
	return count
}

func (g *Game) GetPlayerIDs() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	ids := make([]string, 0, len(g.Players))
	for _, p := range g.Players {
		ids = append(ids, p.ID)
	}
	return ids
}

func (g *Game) GetPhase() GamePhase {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.Phase
}

// IsPlayerInGame returns true if the player is currently in the game (not yet removed).
func (g *Game) IsPlayerInGame(playerID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, p := range g.Players {
		if p.ID == playerID {
			return true
		}
	}
	return false
}

// SetDisconnected marks or unmarks a player as disconnected and broadcasts the state.
// For disconnected mid-game players, we also shorten their turn timeout to 10s.
func (g *Game) SetDisconnected(playerID string, disconnected bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, p := range g.Players {
		if p.ID == playerID {
			p.Disconnected = disconnected
			// If now disconnected and it's their turn, accelerate timeout to 10s
			if disconnected && g.CurrentIdx < len(g.Players) && g.Players[g.CurrentIdx].ID == playerID {
				if g.turnTimer != nil {
					g.turnTimer.Stop()
				}
				g.turnTimer = time.AfterFunc(10*time.Second, func() {
					g.mu.Lock()
					defer g.mu.Unlock()
					if g.CurrentIdx < len(g.Players) && g.Players[g.CurrentIdx].ID == playerID && !p.Folded {
						p.Folded = true
						p.HasActed = true
						g.broadcastState()
						g.checkAdvance()
					}
				})
			}
			g.Broadcast(Message{Type: "game_state", Payload: g.publicState()})
			return
		}
	}
}

// SendStateTo re-sends the current game state to a single player (used for rejoin).
func (g *Game) SendStateTo(playerID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	pub := g.publicState()
	g.SendTo(playerID, Message{Type: "game_state", Payload: pub})
	// Also re-send their hole cards if game is active
	for _, p := range g.Players {
		if p.ID == playerID && len(p.Hand) > 0 {
			g.SendTo(playerID, Message{Type: "deal", Payload: map[string]interface{}{
				"hand": p.Hand,
			}})
			break
		}
	}
}

// communityJSON encodes community cards as a JSON string for storage.
func communityJSON(cards []Card) string {
	data, _ := json.Marshal(cards)
	return string(data)
}

// postHandCleanup removes busted/disconnected players, clears bots if no humans remain,
// and starts the next hand. Must be called with g.mu held.
func (g *Game) postHandCleanup() {
	// Remove players with 0 chips and disconnected players
	alive := make([]*PlayerState, 0, len(g.Players))
	for _, p := range g.Players {
		if p.Chips <= 0 {
			continue
		}
		if p.Disconnected && !p.IsBot {
			continue // disconnected humans leave the table
		}
		alive = append(alive, p)
	}
	g.Players = alive

	// If no human players remain, remove all bots (room will be cleaned up)
	hasHuman := false
	for _, p := range g.Players {
		if !p.IsBot {
			hasHuman = true
			break
		}
	}
	if !hasHuman {
		g.Players = nil
	}

	for i, p := range g.Players {
		p.SeatIdx = i
	}
	g.Phase = PhaseWaiting
	g.Pot = 0
	for _, p := range g.Players {
		if p.IsBot {
			p.IsReady = true
		}
	}
	g.broadcastState()
	g.maybeStart()
}

// Rebuy adds chips to a player's stack (only allowed in waiting phase).
func (g *Game) Rebuy(playerID string, amount int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Phase != PhaseWaiting {
		return fmt.Errorf("只能在等待阶段补充筹码")
	}
	for _, p := range g.Players {
		if p.ID == playerID {
			p.Chips += amount
			g.Broadcast(Message{Type: "game_state", Payload: g.publicState()})
			return nil
		}
	}
	return fmt.Errorf("player not found")
}

// KickPlayer removes a player (only in waiting phase).
func (g *Game) KickPlayer(playerID string) error {
	g.mu.Lock()
	phase := g.Phase
	g.mu.Unlock()
	if phase != PhaseWaiting {
		return fmt.Errorf("只能在等待阶段踢人")
	}
	g.RemovePlayer(playerID)
	g.Broadcast(Message{Type: "game_state", Payload: func() interface{} {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.publicState()
	}()})
	return nil
}

// removePlayerLocked removes a player; caller must hold g.mu.
func (g *Game) removePlayerLocked(playerID string) {
	players := make([]*PlayerState, 0, len(g.Players))
	for _, p := range g.Players {
		if p.ID != playerID {
			players = append(players, p)
		}
	}
	g.Players = players
}
