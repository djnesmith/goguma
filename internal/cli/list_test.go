package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/junnam/wakeguard/internal/ipc"
	"github.com/junnam/wakeguard/internal/model"
	"github.com/junnam/wakeguard/internal/render"
)

func listOutput(t *testing.T, resp ipc.JobsListResp) string {
	t.Helper()
	var buf bytes.Buffer
	printJobs(render.NewPlain(&buf), resp, false)
	return buf.String()
}

func view(name, group string) ipc.JobView {
	return ipc.JobView{
		Job: model.Job{ID: name, Name: name, Schedule: "@daily",
			Detection: model.DetectNone, Group: group, Enabled: true},
		ScheduleDisplay: "daily",
	}
}

// TestAnUngroupedListGainsNoHeadings keeps the feature invisible to everyone
// who has not opted into it: an install where no job has a group must read
// exactly as it did before groups existed.
func TestAnUngroupedListGainsNoHeadings(t *testing.T) {
	out := listOutput(t, ipc.JobsListResp{
		Jobs: []ipc.JobView{view("alpha", ""), view("beta", "")},
	})
	if strings.Contains(out, "ungrouped") {
		t.Errorf("a list with no groups labelled itself:\n%s", out)
	}
	if n := strings.Count(out, "\n"); n != 3 { // header + two rows
		t.Errorf("got %d lines, want the plain table:\n%s", n, out)
	}
}

func TestGroupsAreOrderedAndUngroupedComesLast(t *testing.T) {
	out := listOutput(t, ipc.JobsListResp{
		Jobs: []ipc.JobView{
			view("loose", ""),
			view("zeta", "zebra"),
			view("alef", "alpha"),
		},
		Groups: []string{"alpha", "zebra"},
	})

	order := []string{"alpha", "alef", "zebra", "zeta", "ungrouped", "loose"}
	at := 0
	for _, want := range order {
		i := strings.Index(out[at:], want)
		if i < 0 {
			t.Fatalf("%q is missing or out of order in:\n%s", want, out)
		}
		at += i
	}
}

// TestAnEmptyGroupStillGetsAHeading is why the daemon sends group names rather
// than the client deriving them: a group whose last job just moved out would
// otherwise vanish mid-session.
func TestAnEmptyGroupStillGetsAHeading(t *testing.T) {
	out := listOutput(t, ipc.JobsListResp{
		Jobs:   []ipc.JobView{view("alef", "alpha")},
		Groups: []string{"alpha", "abandoned"},
	})
	if !strings.Contains(out, "abandoned") {
		t.Errorf("the empty group was dropped:\n%s", out)
	}
}
