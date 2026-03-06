package runtime

import (
	"errors"
	"sync"
)

var (
	ErrSnapshotNotFound = errors.New("snapshot not found")
	ErrInvalidRef       = errors.New("invalid ref")
	ErrStaleRef         = errors.New("stale ref")
)

type RefState struct {
	Ref        string
	Kind       string
	Role       string
	Name       string
	TagName    string
	Text       string
	Selector   string
	SnapshotID string
}

type PageState struct {
	URL    string
	Title  string
	Groups map[string]any
}

type SnapshotState struct {
	SnapshotID string
	Page       PageState
	Refs       map[string]RefState
}

type snapshotEntry struct {
	current SnapshotState
	stale   map[string]struct{}
}

type State struct {
	mu        sync.Mutex
	snapshots map[string]snapshotEntry
}

func NewState() *State {
	return &State{
		snapshots: map[string]snapshotEntry{},
	}
}

func (s *State) ReplaceSnapshot(runtimeSessionID string, snap SnapshotState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := s.snapshots[runtimeSessionID]
	stale := entry.stale
	if stale == nil {
		stale = map[string]struct{}{}
	}
	for ref := range entry.current.Refs {
		stale[ref] = struct{}{}
	}
	entry.current = snap
	entry.stale = stale
	s.snapshots[runtimeSessionID] = entry
}

func (s *State) ClearSnapshot(runtimeSessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.snapshots[runtimeSessionID]
	if !ok {
		return
	}
	stale := entry.stale
	if stale == nil {
		stale = map[string]struct{}{}
	}
	for ref := range entry.current.Refs {
		stale[ref] = struct{}{}
	}
	entry.current = SnapshotState{}
	entry.stale = stale
	s.snapshots[runtimeSessionID] = entry
}

func (s *State) GetSnapshot(runtimeSessionID string) (SnapshotState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.snapshots[runtimeSessionID]
	if !ok || entry.current.SnapshotID == "" {
		return SnapshotState{}, ErrSnapshotNotFound
	}
	return entry.current, nil
}

func (s *State) GetRef(runtimeSessionID string, ref string) (RefState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.snapshots[runtimeSessionID]
	if !ok || entry.current.SnapshotID == "" {
		if entry.stale != nil {
			if _, wasStale := entry.stale[ref]; wasStale {
				return RefState{}, ErrStaleRef
			}
		}
		return RefState{}, ErrSnapshotNotFound
	}
	if out, ok := entry.current.Refs[ref]; ok {
		return out, nil
	}
	if _, wasStale := entry.stale[ref]; wasStale {
		return RefState{}, ErrStaleRef
	}
	return RefState{}, ErrInvalidRef
}
