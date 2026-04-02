package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"time"

	_ "modernc.org/sqlite"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID          string
	Username    string
	Chips       int
	HandsPlayed int
	HandsWon    int
	TotalWon    int
	BiggestPot  int
	IsGuest     bool
}

// HandRecord is stored in the hands table.
type HandRecord struct {
	PlayerID string `json:"playerId"`
	Name     string `json:"name"`
	HandRank string `json:"handRank"`
	Won      int    `json:"won"`
	Bet      int    `json:"bet"`
	Net      int    `json:"net"` // chips_after - chips_before
	IsWinner bool   `json:"isWinner"`
}

type Store struct {
	db *sql.DB
}

const startingChips = 1000
const refillThreshold = 100
const tokenTTL = 30 * 24 * time.Hour // 30 days

func NewStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	// WAL mode + 5s busy timeout for concurrent access
	db, err := sql.Open("sqlite", path+"?_journal=WAL&_timeout=5000")
	if err != nil {
		return nil, err
	}
	// SQLite only supports one writer at a time; serialize all access through a single connection.
	db.SetMaxOpenConns(1)

	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS users (
			id            TEXT PRIMARY KEY,
			username      TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			chips         INTEGER NOT NULL DEFAULT 1000,
			hands_played  INTEGER NOT NULL DEFAULT 0,
			hands_won     INTEGER NOT NULL DEFAULT 0,
			total_won     INTEGER NOT NULL DEFAULT 0,
			biggest_pot   INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token      TEXT PRIMARY KEY,
			user_id    TEXT NOT NULL,
			expires_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS hands (
			id        TEXT PRIMARY KEY,
			room_id   TEXT NOT NULL,
			ended_at  INTEGER NOT NULL,
			pot       INTEGER NOT NULL,
			community TEXT NOT NULL,
			players   TEXT NOT NULL
		)`,
		`DELETE FROM sessions WHERE expires_at < ?`,
	} {
		var execErr error
		if stmt == `DELETE FROM sessions WHERE expires_at < ?` {
			_, execErr = db.Exec(stmt, time.Now().Unix())
		} else {
			_, execErr = db.Exec(stmt)
		}
		if execErr != nil {
			return nil, execErr
		}
	}

	// Migrate: add stats columns if missing (idempotent)
	for _, col := range []string{
		"ALTER TABLE users ADD COLUMN hands_played INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE users ADD COLUMN hands_won    INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE users ADD COLUMN total_won    INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE users ADD COLUMN biggest_pot  INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE users ADD COLUMN is_guest     INTEGER NOT NULL DEFAULT 0",
	} {
		db.Exec(col) // ignore error if column already exists
	}

	// Settings table (key-value store)
	db.Exec(`CREATE TABLE IF NOT EXISTS settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`)

	return &Store{db: db}, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func (s *Store) Register(username, password string) (*User, string, error) {
	if len(username) < 2 || len(username) > 16 {
		return nil, "", fmt.Errorf("用户名长度需在2-16个字符之间")
	}
	if len(password) < 4 {
		return nil, "", fmt.Errorf("密码至少4位")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", err
	}

	id := "u_" + randomHex(6)
	if _, err := s.db.Exec(
		`INSERT INTO users (id, username, password_hash, chips) VALUES (?, ?, ?, ?)`,
		id, username, string(hash), startingChips,
	); err != nil {
		return nil, "", fmt.Errorf("用户名已被使用")
	}

	u := &User{ID: id, Username: username, Chips: startingChips}
	token, err := s.newSession(id)
	if err != nil {
		return nil, "", err
	}
	return u, token, nil
}

func (s *Store) Login(username, password string) (*User, string, error) {
	row := s.db.QueryRow(
		`SELECT id, username, password_hash, chips, hands_played, hands_won, total_won, biggest_pot
		 FROM users WHERE username=?`, username,
	)
	var u User
	var hash string
	if err := row.Scan(&u.ID, &u.Username, &hash, &u.Chips, &u.HandsPlayed, &u.HandsWon, &u.TotalWon, &u.BiggestPot); err != nil {
		return nil, "", fmt.Errorf("用户名或密码错误")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return nil, "", fmt.Errorf("用户名或密码错误")
	}
	// Refill chips if below threshold
	if u.Chips < refillThreshold {
		u.Chips = startingChips
		s.db.Exec(`UPDATE users SET chips=? WHERE id=?`, startingChips, u.ID)
	}
	token, err := s.newSession(u.ID)
	if err != nil {
		return nil, "", err
	}
	return &u, token, nil
}

func (s *Store) newSession(userID string) (string, error) {
	token := randomHex(16)
	exp := time.Now().Add(tokenTTL).Unix()
	if _, err := s.db.Exec(
		`INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`,
		token, userID, exp,
	); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) ValidateToken(token string) (*User, bool) {
	row := s.db.QueryRow(
		`SELECT u.id, u.username, u.chips, u.hands_played, u.hands_won, u.total_won, u.biggest_pot, u.is_guest
		 FROM sessions s JOIN users u ON s.user_id = u.id
		 WHERE s.token=? AND s.expires_at>?`,
		token, time.Now().Unix(),
	)
	var u User
	var isGuest int
	if err := row.Scan(&u.ID, &u.Username, &u.Chips, &u.HandsPlayed, &u.HandsWon, &u.TotalWon, &u.BiggestPot, &isGuest); err != nil {
		return nil, false
	}
	u.IsGuest = isGuest == 1
	return &u, true
}

// UpdateChips saves chip count. Refills to startingChips if below refillThreshold.
func (s *Store) UpdateChips(userID string, chips int) error {
	if chips < refillThreshold {
		chips = startingChips
	}
	_, err := s.db.Exec(`UPDATE users SET chips=? WHERE id=?`, chips, userID)
	return err
}

// RecordHand saves hand history and updates player stats.
func (s *Store) RecordHand(roomID string, pot int, community string, players []HandRecord) error {
	playersJSON, err := json.Marshal(players)
	if err != nil {
		return err
	}
	handID := randomHex(8)
	if _, err := s.db.Exec(
		`INSERT INTO hands (id, room_id, ended_at, pot, community, players) VALUES (?, ?, ?, ?, ?, ?)`,
		handID, roomID, time.Now().Unix(), pot, community, string(playersJSON),
	); err != nil {
		return err
	}
	// Update stats for each player
	for _, p := range players {
		if p.PlayerID == "" {
			continue
		}
		wonFlag := 0
		if p.IsWinner {
			wonFlag = 1
		}
		netGain := p.Net
		if netGain < 0 {
			netGain = 0 // total_won only tracks gains, not losses
		}
		s.db.Exec(`
			UPDATE users SET
				hands_played = hands_played + 1,
				hands_won    = hands_won + ?,
				total_won    = total_won + ?,
				biggest_pot  = MAX(biggest_pot, ?)
			WHERE id=?`,
			wonFlag, netGain, pot, p.PlayerID,
		)
	}
	return nil
}

// Leaderboard returns top N players by chips.
func (s *Store) Leaderboard(limit int) ([]User, error) {
	rows, err := s.db.Query(
		`SELECT id, username, chips, hands_played, hands_won, total_won, biggest_pot
		 FROM users ORDER BY chips DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var u User
		rows.Scan(&u.ID, &u.Username, &u.Chips, &u.HandsPlayed, &u.HandsWon, &u.TotalWon, &u.BiggestPot)
		users = append(users, u)
	}
	return users, nil
}

// PlayerHands returns the last N hands a specific player participated in.
func (s *Store) PlayerHands(playerID string, limit int) ([]map[string]interface{}, error) {
	rows, err := s.db.Query(
		`SELECT id, room_id, ended_at, pot, community, players FROM hands
		 WHERE players LIKE '%"playerId":"'||?||'"%'
		 ORDER BY ended_at DESC LIMIT ?`, playerID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []map[string]interface{}
	for rows.Next() {
		var id, roomID, community, players string
		var endedAt, pot int64
		rows.Scan(&id, &roomID, &endedAt, &pot, &community, &players)
		var ps []HandRecord
		json.Unmarshal([]byte(players), &ps)
		result = append(result, map[string]interface{}{
			"id":        id,
			"roomId":    roomID,
			"endedAt":   endedAt,
			"pot":       pot,
			"community": community,
			"players":   ps,
		})
	}
	return result, nil
}

// RecentHands returns the last N hands for a room.
func (s *Store) RecentHands(roomID string, limit int) ([]map[string]interface{}, error) {
	rows, err := s.db.Query(
		`SELECT id, ended_at, pot, community, players FROM hands
		 WHERE room_id=? ORDER BY ended_at DESC LIMIT ?`, roomID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []map[string]interface{}
	for rows.Next() {
		var id, community, players string
		var endedAt, pot int64
		rows.Scan(&id, &endedAt, &pot, &community, &players)
		var ps []HandRecord
		json.Unmarshal([]byte(players), &ps)
		result = append(result, map[string]interface{}{
			"id":        id,
			"endedAt":   endedAt,
			"pot":       pot,
			"community": community,
			"players":   ps,
		})
	}
	return result, nil
}

// IsAdmin checks if the user is the first registered user (admin).
func (s *Store) IsAdmin(userID string) bool {
	var firstID string
	err := s.db.QueryRow(`SELECT id FROM users WHERE is_guest=0 ORDER BY rowid ASC LIMIT 1`).Scan(&firstID)
	return err == nil && firstID == userID
}

// GetSetting reads a setting value. Returns empty string if not found.
func (s *Store) GetSetting(key string) string {
	var val string
	s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&val)
	return val
}

// SetSetting writes a setting value.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value,
	)
	return err
}

// Anonymous name generation
var (
	adjectives = []string{"飞翔的", "快乐的", "勇敢的", "神秘的", "幸运的", "闪亮的", "沉默的", "狂野的", "冷静的", "燃烧的"}
	animals    = []string{"熊猫", "老虎", "白鹤", "青龙", "凤凰", "麒麟", "飞鹰", "银狐", "金蟾", "黑豹"}
)

// RegisterAnonymous creates a temporary guest user with a random Chinese name.
func (s *Store) RegisterAnonymous() (*User, string, error) {
	b := make([]byte, 2)
	rand.Read(b)
	adj := adjectives[int(b[0])%len(adjectives)]
	animal := animals[int(b[1])%len(animals)]
	username := adj + animal

	// Add random suffix to avoid collision
	suffix := randomHex(2)
	username = username + suffix

	id := "g_" + randomHex(6)
	if _, err := s.db.Exec(
		`INSERT INTO users (id, username, password_hash, chips, is_guest) VALUES (?, ?, '', ?, 1)`,
		id, username, startingChips,
	); err != nil {
		return nil, "", fmt.Errorf("创建匿名用户失败")
	}

	u := &User{ID: id, Username: username, Chips: startingChips, IsGuest: true}
	token, err := s.newSession(id)
	if err != nil {
		return nil, "", err
	}
	return u, token, nil
}
