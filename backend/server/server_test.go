package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"poker/auth"
	"poker/game"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	f, _ := os.CreateTemp("", "poker_srv_test_*.db")
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	store, err := auth.NewStore(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	manager := game.NewManager()
	hub := NewHub(manager, store)
	return NewServer(manager, hub, store)
}

func postJSON(t *testing.T, srv *Server, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	return rr
}

func getPath(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	return rr
}

// ===== Auth =====

func TestRegisterEndpoint(t *testing.T) {
	srv := newTestServer(t)
	rr := postJSON(t, srv, "/auth/register", map[string]string{"username": "alice", "password": "pass1234"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rr.Code, rr.Body)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["token"] == nil || resp["userId"] == nil {
		t.Errorf("missing token or userId in response: %v", resp)
	}
	if int(resp["chips"].(float64)) != 1000 {
		t.Errorf("chips = %v, want 1000", resp["chips"])
	}
}

func TestRegisterEndpoint_Duplicate(t *testing.T) {
	srv := newTestServer(t)
	postJSON(t, srv, "/auth/register", map[string]string{"username": "alice", "password": "pass1234"})
	rr := postJSON(t, srv, "/auth/register", map[string]string{"username": "alice", "password": "other1234"})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestLoginEndpoint(t *testing.T) {
	srv := newTestServer(t)
	postJSON(t, srv, "/auth/register", map[string]string{"username": "alice", "password": "pass1234"})

	rr := postJSON(t, srv, "/auth/login", map[string]string{"username": "alice", "password": "pass1234"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["token"] == nil {
		t.Error("missing token in login response")
	}
}

func TestLoginEndpoint_WrongPassword(t *testing.T) {
	srv := newTestServer(t)
	postJSON(t, srv, "/auth/register", map[string]string{"username": "alice", "password": "pass1234"})
	rr := postJSON(t, srv, "/auth/login", map[string]string{"username": "alice", "password": "wrong"})
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestMeEndpoint(t *testing.T) {
	srv := newTestServer(t)
	rr := postJSON(t, srv, "/auth/register", map[string]string{"username": "alice", "password": "pass1234"})
	var reg map[string]any
	json.NewDecoder(rr.Body).Decode(&reg)
	token := reg["token"].(string)

	rr2 := getPath(t, srv, "/auth/me?token="+token)
	if rr2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr2.Code, rr2.Body)
	}
	var me map[string]any
	json.NewDecoder(rr2.Body).Decode(&me)
	if me["username"] != "alice" {
		t.Errorf("username = %v, want alice", me["username"])
	}
}

func TestMeEndpoint_InvalidToken(t *testing.T) {
	srv := newTestServer(t)
	rr := getPath(t, srv, "/auth/me?token=badtoken")
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

// ===== Rooms =====

func TestCreateRoomEndpoint(t *testing.T) {
	srv := newTestServer(t)
	rr := postJSON(t, srv, "/rooms", map[string]any{"maxPlayers": 6, "smallBlind": 10})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rr.Code, rr.Body)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["roomId"] == nil {
		t.Error("missing roomId in response")
	}
}

func TestListRoomsEndpoint(t *testing.T) {
	srv := newTestServer(t)
	postJSON(t, srv, "/rooms", map[string]any{"maxPlayers": 6, "smallBlind": 10})
	postJSON(t, srv, "/rooms", map[string]any{"maxPlayers": 2, "smallBlind": 5})

	rr := getPath(t, srv, "/rooms")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rr.Code, rr.Body)
	}
	var rooms []any
	json.NewDecoder(rr.Body).Decode(&rooms)
	if len(rooms) != 2 {
		t.Errorf("rooms count = %d, want 2", len(rooms))
	}
}

func TestCreateRoom_InvalidMaxPlayers(t *testing.T) {
	srv := newTestServer(t)
	rr := postJSON(t, srv, "/rooms", map[string]any{"maxPlayers": 1, "smallBlind": 10})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}
