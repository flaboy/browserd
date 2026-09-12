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

func TestBrowserSnapshotRuntimeExposesLinkImages(t *testing.T) {
	for _, marker := range []string{
		"formatVersion: 2",
		"img.currentSrc || img.src",
		"attrs.src",
	} {
		if !strings.Contains(browserSnapshotRuntimeScript, marker) {
			t.Fatalf("snapshot must preserve observed link images: missing %q", marker)
		}
	}
}

func TestBrowserSnapshotRuntimeExposesHiddenFileInputs(t *testing.T) {
	for _, marker := range []string{
		"tag === 'input' && type === 'file'",
		"File inputs remain addressable",
		"accept|placeholder",
	} {
		if !strings.Contains(browserSnapshotRuntimeScript, marker) {
			t.Fatalf("snapshot runtime must expose hidden file inputs with marker %q", marker)
		}
	}
}

func TestBrowserSnapshotRuntimeExposesContentEditableInputs(t *testing.T) {
	for _, marker := range []string{
		"state.editable = true",
		"el.isContentEditable",
		"el.getAttribute('data-placeholder')",
	} {
		if !strings.Contains(browserSnapshotRuntimeScript, marker) {
			t.Fatalf("snapshot runtime must expose contenteditable editors with marker %q", marker)
		}
	}
}
