package browser

type VirtualPointerSnapshot struct {
	X              float64 `json:"x"`
	Y              float64 `json:"y"`
	ViewportWidth  float64 `json:"viewportWidth"`
	ViewportHeight float64 `json:"viewportHeight"`
	Visible        bool    `json:"visible"`
	ButtonDown     bool    `json:"buttonDown"`

	// RuntimeSessionID is intentionally omitted from JSON and remains empty in
	// live-view responses. It exists only to make accidental exposure testable.
	RuntimeSessionID string `json:"-"`
}

type PointerSubscription struct {
	C     <-chan VirtualPointerSnapshot
	close func()
}

func (s PointerSubscription) Close() {
	if s.close != nil {
		s.close()
	}
}

func newVirtualPointerSnapshot(_ string, state pointerState) VirtualPointerSnapshot {
	return VirtualPointerSnapshot{
		X:              state.Point.X,
		Y:              state.Point.Y,
		ViewportWidth:  state.Viewport.Width,
		ViewportHeight: state.Viewport.Height,
		Visible:        state.Initialized,
		ButtonDown:     state.ButtonDown,
	}
}
