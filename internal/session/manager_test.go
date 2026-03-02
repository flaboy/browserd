package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"browserd/internal/profile"
)

func TestManager_CreateAndCommit_UsesSingleProfileTGZKey(t *testing.T) {
	tmp := t.TempDir()
	seedDir := filepath.Join(tmp, "seed")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		t.Fatalf("mkdir seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(seedDir, "state.txt"), []byte("seed"), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	seedTGZ := filepath.Join(tmp, "seed.tgz")
	if err := profile.PackDirToTGZ(seedDir, seedTGZ); err != nil {
		t.Fatalf("pack seed tgz: %v", err)
	}
	seedData, err := os.ReadFile(seedTGZ)
	if err != nil {
		t.Fatalf("read seed tgz: %v", err)
	}

	store := profile.NewMemoryStore()
	profilePath := "s3://private/browser-sessions/t1/c1/s1/profile.tgz"
	store.Seed(profilePath, seedData, "v1")

	mgr := NewManager(ManagerOptions{
		Store:      store,
		Workdir:    filepath.Join(tmp, "work"),
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})

	out, err := mgr.Create(CreateInput{
		S3ProfilePath: profilePath,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out.ResolvedVersion != "v1" {
		t.Fatalf("resolvedVersion mismatch: %s", out.ResolvedVersion)
	}

	commitOut, err := mgr.Commit(out.RuntimeSessionID, CommitInput{IfMatchVersion: "v1"})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if commitOut.NewVersion == "" {
		t.Fatalf("expected newVersion")
	}
	if store.LastPutPath() != profilePath {
		t.Fatalf("last put path mismatch: %s", store.LastPutPath())
	}
}

func TestManager_CommitRejectsStaleIfMatchVersion(t *testing.T) {
	tmp := t.TempDir()
	store := profile.NewMemoryStore()
	profilePath := "s3://private/browser-sessions/t2/c2/s2/profile.tgz"
	seedDir := filepath.Join(tmp, "seed2")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		t.Fatalf("mkdir seed2: %v", err)
	}
	if err := os.WriteFile(filepath.Join(seedDir, "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write seed2 file: %v", err)
	}
	seedTGZ := filepath.Join(tmp, "seed2.tgz")
	if err := profile.PackDirToTGZ(seedDir, seedTGZ); err != nil {
		t.Fatalf("pack seed2 tgz: %v", err)
	}
	seedData, err := os.ReadFile(seedTGZ)
	if err != nil {
		t.Fatalf("read seed2 tgz: %v", err)
	}
	store.Seed(profilePath, seedData, "v10")

	mgr := NewManager(ManagerOptions{
		Store:      store,
		Workdir:    filepath.Join(tmp, "work"),
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})
	out, err := mgr.Create(CreateInput{
		S3ProfilePath: profilePath,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = mgr.Commit(out.RuntimeSessionID, CommitInput{IfMatchVersion: "old"})
	if err == nil {
		t.Fatalf("expected version conflict")
	}
	if err != ErrProfileVersionConflict {
		t.Fatalf("expected ErrProfileVersionConflict, got %v", err)
	}
}

func TestManager_CreateRejectsNonProfileTGZPath(t *testing.T) {
	mgr := NewManager(ManagerOptions{
		Store:      profile.NewMemoryStore(),
		Workdir:    t.TempDir(),
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})
	_, err := mgr.Create(CreateInput{
		S3ProfilePath: "s3://private/browser-sessions/t/c/s/profile.zip",
	})
	if err == nil {
		t.Fatalf("expected invalid request")
	}
}

func TestMemoryStore_PutRequiresIfMatch(t *testing.T) {
	store := profile.NewMemoryStore()
	path := "s3://private/browser-sessions/t/c/s/profile.tgz"
	store.Seed(path, []byte("x"), "v1")
	_, err := store.Put(context.Background(), path, []byte("y"), "stale")
	if err == nil {
		t.Fatalf("expected conflict")
	}
}
