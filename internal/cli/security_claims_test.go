package cli

import (
	"regexp"
	"strings"
	"testing"
)

// The count of privileged messages is the same everywhere it is claimed.
//
// Setup tells somebody, at the moment they are deciding whether to type their
// password, that the root helper "answers six messages". SECURITY.md says the
// same and lists them. Both are prose; the helper's dispatcher is the fact.
//
// A seventh message added later would not break anything visible. It would
// quietly turn the one screen where goguma asks for root into a screen that
// understates what it is asking for, which is the worst place in the product
// for a claim to drift.
func TestTheClaimedHelperMessageCountIsTheRealOne(t *testing.T) {
	root := repoRoot(t)

	// What the helper actually answers, read from its dispatcher.
	dispatch := readDoc(t, root, "internal/helper/service.go")
	ops := map[string]bool{}
	for _, m := range regexp.MustCompile(`ipc\.(OpHelper[A-Za-z]+|OpPing)`).
		FindAllStringSubmatch(dispatch, -1) {
		ops[m[1]] = true
	}
	if len(ops) == 0 {
		t.Fatal("found no helper messages; this test is looking in the wrong place")
	}

	words := map[int]string{5: "five", 6: "six", 7: "seven", 8: "eight"}
	want := words[len(ops)]
	if want == "" {
		t.Fatalf("helper answers %d messages, which this test cannot spell", len(ops))
	}

	for _, f := range []string{"internal/cli/install.go", "SECURITY.md"} {
		body := readDoc(t, root, f)
		if !strings.Contains(body, "answers "+want+" messages") &&
			!strings.Contains(body, "answers exactly "+want+" messages") {
			t.Errorf("%s does not say the helper answers %s messages, but it answers %d.\n"+
				"The count is stated to somebody deciding whether to grant root.",
				f, want, len(ops))
		}
	}
}
