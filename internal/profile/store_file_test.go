package profile

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileStorePutGetRoundTrip(t *testing.T) {
	store, err := NewFileStore(FileStoreConfig{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	path := "/browser-sessions/dev/douyin/local/profile.tgz"
	body := []byte("profile archive")

	version, err := store.Put(context.Background(), path, body, "new")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if !strings.HasPrefix(version, "sha256:") {
		t.Fatalf("expected sha256 version, got %q", version)
	}

	got, gotVersion, found, err := store.Get(context.Background(), path)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !found {
		t.Fatalf("expected profile to be found")
	}
	if gotVersion != version {
		t.Fatalf("version mismatch: %q != %q", gotVersion, version)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("body mismatch: %q", string(got))
	}
}

func TestFileStoreRejectsFileURI(t *testing.T) {
	store, err := NewFileStore(FileStoreConfig{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	path := "file:///browser-sessions/dev/xhs/local/profile.tgz"
	if _, err := store.Put(context.Background(), path, []byte("x"), "new"); err == nil {
		t.Fatalf("expected file uri to fail")
	}
}

func TestFileStoreRejectsPathTraversal(t *testing.T) {
	store, err := NewFileStore(FileStoreConfig{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	for _, path := range []string{
		"/browser-sessions/../outside/profile.tgz",
		"/../outside/profile.tgz",
		"/browser-sessions/dev/../outside/profile.tgz",
	} {
		if _, err := store.Put(context.Background(), path, []byte("x"), "new"); err == nil {
			t.Fatalf("expected traversal path to fail: %s", path)
		}
	}
}

func TestFileStoreRejectsSchemeURI(t *testing.T) {
	store, err := NewFileStore(FileStoreConfig{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	if _, _, _, err := store.Get(context.Background(), "s3://private/browser-sessions/a/profile.tgz"); err == nil {
		t.Fatalf("expected scheme uri to fail")
	}
}

func TestFileStoreRejectsStaleIfMatchVersion(t *testing.T) {
	store, err := NewFileStore(FileStoreConfig{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	path := "/browser-sessions/dev/douyin/local/profile.tgz"
	if _, err := store.Put(context.Background(), path, []byte("v1"), "new"); err != nil {
		t.Fatalf("put initial: %v", err)
	}
	_, err = store.Put(context.Background(), path, []byte("v2"), "stale")
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict, got %v", err)
	}
}

func TestFileStorePersistsOnDisk(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileStore(FileStoreConfig{Root: root})
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	path := "/browser-sessions/dev/douyin/local/profile.tgz"
	if _, err := store.Put(context.Background(), path, []byte("x"), "new"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "browser-sessions", "dev", "douyin", "local", "profile.tgz")); err != nil {
		t.Fatalf("expected profile file on disk: %v", err)
	}
}
