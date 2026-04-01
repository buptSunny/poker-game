package game

import "math/rand"

type Card struct {
	Suit  string // "s" "h" "d" "c"
	Rank  int    // 2-14 (14=Ace)
	Label string // "As" "Kh" etc
}

var ranks = []int{2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14}
var suits = []string{"s", "h", "d", "c"}
var rankLabel = map[int]string{2: "2", 3: "3", 4: "4", 5: "5", 6: "6", 7: "7", 8: "8", 9: "9", 10: "T", 11: "J", 12: "Q", 13: "K", 14: "A"}

func newDeck() []Card {
	deck := make([]Card, 0, 52)
	for _, s := range suits {
		for _, r := range ranks {
			deck = append(deck, Card{Suit: s, Rank: r, Label: rankLabel[r] + s})
		}
	}
	return deck
}

func shuffle(deck []Card) {
	rand.Shuffle(len(deck), func(i, j int) { deck[i], deck[j] = deck[j], deck[i] })
}
