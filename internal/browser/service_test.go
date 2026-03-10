package browser

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	browserrt "browserd/internal/runtime"
)

func TestBuildChromeArgs_IncludesNoSandboxAndProfileDir(t *testing.T) {
	args := buildChromeArgs("/tmp/profile")

	hasNoSandbox := false
	hasUserDataDir := false
	for _, arg := range args {
		if arg == "--no-sandbox" {
			hasNoSandbox = true
		}
		if arg == "--user-data-dir=/tmp/profile" {
			hasUserDataDir = true
		}
	}

	if !hasNoSandbox {
		t.Fatalf("expected --no-sandbox in chrome args")
	}
	if !hasUserDataDir {
		t.Fatalf("expected user-data-dir arg")
	}
}

func TestWaitForDevToolsWS_ReturnsWebSocketURLFromActivePortFile(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte("12345\n/devtools/browser/abc\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	got, err := waitForDevToolsWS(dir, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("expected ready websocket, got %v", err)
	}
	if got != "ws://127.0.0.1:12345/devtools/browser/abc" {
		t.Fatalf("unexpected ws url: %s", got)
	}
}

func TestBuildChromeArgs_KeepsAboutBlankBootstrapPage(t *testing.T) {
	args := buildChromeArgs("/tmp/profile")
	if args[len(args)-1] != "about:blank" {
		t.Fatalf("expected about:blank bootstrap page, got %+v", args)
	}
}

func TestSnapshotOutput_UsesPageAsSingleStructure(t *testing.T) {
	out := SnapshotOutput{
		SnapshotID: "snap_1",
		Page: PageSnapshot{
			URL:   "https://example.com",
			Title: "Example",
			Groups: map[string]PageTable{
				"buttons": {
					Columns: []string{"ref", "tag", "text"},
					Rows:    [][]any{{"e1", "BUTTON", "Submit"}},
				},
			},
		},
	}

	if out.Page.URL == "" || out.Page.Title == "" {
		t.Fatalf("page metadata missing")
	}
	if _, ok := out.Page.Groups["buttons"]; !ok {
		t.Fatalf("expected buttons group")
	}
}

func TestValidateActionRef_RejectsTextRefForClick(t *testing.T) {
	err := validateActionRef("click", browserrt.RefState{
		Ref:  "t1",
		Kind: "text",
	})
	if err == nil {
		t.Fatalf("expected invalid ref for click on text ref")
	}
	if err != browserrt.ErrInvalidRef {
		t.Fatalf("expected ErrInvalidRef, got %v", err)
	}
}

func TestValidateActionRef_AllowsTextRefForScrollIntoView(t *testing.T) {
	err := validateActionRef("scrollIntoView", browserrt.RefState{
		Ref:  "t1",
		Kind: "text",
	})
	if err != nil {
		t.Fatalf("expected scrollIntoView to allow text refs, got %v", err)
	}
}
