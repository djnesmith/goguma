package daemon

import (
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/model"
)

// Pause cancels the OS wake, so the bookkeeping must say so. Leaving
// nextWake/wakeOK stale made resume a no-op: scheduleNextWake saw the same
// target still marked registered and short-circuited, status printed a wake
// that did not exist, and the machine slept through the next job.
func TestPauseClearsTheWakeBookkeeping(t *testing.T) {
	d := testDaemon(t)
	at := time.Now().Add(time.Hour)
	d.mu.Lock()
	d.nextWake, d.nextJob, d.wakeOK = &at, "backup", true
	d.mu.Unlock()

	d.setPaused(true)

	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.nextWake != nil || d.wakeOK {
		t.Error("pause kept claiming a wake is registered after cancelling it; " +
			"resume would short-circuit and never re-register")
	}
}

// A hold that exists during a pause is an explicit statement (manual keep
// awake, or a wrapped job that reported in) and must actually block sleep.
// Gating the helper push on paused reported "blocked" in status while
// telling the helper "not blocked": lid closes, machine sleeps, upload dies.
func TestAHoldDuringPauseStillBlocksSleep(t *testing.T) {
	d := testDaemon(t)
	d.mu.Lock()
	d.paused = true
	d.holds[model.KeepAwakeJobID] = &hold{
		job:      &model.Job{ID: model.KeepAwakeJobID, Name: "keep awake"},
		openedAt: time.Now(), fireAt: time.Now(),
		ceiling: time.Hour,
	}
	d.mu.Unlock()

	d.syncSleepBlock()

	d.helper.mu.Lock()
	want := d.helper.wantBlocked
	d.helper.mu.Unlock()
	if !want {
		t.Error("a manual hold during pause was pushed to the helper as not blocked; " +
			"status would claim the machine cannot sleep while the helper lets it")
	}
}
