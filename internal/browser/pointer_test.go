package browser

import "testing"

func TestPlanPointerPathHumanizedMovesTowardTarget(t *testing.T) {
	path := planPointerPath(pointerPoint{X: 10, Y: 20}, targetRect{X: 100, Y: 120, Width: 80, Height: 40}, viewportRect{Width: 300, Height: 240}, "humanized")

	if len(path) < 8 {
		t.Fatalf("expected humanized path to contain multiple move points, got %d: %+v", len(path), path)
	}
	end := path[len(path)-1]
	if end.X < 100 || end.X > 180 || end.Y < 120 || end.Y > 160 {
		t.Fatalf("expected final point inside target rect, got %+v", end)
	}
	for _, point := range path {
		if point.X < 0 || point.X > 300 || point.Y < 0 || point.Y > 240 {
			t.Fatalf("point escaped viewport: %+v in %+v", point, path)
		}
	}
}

func TestPlanPointerPathDirectOnlyReturnsTargetPoint(t *testing.T) {
	path := planPointerPath(pointerPoint{X: 10, Y: 20}, targetRect{X: 100, Y: 120, Width: 80, Height: 40}, viewportRect{Width: 300, Height: 240}, "direct")

	if len(path) != 1 {
		t.Fatalf("expected direct path to contain one point, got %d: %+v", len(path), path)
	}
	if path[0].X < 100 || path[0].X > 180 || path[0].Y < 120 || path[0].Y > 160 {
		t.Fatalf("expected direct point inside target rect, got %+v", path[0])
	}
}
