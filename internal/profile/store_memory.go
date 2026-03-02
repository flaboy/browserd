package profile

import (
	"context"
	"fmt"
	"sync"
)

type memoryObject struct {
	data    []byte
	version string
}

type MemoryStore struct {
	mu          sync.Mutex
	objects     map[string]memoryObject
	lastPutPath string
	seq         int
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		objects: map[string]memoryObject{},
		seq:     1,
	}
}

func (s *MemoryStore) Seed(path string, data []byte, version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	s.objects[path] = memoryObject{data: cp, version: version}
}

func (s *MemoryStore) LastPutPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastPutPath
}

func (s *MemoryStore) Get(_ context.Context, path string) ([]byte, string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[path]
	if !ok {
		return nil, "", false, nil
	}
	cp := make([]byte, len(obj.data))
	copy(cp, obj.data)
	return cp, obj.version, true, nil
}

func (s *MemoryStore) Put(_ context.Context, path string, data []byte, ifMatchVersion string) (string, error) {
	if ifMatchVersion == "" {
		return "", ErrIfMatchRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	obj, ok := s.objects[path]
	if ok {
		if obj.version != ifMatchVersion {
			return "", ErrVersionConflict
		}
	} else {
		if ifMatchVersion != "new" {
			return "", ErrVersionConflict
		}
	}

	newVersion := fmt.Sprintf("v%d", s.seq)
	s.seq++
	cp := make([]byte, len(data))
	copy(cp, data)
	s.objects[path] = memoryObject{data: cp, version: newVersion}
	s.lastPutPath = path
	return newVersion, nil
}
