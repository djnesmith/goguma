package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/junnam586/goguma/internal/render"
)

// TestTheStarAskIsSilentWhenPiped is the rule that keeps it from being a defect.
//
// `goguma status` is run from scripts, cron lines and status bars. A line about
// GitHub in the middle of somebody's monitoring output is not a request, it is
// output they have to filter, and the sort of thing that gets a tool removed
// rather than starred.
func TestTheStarAskIsSilentWhenPiped(t *testing.T) {
	var buf bytes.Buffer
	printStarAsk(render.NewPlain(&buf))
	if buf.Len() != 0 {
		t.Errorf("wrote %q to a non-terminal; it must say nothing there", buf.String())
	}
}

// TestTheStarAskNamesTheRepository, since a request nobody can act on is just
// a line of text.
func TestTheStarAskPointsSomewhere(t *testing.T) {
	if !strings.HasPrefix(repoURL, "https://github.com/") {
		t.Errorf("repoURL = %q, which is not a GitHub repository", repoURL)
	}
}
