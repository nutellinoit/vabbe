package main

import (
	"strings"
	"testing"
)

// In tests stdout isn't a terminal, so color must never be emitted — this guards
// against leaking ANSI escapes into pipes/CI.
func TestColorPlainWhenNotTTY(t *testing.T) {
	if got := green("ok"); got != "ok" || strings.Contains(got, "\x1b") {
		t.Errorf("green() leaked color off a TTY: %q", got)
	}
	noColor = true
	defer func() { noColor = false }()
	if got := red("x"); got != "x" {
		t.Errorf("red() with noColor: %q", got)
	}
}
