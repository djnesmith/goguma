package daemon

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/junnam/wakeguard/internal/config"
	"github.com/junnam/wakeguard/internal/model"
	"github.com/junnam/wakeguard/internal/paths"
	"github.com/junnam/wakeguard/internal/store"
)

func testDaemon(t *testing.T) *Daemon {
	t.Helper()
	layout := paths.Layout{StateDir: t.TempDir()}
	layout.LogDir = layout.StateDir
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	d := New("test", slog.New(slog.NewTextHandler(io.Discard, nil)), store.New(layout), nil)
	d.cfg = config.Default()
	return d
}

// TestFinishHoldDoesNotDeadlock guards a bug that wedged the daemon at exactly
// the wrong moment.
//
// finishHoldLocked runs with d.mu held for writing. It used to reach the
// webhook dispatch through an accessor that takes a read lock, and Go's
// RWMutex is not reentrant — so the ceiling and never-detected paths, the two
// that exist to stop a hung job pinning the machine awake, deadlocked the
// process the first time they fired.
//
// A webhook URL is configured here because the dispatch is only reached when
// one is set, which is what made the bug easy to miss locally.
func TestFinishHoldDoesNotDeadlock(t *testing.T) {
	for _, outcome := range []model.Outcome{
		model.OutcomeCeiling,
		model.OutcomeNeverDetected,
		model.OutcomeOK,
		model.OutcomeFailed,
		model.OutcomeCutout,
	} {
		t.Run(string(outcome), func(t *testing.T) {
			d := testDaemon(t)
			d.cfg.WebhookURL = "http://127.0.0.1:1/never-listening"

			job := &model.Job{ID: "j", Name: "j", Detection: model.DetectMark, Enabled: true}
			h := &hold{
				job: job, openedAt: time.Now().Add(-time.Minute),
				ceiling: 30 * time.Second, detected: true,
				startedAt: time.Now().Add(-30 * time.Second),
			}
			d.holds[job.ID] = h

			done := make(chan struct{})
			go func() {
				d.mu.Lock()
				d.finishHoldLocked(h, time.Now(), outcome)
				d.mu.Unlock()
				close(done)
			}()

			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatalf("finishHoldLocked deadlocked on outcome %q", outcome)
			}

			// The lock must be genuinely free afterwards, not merely released
			// by a goroutine that is still blocked somewhere else.
			acquired := make(chan struct{})
			go func() {
				d.mu.Lock()
				d.mu.Unlock()
				close(acquired)
			}()
			select {
			case <-acquired:
			case <-time.After(2 * time.Second):
				t.Fatal("d.mu was still held after finishHoldLocked returned")
			}

			// Persistence happens off the control path; wait for it so the
			// temp directory is not removed out from under a live write.
			d.bg.Wait()
		})
	}
}

func TestHoldDeadlineTracksDetection(t *testing.T) {
	fire := time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)
	h := &hold{
		job:            &model.Job{ID: "j"},
		fireAt:         fire,
		openedAt:       fire.Add(-90 * time.Second),
		ceiling:        60 * time.Second,
		detectDeadline: detectDeadlineFor(fire, 60*time.Second),
	}

	// Before detection the window is bounded by the detect deadline, so a job
	// that never appears cannot hold the machine indefinitely.
	if got := h.deadline(); !got.Equal(h.detectDeadline) {
		t.Errorf("undetected deadline = %s, want the detect deadline %s", got, h.detectDeadline)
	}

	// Once the job is seen, the ceiling is measured from its real start rather
	// than from the fire time, so a job that starts late still gets its full
	// allowance.
	started := fire.Add(45 * time.Second)
	h.detected, h.startedAt = true, started
	if got, want := h.deadline(), started.Add(60*time.Second); !got.Equal(want) {
		t.Errorf("detected deadline = %s, want %s", got, want)
	}
}

func TestDetectionGraceIsGenerous(t *testing.T) {
	fire := time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)

	// A machine that has just woken from deep sleep can take tens of seconds
	// to run cron. A short ceiling must not shrink the detection window below
	// that, or a perfectly good job gets reported as never-detected.
	got := detectDeadlineFor(fire, 5*time.Second)
	if got.Sub(fire) < detectionGrace {
		t.Errorf("detect deadline is only %s after fire; want at least %s",
			got.Sub(fire), detectionGrace)
	}

	// A long ceiling should widen it rather than being clamped down.
	got = detectDeadlineFor(fire, 30*time.Minute)
	if got.Sub(fire) != 30*time.Minute {
		t.Errorf("detect deadline = %s after fire, want 30m", got.Sub(fire))
	}
}

func TestFinishRecordsHoldAndRuntimeSeparately(t *testing.T) {
	opened := time.Date(2026, 8, 4, 8, 58, 30, 0, time.UTC)
	started := opened.Add(90 * time.Second)
	ended := started.Add(42 * time.Second)

	h := &hold{
		job:      &model.Job{ID: "j", Detection: model.DetectMark},
		openedAt: opened, detected: true, startedAt: started,
		ceiling: 90 * time.Second, wokeMachine: true,
	}
	run := h.finish(ended, model.OutcomeOK, -1)

	// Runtime is what the estimator learns from; hold duration is what costs
	// battery. Conflating them would hide the cost of imperfect detection.
	if run.Duration.D() != 42*time.Second {
		t.Errorf("duration = %s, want 42s", run.Duration)
	}
	if run.HoldDuration.D() != 132*time.Second {
		t.Errorf("hold duration = %s, want 132s (90s wake buffer + 42s runtime)", run.HoldDuration)
	}
	if !run.WokeMachine {
		t.Error("WokeMachine should be preserved — it is the tool's value metric")
	}
}

func TestFinishUndetectedRunHasNoRuntime(t *testing.T) {
	opened := time.Now().Add(-3 * time.Minute)
	h := &hold{job: &model.Job{ID: "j"}, openedAt: opened, ceiling: time.Minute}

	run := h.finish(time.Now(), model.OutcomeNeverDetected, -1)
	if run.Duration != 0 {
		t.Errorf("duration = %s, want 0 for a job that was never seen", run.Duration)
	}
	if run.HoldDuration.D() < 2*time.Minute {
		t.Errorf("hold duration = %s, want the full window that was wasted", run.HoldDuration)
	}
	if run.Outcome.TrainsEstimator() {
		t.Error("a never-detected run must not train the estimator")
	}
}
