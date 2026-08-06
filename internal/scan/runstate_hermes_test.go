package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// write a jobs.json into a fake home and point the provider at it.
func hermesAt(t *testing.T, body string) *hermesProvider {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".hermes", "cron")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "jobs.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	return &hermesProvider{}
}

// The shape Hermes actually writes, taken from a real installed instance.
const hermesIdle = `{"updated_at":"2026-08-06T21:00:00+08:00","jobs":[
 {"id":"gmail-sync","name":"gmail-meeting-calendar-sync","enabled":true,"state":"scheduled",
  "fire_claim":null,
  "last_run_at":"2026-08-06T15:00:12+08:00","last_status":"ok",
  "schedule":{"kind":"cron","expr":"0 6,11,15,20 * * *"}}]}`

const hermesRunning = `{"updated_at":"2026-08-06T21:00:00+08:00","jobs":[
 {"id":"gmail-sync","name":"gmail-meeting-calendar-sync","enabled":true,"state":"running",
  "fire_claim":{"at":"2026-08-06T20:00:00+08:00","by":"machine-1"},
  "last_run_at":"2026-08-06T15:00:12+08:00","last_status":"ok",
  "schedule":{"kind":"cron","expr":"0 6,11,15,20 * * *"}}]}`

// A job in flight must be reported as running, with the start Hermes stamped.
func TestObserveRunSeesAJobInFlight(t *testing.T) {
	h := hermesAt(t, hermesRunning)
	rec, ok := h.ObserveRun(context.Background(), "gmail-sync")
	if !ok {
		t.Fatal("could not read a well-formed jobs.json")
	}
	if !rec.Running {
		t.Error("a job holding a fire_claim was not reported as running")
	}
	want := time.Date(2026, 8, 6, 20, 0, 0, 0, rec.StartedAt.Location())
	if !rec.StartedAt.Equal(want) {
		t.Errorf("StartedAt = %s, want %s (from fire_claim.at)", rec.StartedAt, want)
	}
}

// At rest, the claim is cleared and the completion record stands.
func TestObserveRunSeesACompletedRun(t *testing.T) {
	h := hermesAt(t, hermesIdle)
	rec, ok := h.ObserveRun(context.Background(), "gmail-sync")
	if !ok {
		t.Fatal("could not read a well-formed jobs.json")
	}
	if rec.Running {
		t.Error("a job with no fire_claim was reported as running")
	}
	if rec.CompletedAt.IsZero() {
		t.Error("no completion time from last_run_at")
	}
	if !rec.Succeeded() {
		t.Errorf("Status = %q, want ok", rec.Status)
	}
}

// Matching must work whichever name WakeGuard adopted the job under.
func TestObserveRunMatchesByIDOrSluggedName(t *testing.T) {
	h := hermesAt(t, hermesIdle)
	for _, key := range []string{"gmail-sync", "gmail-meeting-calendar-sync"} {
		if _, ok := h.ObserveRun(context.Background(), key); !ok {
			t.Errorf("did not match job by %q", key)
		}
	}
	if _, ok := h.ObserveRun(context.Background(), "no-such-job"); ok {
		t.Error("matched a job that does not exist")
	}
}

// The degradation contract. Hermes owes us nothing: no schema version, and
// fire_claim is internal state it may rename in a patch release. Every
// unreadable shape must return ok=false so the caller falls back to a fixed
// window rather than reporting a confident wrong duration.
func TestUnreadableStateDegradesRatherThanLying(t *testing.T) {
	cases := map[string]string{
		"not json":            `{{{`,
		"jobs is not a list":  `{"jobs":"nope"}`,
		"empty file":          ``,
		"no jobs key":         `{"updated_at":"2026-08-06T21:00:00+08:00"}`,
		"fire_claim reshaped": `{"jobs":[{"id":"x","fire_claim":"now-a-string"}]}`,
	}
	for name, body := range cases {
		h := hermesAt(t, body)
		if _, ok := h.ObserveRun(context.Background(), "x"); ok {
			t.Errorf("%s: reported success on input it cannot understand", name)
		}
	}
}

// A missing file is not a crash.
func TestNoHermesInstalledIsNotAnError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	h := &hermesProvider{}
	if _, ok := h.ObserveRun(context.Background(), "x"); ok {
		t.Error("claimed a record with no hermes installed")
	}
}

// Duration must never be negative or invented: the estimator trains on it.
func TestDurationIsOnlyReportedWhenBothEndsAreKnown(t *testing.T) {
	now := time.Now()
	if d := (RunRecord{CompletedAt: now}).Duration(); d != 0 {
		t.Errorf("duration without a start = %s, want 0", d)
	}
	if d := (RunRecord{StartedAt: now}).Duration(); d != 0 {
		t.Errorf("duration without a finish = %s, want 0", d)
	}
	backwards := RunRecord{StartedAt: now, CompletedAt: now.Add(-time.Minute)}
	if d := backwards.Duration(); d != 0 {
		t.Errorf("backwards clock produced %s, want 0", d)
	}
	good := RunRecord{StartedAt: now, CompletedAt: now.Add(90 * time.Second)}
	if d := good.Duration(); d != 90*time.Second {
		t.Errorf("duration = %s, want 90s", d)
	}
}
