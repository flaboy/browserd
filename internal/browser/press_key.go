package browser

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/chromedp/chromedp/kb"
)

// pressKeyBindings maps W3C KeyboardEvent.key names to chromedp keyboard
// encodings. chromedp encodes key strings rune by rune, so a name such as
// "Enter" must be translated before it reaches SendKeys, otherwise the five
// characters E, n, t, e, r are typed into the focused element.
//
// Space has no kb constant because its W3C key value is the space rune itself.
var pressKeyBindings = map[string]string{
	"Enter":      kb.Enter,
	"Tab":        kb.Tab,
	"Escape":     kb.Escape,
	"Backspace":  kb.Backspace,
	"Delete":     kb.Delete,
	"ArrowUp":    kb.ArrowUp,
	"ArrowDown":  kb.ArrowDown,
	"ArrowLeft":  kb.ArrowLeft,
	"ArrowRight": kb.ArrowRight,
	"Home":       kb.Home,
	"End":        kb.End,
	"PageUp":     kb.PageUp,
	"PageDown":   kb.PageDown,
	"Space":      " ",
}

// pressKeyHintAliases only feeds the error message. Aliases are never accepted:
// one key has exactly one correct spelling.
var pressKeyHintAliases = map[string]string{
	"return":   "Enter",
	"esc":      "Escape",
	"del":      "Delete",
	"up":       "ArrowUp",
	"down":     "ArrowDown",
	"left":     "ArrowLeft",
	"right":    "ArrowRight",
	"spacebar": "Space",
	"pgup":     "PageUp",
	"pgdn":     "PageDown",
	"pagedn":   "PageDown",
}

// normalizePressKey converts a caller supplied key into the encoding chromedp
// expects. Named keys must match the table exactly; any single printable
// character passes through so site level shortcuts such as "/" keep working.
// Anything else fails instead of being typed as literal text.
func normalizePressKey(key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("%w: press requires a key; %s", ErrInvalidKey, pressKeySupportedHint())
	}
	if encoded, ok := pressKeyBindings[key]; ok {
		return encoded, nil
	}
	if r, size := utf8.DecodeRuneInString(key); size == len(key) && r != utf8.RuneError && unicode.IsPrint(r) {
		return key, nil
	}
	if strings.Contains(key, "+") {
		return "", fmt.Errorf("%w: modifier combinations such as %q are not supported; press one key at a time, for example {\"action\":\"press\",\"ref\":\"e1\",\"key\":\"Enter\"}", ErrInvalidKey, key)
	}
	if suggestion, ok := suggestPressKey(key); ok {
		return "", fmt.Errorf("%w: unsupported key %q; did you mean %q? %s", ErrInvalidKey, key, suggestion, pressKeySupportedHint())
	}
	return "", fmt.Errorf("%w: unsupported key %q; %s", ErrInvalidKey, key, pressKeySupportedHint())
}

func suggestPressKey(key string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(key))
	if lower == "" {
		return "", false
	}
	if canonical, ok := pressKeyHintAliases[lower]; ok {
		return canonical, true
	}
	for _, name := range supportedPressKeys() {
		if strings.ToLower(name) == lower {
			return name, true
		}
	}
	for _, name := range supportedPressKeys() {
		if strings.HasPrefix(strings.ToLower(name), lower) {
			return name, true
		}
	}
	return "", false
}

func supportedPressKeys() []string {
	names := make([]string, 0, len(pressKeyBindings))
	for name := range pressKeyBindings {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func pressKeySupportedHint() string {
	return fmt.Sprintf("supported keys: %s; or a single printable character such as \"a\" or \"/\"", strings.Join(supportedPressKeys(), ", "))
}
