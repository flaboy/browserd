package browser

import (
	"errors"
	"strings"
	"testing"

	"github.com/chromedp/chromedp/kb"
)

func TestNormalizePressKey_EncodesNamedKeys(t *testing.T) {
	cases := []struct {
		key  string
		want string
	}{
		{"Enter", kb.Enter},
		{"Tab", kb.Tab},
		{"Escape", kb.Escape},
		{"Backspace", kb.Backspace},
		{"Delete", kb.Delete},
		{"ArrowUp", kb.ArrowUp},
		{"ArrowDown", kb.ArrowDown},
		{"ArrowLeft", kb.ArrowLeft},
		{"ArrowRight", kb.ArrowRight},
		{"Home", kb.Home},
		{"End", kb.End},
		{"PageUp", kb.PageUp},
		{"PageDown", kb.PageDown},
		{"Space", " "},
	}
	for _, c := range cases {
		got, err := normalizePressKey(c.key)
		if err != nil {
			t.Fatalf("normalizePressKey(%q) returned error: %v", c.key, err)
		}
		if got != c.want {
			t.Fatalf("normalizePressKey(%q) = %q, want %q", c.key, got, c.want)
		}
		if got == c.key && c.key != "Space" {
			t.Fatalf("normalizePressKey(%q) returned the raw name, which chromedp would type as literal text", c.key)
		}
	}
}

func TestNormalizePressKey_PassesThroughSinglePrintableCharacters(t *testing.T) {
	for _, key := range []string{"a", "Z", "1", "/", "?", " ", "中"} {
		got, err := normalizePressKey(key)
		if err != nil {
			t.Fatalf("normalizePressKey(%q) returned error: %v", key, err)
		}
		if got != key {
			t.Fatalf("normalizePressKey(%q) = %q, want unchanged", key, got)
		}
	}
}

func TestNormalizePressKey_RejectsUnsupportedKeys(t *testing.T) {
	for _, key := range []string{"", "NotAKey", "F5", "Shift", "\x00"} {
		got, err := normalizePressKey(key)
		if err == nil {
			t.Fatalf("normalizePressKey(%q) = %q, want error", key, got)
		}
		if !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("normalizePressKey(%q) error = %v, want ErrInvalidKey", key, err)
		}
	}
}

func TestNormalizePressKey_RejectsAliasesWithActionableHint(t *testing.T) {
	cases := []struct {
		key      string
		expected string
	}{
		{"enter", "Enter"},
		{"ENTER", "Enter"},
		{"Return", "Enter"},
		{"esc", "Escape"},
		{"up", "ArrowUp"},
		{"spacebar", "Space"},
	}
	for _, c := range cases {
		_, err := normalizePressKey(c.key)
		if err == nil {
			t.Fatalf("normalizePressKey(%q) accepted an alias; one key must have exactly one spelling", c.key)
		}
		if !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("normalizePressKey(%q) error = %v, want ErrInvalidKey", c.key, err)
		}
		if !strings.Contains(err.Error(), `did you mean "`+c.expected+`"`) {
			t.Fatalf("normalizePressKey(%q) error = %q, want a suggestion of %q", c.key, err.Error(), c.expected)
		}
	}
}

func TestNormalizePressKey_RejectsModifierCombinationsExplicitly(t *testing.T) {
	_, err := normalizePressKey("Control+s")
	if err == nil {
		t.Fatal("normalizePressKey(\"Control+s\") returned no error")
	}
	if !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("error = %v, want ErrInvalidKey", err)
	}
	if !strings.Contains(err.Error(), "modifier combinations") {
		t.Fatalf("error = %q, want it to state that modifier combinations are unsupported", err.Error())
	}
}

func TestNormalizePressKey_ErrorListsSupportedKeys(t *testing.T) {
	_, err := normalizePressKey("NotAKey")
	if err == nil {
		t.Fatal("normalizePressKey(\"NotAKey\") returned no error")
	}
	for _, name := range []string{"Enter", "Escape", "PageDown", "Space"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("error = %q, want it to list supported key %q", err.Error(), name)
		}
	}
}
