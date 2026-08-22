package browser

import (
	"context"
	"time"
)

type behaviorProfile struct {
	MouseMoveStepDelay   time.Duration
	MouseBeforeDownDelay time.Duration
	MouseDownUpDelay     time.Duration
	ActionAfterDelay     time.Duration
	TypeRuneDelay        time.Duration
	KeyAfterDelay        time.Duration
}

func defaultBehaviorProfile() behaviorProfile {
	return behaviorProfile{
		MouseMoveStepDelay:   12 * time.Millisecond,
		MouseBeforeDownDelay: 160 * time.Millisecond,
		MouseDownUpDelay:     70 * time.Millisecond,
		ActionAfterDelay:     220 * time.Millisecond,
		TypeRuneDelay:        55 * time.Millisecond,
		KeyAfterDelay:        80 * time.Millisecond,
	}
}

func sleepBehavior(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func splitTextRunes(text string) []string {
	parts := make([]string, 0, len(text))
	for _, r := range text {
		parts = append(parts, string(r))
	}
	return parts
}
