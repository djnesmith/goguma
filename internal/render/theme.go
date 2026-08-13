package render

// ─────────────────────────────────────────────────────────────────────────────
// THEME: snowscroll
//
// Every visual decision for the command line lives in this file: colours,
// symbols, and the words used for each state. Nothing else in the CLI emits an
// escape sequence or picks a glyph.
//
// The source system is arctic and calm: one accent, everything else quiet
// neutrals. Its stated principles are restraint and honesty, which is why the
// 256-colour values below are the soft variants (72/179/167) rather than the
// saturated ones (40/214/196). A tool that only raises its voice when
// something is genuinely wrong reads as trustworthy; one that shouts at every
// row does not.
// ─────────────────────────────────────────────────────────────────────────────

// Role is a semantic colour plus the symbol that carries the same meaning
// without it.
//
// Both halves matter. Terminal output is frequently piped, redirected, or read
// on a terminal with no colour at all, so colour is treated as additive and
// the symbol is the primary signal. A state that could only be distinguished
// by hue would simply vanish under `NO_COLOR`.
type Role struct {
	// Color256 is an xterm-256 palette index, empty for roles that carry no
	// colour of their own.
	Color256 string
	// ANSI16 is the fallback index for terminals that advertise only the
	// basic sixteen colours.
	ANSI16 string
	// Bold applies the bold attribute, which survives every colour setting.
	Bold bool
	// Symbol is what identifies this state when colour is unavailable.
	Symbol string
}

// The palette. Named by meaning rather than hue, so a call site asks for
// "this failed" rather than "make this red".
var (
	// Accent is the primary highlight: next wake, commands to run, the value
	// the eye should land on. It carries no symbol because it marks a value
	// rather than a state, position and content already identify it.
	//
	// 110 is a mid arctic blue. The brand accent proper is a much lighter
	// #8FB6D4, which is designed as a fill behind dark text and is unreadable
	// as foreground on a light terminal.
	Accent = Role{Color256: "110", ANSI16: "12"}

	// Good is healthy, succeeded, working right now. A pine green rather than
	// a spring green, desaturated to the same key as the arctic blue so that
	// "working" reads as calm rather than celebratory.
	Good = Role{Color256: "72", ANSI16: "2", Symbol: "✓"}

	// Warn needs attention but is not broken: a cold-start ceiling, a job that
	// hit its cap, a wake deliberately held back. Deep amber, clearly warm
	// against an otherwise cool palette, which is what makes it register at a
	// glance without a loud saturation.
	Warn = Role{Color256: "179", ANSI16: "3", Symbol: "!"}

	// Danger is failed, cut out, will not work. Terracotta rather than
	// fire-engine red: muted enough to belong to the palette, distinct enough
	// to stop the reader.
	Danger = Role{Color256: "167", ANSI16: "1", Symbol: "✗"}

	// Muted is secondary text: labels, timestamps, explanations. Also the
	// correct role for "there is nothing to show here", which is why an
	// unobservable job's empty duration is muted and never danger.
	Muted = Role{Color256: "245", ANSI16: "8"}

	// Heading carries no colour at all, only bold.
	//
	// Deliberate: section titles and table headers must stay legible when
	// colour is switched off entirely, and bold is the one attribute that
	// survives every terminal and every NO_COLOR setting. Colour would add
	// nothing a reader could not already see from weight and position.
	Heading = Role{Bold: true}
)

// symbolSet marks state in lists. Chosen to stay legible in a single column
// and to be distinguishable from each other without colour.
type symbolSet struct {
	Holding    string
	Idle       string
	Disabled   string
	OK         string
	Warn       string
	Danger     string
	Bullet     string
	Arrow      string
	SparkChars []rune
}

var symbols = symbolSet{
	Holding:  "●", // sleep is being held off
	Idle:     "○", // the machine can sleep normally
	Disabled: "·",
	OK:       "✓",
	Warn:     "!",
	Danger:   "✗",
	Bullet:   "•",
	Arrow:    "→",
	// The ramp used by the duration sparkline in `history`.
	SparkChars: []rune("▁▂▃▄▅▆▇█"),
}

// asciiSymbols is substituted when the terminal cannot be trusted with
// non-ASCII. A mojibake status column is worse than a plain one.
var asciiSymbols = symbolSet{
	Holding:    "*",
	Idle:       "o",
	Disabled:   ".",
	OK:         "+",
	Warn:       "!",
	Danger:     "x",
	Bullet:     "-",
	Arrow:      "->",
	SparkChars: []rune("._-=+*#@"),
}

// colorDepth is how much colour the terminal can be trusted with.
type colorDepth int

const (
	// depthNone emits no escape sequences whatsoever. This is the piped case,
	// where output is data: a stray bold sequence in a file or in grep's input
	// is corruption, not styling.
	depthNone colorDepth = iota

	// depthBold is a real terminal with colour switched off: NO_COLOR, or
	// TERM=dumb. Bold still renders and still helps, and NO_COLOR asks for the
	// absence of colour rather than the absence of all formatting. Section
	// headings stay legible as a result.
	depthBold

	depth16  // the basic ANSI set
	depth256 // the xterm-256 palette
)

// sequence renders a role as an SGR escape for the given depth.
//
// Returns empty at depthNone so piped output is byte-identical to plain text.
// At depthBold only the bold attribute survives, which is what keeps section
// headings readable for someone who set NO_COLOR on a real terminal.
func (r Role) sequence(d colorDepth) string {
	if d == depthNone {
		return ""
	}
	if d == depthBold {
		if r.Bold {
			return "\x1b[1m"
		}
		return ""
	}

	seq := ""
	if r.Bold {
		seq += "\x1b[1m"
	}
	switch {
	case d >= depth256 && r.Color256 != "":
		seq += "\x1b[38;5;" + r.Color256 + "m"
	case r.ANSI16 != "":
		seq += "\x1b[" + ansiForeground(r.ANSI16) + "m"
	}
	return seq
}

// ansiForeground maps a basic colour index onto its SGR foreground code.
// 0-7 are 30-37; 8-15 are the bright set, 90-97.
func ansiForeground(idx string) string {
	switch idx {
	case "0":
		return "30"
	case "1":
		return "31"
	case "2":
		return "32"
	case "3":
		return "33"
	case "4":
		return "34"
	case "5":
		return "35"
	case "6":
		return "36"
	case "7":
		return "37"
	case "8":
		return "90"
	case "9":
		return "91"
	case "10":
		return "92"
	case "11":
		return "93"
	case "12":
		return "94"
	case "13":
		return "95"
	case "14":
		return "96"
	case "15":
		return "97"
	default:
		return "39" // default foreground
	}
}

const sgrReset = "\x1b[0m"
