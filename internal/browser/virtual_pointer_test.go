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

func TestPointerSnapshotReturnsLatestVirtualPointer(t *testing.T) {
	svc := NewServiceWithOptions(ServiceOptions{})
	svc.setPointer("rt_1", pointerPoint{X: 10, Y: 20}, viewportRect{Width: 100, Height: 80})

	got, ok := svc.PointerSnapshot("rt_1")

	if !ok {
		t.Fatal("expected pointer snapshot")
	}
	if got.X != 10 || got.Y != 20 || got.ViewportWidth != 100 || got.ViewportHeight != 80 {
		t.Fatalf("unexpected snapshot: %+v", got)
	}
	if !got.Visible {
		t.Fatalf("expected visible pointer: %+v", got)
	}
}

func TestPointerSubscriptionReceivesMovementSnapshots(t *testing.T) {
	svc := NewServiceWithOptions(ServiceOptions{})
	sub, err := svc.SubscribePointer("rt_1")
	if err != nil {
		t.Fatalf("subscribe pointer: %v", err)
	}
	defer sub.Close()

	svc.setPointer("rt_1", pointerPoint{X: 11, Y: 22}, viewportRect{Width: 100, Height: 80})

	select {
	case got := <-sub.C:
		if got.X != 11 || got.Y != 22 {
			t.Fatalf("unexpected snapshot: %+v", got)
		}
	default:
		t.Fatal("expected pointer snapshot to be delivered")
	}
}
