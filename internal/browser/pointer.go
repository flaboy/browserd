package browser

import (
	"math"
	"time"
)

const humanizedPointerMinStepPX = 1.0
const humanizedPointerTargetDuration = 2 * time.Second
const humanizedPointerCorrectionRadiusPX = 14.0
const humanizedPointerCurveSamplePX = 2.0

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
	Width          float64
	Height         float64
	ContentOffsetX float64
	ContentOffsetY float64
}

func planPointerPath(start pointerPoint, target targetRect, viewport viewportRect, profile string, stepDelay time.Duration) []pointerPoint {
	end := targetCenter(target)
	end = clampPoint(end, viewport)
	if profile == "direct" {
		return []pointerPoint{end}
	}
	return humanizedPointerPath(start, end, viewport, stepDelay)
}

func humanizedPointerPath(start pointerPoint, end pointerPoint, viewport viewportRect, stepDelay time.Duration) []pointerPoint {
	distance := math.Hypot(end.X-start.X, end.Y-start.Y)
	if distance == 0 {
		return []pointerPoint{end}
	}
	stepSize := pointerStepSize(distance, stepDelay)

	correctionStart := pointToward(start, end, math.Max(0, distance-humanizedPointerCorrectionRadiusPX))
	control := pointerControlPoint(start, correctionStart, distance)
	mainSteps := pointerSteps(math.Hypot(correctionStart.X-start.X, correctionStart.Y-start.Y), stepSize)
	correctionSteps := pointerSteps(math.Hypot(end.X-correctionStart.X, end.Y-correctionStart.Y), stepSize)

	path := make([]pointerPoint, 0, mainSteps+correctionSteps)
	path = appendResampledCurve(path, start, control, correctionStart, viewport, stepSize)
	path = appendResampledPolyline(path, []pointerPoint{correctionStart, end}, viewport, stepSize)
	return path
}

func appendResampledCurve(path []pointerPoint, start pointerPoint, control pointerPoint, end pointerPoint, viewport viewportRect, stepSize float64) []pointerPoint {
	distance := math.Hypot(end.X-start.X, end.Y-start.Y)
	samples := int(math.Ceil(distance / humanizedPointerCurveSamplePX))
	if samples < 32 {
		samples = 32
	}
	polyline := make([]pointerPoint, 0, samples+1)
	for i := 0; i <= samples; i++ {
		t := float64(i) / float64(samples)
		polyline = append(polyline, quadraticBezier(start, control, end, t))
	}
	return appendResampledPolyline(path, polyline, viewport, stepSize)
}

func pointerStepSize(distance float64, stepDelay time.Duration) float64 {
	if stepDelay <= 0 {
		return humanizedPointerMinStepPX
	}
	targetSteps := humanizedPointerTargetDuration.Seconds() / stepDelay.Seconds()
	if targetSteps <= 0 {
		return humanizedPointerMinStepPX
	}
	stepSize := distance / targetSteps
	if stepSize < humanizedPointerMinStepPX {
		return humanizedPointerMinStepPX
	}
	return stepSize
}

func pointerSteps(distance float64, stepSize float64) int {
	if stepSize < humanizedPointerMinStepPX {
		stepSize = humanizedPointerMinStepPX
	}
	steps := int(math.Ceil(distance / stepSize))
	if steps < 4 {
		steps = 4
	}
	return steps
}

func appendResampledPolyline(path []pointerPoint, polyline []pointerPoint, viewport viewportRect, stepSize float64) []pointerPoint {
	if len(polyline) < 2 {
		return path
	}
	if stepSize < humanizedPointerMinStepPX {
		stepSize = humanizedPointerMinStepPX
	}
	distanceToNext := stepSize
	lastEmitted := polyline[0]
	for i := 1; i < len(polyline); i++ {
		segmentStart := polyline[i-1]
		segmentEnd := polyline[i]
		for {
			segmentDistance := math.Hypot(segmentEnd.X-segmentStart.X, segmentEnd.Y-segmentStart.Y)
			if segmentDistance < distanceToNext || segmentDistance == 0 {
				distanceToNext -= segmentDistance
				break
			}
			t := distanceToNext / segmentDistance
			next := pointerPoint{
				X: segmentStart.X + (segmentEnd.X-segmentStart.X)*t,
				Y: segmentStart.Y + (segmentEnd.Y-segmentStart.Y)*t,
			}
			next = clampPoint(next, viewport)
			path = append(path, next)
			lastEmitted = next
			segmentStart = next
			distanceToNext = stepSize
		}
	}
	final := clampPoint(polyline[len(polyline)-1], viewport)
	if math.Hypot(final.X-lastEmitted.X, final.Y-lastEmitted.Y) > 0 {
		path = append(path, final)
	}
	return path
}

func pointerControlPoint(start pointerPoint, end pointerPoint, distance float64) pointerPoint {
	mid := midpoint(start, end)
	dx := end.X - start.X
	dy := end.Y - start.Y
	length := math.Hypot(dx, dy)
	if length == 0 {
		return mid
	}
	normalX := -dy / length
	normalY := dx / length
	direction := 1.0
	if start.X+start.Y > end.X+end.Y {
		direction = -1
	}
	offset := distance * 0.12
	if offset < 16 {
		offset = 16
	}
	if offset > 90 {
		offset = 90
	}
	return pointerPoint{
		X: mid.X + normalX*offset*direction,
		Y: mid.Y + normalY*offset*direction,
	}
}

func quadraticBezier(start pointerPoint, control pointerPoint, end pointerPoint, t float64) pointerPoint {
	inverse := 1 - t
	return pointerPoint{
		X: inverse*inverse*start.X + 2*inverse*t*control.X + t*t*end.X,
		Y: inverse*inverse*start.Y + 2*inverse*t*control.Y + t*t*end.Y,
	}
}

func pointToward(start pointerPoint, end pointerPoint, distanceFromStart float64) pointerPoint {
	total := math.Hypot(end.X-start.X, end.Y-start.Y)
	if total == 0 {
		return end
	}
	if distanceFromStart < 0 {
		distanceFromStart = 0
	}
	if distanceFromStart > total {
		distanceFromStart = total
	}
	t := distanceFromStart / total
	return pointerPoint{
		X: start.X + (end.X-start.X)*t,
		Y: start.Y + (end.Y-start.Y)*t,
	}
}

func limitStep(previous pointerPoint, next pointerPoint) pointerPoint {
	dx := next.X - previous.X
	dy := next.Y - previous.Y
	distance := math.Hypot(dx, dy)
	if distance <= humanizedPointerMinStepPX || distance == 0 {
		return next
	}
	scale := humanizedPointerMinStepPX / distance
	return pointerPoint{
		X: previous.X + dx*scale,
		Y: previous.Y + dy*scale,
	}
}

func midpoint(a pointerPoint, b pointerPoint) pointerPoint {
	return pointerPoint{X: (a.X + b.X) / 2, Y: (a.Y + b.Y) / 2}
}

func minimumJerk(t float64) float64 {
	if t < 0 {
		return 0
	}
	if t > 1 {
		return 1
	}
	return 10*math.Pow(t, 3) - 15*math.Pow(t, 4) + 6*math.Pow(t, 5)
}

func targetCenter(rect targetRect) pointerPoint {
	return pointerPoint{
		X: rect.X + rect.Width/2,
		Y: rect.Y + rect.Height/2,
	}
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
