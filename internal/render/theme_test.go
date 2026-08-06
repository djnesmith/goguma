package render

import (
	"strings"
	"testing"
)

func TestRoleDegradesWithTheTerminal(t *testing.T) {
	tests := []struct {
		role  Role
		depth colorDepth
		want  string
	}{
		// 256-colour terminals get the palette proper.
		{Accent, depth256, "\x1b[38;5;110m"},
		{Good, depth256, "\x1b[38;5;72m"},
		{Warn, depth256, "\x1b[38;5;179m"},
		{Danger, depth256, "\x1b[38;5;167m"},
		{Muted, depth256, "\x1b[38;5;245m"},

		// Sixteen-colour terminals get the basic set. Emitting a 256 index
		// here renders as the wrong colour or as literal garbage.
		{Accent, depth16, "\x1b[94m"},
		{Good, depth16, "\x1b[32m"},
		{Warn, depth16, "\x1b[33m"},
		{Danger, depth16, "\x1b[31m"},
		{Muted, depth16, "\x1b[90m"},

		// Piped: nothing at all, so the output is byte-identical to plain
		// text and safe to put through grep or into a file.
		{Accent, depthNone, ""},
		{Good, depthNone, ""},
		{Danger, depthNone, ""},
		{Heading, depthNone, ""},

		// A real terminal with NO_COLOR: bold survives, colour does not.
		{Heading, depthBold, "\x1b[1m"},
		{Good, depthBold, ""},
		{Danger, depthBold, ""},
	}
	for _, tc := range tests {
		if got := tc.role.sequence(tc.depth); got != tc.want {
			t.Errorf("sequence(depth %d) = %q, want %q", tc.depth, got, tc.want)
		}
	}
}

func TestHeadingSurvivesNoColor(t *testing.T) {
	// Headings carry no colour on purpose: bold is the one attribute that
	// works on every terminal and under NO_COLOR, so section titles stay
	// legible when everything else flattens to plain text.
	if Heading.Color256 != "" || Heading.ANSI16 != "" {
		t.Error("Heading must carry no colour")
	}
	// Bold survives everywhere a terminal can show it. It is dropped only when
	// piped, where any escape would be corruption rather than formatting.
	for _, d := range []colorDepth{depthBold, depth16, depth256} {
		got := Heading.sequence(d)
		if !strings.Contains(got, "\x1b[1m") {
			t.Errorf("at depth %d Heading = %q, want it to include bold", d, got)
		}
	}
	if got := Heading.sequence(depthNone); got != "" {
		t.Errorf("piped Heading = %q, want no escapes at all", got)
	}
}

func TestStateRolesAllCarryASymbol(t *testing.T) {
	// Colour is additive; the symbol is the primary signal. A state
	// distinguishable only by hue disappears entirely when piped, and is
	// invisible to a colourblind reader even when it is not.
	for name, role := range map[string]Role{"good": Good, "warn": Warn, "danger": Danger} {
		if role.Symbol == "" {
			t.Errorf("%s has no symbol, so it would vanish without colour", name)
		}
	}
	// Warn and danger must not merely differ by colour: "hit its ceiling" and
	// "never ran" call for different actions.
	if Warn.Symbol == Danger.Symbol {
		t.Error("warn and danger share a symbol, so they are colour-only distinct")
	}
}

func TestAccentAndMutedCarryNoSymbol(t *testing.T) {
	// These mark a value or de-emphasise text rather than announcing a state,
	// so a glyph would be noise. Position and content already identify them.
	if Accent.Symbol != "" || Muted.Symbol != "" {
		t.Error("accent and muted should not carry state symbols")
	}
}

func TestPlainRendererEmitsNoEscapes(t *testing.T) {
	var b strings.Builder
	r := NewPlain(&b)

	out := r.Good("ok") + r.Warn("careful") + r.Danger("failed") +
		r.Accent("value") + r.Muted("aside") + r.Heading("title")
	if strings.Contains(out, "\x1b") {
		t.Errorf("plain renderer emitted an escape sequence: %q", out)
	}
	if !strings.Contains(out, "ok") || !strings.Contains(out, "failed") {
		t.Error("plain renderer dropped content")
	}
}

func TestSymbolsAreDistinctWithinASet(t *testing.T) {
	// Two states sharing a glyph would be indistinguishable in a column of
	// piped output, where the glyph is all there is.
	for name, s := range map[string]symbolSet{"unicode": symbols, "ascii": asciiSymbols} {
		seen := map[string]string{}
		for label, glyph := range map[string]string{
			"holding": s.Holding, "idle": s.Idle, "disabled": s.Disabled,
			"ok": s.OK, "warn": s.Warn, "danger": s.Danger,
		} {
			if prev, dup := seen[glyph]; dup {
				t.Errorf("%s set: %q and %q share the glyph %q", name, prev, label, glyph)
			}
			seen[glyph] = label
		}
	}
}

func TestAsciiFallbackIsPureASCII(t *testing.T) {
	// The point of the fallback is terminals that mangle non-ASCII, so it
	// must not itself contain any.
	s := asciiSymbols
	all := s.Holding + s.Idle + s.Disabled + s.OK + s.Warn + s.Danger +
		s.Bullet + s.Arrow + string(s.SparkChars)
	for _, r := range all {
		if r > 127 {
			t.Errorf("ascii fallback contains non-ASCII rune %q", r)
		}
	}
}

func TestPaletteUsesTheSoftVariants(t *testing.T) {
	// The source system's principle is restraint. The saturated 256 indices
	// (40 / 214 / 196) read as traffic lights; the soft ones keep the CLI in
	// the same calm register as the app.
	loud := map[string]string{"40": "good", "214": "warn", "196": "danger", "9": "danger"}
	for _, role := range []Role{Good, Warn, Danger, Accent} {
		if name, isLoud := loud[role.Color256]; isLoud {
			t.Errorf("%s uses the saturated index %s; the palette calls for the soft variant",
				name, role.Color256)
		}
	}
}
