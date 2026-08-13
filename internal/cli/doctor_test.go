package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/ipc"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/paths"
	"github.com/junnam586/goguma/internal/render"
)

// TestDoctorSaysAnIntervalTimeIsEstimated covers the one honest thing that has
// to be said out loud somewhere.
//
// "every 6h" has a cadence and no time of day, so goguma counts from when
// the job was added, and for an adopted job that is when goguma first saw
// it, not when the source scheduler's own clock started. The predicted time
// can therefore be a full interval out. It is not a warning: nothing is broken
// and no command fixes it. But doctor is where a user asks whether a job is
// really covered, so the estimate must not be presented as a certainty there.
func TestDoctorSaysAnIntervalTimeIsEstimated(t *testing.T) {
	var buf bytes.Buffer
	ctx := &Context{Out: render.NewPlain(&buf), Err: render.NewPlain(&buf)}

	fire := time.Now().Add(2 * time.Hour)
	v := ipc.JobView{
		Job: model.Job{
			ID: "watchdog", Name: "watchdog",
			Schedule:  "every 6h",
			Detection: model.DetectNone,
			Enabled:   true,
			Managed:   true,
			CreatedAt: time.Now().Add(-4 * time.Hour),
		},
		NextFire: &fire,
	}
	if got := checkOneJob(ctx, v).detail; !strings.Contains(got, "out of phase") {
		t.Errorf("detail = %q, want it to qualify the predicted time", got)
	}

	// A schedule that names its own time of day is not an estimate, and
	// saying so about one would be noise.
	for _, expr := range []string{"0 9 * * *", "@daily", "@hourly"} {
		v.Job.Schedule = expr
		if got := checkOneJob(ctx, v).detail; strings.Contains(got, "out of phase") {
			t.Errorf("%s: detail = %q, but a calendar schedule fires at a known time", expr, got)
		}
	}
}

// A quarantined jobs.json entry must be reported somewhere a human looks.
// The store keeps it in the file untouched; doctor is the promised place
// that says which entry is broken and why.
func TestDoctorReportsQuarantinedEntries(t *testing.T) {
	layout := paths.Layout{StateDir: t.TempDir()}
	layout.LogDir = layout.StateDir
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	broken := `{"version":1,"jobs":[
	  {"id":"typo","name":"typo","schedule":"0 2 * * *","detection":"pattern","match":"restic (","enabled":true}
	]}`
	if err := os.WriteFile(layout.JobsFile(), []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	ctx := &Context{Out: render.NewPlain(&buf), Err: render.NewPlain(&buf), Layout: layout}
	checks := checkInvalidEntries(ctx)
	if len(checks) != 1 || checks[0].status != checkFail {
		t.Fatalf("checks = %+v, want one failure for the quarantined entry", checks)
	}
	if !strings.Contains(checks[0].detail, "typo") {
		t.Errorf("detail %q does not name the broken job", checks[0].detail)
	}
}
