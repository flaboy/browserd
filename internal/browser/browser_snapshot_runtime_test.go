package browser

import (
	"strings"
	"testing"
)

func TestBrowserSnapshotRuntimeComesFromPackage(t *testing.T) {
	if browserSnapshotRuntimeScript == "" {
		t.Fatal("expected browser snapshot runtime script")
	}
	if !strings.Contains(browserSnapshotRuntimeScript, "document") {
		t.Fatalf("expected runtime script to reference document")
	}
	if strings.Contains(strings.ToLower(browserSnapshotRuntimeScript), "browserd local snapshot") {
		t.Fatalf("expected runtime script to come from browser-snapshot package")
	}
}
