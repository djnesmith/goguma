package estimate

import (
	"strconv"
	"strings"
)

// These render the human-readable Reason string attached to every ceiling.
// The reason is shown verbatim by `goguma list --explain` and in the GUI,
// so a ceiling is never an unexplained number the user has to trust blindly.

func itoa(n int) string { return strconv.Itoa(n) }

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// trimFloat renders a multiplier without trailing zeros: 1.2 not 1.200000.
func trimFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}
