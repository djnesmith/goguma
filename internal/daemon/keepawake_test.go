package daemon

import (
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/power"
	"github.com/junnam586/goguma/internal/schedule"
)

// fakeAssertion counts releases, so a test can prove the idle assertion of a
// replaced window was handed back rather than orphaned.
type fakeAssertion struct{ released int }

func (a *fakeAssertion) Release() error { a.released++; return nil }

// fakePlatform stands in for the OS. The manual keep-awake path is the first
// daemon path a test drives that actually takes an idle assertion, and the
// point of these tests is the policy around the hold, not the syscall under it.
type fakePlatform struct{ assertions []*fakeAssertion }

func (p *fakePlatform) Name() string { return "fake" }

func (p *fakePlatform) HoldIdleSleep(string) (power.IdleAssertion, error) {
	a := &fakeAssertion{}
	p.assertions = append(p.assertions, a)
	return a, nil
}

func (p *fakePlatform) ReadState() (power.State, error) { return power.State{BatteryPct: -1}, nil }

func (p *fakePlatform) SleepHistory(time.Duration) (*schedule.SleepHistory, error) {
	return nil, nil
}

func (p *fakePlatform) SupportsClamshellHold() bool { return true }

func (p *fakePlatform) WakeScheduleSupported() (bool, string) { return true, "" }

func keepAwakeDaemon(t *testing.T) (*Daemon, *fakePlatform) {
	t.Helper()
	d := testDaemon(t)
	plat := &fakePlatform{}
	d.plat = plat
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	return d, plat
}

func TestKeepAwakeOpensOneHoldAndReplacesIt(t *testing.T) {
	d, plat := keepAwakeDaemon(t)
	now := time.Now()

	resp, err := d.KeepAwake(30*time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Active || resp.Until == nil {
		t.Fatalf("KeepAwake returned %+v, want an active window", resp)
	}
	if n := len(d.holds); n != 1 {
		t.Fatalf("opened %d holds, want exactly 1", n)
	}

	// Asking again is a new deadline from now, not an extension. Stacking would
	// mean a user who taps the button twice is awake for twice as long as
	// either request said, and the second window would leak the first's
	// assertion.
	later := now.Add(5 * time.Minute)
	resp2, err := d.KeepAwake(time.Hour, later)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(d.holds); n != 1 {
		t.Fatalf("holding %d windows after a second request, want 1 (it must replace, not stack)", n)
	}
	if want := later.Add(time.Hour); !resp2.Until.Equal(want) {
		t.Errorf("until = %s, want %s; the window runs from the new request", resp2.Until, want)
	}

	if len(plat.assertions) != 2 {
		t.Fatalf("took %d idle assertions, want 2", len(plat.assertions))
	}
	if plat.assertions[0].released != 1 {
		t.Error("the replaced window's idle assertion was not released; nothing else can ever release it")
	}
	if plat.assertions[1].released != 0 {
		t.Error("the current window's assertion was released while it is still open")
	}
	d.bg.Wait()
}

func TestKeepAwakeCancelReleases(t *testing.T) {
	d, plat := keepAwakeDaemon(t)
	now := time.Now()

	if _, err := d.KeepAwake(30*time.Minute, now); err != nil {
		t.Fatal(err)
	}
	resp, err := d.KeepAwake(0, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Active || resp.Until != nil {
		t.Errorf("cancelling returned %+v, want an inactive window", resp)
	}
	if n := len(d.holds); n != 0 {
		t.Errorf("%d holds remain after cancelling", n)
	}
	if plat.assertions[0].released != 1 {
		t.Error("cancelling did not release the idle assertion, so the machine still cannot idle-sleep")
	}

	// Cancelling when nothing is open is not an error; it is the state the
	// caller asked for.
	if _, err := d.KeepAwake(-time.Second, now); err != nil {
		t.Errorf("cancelling with nothing open failed: %v", err)
	}
	d.bg.Wait()
}

func TestKeepAwakeDeadlineIsNowPlusDurationAndExpires(t *testing.T) {
	d, _ := keepAwakeDaemon(t)
	now := time.Date(2026, 8, 5, 14, 0, 0, 0, time.UTC)

	resp, err := d.KeepAwake(30*time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	// A wake-only hold has nothing to observe, so its window is exactly the
	// fixed period that was asked for: no detection grace, no extension.
	if want := now.Add(30 * time.Minute); !resp.Until.Equal(want) {
		t.Errorf("until = %s, want %s", resp.Until, want)
	}

	// The ordinary deadline sweep is what ends it, the same one that force-
	// releases a job that overran.
	d.enforceCeilings(now.Add(29*time.Minute), config.Default())
	if len(d.holds) != 1 {
		t.Fatal("the window was released before its deadline")
	}
	d.enforceCeilings(now.Add(30*time.Minute+time.Second), config.Default())
	if n := len(d.holds); n != 0 {
		t.Errorf("%d holds survived past the deadline", n)
	}
	d.bg.Wait()
}

func TestKeepAwakeClampsBothEnds(t *testing.T) {
	d, _ := keepAwakeDaemon(t)
	now := time.Now()

	tests := []struct {
		name    string
		want    time.Duration
		expect  time.Duration
		clamped bool
	}{
		{"below the floor", time.Second, MinKeepAwake, true},
		{"at the floor", MinKeepAwake, MinKeepAwake, false},
		{"in range", 30 * time.Minute, 30 * time.Minute, false},
		{"at the ceiling", MaxKeepAwake, MaxKeepAwake, false},
		{"above the ceiling", 48 * time.Hour, MaxKeepAwake, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := d.KeepAwake(tc.want, now)
			if err != nil {
				t.Fatal(err)
			}
			if got := resp.Until.Sub(now); got != tc.expect {
				t.Errorf("window = %s, want %s", got, tc.expect)
			}
			// The caller has to be able to say the request was adjusted;
			// silently substituting a different duration reads as the command
			// having been ignored.
			if resp.Clamped != tc.clamped {
				t.Errorf("Clamped = %v, want %v", resp.Clamped, tc.clamped)
			}
		})
	}
	d.releaseKeepAwake(now)
	d.bg.Wait()
}

// TestKeepAwakeIsNeverRecordedAsARun is the correctness requirement that
// matters most here.
//
// A manual window's length is a number the user typed, not a measurement of
// anything. Filed as a run it would train the ceiling estimator, count towards
// job statistics, and appear as `status`'s "last run", teaching the tool that
// jobs take as long as somebody once wanted a coffee break.
func TestKeepAwakeIsNeverRecordedAsARun(t *testing.T) {
	d, _ := keepAwakeDaemon(t)
	now := time.Now()

	if _, err := d.KeepAwake(time.Hour, now); err != nil {
		t.Fatal(err)
	}
	// Every route out of a manual window, not just the tidy one.
	d.enforceCeilings(now.Add(2*time.Hour), config.Default()) // expiry
	if _, err := d.KeepAwake(time.Hour, now); err != nil {
		t.Fatal(err)
	}
	d.SleepNow() // explicit release
	if _, err := d.KeepAwake(time.Hour, now); err != nil {
		t.Fatal(err)
	}
	d.releaseKeepAwake(now.Add(time.Minute)) // cancel
	if _, err := d.KeepAwake(time.Hour, now); err != nil {
		t.Fatal(err)
	}
	d.shutdown() // daemon exit

	// Persistence is deliberately off the control path, so wait for it before
	// asserting that nothing was written.
	d.bg.Wait()

	runs, err := d.store.Runs(model.KeepAwakeJobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Errorf("the run store holds %d records for the manual window; it must hold none", len(runs))
	}

	d.mu.RLock()
	last := d.lastRun
	d.mu.RUnlock()
	if last != nil {
		t.Errorf("lastRun = %+v; a manual hold ran nothing and must not be reported as a run", last)
	}
}

func TestCutoutReleasesTheKeepAwakeHold(t *testing.T) {
	d, plat := keepAwakeDaemon(t)
	now := time.Now()

	if _, err := d.KeepAwake(4*time.Hour, now); err != nil {
		t.Fatal(err)
	}

	// A lid-closed machine that has reached its thermal limit is exactly the
	// hazard the valve exists for, and a manual hold is not exempt from it:
	// the user asking to stay awake cannot know the machine is cooking in a bag.
	hot := power.State{LidClosed: true, TempC: tempOf(95), OnAC: true, BatteryPct: 100}
	d.evaluateCutouts(hot, config.Default(), now.Add(time.Minute))

	if n := len(d.holds); n != 0 {
		t.Fatalf("%d holds survived a thermal cutout", n)
	}
	if plat.assertions[0].released != 1 {
		t.Error("the cutout dropped the hold without releasing its idle assertion")
	}
	d.mu.RLock()
	cut := d.lastCut
	d.mu.RUnlock()
	if cut == nil || cut.Released != 1 {
		t.Errorf("cutout = %+v, want it to report the released manual hold", cut)
	}

	// And it must not be re-openable while the hazard is latched: cutouts stop
	// firing once latched, so a window opened now would sit out its full four
	// hours on a machine already judged too hot to hold awake.
	if _, err := d.KeepAwake(time.Hour, now.Add(2*time.Minute)); err == nil {
		t.Error("a keep-awake window was opened during a latched cutout")
	}
	d.bg.Wait()
}

func TestKeepAwakeIsNotAJob(t *testing.T) {
	d, _ := keepAwakeDaemon(t)
	now := time.Now()

	if _, err := d.KeepAwake(time.Hour, now); err != nil {
		t.Fatal(err)
	}

	// Not a registered job: it must not reach the job list, the group list, or
	// jobs.json, all of which read the store rather than the hold map.
	if list := d.jobsList(); len(list.Jobs) != 0 {
		t.Errorf("jobsList returned %d jobs, want none", len(list.Jobs))
	}
	if n := len(d.store.Jobs()); n != 0 {
		t.Errorf("the store holds %d jobs, want none", n)
	}
	if g := d.store.Groups(); len(g) != 0 {
		t.Errorf("Groups() = %v, want none", g)
	}
	if _, ok := d.store.Job(model.KeepAwakeJobID); ok {
		t.Error("the synthetic keep-awake job is resolvable from the store")
	}

	// It is still a hold, though; that is the entire point of building it as
	// one, and Status carries the deadline so a client needs no second call.
	st := d.Status()
	if !st.Holding || len(st.Holds) != 1 {
		t.Errorf("status reports holding=%v with %d holds, want one held window", st.Holding, len(st.Holds))
	}
	if st.KeepAwakeUntil == nil || !st.KeepAwakeUntil.Equal(now.Add(time.Hour)) {
		t.Errorf("KeepAwakeUntil = %v, want %s", st.KeepAwakeUntil, now.Add(time.Hour))
	}

	d.releaseKeepAwake(now)
	if st := d.Status(); st.KeepAwakeUntil != nil {
		t.Errorf("KeepAwakeUntil = %v after release, want nil", st.KeepAwakeUntil)
	}
	d.bg.Wait()
}
