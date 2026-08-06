package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/junnam/wakeguard/internal/ipc"
	"github.com/junnam/wakeguard/internal/model"
	"github.com/junnam/wakeguard/internal/render"
)

// TestDoctorSaysAnIntervalTimeIsEstimated covers the one honest thing that has
// to be said out loud somewhere.
//
// "every 6h" has a cadence and no time of day, so WakeGuard counts from when
// the job was added — and for an adopted job that is when WakeGuard first saw
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
