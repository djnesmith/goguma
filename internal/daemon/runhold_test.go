package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/ipc"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/power"
)

func runDaemon(t *testing.T) (*Daemon, *fakePlatform) {
	t.Helper()
	d := testDaemon(t)
	plat := &fakePlatform{}
	d.plat = plat
	return d, plat
}

// TestTwoRunsHoldIndependently is the reason run holds are not the keep-awake
// window. Two terminals each running an agent must not release each other's
// hold, and the machine stays awake until the last of them is done.
func TestTwoRunsHoldIndependently(t *testing.T) {
	d, _ := runDaemon(t)
	now := time.Now()

	a, err := d.StartRun(ipc.RunStartReq{Label: "agent-a"}, now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := d.StartRun(ipc.RunStartReq{Label: "agent-b"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatalf("both runs got the id %q; one would have replaced the other", a.ID)
	}
	if got := len(d.holdsSnapshot()); got != 2 {
		t.Fatalf("holds = %d, want 2", got)
	}

	// The first finishing must leave the second holding.
	d.EndRun(a.ID, nil, now)
	holds := d.holdsSnapshot()
	if len(holds) != 1 {
		t.Fatalf("after one run finished, holds = %d, want 1", len(holds))
	}

	d.EndRun(b.ID, nil, now)
	if got := len(d.holdsSnapshot()); got != 0 {
		t.Fatalf("after both finished, holds = %d, want 0", got)
	}
}

// TestAnUnrenewedRunHoldExpires is the safety property the whole design turns
// on. A wrapper that is SIGKILLed never sends run.end, and a hold nobody can
// close is exactly the failure the safety chapter exists to prevent. So the
// hold has to lapse on its own.
func TestAnUnrenewedRunHoldExpires(t *testing.T) {
	d, _ := runDaemon(t)
	now := time.Now()

	r, err := d.StartRun(ipc.RunStartReq{Label: "abandoned"}, now)
	if err != nil {
		t.Fatal(err)
	}
	// Still held a moment later: an eager expiry would drop live commands.
	d.enforceCeilings(now.Add(time.Second), config.Default())
	if len(d.holdsSnapshot()) != 1 {
		t.Fatal("the hold went away a second after it opened")
	}

	// Gone once the lease has run out with nothing renewing it.
	d.enforceCeilings(now.Add(runLease+time.Second), config.Default())
	if got := len(d.holdsSnapshot()); got != 0 {
		t.Errorf("holds = %d after the lease lapsed, want 0: a killed wrapper would strand this", got)
	}
	if d.RenewRun(r.ID, now.Add(runLease+time.Second)).Held {
		t.Error("RenewRun reported a lapsed hold as still held")
	}
}

// TestRenewingKeepsALiveCommandHeld: the other half of the lease. A command
// that is still going must not be dropped at 90 seconds.
func TestRenewingKeepsALiveCommandHeld(t *testing.T) {
	d, _ := runDaemon(t)
	now := time.Now()
	r, err := d.StartRun(ipc.RunStartReq{Label: "long-build"}, now)
	if err != nil {
		t.Fatal(err)
	}

	// Renew every third of a lease for ten leases' worth of time, as the
	// wrapper does, and check it survives throughout.
	step := runLease / 3
	for at := now; at.Before(now.Add(10 * runLease)); at = at.Add(step) {
		if !d.RenewRun(r.ID, at).Held {
			t.Fatalf("hold lapsed at %s despite being renewed", at.Sub(now))
		}
		d.enforceCeilings(at, config.Default())
	}
	if got := len(d.holdsSnapshot()); got != 1 {
		t.Fatalf("holds = %d after 10 leases of renewal, want 1", got)
	}
}

// TestARunHoldCannotOutliveTheHardBound: the lease covers a dead wrapper, not a
// live one attached to something that never finishes. A laptop must not be held
// awake indefinitely because a command hung.
func TestARunHoldCannotOutliveTheHardBound(t *testing.T) {
	d, _ := runDaemon(t)
	now := time.Now()
	r, err := d.StartRun(ipc.RunStartReq{Label: "never-finishes"}, now)
	if err != nil {
		t.Fatal(err)
	}

	past := now.Add(maxRun + time.Hour)
	// The wrapper is alive and still renewing, so this reports held...
	if !d.RenewRun(r.ID, past).Held {
		t.Fatal("renew failed before the bound was reached")
	}
	// ...but the deadline is clamped, so the ordinary release path takes it.
	d.enforceCeilings(past, config.Default())
	if got := len(d.holdsSnapshot()); got != 0 {
		t.Errorf("holds = %d past the %s bound, want 0", got, maxRun)
	}
}

// TestARunIsNotAJobRun guards the exclusion that keeps run holds out of every
// number goguma reports about jobs. A wrapped command's runtime is a fact about
// whatever the user chose to wrap, not evidence about any registered job.
func TestARunIsNotAJobRun(t *testing.T) {
	d, _ := runDaemon(t)
	now := time.Now()

	r, err := d.StartRun(ipc.RunStartReq{Label: "some-agent"}, now)
	if err != nil {
		t.Fatal(err)
	}
	d.EndRun(r.ID, nil, now.Add(time.Minute))

	runs, _ := d.store.Runs(r.ID)
	if len(runs) != 0 {
		t.Errorf("a run hold produced %d run records; it must teach the estimator nothing", len(runs))
	}
	d.mu.RLock()
	last := d.lastRun
	d.mu.RUnlock()
	if last != nil {
		t.Error("a run hold set lastRun, so `goguma status` would report it as the last job run")
	}
}

// TestRunHoldsCountAsManual pins the single gate that produces the exclusions
// above, and keeps them out of sleep-back: a wrapped command is user-initiated,
// so finishing one is never a reason to sleep the machine.
func TestRunHoldsCountAsManual(t *testing.T) {
	h := &hold{job: runHoldJob(model.RunHoldPrefix+"7", "x")}
	if !h.manual() {
		t.Error("a run hold is not counted as manual; it would reach run history and sleep-back")
	}
	ordinary := &hold{job: &model.Job{ID: "nightly-backup"}}
	if ordinary.manual() {
		t.Error("an ordinary job hold was counted as manual")
	}
}

// TestARunIdCannotBeRegisteredAsAJob: a hand-edited jobs.json naming this
// namespace would fight a live run for the same slot in the hold map, and one
// of the two would be dropped mid-flight.
func TestARunIdCannotBeRegisteredAsAJob(t *testing.T) {
	j := &model.Job{
		ID: model.RunHoldPrefix + "1", Name: "sneaky",
		Schedule: "@daily", Detection: model.DetectNone,
	}
	err := j.Validate()
	if err == nil {
		t.Fatal("a job claiming a run-hold id was accepted")
	}
	if !strings.Contains(err.Error(), model.RunHoldPrefix) {
		t.Errorf("error does not name the reserved prefix: %v", err)
	}
	// And no name a user can type reaches it.
	for _, name := range []string{"__run__:1", "run:1", "__run__", "  __run__:2  "} {
		if s := model.Slug(name); strings.HasPrefix(s, model.RunHoldPrefix) {
			t.Errorf("Slug(%q) = %q, which lands in the reserved namespace", name, s)
		}
	}
}

// TestStartRunRefusesDuringACutout mirrors KeepAwake. Once the latch is engaged
// evaluateCutouts stops firing, so a hold opened now would sit out its lease on
// a machine already judged too hot or too flat to hold awake.
func TestStartRunRefusesDuringACutout(t *testing.T) {
	d, _ := runDaemon(t)
	cfg := config.Default()
	hot := power.State{LidClosed: true, TempC: tempOf(95), BatteryPct: 90, OnAC: true}
	d.latch.Engage(EvaluateCutout(hot, cfg, true), 1, time.Now())
	if d.latch.Active() == nil {
		t.Fatal("the latch did not engage on a 95C lid-closed machine while holding")
	}
	if _, err := d.StartRun(ipc.RunStartReq{Label: "agent"}, time.Now()); err == nil {
		t.Error("StartRun opened a hold during a latched cutout")
	}
	if got := len(d.holdsSnapshot()); got != 0 {
		t.Errorf("holds = %d after a refused start, want 0", got)
	}
}

// TestEndingAnUnknownRunIsHarmless: the wrapper retries nothing and the lease
// may have already closed the hold, so a late run.end must be a no-op rather
// than an error or a panic.
func TestEndingAnUnknownRunIsHarmless(t *testing.T) {
	d, _ := runDaemon(t)
	if d.EndRun(model.RunHoldPrefix+"999", nil, time.Now()) {
		t.Error("EndRun reported closing a hold that never existed")
	}
}

// TestAKeyedHoldIsRenewedRatherThanDuplicated is what makes an agent's hooks
// work at all. A harness reports the same session on every prompt and every
// tool call, and without this each report would open a hold that nothing ever
// closes, leaving the machine pinned awake by a stack of them.
func TestAKeyedHoldIsRenewedRatherThanDuplicated(t *testing.T) {
	d, _ := runDaemon(t)
	now := time.Now()

	first, err := d.StartRun(ipc.RunStartReq{Label: "agent", Key: "sess-a"}, now)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 20; i++ {
		again, err := d.StartRun(ipc.RunStartReq{Label: "agent", Key: "sess-a"},
			now.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if again.ID != first.ID {
			t.Fatalf("report %d opened a second hold (%s vs %s)", i, again.ID, first.ID)
		}
	}
	if got := len(d.holdsSnapshot()); got != 1 {
		t.Errorf("holds = %d after 21 reports of one session, want 1", got)
	}
}

// TestRenewingByKeyKeepsTheHoldAliveIndefinitely: an agent that works for hours
// must not be dropped because its lease ran out between tool calls.
func TestRenewingByKeyKeepsTheHoldAliveIndefinitely(t *testing.T) {
	d, _ := runDaemon(t)
	now := time.Now()
	if _, err := d.StartRun(ipc.RunStartReq{Label: "agent", Key: "sess-a"}, now); err != nil {
		t.Fatal(err)
	}
	// Reported every ten minutes for four hours, which is well inside the
	// lease and well past anything a fixed window would have covered.
	for at := now; at.Before(now.Add(4 * time.Hour)); at = at.Add(10 * time.Minute) {
		if _, err := d.StartRun(ipc.RunStartReq{Label: "agent", Key: "sess-a"}, at); err != nil {
			t.Fatal(err)
		}
		d.enforceCeilings(at, config.Default())
	}
	if got := len(d.holdsSnapshot()); got != 1 {
		t.Fatalf("the hold was dropped under an agent still reporting; holds = %d", got)
	}
}

// TestOneAgentFinishingLeavesTheOthersHolding. Two editors open is ordinary,
// and the session that finishes first must not sleep the machine under the one
// that is still going.
func TestOneAgentFinishingLeavesTheOthersHolding(t *testing.T) {
	d, _ := runDaemon(t)
	now := time.Now()
	for _, k := range []string{"sess-a", "sess-b", "sess-c"} {
		if _, err := d.StartRun(ipc.RunStartReq{Label: k, Key: k}, now); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(d.holdsSnapshot()); got != 3 {
		t.Fatalf("holds = %d, want 3", got)
	}

	d.EndRun(keyedRunID("sess-a"), nil, now)
	if got := len(d.holdsSnapshot()); got != 2 {
		t.Fatalf("holds = %d after one agent finished, want 2", got)
	}
	d.EndRun(keyedRunID("sess-b"), nil, now)
	d.EndRun(keyedRunID("sess-c"), nil, now)
	if got := len(d.holdsSnapshot()); got != 0 {
		t.Errorf("holds = %d after all finished, want 0", got)
	}
}

// TestAnAgentThatNeverSaysItStoppedIsStillReleased. The stop hook is the
// ordinary path, and a crashed harness never fires it.
func TestAnAgentThatNeverSaysItStoppedIsStillReleased(t *testing.T) {
	d, _ := runDaemon(t)
	now := time.Now()
	if _, err := d.StartRun(ipc.RunStartReq{Label: "agent", Key: "sess-a"}, now); err != nil {
		t.Fatal(err)
	}
	// Still held well inside the lease: an agent thinking between tool calls
	// reports nothing, and dropping it there is the bug this lease length
	// exists to avoid.
	d.enforceCeilings(now.Add(10*time.Minute), config.Default())
	if len(d.holdsSnapshot()) != 1 {
		t.Fatal("the hold was dropped ten minutes in, well inside the lease")
	}
	// Gone once the lease has run out with nothing renewing it.
	d.enforceCeilings(now.Add(agentLease+time.Minute), config.Default())
	if got := len(d.holdsSnapshot()); got != 0 {
		t.Errorf("holds = %d past the lease, want 0: a crashed agent would strand this", got)
	}
}

// TestAKeyedHoldStillCannotOutliveTheHardBound. Renewal keeps a working agent
// held; it must not keep a wedged one held forever.
func TestAKeyedHoldStillCannotOutliveTheHardBound(t *testing.T) {
	d, _ := runDaemon(t)
	now := time.Now()
	if _, err := d.StartRun(ipc.RunStartReq{Label: "agent", Key: "sess-a"}, now); err != nil {
		t.Fatal(err)
	}
	past := now.Add(maxRun + time.Hour)
	if _, err := d.StartRun(ipc.RunStartReq{Label: "agent", Key: "sess-a"}, past); err != nil {
		t.Fatal(err)
	}
	d.enforceCeilings(past, config.Default())
	if got := len(d.holdsSnapshot()); got != 0 {
		t.Errorf("holds = %d past the %s bound, want 0", got, maxRun)
	}
}

// TestARequestedLeaseIsClamped: the lease arrives over IPC, so a client could
// ask for a week.
func TestARequestedLeaseIsClamped(t *testing.T) {
	d, _ := runDaemon(t)
	resp, err := d.StartRun(ipc.RunStartReq{
		Label: "agent", Key: "sess-a", Lease: model.Duration(7 * 24 * time.Hour),
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Lease.D() > maxLease {
		t.Errorf("lease = %s, over the %s cap", resp.Lease.D(), maxLease)
	}
}

// TestAKeyedAndAnAnonymousHoldCannotCollide. `goguma run` names holds with a
// counter and an agent names them with a session; the seventh wrapped command
// and a session called "7" must not be the same hold.
func TestAKeyedAndAnAnonymousHoldCannotCollide(t *testing.T) {
	d, _ := runDaemon(t)
	now := time.Now()
	anon, err := d.StartRun(ipc.RunStartReq{Label: "build"}, now)
	if err != nil {
		t.Fatal(err)
	}
	keyed, err := d.StartRun(ipc.RunStartReq{Label: "agent", Key: "1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if anon.ID == keyed.ID {
		t.Fatalf("both hold ids are %q", anon.ID)
	}
	if got := len(d.holdsSnapshot()); got != 2 {
		t.Errorf("holds = %d, want 2", got)
	}
}
