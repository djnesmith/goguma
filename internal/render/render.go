// Package render is the CLI's presentation layer.
//
// Output is rich when stdout is a terminal and plain when it is not, decided
// once at construction. Piping `goguma list` into grep or a script yields
// clean text with no escape sequences and no box drawing, without the user
// needing a --no-color flag.
package render

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// Renderer writes styled output to a stream.
type Renderer struct {
	w     io.Writer
	depth colorDepth
	sym   symbolSet
	width int
}

// New builds a Renderer for w, auto-detecting capabilities.
func New(w io.Writer) *Renderer {
	sym := symbols
	if !supportsUnicode() {
		sym = asciiSymbols
	}
	return &Renderer{w: w, depth: detectColorDepth(w), sym: sym, width: terminalWidth(w)}
}

// NewPlain builds a Renderer that never styles, for tests and --no-color.
func NewPlain(w io.Writer) *Renderer {
	return &Renderer{w: w, depth: depthNone, sym: asciiSymbols, width: 80}
}

func (r *Renderer) Writer() io.Writer { return r.w }
func (r *Renderer) Color() bool       { return r.depth > depthNone }
func (r *Renderer) Width() int        { return r.width }

// style wraps s in a role's escape sequence, degrading with the terminal.
// Every styled write in the package goes through here.
func (r *Renderer) style(s string, role Role) string {
	if s == "" {
		return s
	}
	seq := role.sequence(r.depth)
	if seq == "" {
		return s
	}
	return seq + s + sgrReset
}

// Semantic text helpers. Callers ask for meaning, never for a colour.
func (r *Renderer) Accent(s string) string  { return r.style(s, Accent) }
func (r *Renderer) Good(s string) string    { return r.style(s, Good) }
func (r *Renderer) Warn(s string) string    { return r.style(s, Warn) }
func (r *Renderer) Danger(s string) string  { return r.style(s, Danger) }
func (r *Renderer) Muted(s string) string   { return r.style(s, Muted) }
func (r *Renderer) Bold(s string) string    { return r.style(s, Role{Bold: true}) }
func (r *Renderer) Heading(s string) string { return r.style(s, Heading) }

// Symbols exposes the active glyph set.
func (r *Renderer) Sym() symbolSet { return r.sym }

func (r *Renderer) Printf(format string, a ...any) {
	fmt.Fprintf(r.w, format, a...)
}

func (r *Renderer) Println(a ...any) {
	fmt.Fprintln(r.w, a...)
}

// Line writes a single line.
func (r *Renderer) Line(s string) {
	fmt.Fprintln(r.w, s)
}

// Blank writes an empty line.
func (r *Renderer) Blank() { fmt.Fprintln(r.w) }

// Field writes an aligned "label  value" pair, the shape used throughout
// `status`.
func (r *Renderer) Field(label, value string, labelWidth int) {
	pad := labelWidth - utf8.RuneCountInString(label)
	if pad < 0 {
		pad = 0
	}
	fmt.Fprintf(r.w, "  %s%s  %s\n", r.Muted(label), strings.Repeat(" ", pad), value)
}

// Section writes a heading.
func (r *Renderer) Section(title string) {
	fmt.Fprintf(r.w, "%s\n", r.Heading(title))
}

// Problem writes a warning block with its suggested fix, indented so the fix
// reads as a command the user can copy.
func (r *Renderer) Problem(message, fix string) {
	fmt.Fprintf(r.w, "  %s %s\n", r.Warn(r.sym.Warn), message)
	if fix != "" {
		fmt.Fprintf(r.w, "    %s %s\n", r.Muted("fix:"), r.Accent(fix))
	}
}

// Errorf writes an error to stderr in the standard shape.
func Errorf(format string, a ...any) {
	r := New(os.Stderr)
	fmt.Fprintf(os.Stderr, "%s %s\n", r.Danger("goguma:"), fmt.Sprintf(format, a...))
}

// Sparkline renders values as a compact inline chart.
//
// Used by `history` to make a duration trend visible at a glance, the point
// being to see whether a job's ceiling has converged on its real runtime, or
// whether one run is wildly out of line with the rest.
func (r *Renderer) Sparkline(values []float64) string {
	if len(values) == 0 {
		return ""
	}
	min, max := values[0], values[0]
	for _, v := range values {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	ramp := r.sym.SparkChars
	span := max - min
	var b strings.Builder
	for _, v := range values {
		idx := 0
		if span > 0 {
			idx = int((v - min) / span * float64(len(ramp)-1))
		}
		if idx < 0 {
			idx = 0
		}
		if idx >= len(ramp) {
			idx = len(ramp) - 1
		}
		b.WriteRune(ramp[idx])
	}
	return b.String()
}

// Truncate shortens s to n display columns, with an ellipsis when cut. Used
// for command lines in tables, which are frequently longer than a terminal.
func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	runes := []rune(s)
	return string(runes[:n-1]) + "…"
}
