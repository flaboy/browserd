package live

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

var ErrInvalidTokenRequest = errors.New("invalid token request")

type Permission string

const (
	PermissionView    Permission = "view"
	PermissionControl Permission = "control"
)

type TokenStoreOptions struct {
	Now func() time.Time
}

type IssueRequest struct {
	RuntimeSessionID string
	HandoffID        string
	Permission       Permission
	TTL              time.Duration
}

type TokenState struct {
	RuntimeSessionID string
	HandoffID        string
	Permission       Permission
	ExpiresAt        time.Time
	Revoked          bool
}

type TokenStore struct {
	mu     sync.Mutex
	now    func() time.Time
	tokens map[string]TokenState
}

func NewTokenStore(opts TokenStoreOptions) *TokenStore {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &TokenStore{
		now:    now,
		tokens: map[string]TokenState{},
	}
}

func (s *TokenStore) Issue(req IssueRequest) (string, TokenState, error) {
	runtimeSessionID := strings.TrimSpace(req.RuntimeSessionID)
	handoffID := strings.TrimSpace(req.HandoffID)
	permission := req.Permission
	if permission == "" {
		permission = PermissionView
	}
	if runtimeSessionID == "" || handoffID == "" || req.TTL <= 0 || !validPermission(permission) {
		return "", TokenState{}, ErrInvalidTokenRequest
	}

	token, err := randomToken()
	if err != nil {
		return "", TokenState{}, err
	}
	state := TokenState{
		RuntimeSessionID: runtimeSessionID,
		HandoffID:        handoffID,
		Permission:       permission,
		ExpiresAt:        s.now().UTC().Add(req.TTL),
	}

	s.mu.Lock()
	s.tokens[token] = state
	s.mu.Unlock()

	return token, state, nil
}

func (s *TokenStore) Lookup(token string) (TokenState, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return TokenState{}, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.tokens[token]
	if !ok || state.Revoked || !s.now().UTC().Before(state.ExpiresAt) {
		return TokenState{}, false
	}
	return state, true
}

func (s *TokenStore) Revoke(token string) {
	token = strings.TrimSpace(token)
	if token == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.tokens[token]
	if !ok {
		return
	}
	state.Revoked = true
	s.tokens[token] = state
}

func (s *TokenStore) RevokeHandoff(runtimeSessionID string, handoffID string) {
	runtimeSessionID = strings.TrimSpace(runtimeSessionID)
	handoffID = strings.TrimSpace(handoffID)
	if runtimeSessionID == "" || handoffID == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for token, state := range s.tokens {
		if state.RuntimeSessionID == runtimeSessionID && state.HandoffID == handoffID {
			state.Revoked = true
			s.tokens[token] = state
		}
	}
}

func (s *TokenStore) RevokeSession(runtimeSessionID string) {
	runtimeSessionID = strings.TrimSpace(runtimeSessionID)
	if runtimeSessionID == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for token, state := range s.tokens {
		if state.RuntimeSessionID == runtimeSessionID {
			state.Revoked = true
			s.tokens[token] = state
		}
	}
}

func RedactToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return "token_sha256:" + hex.EncodeToString(sum[:])[:12]
}

func validPermission(permission Permission) bool {
	return permission == PermissionView || permission == PermissionControl
}

func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
