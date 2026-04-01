package auth

import (
	"os"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	f, err := os.CreateTemp("", "poker_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	s, err := NewStore(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRegister_OK(t *testing.T) {
	s := newTestStore(t)
	u, token, err := s.Register("alice", "pass1234")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.Username != "alice" {
		t.Errorf("username = %q, want alice", u.Username)
	}
	if u.Chips != startingChips {
		t.Errorf("chips = %d, want %d", u.Chips, startingChips)
	}
	if token == "" {
		t.Error("token should not be empty")
	}
}

func TestRegister_DuplicateUsername(t *testing.T) {
	s := newTestStore(t)
	if _, _, err := s.Register("alice", "pass1234"); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.Register("alice", "other1234")
	if err == nil {
		t.Error("expected error for duplicate username, got nil")
	}
}

func TestRegister_Validation(t *testing.T) {
	s := newTestStore(t)
	cases := []struct{ user, pass string }{
		{"a", "pass1234"},        // username too short
		{"toolongusername!!", "pass1234"}, // username too long
		{"bob", "abc"},           // password too short
	}
	for _, c := range cases {
		if _, _, err := s.Register(c.user, c.pass); err == nil {
			t.Errorf("Register(%q, %q) expected error", c.user, c.pass)
		}
	}
}

func TestLogin_OK(t *testing.T) {
	s := newTestStore(t)
	s.Register("alice", "pass1234")

	u, token, err := s.Login("alice", "pass1234")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.Username != "alice" {
		t.Errorf("username = %q", u.Username)
	}
	if token == "" {
		t.Error("token should not be empty")
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	s := newTestStore(t)
	s.Register("alice", "pass1234")

	_, _, err := s.Login("alice", "wrong")
	if err == nil {
		t.Error("expected error for wrong password")
	}
}

func TestLogin_UnknownUser(t *testing.T) {
	s := newTestStore(t)
	_, _, err := s.Login("nobody", "pass1234")
	if err == nil {
		t.Error("expected error for unknown user")
	}
}

func TestValidateToken(t *testing.T) {
	s := newTestStore(t)
	u, token, _ := s.Register("alice", "pass1234")

	got, ok := s.ValidateToken(token)
	if !ok {
		t.Fatal("token should be valid")
	}
	if got.ID != u.ID {
		t.Errorf("user id mismatch: got %q, want %q", got.ID, u.ID)
	}
}

func TestValidateToken_Invalid(t *testing.T) {
	s := newTestStore(t)
	_, ok := s.ValidateToken("badtoken")
	if ok {
		t.Error("invalid token should not validate")
	}
}

func TestUpdateChips(t *testing.T) {
	s := newTestStore(t)
	u, token, _ := s.Register("alice", "pass1234")

	if err := s.UpdateChips(u.ID, 1500); err != nil {
		t.Fatalf("UpdateChips: %v", err)
	}

	got, _ := s.ValidateToken(token)
	if got.Chips != 1500 {
		t.Errorf("chips = %d, want 1500", got.Chips)
	}
}

func TestUpdateChips_Refill(t *testing.T) {
	s := newTestStore(t)
	u, token, _ := s.Register("alice", "pass1234")

	// chips < refillThreshold → should be refilled to startingChips
	s.UpdateChips(u.ID, 0)
	got, _ := s.ValidateToken(token)
	if got.Chips != startingChips {
		t.Errorf("chips after refill = %d, want %d", got.Chips, startingChips)
	}
}

func TestLogin_RefillsOnLogin(t *testing.T) {
	s := newTestStore(t)
	u, _, _ := s.Register("alice", "pass1234")
	s.UpdateChips(u.ID, 0) // set broke

	got, _, err := s.Login("alice", "pass1234")
	if err != nil {
		t.Fatal(err)
	}
	if got.Chips != startingChips {
		t.Errorf("chips after login refill = %d, want %d", got.Chips, startingChips)
	}
}
