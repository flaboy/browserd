package browser

import (
	"context"
	"testing"
	"time"
)

func TestDefaultBehaviorProfileHasNonZeroHumanTiming(t *testing.T) {
	profile := defaultBehaviorProfile()

	if profile.MouseMoveStepDelay <= 0 {
		t.Fatalf("expected mouse move step delay to be non-zero")
	}
	if profile.MouseBeforeDownDelay <= 0 {
		t.Fatalf("expected mouse before-down delay to be non-zero")
	}
	if profile.MouseDownUpDelay <= 0 {
		t.Fatalf("expected mouse down/up delay to be non-zero")
	}
	if profile.ActionAfterDelay <= 0 {
		t.Fatalf("expected action after delay to be non-zero")
	}
	if profile.TypeRuneDelay <= 0 {
		t.Fatalf("expected per-rune typing delay to be non-zero")
	}
}

func TestBehaviorSleepHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := sleepBehavior(ctx, time.Second)

	if err == nil {
		t.Fatalf("expected canceled sleep to return an error")
	}
}

func TestSplitTextRunesPreservesUnicodeCharacters(t *testing.T) {
	got := splitTextRunes("A你B")

	want := []string{"A", "你", "B"}
	if len(got) != len(want) {
		t.Fatalf("unexpected rune count: got %+v want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected rune at %d: got %q want %q", i, got[i], want[i])
		}
	}
}
