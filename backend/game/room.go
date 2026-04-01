package game

import (
	"fmt"
	"math/rand"
	"sync"
	"time"
)

type Room struct {
	ID            string `json:"id"`
	MaxPlayers    int    `json:"maxPlayers"`
	SmallBlind    int    `json:"smallBlind"`
	StartingChips int    `json:"startingChips"`
	OwnerID       string `json:"ownerId"`
	Game          *Game
}

type RoomInfo struct {
	ID            string    `json:"id"`
	MaxPlayers    int       `json:"maxPlayers"`
	Players       int       `json:"players"`
	PlayerIDs     []string  `json:"playerIds"`
	SmallBlind    int       `json:"smallBlind"`
	StartingChips int       `json:"startingChips"`
	OwnerID       string    `json:"ownerId"`
	Phase         GamePhase `json:"phase"`
}

type Manager struct {
	mu    sync.RWMutex
	rooms map[string]*Room
}

func NewManager() *Manager {
	m := &Manager{rooms: map[string]*Room{}}
	go m.cleanupLoop()
	return m
}

// cleanupLoop removes rooms that have been empty or bot-only for >30 seconds.
func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.Lock()
		for id, room := range m.rooms {
			if room.Game.PlayerCount() == 0 || room.Game.HumanCount() == 0 {
				delete(m.rooms, id)
			}
		}
		m.mu.Unlock()
	}
}

func (m *Manager) CreateRoom(maxPlayers, smallBlind, startingChips int, broadcast func(string, Message), sendTo func(string, string, Message)) (*Room, error) {
	if maxPlayers < 2 || maxPlayers > 9 {
		return nil, fmt.Errorf("maxPlayers must be 2-9")
	}
	if startingChips < 100 {
		startingChips = 100
	}
	id := fmt.Sprintf("%04d", rand.Intn(9000)+1000)
	m.mu.Lock()
	defer m.mu.Unlock()
	room := &Room{
		ID:            id,
		MaxPlayers:    maxPlayers,
		SmallBlind:    smallBlind,
		StartingChips: startingChips,
	}
	room.Game = NewGame(id,
		smallBlind,
		func(msg Message) { broadcast(id, msg) },
		func(playerID string, msg Message) { sendTo(id, playerID, msg) },
	)
	m.rooms[id] = room
	return room, nil
}

func (m *Manager) GetRoom(id string) (*Room, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.rooms[id]
	return r, ok
}

func (m *Manager) ListRooms() []RoomInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var list []RoomInfo
	for _, r := range m.rooms {
		if r.Game.PlayerCount() == 0 {
			continue // hide empty rooms; cleanup goroutine will delete them
		}
		list = append(list, RoomInfo{
			ID:            r.ID,
			MaxPlayers:    r.MaxPlayers,
			Players:       r.Game.PlayerCount(),
			PlayerIDs:     r.Game.GetPlayerIDs(),
			SmallBlind:    r.SmallBlind,
			StartingChips: r.StartingChips,
			OwnerID:       r.OwnerID,
			Phase:         r.Game.GetPhase(),
		})
	}
	return list
}

func (m *Manager) DeleteRoom(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rooms, id)
}
