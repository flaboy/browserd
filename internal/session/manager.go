package session

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"browserd/internal/profile"
)

var (
	ErrInvalidRequest         = errors.New("invalid request")
	ErrSessionNotFound        = errors.New("session not found")
	ErrProfileVersionConflict = errors.New("profile version conflict")
)

type CreateInput struct {
	S3ProfilePath   string
	ExpectedVersion string
	LeaseID         string
	TTLSeconds      int
}

type CreateOutput struct {
	RuntimeSessionID string `json:"runtimeSessionId"`
	CDPWsURL         string `json:"cdpWsUrl"`
	LeaseID          string `json:"leaseId"`
	ResolvedVersion  string `json:"resolvedVersion,omitempty"`
}

type CommitInput struct {
	IfMatchVersion string `json:"ifMatchVersion"`
}

type CommitOutput struct {
	NewVersion string `json:"newVersion"`
	Bytes      int64  `json:"bytes"`
	DurationMs int64  `json:"durationMs"`
}

type SessionInfo struct {
	RuntimeSessionID string
	ProfilePath      string
	ProfileDir       string
	Version          string
	LeaseID          string
	ExpiresAt        time.Time
}

type runtimeSession struct {
	RuntimeSessionID string
	ProfilePath      string
	ProfileDir       string
	Version          string
	LeaseID          string
	ExpiresAt        time.Time
}

type Manager interface {
	Create(input CreateInput) (CreateOutput, error)
	Commit(runtimeSessionID string, input CommitInput) (CommitOutput, error)
	Delete(runtimeSessionID string) error
	Get(runtimeSessionID string) (SessionInfo, error)
}

type ManagerOptions struct {
	Store      profile.Store
	Workdir    string
	CDPBaseURL string
}

type manager struct {
	mu         sync.Mutex
	store      profile.Store
	workdir    string
	cdpBaseURL string
	sessions   map[string]runtimeSession
}

func NewManager(opts ManagerOptions) Manager {
	workdir := strings.TrimSpace(opts.Workdir)
	if workdir == "" {
		workdir = filepath.Join(os.TempDir(), "browserd")
	}
	cdpBase := strings.TrimRight(strings.TrimSpace(opts.CDPBaseURL), "/")
	if cdpBase == "" {
		cdpBase = "ws://browserd:9222/devtools/browser"
	}
	st := opts.Store
	if st == nil {
		st = profile.NewMemoryStore()
	}
	return &manager{
		store:      st,
		workdir:    workdir,
		cdpBaseURL: cdpBase,
		sessions:   map[string]runtimeSession{},
	}
}

func (m *manager) Create(input CreateInput) (CreateOutput, error) {
	if strings.TrimSpace(input.S3ProfilePath) == "" {
		return CreateOutput{}, ErrInvalidRequest
	}
	if !strings.HasSuffix(strings.TrimSpace(input.S3ProfilePath), "profile.tgz") {
		return CreateOutput{}, ErrInvalidRequest
	}

	ttl := input.TTLSeconds
	if ttl <= 0 {
		ttl = 900
	}
	leaseID := strings.TrimSpace(input.LeaseID)
	if leaseID == "" {
		leaseID = fmt.Sprintf("lease_%d", time.Now().UnixNano())
	}
	rid := fmt.Sprintf("rt_%d_%d", time.Now().UnixNano(), rand.Intn(1000))
	sessionRoot := filepath.Join(m.workdir, "sessions", rid)
	profileDir := filepath.Join(sessionRoot, "profile")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return CreateOutput{}, err
	}

	data, version, found, err := m.store.Get(context.Background(), input.S3ProfilePath)
	if err != nil {
		return CreateOutput{}, err
	}
	resolvedVersion := "new"
	if found {
		tmpTGZ := filepath.Join(sessionRoot, "profile.tgz")
		if err := os.WriteFile(tmpTGZ, data, 0o644); err != nil {
			return CreateOutput{}, err
		}
		if err := profile.UnpackTGZToDir(tmpTGZ, profileDir); err != nil {
			return CreateOutput{}, err
		}
		if strings.TrimSpace(version) != "" {
			resolvedVersion = strings.TrimSpace(version)
		}
	}

	if ev := strings.TrimSpace(input.ExpectedVersion); ev != "" && ev != resolvedVersion {
		return CreateOutput{}, ErrProfileVersionConflict
	}

	out := CreateOutput{
		RuntimeSessionID: rid,
		CDPWsURL:         m.cdpBaseURL + "/" + rid,
		LeaseID:          leaseID,
		ResolvedVersion:  resolvedVersion,
	}

	m.mu.Lock()
	m.sessions[rid] = runtimeSession{
		RuntimeSessionID: rid,
		ProfilePath:      input.S3ProfilePath,
		ProfileDir:       profileDir,
		Version:          resolvedVersion,
		LeaseID:          leaseID,
		ExpiresAt:        time.Now().UTC().Add(time.Duration(ttl) * time.Second),
	}
	m.mu.Unlock()
	return out, nil
}

func (m *manager) Commit(runtimeSessionID string, input CommitInput) (CommitOutput, error) {
	if strings.TrimSpace(runtimeSessionID) == "" || strings.TrimSpace(input.IfMatchVersion) == "" {
		return CommitOutput{}, ErrInvalidRequest
	}

	m.mu.Lock()
	s, ok := m.sessions[runtimeSessionID]
	m.mu.Unlock()
	if !ok {
		return CommitOutput{}, ErrSessionNotFound
	}

	start := time.Now()
	tmpTGZ := filepath.Join(filepath.Dir(s.ProfileDir), "upload.tgz")
	if err := profile.PackDirToTGZ(s.ProfileDir, tmpTGZ); err != nil {
		return CommitOutput{}, err
	}
	buf, err := os.ReadFile(tmpTGZ)
	if err != nil {
		return CommitOutput{}, err
	}

	newVersion, err := m.store.Put(context.Background(), s.ProfilePath, buf, input.IfMatchVersion)
	if err != nil {
		if errors.Is(err, profile.ErrVersionConflict) {
			return CommitOutput{}, ErrProfileVersionConflict
		}
		return CommitOutput{}, err
	}

	m.mu.Lock()
	s.Version = newVersion
	m.sessions[runtimeSessionID] = s
	m.mu.Unlock()

	return CommitOutput{
		NewVersion: newVersion,
		Bytes:      int64(len(buf)),
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

func (m *manager) Delete(runtimeSessionID string) error {
	if strings.TrimSpace(runtimeSessionID) == "" {
		return ErrInvalidRequest
	}

	m.mu.Lock()
	s, ok := m.sessions[runtimeSessionID]
	if !ok {
		m.mu.Unlock()
		return ErrSessionNotFound
	}
	delete(m.sessions, runtimeSessionID)
	m.mu.Unlock()

	_ = os.RemoveAll(filepath.Dir(s.ProfileDir))
	return nil
}

func (m *manager) Get(runtimeSessionID string) (SessionInfo, error) {
	if strings.TrimSpace(runtimeSessionID) == "" {
		return SessionInfo{}, ErrInvalidRequest
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[runtimeSessionID]
	if !ok {
		return SessionInfo{}, ErrSessionNotFound
	}
	return SessionInfo{
		RuntimeSessionID: s.RuntimeSessionID,
		ProfilePath:      s.ProfilePath,
		ProfileDir:       s.ProfileDir,
		Version:          s.Version,
		LeaseID:          s.LeaseID,
		ExpiresAt:        s.ExpiresAt,
	}, nil
}
