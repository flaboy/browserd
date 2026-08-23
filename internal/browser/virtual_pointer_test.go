package browser

import "testing"

func TestVirtualPointerSnapshotIsSanitized(t *testing.T) {
	state := pointerState{
		Point:       pointerPoint{X: 42.5, Y: 81.25},
		Viewport:    viewportRect{Width: 1366, Height: 768},
		Initialized: true,
		ButtonDown:  true,
	}

	got := newVirtualPointerSnapshot("rt_1", state)

	if got.RuntimeSessionID != "" {
		t.Fatalf("snapshot must not expose runtime session id, got %q", got.RuntimeSessionID)
	}
	if got.X != 42.5 || got.Y != 81.25 {
		t.Fatalf("unexpected pointer coordinates: %+v", got)
	}
	if got.ViewportWidth != 1366 || got.ViewportHeight != 768 {
		t.Fatalf("unexpected viewport: %+v", got)
	}
	if !got.Visible || !got.ButtonDown {
		t.Fatalf("expected visible button-down pointer: %+v", got)
	}
}
