package live

import (
	"strings"
	"testing"
	"time"
)

func TestTokenStoreIssuesOpaqueToken(t *testing.T) {
	now := time.Date(2026, 5, 27, 10, 0, 0, 0, time.UTC)
	store := NewTokenStore(TokenStoreOptions{Now: func() time.Time { return now }})

	token, state, err := store.Issue(IssueRequest{
		RuntimeSessionID: "rt_123",
		HandoffID:        "ho_123",
		Permission:       PermissionControl,
		TTL:              10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	if token == "" {
		t.Fatal("expected token")
	}
	if strings.Contains(token, "rt_123") || strings.Contains(token, "ho_123") {
		t.Fatalf("token must be opaque, got %q", token)
	}
	if state.RuntimeSessionID != "rt_123" {
		t.Fatalf("runtime session mismatch: %+v", state)
	}
	if state.HandoffID != "ho_123" {
		t.Fatalf("handoff mismatch: %+v", state)
	}
	if state.Permission != PermissionControl {
		t.Fatalf("permission mismatch: %+v", state)
	}
	if !state.ExpiresAt.Equal(now.Add(10 * time.Minute)) {
		t.Fatalf("expiresAt mismatch: %s", state.ExpiresAt)
	}
}

func TestTokenStoreRejectsExpiredAndRevokedTokens(t *testing.T) {
	now := time.Date(2026, 5, 27, 10, 0, 0, 0, time.UTC)
	store := NewTokenStore(TokenStoreOptions{Now: func() time.Time { return now }})

	token, _, err := store.Issue(IssueRequest{
		RuntimeSessionID: "rt_123",
		HandoffID:        "ho_123",
		Permission:       PermissionControl,
		TTL:              time.Minute,
	})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	store.Revoke(token)
	if _, ok := store.Lookup(token); ok {
		t.Fatal("expected revoked token to be rejected")
	}

	now = now.Add(2 * time.Minute)
	expired, _, err := store.Issue(IssueRequest{
		RuntimeSessionID: "rt_456",
		HandoffID:        "ho_456",
		Permission:       PermissionView,
		TTL:              time.Minute,
	})
	if err != nil {
		t.Fatalf("issue second token: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, ok := store.Lookup(expired); ok {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestTokenStoreRevokesBySessionAndHandoff(t *testing.T) {
	now := time.Date(2026, 5, 27, 10, 0, 0, 0, time.UTC)
	store := NewTokenStore(TokenStoreOptions{Now: func() time.Time { return now }})

	first, _, err := store.Issue(IssueRequest{
		RuntimeSessionID: "rt_123",
		HandoffID:        "ho_123",
		Permission:       PermissionControl,
		TTL:              time.Minute,
	})
	if err != nil {
		t.Fatalf("issue first token: %v", err)
	}
	second, _, err := store.Issue(IssueRequest{
		RuntimeSessionID: "rt_123",
		HandoffID:        "ho_456",
		Permission:       PermissionView,
		TTL:              time.Minute,
	})
	if err != nil {
		t.Fatalf("issue second token: %v", err)
	}

	store.RevokeHandoff("rt_123", "ho_123")
	if _, ok := store.Lookup(first); ok {
		t.Fatal("expected handoff token to be revoked")
	}
	if _, ok := store.Lookup(second); !ok {
		t.Fatal("expected other token to remain valid")
	}

	store.RevokeSession("rt_123")
	if _, ok := store.Lookup(second); ok {
		t.Fatal("expected session tokens to be revoked")
	}
}

func TestRedactTokenDoesNotExposeFullToken(t *testing.T) {
	token := "abcdefghijklmnopqrstuvwxyz0123456789"
	redacted := RedactToken(token)
	if redacted == "" {
		t.Fatal("expected redacted token")
	}
	if strings.Contains(redacted, token) {
		t.Fatalf("redacted token exposes full token: %q", redacted)
	}
}
