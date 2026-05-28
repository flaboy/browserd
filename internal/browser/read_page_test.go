package browser

import (
	"strings"
	"testing"
)

func TestBrowserSnapshotRuntimeScriptIncludesPackageSnapshotBuilder(t *testing.T) {
	if !strings.Contains(browserSnapshotRuntimeScript, "captureSnapshotRows") {
		t.Fatalf("expected runtime script to include package row capture")
	}
	if !strings.Contains(browserSnapshotRuntimeScript, "buildPageSnapshot") {
		t.Fatalf("expected runtime script to include package page snapshot builder")
	}
}

func TestBrowserSnapshotRuntimeScriptReturnsPageAndRefs(t *testing.T) {
	if !strings.Contains(browserSnapshotRuntimeScript, "return buildPageSnapshot(rows, location.href, document.title)") {
		t.Fatalf("expected runtime script to return page and refs envelope")
	}
}
