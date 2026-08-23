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

func TestBrowserSnapshotRuntimeExposesHiddenFileInputs(t *testing.T) {
	for _, marker := range []string{
		"tag === 'input' && type === 'file'",
		"accept: normalize(el.getAttribute('accept') || '', 200)",
		"['ref', 'tag', 'type', 'accept', 'value', 'placeholder']",
	} {
		if !strings.Contains(browserSnapshotRuntimeScript, marker) {
			t.Fatalf("snapshot runtime must expose hidden file inputs with marker %q", marker)
		}
	}
}
