package main

import (
	"os"

	"golang.org/x/term"
)

// noColor is set by the --no-color flag; NO_COLOR (the de-facto standard) and a
// non-terminal stdout also disable color. We never emit ANSI into pipes/CI.
var noColor bool

func useColor() bool {
	return !noColor && os.Getenv("NO_COLOR") == "" && term.IsTerminal(int(os.Stdout.Fd()))
}

func colorize(code, s string) string {
	if !useColor() {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func green(s string) string  { return colorize("32", s) }
func yellow(s string) string { return colorize("33", s) }
func red(s string) string    { return colorize("31", s) }
func cyan(s string) string   { return colorize("36", s) }
func blue(s string) string   { return colorize("34", s) }
func bold(s string) string   { return colorize("1", s) }
