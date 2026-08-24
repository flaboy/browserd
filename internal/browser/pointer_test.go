package browser

import (
	"math"
	"testing"
	"time"
)

func TestPlanPointerPathHumanizedMovesTowardTarget(t *testing.T) {
	path := planPointerPath(pointerPoint{X: 10, Y: 20}, targetRect{X: 100, Y: 120, Width: 80, Height: 40}, viewportRect{Width: 300, Height: 240}, "humanized", defaultBehaviorProfile().MouseMoveStepDelay)

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
	path := planPointerPath(pointerPoint{X: 10, Y: 20}, targetRect{X: 100, Y: 120, Width: 80, Height: 40}, viewportRect{Width: 300, Height: 240}, "direct", defaultBehaviorProfile().MouseMoveStepDelay)

	if len(path) != 1 {
		t.Fatalf("expected direct path to contain one point, got %d: %+v", len(path), path)
	}
	if path[0].X < 100 || path[0].X > 180 || path[0].Y < 120 || path[0].Y > 160 {
		t.Fatalf("expected direct point inside target rect, got %+v", path[0])
	}
}

func TestPlanPointerPathHumanizedCalculatesStepForOneToThreeSecondMovement(t *testing.T) {
	start := pointerPoint{X: 20, Y: 20}
	delay := defaultBehaviorProfile().MouseMoveStepDelay
	path := planPointerPath(start, targetRect{X: 1260, Y: 690, Width: 80, Height: 40}, viewportRect{Width: 1366, Height: 768}, "humanized", delay)

	previous := start
	maxStep := 0.0
	for i, point := range path {
		step := math.Hypot(point.X-previous.X, point.Y-previous.Y)
		if step < 1 && i < len(path)-1 {
			t.Fatalf("step %d is too small to be visible: %.3fpx from %+v to %+v", i, step, previous, point)
		}
		if step > maxStep {
			maxStep = step
		}
		previous = point
	}
	estimatedDuration := time.Duration(len(path)) * delay
	if estimatedDuration < time.Second || estimatedDuration > 3*time.Second {
		t.Fatalf("expected long movement to take 1-3s, got %s from %d steps", estimatedDuration, len(path))
	}
	if maxStep <= 5 {
		t.Fatalf("expected dynamic K to exceed old fixed 5px cap for long movement, max step %.2fpx", maxStep)
	}
	if maxStep > 18 {
		t.Fatalf("expected dynamic K to stay visually trackable, max step %.2fpx", maxStep)
	}
}

func TestPlanPointerPathHumanizedAvoidsInvisibleMicroSteps(t *testing.T) {
	start := pointerPoint{X: 1024, Y: 314}
	path := planPointerPath(start, targetRect{X: 0, Y: 0, Width: 94, Height: 44}, viewportRect{Width: 1366, Height: 768}, "humanized", defaultBehaviorProfile().MouseMoveStepDelay)

	previous := start
	for i, point := range path {
		step := math.Hypot(point.X-previous.X, point.Y-previous.Y)
		if step < 1 && i < len(path)-1 {
			t.Fatalf("step %d is too small to be visible: %.3fpx from %+v to %+v", i, step, previous, point)
		}
		previous = point
	}
}

func TestPlanPointerPathHumanizedUsesCurvedTrajectory(t *testing.T) {
	start := pointerPoint{X: 120, Y: 650}
	target := targetRect{X: 980, Y: 120, Width: 180, Height: 40}
	end := targetCenter(target)
	path := planPointerPath(start, target, viewportRect{Width: 1366, Height: 768}, "humanized", defaultBehaviorProfile().MouseMoveStepDelay)

	maxDeviation := 0.0
	for _, point := range path {
		deviation := distanceFromLine(point, start, end)
		if deviation > maxDeviation {
			maxDeviation = deviation
		}
	}
	if maxDeviation < 12 {
		t.Fatalf("expected visibly curved path, max deviation %.2fpx in %+v", maxDeviation, path)
	}
}

func TestPlanPointerPathHumanizedEndsWithCorrectionSegment(t *testing.T) {
	start := pointerPoint{X: 683, Y: 384}
	target := targetRect{X: 940, Y: 350, Width: 260, Height: 24}
	path := planPointerPath(start, target, viewportRect{Width: 1366, Height: 768}, "humanized", defaultBehaviorProfile().MouseMoveStepDelay)

	end := targetCenter(target)
	enteredCorrection := false
	for _, point := range path {
		if math.Hypot(point.X-end.X, point.Y-end.Y) <= 14 {
			enteredCorrection = true
			break
		}
	}
	if !enteredCorrection {
		t.Fatalf("expected final correction segment near target center, got %+v", path)
	}
	if len(path) < 80 {
		t.Fatalf("expected long movement to use many small correction-capable steps, got %d", len(path))
	}
}

func distanceFromLine(point pointerPoint, start pointerPoint, end pointerPoint) float64 {
	dx := end.X - start.X
	dy := end.Y - start.Y
	denominator := math.Hypot(dx, dy)
	if denominator == 0 {
		return math.Hypot(point.X-start.X, point.Y-start.Y)
	}
	return math.Abs(dy*point.X-dx*point.Y+end.X*start.Y-end.Y*start.X) / denominator
}
