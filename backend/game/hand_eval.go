package game

import "sort"

// Hand ranks
const (
	HighCard      = 1
	OnePair       = 2
	TwoPair       = 3
	ThreeOfAKind  = 4
	Straight      = 5
	Flush         = 6
	FullHouse     = 7
	FourOfAKind   = 8
	StraightFlush = 9
	RoyalFlush    = 10
)

var handNames = map[int]string{
	HighCard: "高牌", OnePair: "一对", TwoPair: "两对",
	ThreeOfAKind: "三条", Straight: "顺子", Flush: "同花",
	FullHouse: "葫芦", FourOfAKind: "四条", StraightFlush: "同花顺", RoyalFlush: "皇家同花顺",
}

type HandResult struct {
	Rank     int
	RankName string
	Tiebreak []int // for comparison
	BestFive []Card
}

// Best5 returns the best 5-card hand from up to 7 cards
func Best5(cards []Card) HandResult {
	best := HandResult{}
	combos := combinations(cards, 5)
	for _, combo := range combos {
		r := evaluate5(combo)
		if r.Rank > best.Rank || (r.Rank == best.Rank && compareTiebreak(r.Tiebreak, best.Tiebreak) > 0) {
			best = r
		}
	}
	return best
}

func evaluate5(cards []Card) HandResult {
	sort.Slice(cards, func(i, j int) bool { return cards[i].Rank > cards[j].Rank })
	flush := isFlush(cards)
	straight, highCard := isStraight(cards)
	groups := groupByRank(cards)

	var tiebreak []int
	rank := HighCard

	switch {
	case flush && straight && highCard == 14:
		rank = RoyalFlush
		tiebreak = []int{14}
	case flush && straight:
		rank = StraightFlush
		tiebreak = []int{highCard}
	case hasGroup(groups, 4):
		rank = FourOfAKind
		tiebreak = groupTiebreak(groups, []int{4, 1})
	case hasGroup(groups, 3) && hasGroup(groups, 2):
		rank = FullHouse
		tiebreak = groupTiebreak(groups, []int{3, 2})
	case flush:
		rank = Flush
		tiebreak = rankList(cards)
	case straight:
		rank = Straight
		tiebreak = []int{highCard}
	case hasGroup(groups, 3):
		rank = ThreeOfAKind
		tiebreak = groupTiebreak(groups, []int{3, 1, 1})
	case countGroups(groups, 2) == 2:
		rank = TwoPair
		tiebreak = groupTiebreak(groups, []int{2, 2, 1})
	case hasGroup(groups, 2):
		rank = OnePair
		tiebreak = groupTiebreak(groups, []int{2, 1, 1, 1})
	default:
		rank = HighCard
		tiebreak = rankList(cards)
	}

	return HandResult{Rank: rank, RankName: handNames[rank], Tiebreak: tiebreak, BestFive: cards}
}

func isFlush(cards []Card) bool {
	s := cards[0].Suit
	for _, c := range cards[1:] {
		if c.Suit != s {
			return false
		}
	}
	return true
}

func isStraight(cards []Card) (bool, int) {
	ranks := rankList(cards)
	// Ace-low straight: A2345
	if ranks[0] == 14 && ranks[1] == 5 && ranks[2] == 4 && ranks[3] == 3 && ranks[4] == 2 {
		return true, 5
	}
	for i := 1; i < len(ranks); i++ {
		if ranks[i] != ranks[i-1]-1 {
			return false, 0
		}
	}
	return true, ranks[0]
}

func groupByRank(cards []Card) map[int]int {
	m := map[int]int{}
	for _, c := range cards {
		m[c.Rank]++
	}
	return m
}

func hasGroup(groups map[int]int, n int) bool {
	for _, v := range groups {
		if v == n {
			return true
		}
	}
	return false
}

func countGroups(groups map[int]int, n int) int {
	count := 0
	for _, v := range groups {
		if v == n {
			count++
		}
	}
	return count
}

// groupTiebreak orders cards by group size desc, then rank desc
func groupTiebreak(groups map[int]int, order []int) []int {
	type rankCount struct{ rank, count int }
	var rcs []rankCount
	for r, c := range groups {
		rcs = append(rcs, rankCount{r, c})
	}
	sort.Slice(rcs, func(i, j int) bool {
		if rcs[i].count != rcs[j].count {
			return rcs[i].count > rcs[j].count
		}
		return rcs[i].rank > rcs[j].rank
	})
	result := []int{}
	_ = order
	for _, rc := range rcs {
		for k := 0; k < rc.count; k++ {
			result = append(result, rc.rank)
		}
	}
	return result
}

func rankList(cards []Card) []int {
	r := make([]int, len(cards))
	for i, c := range cards {
		r[i] = c.Rank
	}
	return r
}

func compareTiebreak(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] > b[i] {
			return 1
		}
		if a[i] < b[i] {
			return -1
		}
	}
	return 0
}

func combinations(cards []Card, k int) [][]Card {
	var result [][]Card
	var combo func(start int, cur []Card)
	combo = func(start int, cur []Card) {
		if len(cur) == k {
			cp := make([]Card, k)
			copy(cp, cur)
			result = append(result, cp)
			return
		}
		for i := start; i < len(cards); i++ {
			combo(i+1, append(cur, cards[i]))
		}
	}
	combo(0, nil)
	return result
}
