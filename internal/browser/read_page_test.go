package browser

import (
	"strings"
	"testing"
)

func TestBrowserSnapshotRuntimeScriptIncludesPackageSnapshotBuilder(t *testing.T) {
	if !strings.Contains(browserSnapshotRuntimeScript, "captureSnapshotTree") {
		t.Fatalf("expected runtime script to include package tree capture")
	}
	if strings.Contains(browserSnapshotRuntimeScript, "captureSnapshotRows") {
		t.Fatalf("new runtime must not emit the legacy row representation")
	}
}

func TestBrowserSnapshotRuntimeScriptReturnsPageAndRefs(t *testing.T) {
	if !strings.Contains(browserSnapshotRuntimeScript, "page: encodeCompactPage(captured.page)") {
		t.Fatalf("expected runtime script to return page and refs envelope")
	}
}
