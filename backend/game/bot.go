package game

import (
	"math/rand"
	"time"
)

// decideBotAction returns (action, raiseAmount) for the bot at current turn.
func decideBotAction(g *Game, p *PlayerState) (string, int) {
	strength := botHandStrength(g, p)
	// add noise so the bot doesn't play identically every time
	strength += (rand.Float64() - 0.5) * 0.12
	if strength < 0 {
		strength = 0
	}
	if strength > 1 {
		strength = 1
	}

	callAmt := g.RoundBet - p.Bet
	pot := g.Pot
	if pot == 0 {
		pot = g.BigBlind
	}

	// Pot odds: minimum equity needed to call profitably
	potOdds := float64(callAmt) / float64(pot+callAmt)

	// Position: how many players still act after this bot (0 = last to act = best)
	position := botPosition(g, p)
	positionBonus := (float64(len(g.Players)-1-position) / float64(max(len(g.Players)-1, 1))) * 0.08

	effectiveStrength := strength + positionBonus
	if effectiveStrength > 1 {
		effectiveStrength = 1
	}

	// Stack-to-pot ratio (SPR): deep stacks warrant more speculative play
	spr := float64(p.Chips) / float64(pot)

	if callAmt == 0 {
		// No bet to call — check or bet
		return botCheckOrBet(g, p, effectiveStrength, pot, spr, position)
	}

	// There is a bet to call
	return botCallOrFold(g, p, effectiveStrength, float64(callAmt), pot, potOdds, spr, position)
}

func botCheckOrBet(g *Game, p *PlayerState, strength float64, pot int, spr float64, position int) (string, int) {
	// Bluff frequency: more often in late position with low SPR
	bluffThreshold := 0.68
	if position <= 1 { // late position
		bluffThreshold = 0.58
	}

	switch {
	case strength > 0.80:
		// Strong hand: value bet, sizing depends on SPR
		if !p.CanRaise {
			return "allin", 0
		}
		size := valueBetSize(g, p, pot, strength)
		if size >= p.Chips+p.Bet {
			return "allin", 0
		}
		return betOrRaise(g), size

	case strength > bluffThreshold && p.CanRaise && rand.Float64() > 0.45:
		// Semi-bluff or thin value bet
		size := g.RoundBet + g.MinRaise
		if spr > 3 {
			size += rand.Intn(g.BigBlind*2 + 1)
		}
		if size >= p.Chips+p.Bet {
			return "allin", 0
		}
		return betOrRaise(g), size

	case strength < 0.30 && position <= 1 && rand.Float64() > 0.80:
		// Pure bluff in position (rare)
		size := g.RoundBet + g.MinRaise
		if size >= p.Chips+p.Bet {
			return "check", 0
		}
		return betOrRaise(g), size

	default:
		return "check", 0
	}
}

func botCallOrFold(g *Game, p *PlayerState, strength, callAmt float64, pot int, potOdds float64, spr float64, position int) (string, int) {
	callRatio := callAmt / float64(max(p.Chips, 1))

	switch {
	case strength >= 0.78:
		// Strong: always call, often re-raise
		if p.CanRaise && rand.Float64() > 0.35 {
			size := valueBetSize(g, p, pot, strength)
			if size >= p.Chips+int(p.Bet) {
				return "allin", 0
			}
			return "raise", size
		}
		return "call", 0

	case strength >= 0.50:
		// Medium: call if equity > pot odds, sometimes raise
		if strength > potOdds {
			if p.CanRaise && strength > 0.65 && rand.Float64() > 0.60 {
				size := g.RoundBet + g.MinRaise
				if size >= p.Chips+int(p.Bet) {
					return "call", 0
				}
				return "raise", size
			}
			return "call", 0
		}
		// Borderline: call small bets
		if callRatio < 0.12 {
			return "call", 0
		}
		return "fold", 0

	case strength >= 0.28:
		// Weak: call only if pot odds are very favorable
		if strength > potOdds && callRatio < 0.18 {
			return "call", 0
		}
		// Bluff-raise in late position (steal attempt)
		if position <= 1 && callRatio < 0.08 && rand.Float64() > 0.85 {
			size := g.RoundBet + g.MinRaise
			if size < p.Chips+int(p.Bet) {
				return "raise", size
			}
		}
		return "fold", 0

	default:
		// Very weak: fold, occasionally float small bets
		if callRatio < 0.04 && rand.Float64() > 0.70 {
			return "call", 0
		}
		return "fold", 0
	}
}

// valueBetSize returns a reasonable bet size based on hand strength and pot.
func valueBetSize(g *Game, p *PlayerState, pot int, strength float64) int {
	// Scale bet between 50% and 100% of pot based on strength
	fraction := 0.50 + strength*0.50
	size := g.RoundBet + int(float64(pot)*fraction)
	// Round to big blind
	bb := g.BigBlind
	if bb > 0 {
		size = ((size + bb/2) / bb) * bb
	}
	if size < g.RoundBet+g.MinRaise {
		size = g.RoundBet + g.MinRaise
	}
	return size
}

// betOrRaise returns "bet" when there's no prior bet, "raise" otherwise.
func betOrRaise(g *Game) string {
	if g.RoundBet == 0 {
		return "bet"
	}
	return "raise"
}

// botPosition returns how many players act AFTER this bot this round (0 = last).
func botPosition(g *Game, p *PlayerState) int {
	n := len(g.Players)
	after := 0
	cur := g.CurrentIdx
	for i := 1; i < n; i++ {
		next := (cur + i) % n
		np := g.Players[next]
		if !np.Folded && !np.AllIn {
			after++
		}
	}
	return after
}

// botHandStrength returns 0–1 based on hole cards + community cards.
func botHandStrength(g *Game, p *PlayerState) float64 {
	community := g.Community

	if len(community) == 0 {
		return preFlopStrength(p.Hand)
	}

	// post-flop: use best 5 hand rank
	cards := append(p.Hand, community...)
	result := Best5(cards)

	// map rank 1-10 to 0-1
	base := float64(result.Rank-1) / 9.0

	// bonus for strong tiebreakers
	if len(result.Tiebreak) > 0 {
		base += float64(result.Tiebreak[0]-2) / 12.0 * 0.08
	}
	if base > 1 {
		base = 1
	}

	// Discount for draw-heavy boards (opponents may improve)
	if len(community) < 5 {
		base *= 0.95 // slight discount for incomplete board
	}

	return base
}

// preFlopStrength scores hole cards 0–1 using Chen formula approximation.
func preFlopStrength(hand []Card) float64 {
	if len(hand) < 2 {
		return 0.3
	}
	a, b := hand[0], hand[1]
	// normalize rank: 2→0 .. A(14)→1
	ra := float64(a.Rank-2) / 12.0
	rb := float64(b.Rank-2) / 12.0

	isPair := a.Rank == b.Rank
	isSuited := a.Suit == b.Suit
	gap := abs(a.Rank - b.Rank)

	score := (ra + rb) / 2.0

	if isPair {
		score = 0.55 + ra*0.45
	} else {
		if isSuited {
			score += 0.07
		}
		if gap <= 1 {
			score += 0.07 // connected
		} else if gap == 2 {
			score += 0.04
		} else if gap > 4 {
			score -= 0.10
		}
		// High card bonus
		if a.Rank >= 13 || b.Rank >= 13 {
			score += 0.04
		}
	}
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return score
}

// botThinkDelay returns a realistic "thinking" pause.
func botThinkDelay() time.Duration {
	ms := 600 + rand.Intn(1600)
	return time.Duration(ms) * time.Millisecond
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
