package render

import (
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// detectColorDepth decides how much colour the terminal can be trusted with.
//
// The distinction matters because the palette is chosen from the xterm-256
// range. On a terminal that only advertises sixteen colours, emitting a 256
// index either renders as the wrong colour or as literal garbage, so those
// callers get the basic set instead.
func detectColorDepth(w io.Writer) colorDepth {
	if !supportsColor(w) {
		// A real terminal with colour turned off can still show bold, and
		// NO_COLOR asks for no colour rather than no formatting. A pipe gets
		// nothing at all, because there the output is data.
		if isTerminal(w) {
			return depthBold
		}
		return depthNone
	}
	term := os.Getenv("TERM")
	switch {
	case os.Getenv("COLORTERM") != "":
		// truecolor or 24bit; either way 256 is safe.
		return depth256
	case strings.Contains(term, "256color"), strings.Contains(term, "truecolor"):
		return depth256
	case term == "":
		// FORCE_COLOR with no TERM, e.g. some CI. The basic set is the safe
		// assumption rather than the flattering one.
		return depth16
	default:
		return depth16
	}
}

// supportsColor decides whether to emit escape sequences.
//
// The rules, in priority order, follow the conventions users already expect:
//   - NO_COLOR set (any value) disables colour (https://no-color.org)
//   - FORCE_COLOR or CLICOLOR_FORCE enables it even when piped, which is how
//     CI systems and tools like `less -R` opt in
//   - TERM=dumb disables it
//   - otherwise, colour only when the stream is a real terminal
//
// This is what makes `goguma list | grep` produce clean text without the
// user having to remember a flag.
func supportsColor(w io.Writer) bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	if os.Getenv("FORCE_COLOR") != "" || os.Getenv("CLICOLOR_FORCE") != "" {
		return true
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return isTerminal(w)
}

// supportsUnicode reports whether box-drawing and symbol glyphs are safe.
func supportsUnicode() bool {
	for _, v := range []string{os.Getenv("LC_ALL"), os.Getenv("LC_CTYPE"), os.Getenv("LANG")} {
		if strings.Contains(strings.ToUpper(v), "UTF-8") || strings.Contains(strings.ToUpper(v), "UTF8") {
			return true
		}
	}
	// macOS terminals are UTF-8 by default and frequently ship with none of
	// those variables set, so an unset locale should not force ASCII there.
	return isDarwin
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	_, err := unix.IoctlGetTermios(int(f.Fd()), ioctlReadTermios)
	return err == nil
}

// terminalWidth returns the usable column count, defaulting to 80 when the
// stream is not a terminal so piped output has a stable shape.
func terminalWidth(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok {
		return 80
	}
	ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 {
		if c := os.Getenv("COLUMNS"); c != "" {
			if n := atoiSafe(c); n > 0 {
				return n
			}
		}
		return 80
	}
	return int(ws.Col)
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
