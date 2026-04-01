package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"poker/auth"
	"poker/game"
)

type Server struct {
	hub     *Hub
	manager *game.Manager
	store   *auth.Store
}

func NewServer(manager *game.Manager, hub *Hub, store *auth.Store) *Server {
	return &Server{hub: hub, manager: manager, store: store}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/register", s.register)
	mux.HandleFunc("POST /auth/login", s.login)
	mux.HandleFunc("GET /auth/me", s.me)
	mux.HandleFunc("GET /auth/hands", s.myHands)
	mux.HandleFunc("GET /rooms", s.listRooms)
	mux.HandleFunc("POST /rooms", s.createRoom)
	mux.HandleFunc("POST /rooms/{roomId}/bots", s.addBot)
	mux.HandleFunc("GET /rooms/{roomId}/hands", s.roomHands)
	mux.HandleFunc("GET /leaderboard", s.leaderboard)
	mux.HandleFunc("POST /auth/anonymous", s.anonymous)
	mux.HandleFunc("GET /settings/leaderboard", s.getLeaderboardSetting)
	mux.HandleFunc("POST /settings/leaderboard", s.setLeaderboardSetting)
	mux.HandleFunc("GET /auth/admin", s.checkAdmin)
	mux.HandleFunc("GET /ws", s.hub.ServeWS)
	// serve frontend static files
	mux.Handle("/", http.FileServer(http.Dir("../frontend")))
	return corsMiddleware(mux)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	user, token, err := s.store.Register(req.Username, req.Password)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"token":    token,
		"userId":   user.ID,
		"username": user.Username,
		"chips":    user.Chips,
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	user, token, err := s.store.Login(req.Username, req.Password)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":    token,
		"userId":   user.ID,
		"username": user.Username,
		"chips":    user.Chips,
	})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing token"})
		return
	}
	user, ok := s.store.ValidateToken(token)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"userId":      user.ID,
		"username":    user.Username,
		"chips":       user.Chips,
		"handsPlayed": user.HandsPlayed,
		"handsWon":    user.HandsWon,
		"totalWon":    user.TotalWon,
		"biggestPot":  user.BiggestPot,
	})
}

func (s *Server) listRooms(w http.ResponseWriter, r *http.Request) {
	rooms := s.manager.ListRooms()
	if rooms == nil {
		rooms = []game.RoomInfo{}
	}
	writeJSON(w, http.StatusOK, rooms)
}

func (s *Server) addBot(w http.ResponseWriter, r *http.Request) {
	roomId := r.PathValue("roomId")
	room, ok := s.manager.GetRoom(roomId)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "room not found"})
		return
	}

	// count existing bots to generate a unique name/id
	botNum := 1
	for {
		botID := fmt.Sprintf("bot_%s_%d", roomId, botNum)
		botName := fmt.Sprintf("Bot %d", botNum)
		if err := room.Game.AddBotPlayer(botID, botName); err != nil {
			if err.Error() == "bot already in room" {
				botNum++
				continue
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"botId": botID, "name": botName})
		return
	}
}

func (s *Server) createRoom(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MaxPlayers    int `json:"maxPlayers"`
		SmallBlind    int `json:"smallBlind"`
		StartingChips int `json:"startingChips"`
	}
	req.MaxPlayers = 6
	req.SmallBlind = 10
	req.StartingChips = 1000
	json.NewDecoder(r.Body).Decode(&req)

	room, err := s.manager.CreateRoom(req.MaxPlayers, req.SmallBlind, req.StartingChips,
		s.hub.Broadcast,
		s.hub.SendTo,
	)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Wire callbacks: save chips + record hand history after each hand
	store := s.store
	room.Game.SaveChips = func(playerID string, chips int) {
		if err := store.UpdateChips(playerID, chips); err != nil {
			log.Println("SaveChips error:", err)
		}
	}
	room.Game.OnHandEnd = func(roomID, community string, pot int, results []game.ShowdownResult) {
		records := make([]auth.HandRecord, len(results))
		for i, r := range results {
			records[i] = auth.HandRecord{
				PlayerID: r.PlayerID,
				Name:     r.Name,
				HandRank: r.HandRank,
				Won:      r.Won,
				Bet:      r.Bet,
				Net:      r.Net,
				IsWinner: r.IsWinner,
			}
		}
		if err := store.RecordHand(roomID, pot, community, records); err != nil {
			log.Println("RecordHand error:", err)
		}
	}

	writeJSON(w, http.StatusCreated, map[string]string{"roomId": room.ID})
}

func (s *Server) leaderboard(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	users, err := s.store.Leaderboard(limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	type row struct {
		Username    string `json:"username"`
		Chips       int    `json:"chips"`
		HandsPlayed int    `json:"handsPlayed"`
		HandsWon    int    `json:"handsWon"`
		TotalWon    int    `json:"totalWon"`
		BiggestPot  int    `json:"biggestPot"`
	}
	result := make([]row, len(users))
	for i, u := range users {
		result[i] = row{u.Username, u.Chips, u.HandsPlayed, u.HandsWon, u.TotalWon, u.BiggestPot}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) myHands(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	user, ok := s.store.ValidateToken(token)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
		return
	}
	hands, err := s.store.PlayerHands(user.ID, 30)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if hands == nil {
		hands = []map[string]interface{}{}
	}
	writeJSON(w, http.StatusOK, hands)
}

func (s *Server) roomHands(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("roomId")
	hands, err := s.store.RecentHands(roomID, 20)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if hands == nil {
		hands = []map[string]interface{}{}
	}
	writeJSON(w, http.StatusOK, hands)
}

func (s *Server) anonymous(w http.ResponseWriter, r *http.Request) {
	user, token, err := s.store.RegisterAnonymous()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":    token,
		"userId":   user.ID,
		"username": user.Username,
		"chips":    user.Chips,
		"isGuest":  true,
	})
}

func (s *Server) getLeaderboardSetting(w http.ResponseWriter, r *http.Request) {
	val := s.store.GetSetting("leaderboard_visible")
	visible := val != "false" // default visible
	writeJSON(w, http.StatusOK, map[string]interface{}{"visible": visible})
}

func (s *Server) setLeaderboardSetting(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	user, ok := s.store.ValidateToken(token)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "未授权"})
		return
	}
	if !s.store.IsAdmin(user.ID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "仅管理员可操作"})
		return
	}
	var body struct {
		Visible bool `json:"visible"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "参数错误"})
		return
	}
	val := "true"
	if !body.Visible {
		val = "false"
	}
	s.store.SetSetting("leaderboard_visible", val)
	writeJSON(w, http.StatusOK, map[string]interface{}{"visible": body.Visible})
}

func (s *Server) checkAdmin(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	user, ok := s.store.ValidateToken(token)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]interface{}{"isAdmin": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"isAdmin": s.store.IsAdmin(user.ID)})
}
