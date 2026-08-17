package daemon

import (
	"errors"
	"github.com/junnam586/goguma/internal/power"
	"strings"
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/model"
)

// armed puts the daemon in the state it reaches the instant the last hold of a
// goguma-caused wake closes, with the grace period already elapsed.
func armed(t *testing.T, idle time.Duration) (*Daemon, *fakePlatform, time.Time, config.Config) {
	t.Helper()
	d := testDaemon(t)
	plat := &fakePlatform{userIdle: idle}
	d.plat = plat

	now := time.Now()
	d.mu.Lock()
	d.sleepBackAt = now.Add(-time.Second) // due
	d.sleepBackJob = "nightly-backup"
	d.mu.Unlock()

	cfg := config.Default()
	return d, plat, now, cfg
}

// TestSleepsBackAfterAWakeItCaused is the whole point: goguma woke the machine,
// the job is done, nobody is there, so the machine goes back down.
func TestSleepsBackAfterAWakeItCaused(t *testing.T) {
	d, plat, now, cfg := armed(t, 10*time.Minute)
	d.maybeSleepBack(now, cfg)
	if plat.slept != 1 {
		t.Fatalf("SleepNow called %d times, want 1", plat.slept)
	}
	// And it does not fire twice for one wake.
	d.maybeSleepBack(now, cfg)
	if plat.slept != 1 {
		t.Errorf("SleepNow called %d times after a second tick, want 1", plat.slept)
	}
}

// TestDoesNotSleepWhileSomeoneIsThere is the guard that matters most. The
// machine woke on its own at an hour nobody asked about; if there has been any
// input, sleeping it under them is hostile.
func TestDoesNotSleepWhileSomeoneIsThere(t *testing.T) {
	d, plat, now, cfg := armed(t, 3*time.Second)
	d.maybeSleepBack(now, cfg)
	if plat.slept != 0 {
		t.Errorf("slept with the user %s idle; nobody had left the keyboard", 3*time.Second)
	}
}

// TestUnreadableIdleTimeMeansSomeoneMayBeThere: an error is "cannot tell",
// never "nobody". Being wrong in this direction costs battery; being wrong in
// the other direction darkens someone's screen mid-sentence.
func TestUnreadableIdleTimeMeansSomeoneMayBeThere(t *testing.T) {
	d, plat, now, cfg := armed(t, time.Hour)
	plat.userIdleErr = errors.New("no HIDIdleTime here")
	d.maybeSleepBack(now, cfg)
	if plat.slept != 0 {
		t.Error("slept without being able to tell whether anyone was using the machine")
	}
}

// TestDoesNotSleepWhenTheSettingIsOff.
func TestDoesNotSleepWhenTheSettingIsOff(t *testing.T) {
	d, plat, now, cfg := armed(t, time.Hour)
	cfg.SleepAfterWake = false
	d.maybeSleepBack(now, cfg)
	if plat.slept != 0 {
		t.Error("slept with sleep_after_wake off")
	}
}

// TestDoesNotSleepWhileAHoldIsOpen. Something claimed the machine during the
// grace period, so it is wanted awake and this is no longer goguma's call.
func TestDoesNotSleepWhileAHoldIsOpen(t *testing.T) {
	d, plat, now, cfg := armed(t, time.Hour)
	d.mu.Lock()
	d.holds["something"] = &hold{job: &model.Job{ID: "something", Name: "something"}}
	d.mu.Unlock()
	d.maybeSleepBack(now, cfg)
	if plat.slept != 0 {
		t.Error("slept the machine while a hold was open")
	}
}

// TestDoesNotSleepIntoAnImminentWake. Sleeping four seconds before the alarm is
// churn, not thrift.
func TestDoesNotSleepIntoAnImminentWake(t *testing.T) {
	d, plat, now, cfg := armed(t, time.Hour)
	soon := now.Add(20 * time.Second)
	d.mu.Lock()
	d.nextWake = &soon
	d.mu.Unlock()
	d.maybeSleepBack(now, cfg)
	if plat.slept != 0 {
		t.Error("slept 20s before the next scheduled wake")
	}
}

// TestSleepsWhenTheNextWakeIsFarOff, the other side of that boundary.
func TestSleepsWhenTheNextWakeIsFarOff(t *testing.T) {
	d, plat, now, cfg := armed(t, time.Hour)
	later := now.Add(6 * time.Hour)
	d.mu.Lock()
	d.nextWake = &later
	d.mu.Unlock()
	d.maybeSleepBack(now, cfg)
	if plat.slept != 1 {
		t.Errorf("SleepNow called %d times with the next wake 6h away, want 1", plat.slept)
	}
}

// TestNothingHappensBeforeTheGracePeriodElapses.
func TestNothingHappensBeforeTheGracePeriodElapses(t *testing.T) {
	d, plat, now, cfg := armed(t, time.Hour)
	d.mu.Lock()
	d.sleepBackAt = now.Add(30 * time.Second)
	d.mu.Unlock()
	d.maybeSleepBack(now, cfg)
	if plat.slept != 0 {
		t.Error("slept before the grace period was up")
	}
}

// TestOnlyArmsForWakesGogumaCaused. A job that ran while the user was already
// at the machine must never lead to goguma sleeping it.
func TestOnlyArmsForWakesGogumaCaused(t *testing.T) {
	d := testDaemon(t)
	now := time.Now()
	job := &model.Job{ID: "j", Name: "j"}

	d.mu.Lock()
	d.armSleepBack(&hold{job: job, wokeMachine: false, detected: true}, now, model.OutcomeOK)
	notArmed := d.sleepBackAt.IsZero()
	d.armSleepBack(&hold{job: job, wokeMachine: true, detected: true}, now, model.OutcomeOK)
	nowArmed := !d.sleepBackAt.IsZero()
	d.mu.Unlock()

	if !notArmed {
		t.Error("armed a sleep for a wake goguma did not cause")
	}
	if !nowArmed {
		t.Error("did not arm a sleep for a wake goguma did cause")
	}
}

// TestAManualKeepAwakeNeverSleepsTheMachine. Someone asked for the machine to
// stay up; ending that window is not permission to sleep it.
func TestAManualKeepAwakeNeverSleepsTheMachine(t *testing.T) {
	d := testDaemon(t)
	now := time.Now()
	d.mu.Lock()
	d.armSleepBack(&hold{
		job:         &model.Job{ID: model.KeepAwakeJobID, Name: "keep awake"},
		wokeMachine: true, detected: true,
	}, now, model.OutcomeOK)
	armed := !d.sleepBackAt.IsZero()
	d.mu.Unlock()
	if armed {
		t.Error("armed a sleep after a manual keep-awake window")
	}
}

// TestAFailedSleepIsNotFatal. The machine stays awake, which is exactly where
// it was before this feature existed.
func TestAFailedSleepIsNotFatal(t *testing.T) {
	d, plat, now, cfg := armed(t, time.Hour)
	plat.sleepErr = errors.New("pmset: permission denied")
	d.maybeSleepBack(now, cfg) // must not panic
	if plat.slept != 1 {
		t.Errorf("SleepNow attempted %d times, want 1", plat.slept)
	}
	d.mu.Lock()
	stillArmed := !d.sleepBackAt.IsZero()
	d.mu.Unlock()
	if stillArmed {
		t.Error("a failed sleep stayed armed and would retry every tick")
	}
}

// TestArmsOnTheKernelWakeWhenTheTickGapMissedIt is the bug the first live run
// found.
//
// goguma woke the Mac at 12:58:30 and again at 13:58:30, ran a job each time,
// and left it awake both times. wokeMachine was false because the machine had
// been surfacing every fifteen minutes, so the daemon never saw a gap in its
// own ticks big enough to call sleep. It had asked for those wakes and got
// them; it just was not using the kernel's record of them.
func TestArmsOnTheKernelWakeWhenTheTickGapMissedIt(t *testing.T) {
	d := testDaemon(t)
	now := time.Now()
	job := &model.Job{ID: "j", Name: "j"}

	d.mu.Lock()
	d.armSleepBack(&hold{job: job, wokeMachine: false, followsWake: true, detected: true},
		now, model.OutcomeOK)
	armed := !d.sleepBackAt.IsZero()
	d.mu.Unlock()

	if !armed {
		t.Error("did not arm despite the kernel reporting a wake moments earlier")
	}
}

// TestDoesNotArmWhenNeitherSignalSaysAWakeHappened.
func TestDoesNotArmWhenNeitherSignalSaysAWakeHappened(t *testing.T) {
	d := testDaemon(t)
	now := time.Now()
	d.mu.Lock()
	d.armSleepBack(&hold{
		job: &model.Job{ID: "j", Name: "j"}, wokeMachine: false, followsWake: false,
		detected: true,
	}, now, model.OutcomeOK)
	armed := !d.sleepBackAt.IsZero()
	d.mu.Unlock()
	if armed {
		t.Error("armed for a job that ran on a machine nobody had woken")
	}
}

// TestFollowsRecentWakeReadsTheKernel covers the window either side of the
// threshold, and the two ways of not knowing.
func TestFollowsRecentWakeReadsTheKernel(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name string
		wake time.Time
		err  error
		want bool
	}{
		{"woke seconds ago", now.Add(-3 * time.Second), nil, true},
		{"woke inside the window", now.Add(-2 * time.Minute), nil, true},
		{"woke well before the window", now.Add(-30 * time.Minute), nil, false},
		{"never slept since boot", time.Time{}, nil, false},
		{"cannot read it", now, errors.New("no such sysctl"), false},
		// A clock that moved backwards must not read as "woke in the future".
		{"wake in the future", now.Add(time.Minute), nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := testDaemon(t)
			d.plat = &fakePlatform{lastWake: tc.wake, lastWakeErr: tc.err}
			if got := d.followsRecentWake(now); got != tc.want {
				t.Errorf("followsRecentWake = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestNeverSleepsOnAWindowThatDidNotSeeTheJobFinish.
//
// Three ways a window closes without the job being known to be done, and none
// of them may put the machine to sleep on top of it. The wake-only case is the
// one that matters most in practice: it is the commonest kind of adopted job,
// its window is a three-minute guess, and a backup taking four minutes would
// otherwise be suspended by the tool that woke the machine to run it.
func TestNeverSleepsOnAWindowThatDidNotSeeTheJobFinish(t *testing.T) {
	job := &model.Job{ID: "j", Name: "j"}
	for _, tc := range []struct {
		name     string
		detected bool
		outcome  model.Outcome
	}{
		{"still running when the ceiling expired", true, model.OutcomeCeiling},
		{"never detected at all", false, model.OutcomeNeverDetected},
		{"wake-only, held a fixed window and saw nothing", false, model.OutcomeOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := testDaemon(t)
			now := time.Now()
			d.mu.Lock()
			d.armSleepBack(&hold{
				job: job, wokeMachine: true, followsWake: true, detected: tc.detected,
			}, now, tc.outcome)
			armed := !d.sleepBackAt.IsZero()
			d.mu.Unlock()
			if armed {
				t.Errorf("armed a sleep after %q; the job may still be running", tc.name)
			}
		})
	}
}

// TestSleepsAfterAJobItWatchedFinish, including a failed one: a non-zero exit
// is still an exit, and the machine has nothing left to stay up for.
func TestSleepsAfterAJobItWatchedFinish(t *testing.T) {
	job := &model.Job{ID: "j", Name: "j"}
	for _, outcome := range []model.Outcome{model.OutcomeOK, model.OutcomeFailed} {
		d := testDaemon(t)
		now := time.Now()
		d.mu.Lock()
		d.armSleepBack(&hold{
			job: job, wokeMachine: true, detected: true,
		}, now, outcome)
		armed := !d.sleepBackAt.IsZero()
		d.mu.Unlock()
		if !armed {
			t.Errorf("did not arm after an observed %s finish", outcome)
		}
	}
}

// TestWarnsWhenPoweringOnCannotActuallyRunAnything.
//
// `use_wake_or_power_on` reads as though powering on were the same as waking.
// On a FileVault machine it is not: the Mac lights up, stops at the unlock
// screen, and the job is missed exactly as it would have been asleep. Nobody
// watching would know why, because from the outside the machine did turn on.
func TestWarnsWhenPoweringOnCannotActuallyRunAnything(t *testing.T) {
	d := testDaemon(t)
	plat := &fakePlatform{powerOnOK: false, powerOnWhy: "FileVault is on"}
	d.plat = plat

	cfg := config.Default()
	cfg.UseWakeOrPowerOn = true
	d.refreshWarnings(power.State{BatteryPct: -1}, cfg)

	d.mu.RLock()
	warnings := d.warnings
	d.mu.RUnlock()

	var found *model.Warning
	for i := range warnings {
		if warnings[i].Kind == model.WarnPowerOnCannotRun {
			found = &warnings[i]
		}
	}
	if found == nil {
		t.Fatal("no warning about a power-on that cannot run anything")
	}
	if found.Fix == "" {
		t.Error("the warning offers no way to act on it")
	}
	if !strings.Contains(found.Message, "FileVault is on") {
		t.Errorf("the warning does not say why: %q", found.Message)
	}
}

// TestStaysQuietWhenPoweringOnWouldWork, and when the setting is off.
func TestStaysQuietWhenPoweringOnWouldWork(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		ok      bool
	}{
		{"setting off, machine could not anyway", false, false},
		{"setting on, machine can", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := testDaemon(t)
			d.plat = &fakePlatform{powerOnOK: tc.ok, powerOnWhy: "nope"}
			cfg := config.Default()
			cfg.UseWakeOrPowerOn = tc.enabled
			d.refreshWarnings(power.State{BatteryPct: -1}, cfg)
			d.mu.RLock()
			warnings := d.warnings
			d.mu.RUnlock()
			for _, w := range warnings {
				if w.Kind == model.WarnPowerOnCannotRun {
					t.Error("warned when there was nothing to warn about")
				}
			}
		})
	}
}
