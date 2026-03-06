package browser

import (
	"testing"

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
