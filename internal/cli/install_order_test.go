package cli

import (
	"strings"
	"testing"
)

// Setup runs its steps in the order the plan gives them.
//
// The progress output groups consecutive steps of the same privilege into one
// line, which is a reporting decision. Implementing it by collecting all the
// unprivileged steps and then all the privileged ones would produce identical
// output today, because the plan already happens to be ordered that way, and
// would silently reorder an installation the first time it was not — writing a
// LaunchAgent before the binary it points at, for instance.
func TestSetupNeverReordersItsSteps(t *testing.T) {
	root := repoRoot(t)
	src := readDoc(t, root, "internal/cli/install.go")

	// The loop indexes the plan directly rather than building filtered slices.
	if !strings.Contains(src, "for i := 0; i < len(plan.Steps); {") {
		t.Error("the step loop no longer walks plan.Steps in order")
	}
	for _, bad := range []string{
		"if s.Privileged == privileged {",
		"runSteps(\"installing goguma\", false)",
	} {
		if strings.Contains(src, bad) {
			t.Errorf("steps are being filtered by privilege before running (%q), "+
				"which reorders the install whenever the plan interleaves them", bad)
		}
	}
}
