package browser

import "math"

type pointerPoint struct {
	X float64
	Y float64
}

type targetRect struct {
	X      float64
	Y      float64
	Width  float64
	Height float64
}

type viewportRect struct {
	Width  float64
	Height float64
}

func planPointerPath(start pointerPoint, target targetRect, viewport viewportRect, profile string) []pointerPoint {
	end := targetCenter(target)
	end = clampPoint(end, viewport)
	if profile == "direct" {
		return []pointerPoint{end}
	}

	distance := math.Hypot(end.X-start.X, end.Y-start.Y)
	steps := int(distance / 24)
	if steps < 8 {
		steps = 8
	}
	if steps > 48 {
		steps = 48
	}
	path := make([]pointerPoint, 0, steps)
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		eased := easeInOut(t)
		point := pointerPoint{
			X: start.X + (end.X-start.X)*eased,
			Y: start.Y + (end.Y-start.Y)*eased,
		}
		if i != steps {
			jitter := math.Sin(float64(i)*1.618) * 1.2
			point.X += jitter
			point.Y -= jitter / 2
		}
		path = append(path, clampPoint(point, viewport))
	}
	return path
}

func targetCenter(rect targetRect) pointerPoint {
	return pointerPoint{
		X: rect.X + rect.Width/2,
		Y: rect.Y + rect.Height/2,
	}
}

func easeInOut(t float64) float64 {
	if t < 0.5 {
		return 2 * t * t
	}
	return 1 - math.Pow(-2*t+2, 2)/2
}

func clampPoint(point pointerPoint, viewport viewportRect) pointerPoint {
	if point.X < 0 {
		point.X = 0
	}
	if point.Y < 0 {
		point.Y = 0
	}
	if viewport.Width > 0 && point.X > viewport.Width {
		point.X = viewport.Width
	}
	if viewport.Height > 0 && point.Y > viewport.Height {
		point.Y = viewport.Height
	}
	return point
}
