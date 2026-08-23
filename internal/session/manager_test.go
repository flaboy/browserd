package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"browserd/internal/fingerprint"
	"browserd/internal/profile"
)

func testFingerprintConfig() fingerprint.Config {
	return fingerprint.Config{
		Seed:                "fp_seed_1",
		Locale:              "en-US",
		Languages:           []string{"en-US", "en"},
		AcceptLanguage:      "en-US,en;q=0.9",
		Timezone:            "America/New_York",
		Platform:            "Win32",
		OS:                  "Windows",
		UserAgent:           "Mozilla/5.0 test",
		ViewportWidth:       1366,
		ViewportHeight:      768,
		ScreenWidth:         1366,
		ScreenHeight:        768,
		DeviceScaleFactor:   1,
		HardwareConcurrency: 8,
		DeviceMemory:        8,
		WebGLVendor:         "Google Inc.",
		WebGLRenderer:       "ANGLE Test",
	}
}

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
	profilePath := "/browser-sessions/t1/c1/s1/profile.tgz"
	store.Seed(profilePath, seedData, "v1")

	mgr := NewManager(ManagerOptions{
		Store:      store,
		Workdir:    filepath.Join(tmp, "work"),
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})

	out, err := mgr.Create(CreateInput{
		ProfilePath: profilePath,
		Fingerprint: testFingerprintConfig(),
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

func TestManager_CreateAcceptsLogicalProfilePath(t *testing.T) {
	tmp := t.TempDir()
	store := profile.NewMemoryStore()
	profilePath := "/browser-sessions/dev/douyin/local/profile.tgz"
	mgr := NewManager(ManagerOptions{
		Store:      store,
		Workdir:    filepath.Join(tmp, "work"),
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})

	out, err := mgr.Create(CreateInput{
		ProfilePath: profilePath,
		Fingerprint: testFingerprintConfig(),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	info, err := mgr.Get(out.RuntimeSessionID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if info.ProfilePath != profilePath {
		t.Fatalf("profile path mismatch: %s", info.ProfilePath)
	}

	if _, err := mgr.Commit(out.RuntimeSessionID, CommitInput{IfMatchVersion: "new"}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if store.LastPutPath() != profilePath {
		t.Fatalf("last put path mismatch: %s", store.LastPutPath())
	}
}

func TestManager_CommitRejectsStaleIfMatchVersion(t *testing.T) {
	tmp := t.TempDir()
	store := profile.NewMemoryStore()
	profilePath := "/browser-sessions/t2/c2/s2/profile.tgz"
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
		ProfilePath: profilePath,
		Fingerprint: testFingerprintConfig(),
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
		ProfilePath: "/browser-sessions/t/c/s/profile.zip",
		Fingerprint: testFingerprintConfig(),
	})
	if err == nil {
		t.Fatalf("expected invalid request")
	}
}

func TestManager_CreateRejectsProfilePathWithoutLeadingSlash(t *testing.T) {
	mgr := NewManager(ManagerOptions{
		Store:      profile.NewMemoryStore(),
		Workdir:    t.TempDir(),
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})
	_, err := mgr.Create(CreateInput{
		ProfilePath: "browser-sessions/t/c/s/profile.tgz",
		Fingerprint: testFingerprintConfig(),
	})
	if err == nil {
		t.Fatalf("expected invalid request")
	}
}

func TestManager_CreateRejectsProfilePathWithScheme(t *testing.T) {
	mgr := NewManager(ManagerOptions{
		Store:      profile.NewMemoryStore(),
		Workdir:    t.TempDir(),
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})
	_, err := mgr.Create(CreateInput{
		ProfilePath: "s3://private/browser-sessions/t/c/s/profile.tgz",
		Fingerprint: testFingerprintConfig(),
	})
	if err == nil {
		t.Fatalf("expected invalid request")
	}
}

func TestManager_CreateRejectsMissingFingerprintConfig(t *testing.T) {
	mgr := NewManager(ManagerOptions{
		Store:      profile.NewMemoryStore(),
		Workdir:    t.TempDir(),
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})
	_, err := mgr.Create(CreateInput{
		ProfilePath: "/browser-sessions/t/c/s/profile.tgz",
	})
	if err == nil {
		t.Fatalf("expected invalid request")
	}
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestManager_CreateStoresFingerprintAndProxyServer(t *testing.T) {
	mgr := NewManager(ManagerOptions{
		Store:      profile.NewMemoryStore(),
		Workdir:    t.TempDir(),
		CDPBaseURL: "ws://browserd:9222/devtools/browser",
	})
	out, err := mgr.Create(CreateInput{
		ProfilePath: "/browser-sessions/t/c/s/profile.tgz",
		Fingerprint: testFingerprintConfig(),
		ProxyServer: "http://user:pass@proxy.example.com:8080",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	info, err := mgr.Get(out.RuntimeSessionID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if info.Fingerprint.Seed != "fp_seed_1" || info.Fingerprint.Timezone != "America/New_York" {
		t.Fatalf("fingerprint mismatch: %+v", info)
	}
	if info.ProxyServer != "http://user:pass@proxy.example.com:8080" {
		t.Fatalf("proxy server mismatch: %+v", info)
	}
}

func TestMemoryStore_PutRequiresIfMatch(t *testing.T) {
	store := profile.NewMemoryStore()
	path := "/browser-sessions/t/c/s/profile.tgz"
	store.Seed(path, []byte("x"), "v1")
	_, err := store.Put(context.Background(), path, []byte("y"), "stale")
	if err == nil {
		t.Fatalf("expected conflict")
	}
}
