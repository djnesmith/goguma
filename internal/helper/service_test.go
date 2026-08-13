package helper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/ipc"
)

// fakeOps records what the helper asked the platform to do.
//
// Only the two write operations genuinely need root, so replacing them makes
// every policy decision around them testable: crash recovery, state
// reconciliation, the dead-man switch, and the shutdown ordering. Those are
// where a bug would actually live, a stranded sleep block leaves a laptop
// unable to sleep until someone notices by hand.
type fakeOps struct {
	mu sync.Mutex

	blocked     bool
	setCalls    []bool
	schedules   []time.Time
	schedKinds  []bool
	cancels     []time.Time
	cancelKinds []bool
	wakes       []wakeEntry

	setErr   error
	listErr  error
	readErr  error
	schedErr error
}

func (f *fakeOps) ops() platformOps {
	return platformOps{
		setBlocked: func(b bool) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.setCalls = append(f.setCalls, b)
			if f.setErr != nil {
				return f.setErr
			}
			f.blocked = b
			return nil
		},
		readBlocked: func() (bool, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.blocked, f.readErr
		},
		scheduleAt: func(t time.Time, kind bool) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.schedErr != nil {
				return f.schedErr
			}
			f.schedules = append(f.schedules, t)
			f.schedKinds = append(f.schedKinds, kind)
			f.wakes = append(f.wakes, wakeEntry{at: t, powerOn: kind, owner: wakeOwnerTag})
			return nil
		},
		cancelAt: func(e wakeEntry) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.cancels = append(f.cancels, e.at)
			f.cancelKinds = append(f.cancelKinds, e.powerOn)
			// Actually remove it, the way pmset does: matching on time, kind
			// and owner. Recording the call while leaving the entry in
			// `wakes` would let a duplicate-accumulation bug pass its own
			// regression test.
			kept := f.wakes[:0]
			for _, w := range f.wakes {
				if !(w.at.Equal(e.at) && w.powerOn == e.powerOn && w.owner == e.owner) {
					kept = append(kept, w)
				}
			}
			f.wakes = kept
			return nil
		},
		listWakes: func() ([]wakeEntry, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.listErr != nil {
				return nil, f.listErr
			}
			return append([]wakeEntry(nil), f.wakes...), nil
		},
	}
}

func withOps(f *fakeOps) *Service {
	s := New("test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.ops = f.ops()
	return s
}

// TestRecoverClearsAStrandedBlock covers the boot-time safety net.
//
// disablesleep is global and is NOT cleared when the process that set it dies.
// A helper that crashed while blocked, or a machine force-powered-off mid-hold,
// would otherwise come back up unable to sleep at all, and the unit is
// configured to run this before any user logs in precisely so nobody has to
// notice and fix it by hand.
func TestRecoverClearsAStrandedBlock(t *testing.T) {
	f := &fakeOps{blocked: true} // as if a previous run died while holding
	s := withOps(f)

	s.Recover()

	if f.blocked {
		t.Error("a stranded sleep block survived startup")
	}
	if len(f.setCalls) != 1 || f.setCalls[0] != false {
		t.Errorf("setBlocked calls = %v, want exactly one clear", f.setCalls)
	}
}

func TestRecoverDoesNothingWhenNotBlocked(t *testing.T) {
	// The overwhelmingly common case. Touching the global setting when there
	// is nothing to clear would be a pointless privileged write on every boot.
	f := &fakeOps{blocked: false}
	s := withOps(f)
	s.Recover()
	if len(f.setCalls) != 0 {
		t.Errorf("setBlocked was called %d times on a clean start", len(f.setCalls))
	}
}

func TestRecoverSurvivesAnUnreadableState(t *testing.T) {
	// If the state cannot be read, guessing would be worse than doing nothing:
	// clearing blindly could release a block another process legitimately set.
	f := &fakeOps{blocked: true, readErr: errors.New("pmset unavailable")}
	s := withOps(f)
	s.Recover()
	if len(f.setCalls) != 0 {
		t.Error("the helper wrote state it could not first read")
	}
}

// TestSetBlockedAlwaysCallsThrough guards the self-healing property.
//
// The daemon re-pushes its state every 60s so that a failed pmset, a raced
// helper restart, or a kernel reset across sleep/wake gets repaired. Skipping
// the call when the state looks unchanged would turn every re-push into a
// no-op and defeat the entire mechanism.
func TestSetBlockedAlwaysCallsThrough(t *testing.T) {
	f := &fakeOps{}
	s := withOps(f)

	for range 3 {
		if err := s.SetBlocked(true, "jobs: backup"); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.setCalls) != 3 {
		t.Errorf("setBlocked called %d times across three pushes, want 3; "+
			"short-circuiting would break the repair path", len(f.setCalls))
	}
}

func TestSetBlockedDoesNotRecordStateWhenTheCallFails(t *testing.T) {
	// Believing sleep is blocked when it is not would let the daemon report a
	// hold it does not have, and would stop the next push from repairing it.
	f := &fakeOps{setErr: errors.New("pmset failed")}
	s := withOps(f)

	if err := s.SetBlocked(true, "jobs: backup"); err == nil {
		t.Fatal("a failed pmset reported success")
	}
	s.mu.Lock()
	recorded := s.blocked
	s.mu.Unlock()
	if recorded {
		t.Error("the helper recorded a block that was never applied")
	}
}

// TestStatusReconcilesWithTheKernel matters because disablesleep is a global
// setting: another process, or a sleep/wake cycle, can change it underneath us.
func TestStatusReconcilesWithTheKernel(t *testing.T) {
	f := &fakeOps{}
	s := withOps(f)

	if err := s.SetBlocked(true, "jobs: x"); err != nil {
		t.Fatal(err)
	}
	// Something external clears it.
	f.mu.Lock()
	f.blocked = false
	f.mu.Unlock()

	out, err := s.Handle(context.Background(), ipc.OpHelperStatus, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp := out.(ipc.HelperStatusResp)
	if resp.SleepBlocked {
		t.Error("status reported its own belief rather than the real state")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.blocked {
		t.Error("the helper did not adopt the real state after diverging")
	}
}

func TestScheduleWakeCancelsThePreviousEntry(t *testing.T) {
	// Re-assertion happens every 60s. Without cancelling first, the system
	// schedule accumulates a stale entry per push, and `pmset -g sched` on an
	// ordinary Mac is already crowded with other applications' events.
	f := &fakeOps{}
	s := withOps(f)

	first := time.Now().Add(time.Hour).Truncate(time.Second)
	second := first.Add(30 * time.Minute)

	if err := s.ScheduleWake(first, false); err != nil {
		t.Fatal(err)
	}
	if err := s.ScheduleWake(second, false); err != nil {
		t.Fatal(err)
	}
	if len(f.cancels) != 1 || !f.cancels[0].Equal(first) {
		t.Errorf("cancels = %v, want exactly the first entry", f.cancels)
	}
}

func TestReschedulingTheSameTimeDoesNotCancel(t *testing.T) {
	// The daemon re-pushes an unchanged target constantly. Cancelling and
	// re-adding each time would leave a window with no wake registered at all.
	f := &fakeOps{}
	s := withOps(f)
	at := time.Now().Add(time.Hour).Truncate(time.Second)

	for range 3 {
		if err := s.ScheduleWake(at, false); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.cancels) != 0 {
		t.Errorf("cancelled %d times while re-asserting the same target", len(f.cancels))
	}
}

func TestFailedScheduleDoesNotRecordATarget(t *testing.T) {
	// Recording a target that was never registered would make a later cancel
	// address an entry that does not exist, and would make status lie.
	f := &fakeOps{schedErr: errors.New("pmset refused")}
	s := withOps(f)

	if err := s.ScheduleWake(time.Now().Add(time.Hour), false); err == nil {
		t.Fatal("a failed schedule reported success")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scheduledAt != nil {
		t.Error("the helper recorded a wake it never registered")
	}
}

func TestCancelWakeIsSafeWithNothingScheduled(t *testing.T) {
	f := &fakeOps{}
	s := withOps(f)
	if err := s.CancelWake(); err != nil {
		t.Errorf("cancelling nothing returned an error: %v", err)
	}
	if len(f.cancels) != 0 {
		t.Error("cancelled an entry that was never scheduled")
	}
}

// A helper restart forgets scheduledAt while the persisted pmset entries
// survive. Cancel must reconcile against the OS, not memory, or it reports
// success with the wake still registered and the machine keeps waking.
func TestCancelWakeSweepsEntriesThatSurviveARestart(t *testing.T) {
	f := &fakeOps{wakes: []wakeEntry{
		{at: time.Now().Add(time.Hour).Truncate(time.Second), owner: wakeOwnerTag},
		{at: time.Now().Add(2 * time.Hour).Truncate(time.Second), owner: wakeOwnerTag},
	}}
	s := withOps(f) // a fresh service remembers nothing, as after a restart
	if err := s.CancelWake(); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	if len(f.wakes) != 0 {
		t.Errorf("%d owner-tagged entries survived a cancel; nothing would ever remove them", len(f.wakes))
	}
}

// When the schedule cannot be read and nothing is remembered, reporting
// success would end the caller's retries with an unknown entry still
// registered.
func TestCancelWakeReportsAnUnreadableSchedule(t *testing.T) {
	f := &fakeOps{listErr: errors.New("pmset unavailable")}
	s := withOps(f)
	if err := s.CancelWake(); err == nil {
		t.Error("an unreadable schedule with nothing remembered reported success; the daemon would stop retrying")
	}
}

// pmset cancel matches on kind as well as time, so a kind change must drop
// and re-add the entry. Recording the new kind over the old entry leaves an
// OS entry no later cancel can match.
func TestScheduleWakeReRegistersWhenTheKindChanges(t *testing.T) {
	f := &fakeOps{}
	s := withOps(f)
	at := time.Now().Add(time.Hour).Truncate(time.Second)

	if err := s.ScheduleWake(at, false); err != nil {
		t.Fatal(err)
	}
	if err := s.ScheduleWake(at, true); err != nil {
		t.Fatal(err)
	}

	if len(f.cancels) == 0 || !f.cancels[len(f.cancels)-1].Equal(at) ||
		f.cancelKinds[len(f.cancelKinds)-1] != false {
		t.Error("the old-kind entry was not cancelled before re-registering")
	}
	if k := f.schedKinds[len(f.schedKinds)-1]; k != true {
		t.Errorf("re-registered with kind %v, want the new kind", k)
	}
	if len(f.wakes) != 1 {
		t.Errorf("%d entries registered, want exactly one", len(f.wakes))
	}
}

// TestVerifyWakeDetectsAVanishedEntry is the mitigation for PRD §5.2: a
// scheduled wake can be silently dropped by the system, so "the command
// succeeded" is not treated as proof it is still there.
func TestVerifyWakeDetectsAVanishedEntry(t *testing.T) {
	f := &fakeOps{}
	s := withOps(f)
	at := time.Now().Add(time.Hour).Truncate(time.Second)

	if err := s.ScheduleWake(at, false); err != nil {
		t.Fatal(err)
	}
	ok, err := s.VerifyWake(at)
	if err != nil || !ok {
		t.Fatalf("a registered wake did not verify: ok=%v err=%v", ok, err)
	}

	// The system drops it, as macOS is documented to do.
	f.mu.Lock()
	f.wakes = nil
	f.mu.Unlock()

	if ok, _ := s.VerifyWake(at); ok {
		t.Error("verification passed for an entry that is no longer registered")
	}
}

func TestVerifyWakeToleratesSubSecondDrift(t *testing.T) {
	// pmset stores second resolution, so an exact comparison would report a
	// perfectly good entry as missing.
	f := &fakeOps{}
	s := withOps(f)
	at := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := s.ScheduleWake(at, false); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.VerifyWake(at.Add(300 * time.Millisecond)); !ok {
		t.Error("a 300ms difference was treated as a missing entry")
	}
	if ok, _ := s.VerifyWake(at.Add(5 * time.Second)); ok {
		t.Error("a 5s difference was treated as the same entry")
	}
}

// TestDeadManClearsAnAbandonedBlock is the last line of defence.
//
// If the daemon is SIGKILLed at logout while blocked (which is exactly what
// happens at logout), nothing else would ever clear the global setting.
func TestDeadManClearsAnAbandonedBlock(t *testing.T) {
	f := &fakeOps{}
	s := withOps(f)

	if err := s.SetBlocked(true, "jobs: backup"); err != nil {
		t.Fatal(err)
	}
	// No connections, and no contact for longer than the timeout.
	s.mu.Lock()
	s.connections = 0
	s.lastContact = time.Now().Add(-deadManTimeout - time.Second)
	s.mu.Unlock()

	s.checkDeadMan()

	if f.blocked {
		t.Error("an abandoned sleep block was not cleared")
	}
}

func TestShutdownClearsTheBlock(t *testing.T) {
	// SIGTERM at logout or shutdown. Skipping this leaves the machine unable
	// to sleep, because the setting outlives the process.
	f := &fakeOps{}
	s := withOps(f)
	if err := s.SetBlocked(true, "jobs: x"); err != nil {
		t.Fatal(err)
	}
	s.Shutdown()
	if f.blocked {
		t.Error("the sleep block survived shutdown")
	}
}

func TestShutdownLeavesAScheduledWakeInPlace(t *testing.T) {
	// Deliberate: the machine should still wake for the job even if the helper
	// is merely restarting, and a stale entry is harmless; it wakes the
	// machine once and is consumed.
	f := &fakeOps{}
	s := withOps(f)
	at := time.Now().Add(time.Hour)
	if err := s.ScheduleWake(at, false); err != nil {
		t.Fatal(err)
	}
	s.Shutdown()
	if len(f.cancels) != 0 {
		t.Error("shutdown cancelled the pending wake, so the job would be missed")
	}
}

func TestConcurrentRequestsAreSafe(t *testing.T) {
	f := &fakeOps{}
	s := withOps(f)

	var wg sync.WaitGroup
	for i := range 30 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch i % 4 {
			case 0:
				_ = s.SetBlocked(i%8 == 0, "concurrent")
			case 1:
				_, _ = s.Handle(context.Background(), ipc.OpHelperStatus, nil)
			case 2:
				_ = s.ScheduleWake(time.Now().Add(time.Duration(i)*time.Minute), false)
			case 3:
				s.checkDeadMan()
			}
		}(i)
	}
	wg.Wait()
}

func TestHandleRoutesEveryPrivilegedOperation(t *testing.T) {
	f := &fakeOps{}
	s := withOps(f)
	at := time.Now().Add(time.Hour).Truncate(time.Second)

	blockReq, _ := json.Marshal(ipc.SetSleepBlockedReq{Blocked: true, Reason: "test"})
	if _, err := s.Handle(context.Background(), ipc.OpHelperSetSleepBlocked, blockReq); err != nil {
		t.Fatal(err)
	}
	if !f.blocked {
		t.Error("set_sleep_blocked did not reach the platform")
	}

	wakeReq, _ := json.Marshal(ipc.ScheduleWakeReq{At: at})
	if _, err := s.Handle(context.Background(), ipc.OpHelperScheduleWake, wakeReq); err != nil {
		t.Fatal(err)
	}
	if len(f.schedules) != 1 {
		t.Error("schedule_wake did not reach the platform")
	}

	if _, err := s.Handle(context.Background(), ipc.OpHelperCancelWake, nil); err != nil {
		t.Fatal(err)
	}
	if len(f.cancels) != 1 {
		t.Error("cancel_wake did not reach the platform")
	}
}

// The verify op is how the daemon learns the OS really holds the alarm it
// asked for. It existed but was unreachable: no IPC op, no caller, so a
// dropped or firmware-offset wake was reported as scheduled forever.
func TestVerifyWakeIsReachableOverIPC(t *testing.T) {
	f := &fakeOps{}
	s := withOps(f)
	at := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := s.ScheduleWake(at, false); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(ipc.VerifyWakeReq{At: at})
	got, err := s.Handle(context.Background(), ipc.OpHelperVerifyWake, payload)
	if err != nil {
		t.Fatal(err)
	}
	resp, ok := got.(ipc.VerifyWakeResp)
	if !ok || !resp.Registered {
		t.Fatalf("a registered wake did not verify over IPC: %+v", got)
	}

	// And a time the OS does not hold must come back unregistered.
	payload, _ = json.Marshal(ipc.VerifyWakeReq{At: at.Add(time.Hour)})
	got, err = s.Handle(context.Background(), ipc.OpHelperVerifyWake, payload)
	if err != nil {
		t.Fatal(err)
	}
	if resp := got.(ipc.VerifyWakeResp); resp.Registered {
		t.Fatal("a wake the OS does not hold verified as registered")
	}
}
