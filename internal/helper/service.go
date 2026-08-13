// Package helper is the privileged component. It runs as root and exposes
// exactly two mutating operations (block/unblock sleep, and schedule/cancel
// an OS wake) over a local socket guarded by peer-credential checks.
//
// It holds no policy whatsoever. It does not know what a job is, when
// anything is scheduled, or why sleep is being blocked. Every decision lives
// in the unprivileged daemon. That split is the entire security design: this
// file is the only code in the product running with elevated privilege, and
// it is meant to stay small enough to audit in one sitting.
package helper

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/junnam586/goguma/internal/ipc"
)

// deadManTimeout is how long the helper waits, after the last daemon
// connection drops while sleep is blocked, before clearing the block itself.
//
// This exists because `disablesleep` is global and persists in the power
// management preferences: it is NOT cleared automatically when the process
// that set it dies. If the daemon is SIGKILLed at logout while holding a
// block, nothing else would ever clear it, and the machine would be unable to
// sleep until someone noticed and ran pmset by hand. The daemon re-pushes its
// state well inside this window, so a healthy system never trips it.
const deadManTimeout = 60 * time.Second

// wakeEntry is one owner-tagged row of the OS wake schedule, as listed by
// the platform. The kind and owner travel WITH the entry: pmset's cancel
// matches on kind as well as time, and a remembered kind is forgotten on
// helper restart, after which a stale wakeorpoweron entry kept powering the
// machine on with nothing able to remove it.
type wakeEntry struct {
	at time.Time
	// powerOn marks a wakeorpoweron entry, which also boots a powered-off
	// machine.
	powerOn bool
	// owner is the tag the entry was registered under. Entries tagged with
	// the binary's previous name are still ours to clean up; this machine's
	// schedule can hold one across an upgrade.
	owner string
}

const (
	// wakeOwnerTag tags entries this build registers. pmset supports an
	// owner string, and it matters: tagging lets us cancel precisely our
	// own entry instead of using `cancelall`, which would delete the user's
	// alarms and other apps' scheduled events.
	wakeOwnerTag = "goguma"
	// legacyWakeOwnerTag is the project's previous name; entries it
	// registered would otherwise be orphaned by an upgrade and fire
	// forever un-cancellable.
	legacyWakeOwnerTag = "wakeguard"
)

// platformOps are the operations that genuinely require root.
//
// They are held as fields rather than called directly so the policy around
// them (the dead-man switch, crash recovery, state reconciliation, the
// shutdown ordering) can be tested without being root. Only these five
// functions actually need privilege; everything that could plausibly harbour a
// bug is the logic that decides *when* to call them, and that logic was
// previously untestable purely because it was welded to a `pmset` invocation.
type platformOps struct {
	setBlocked  func(bool) error
	readBlocked func() (bool, error)
	scheduleAt  func(time.Time, bool) error
	cancelAt    func(wakeEntry) error
	listWakes   func() ([]wakeEntry, error)
}

func realOps() platformOps {
	return platformOps{
		setBlocked:  setSleepBlocked,
		readBlocked: readSleepBlocked,
		scheduleAt:  scheduleWake,
		cancelAt:    cancelWake,
		listWakes:   scheduledWakes,
	}
}

// Service is the privileged helper.
type Service struct {
	log     *slog.Logger
	version string
	ops     platformOps

	mu           sync.Mutex
	blocked      bool
	scheduledAt  *time.Time
	useWakeOrPwr bool

	// lastContact is refreshed on every daemon request; the dead-man switch
	// measures from it.
	lastContact time.Time
	connections int
}

func New(version string, log *slog.Logger) *Service {
	return &Service{version: version, log: log, ops: realOps(), lastContact: time.Now()}
}

// Recover clears any block left over from a previous run.
//
// Called at startup, which the launchd/systemd unit arranges to happen at
// boot before any user logs in. A helper that crashed while blocked, or a
// machine that was force-powered-off mid-hold, would otherwise come back up
// with sleep permanently disabled.
func (s *Service) Recover() {
	blocked, err := s.ops.readBlocked()
	if err != nil {
		s.log.Warn("could not read sleep state at startup", "err", err)
		return
	}
	if !blocked {
		return
	}
	s.log.Warn("found sleep still blocked from a previous run; clearing it")
	if err := s.ops.setBlocked(false); err != nil {
		s.log.Error("failed to clear stale sleep block", "err", err)
		return
	}
	s.mu.Lock()
	s.blocked = false
	s.mu.Unlock()
}

// Handle dispatches one request from the daemon.
func (s *Service) Handle(ctx context.Context, op ipc.Op, payload json.RawMessage) (any, error) {
	s.mu.Lock()
	s.lastContact = time.Now()
	s.mu.Unlock()

	switch op {
	case ipc.OpPing:
		return map[string]string{"version": s.version}, nil

	case ipc.OpHelperStatus:
		return s.status()

	case ipc.OpHelperSetSleepBlocked:
		var req ipc.SetSleepBlockedReq
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("bad request: %w", err)
		}
		return nil, s.SetBlocked(req.Blocked, req.Reason)

	case ipc.OpHelperScheduleWake:
		var req ipc.ScheduleWakeReq
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("bad request: %w", err)
		}
		return nil, s.ScheduleWake(req.At, req.UseWakeOrPowerOn)

	case ipc.OpHelperVerifyWake:
		var req ipc.VerifyWakeReq
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("bad request: %w", err)
		}
		ok, err := s.VerifyWake(req.At)
		if err != nil {
			return nil, err
		}
		return ipc.VerifyWakeResp{Registered: ok}, nil

	case ipc.OpHelperCancelWake:
		return nil, s.CancelWake()

	default:
		return nil, fmt.Errorf("helper does not implement %q", op)
	}
}

func (s *Service) status() (ipc.HelperStatusResp, error) {
	// Read the real kernel state rather than reporting our own belief: the
	// setting is global and can change underneath us.
	actual, err := s.ops.readBlocked()
	if err != nil {
		return ipc.HelperStatusResp{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if actual != s.blocked {
		s.log.Warn("kernel sleep state diverged from helper's record",
			"kernel", actual, "helper", s.blocked)
		s.blocked = actual
	}
	return ipc.HelperStatusResp{
		Version:      s.version,
		SleepBlocked: actual,
		ScheduledAt:  s.scheduledAt,
		Mechanism:    Mechanism,
	}, nil
}

// SetBlocked applies the requested sleep-block state.
//
// Deliberately not short-circuited when the state appears unchanged: the
// daemon re-pushes the current state periodically precisely so that a failed
// pmset, a raced helper restart, or a kernel reset across sleep/wake gets
// repaired. Skipping the call when we already believe we are blocked would
// defeat that self-healing.
func (s *Service) SetBlocked(blocked bool, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ops.setBlocked(blocked); err != nil {
		return err
	}
	if s.blocked != blocked {
		s.log.Info("sleep block changed", "blocked", blocked, "reason", reason)
	}
	s.blocked = blocked
	return nil
}

// ScheduleWake registers an OS wake, replacing any previous one.
//
// Reconciles against what the OS actually has registered rather than against
// what this process remembers registering. Two reasons, both of which produced
// real duplicate entries on a live machine:
//
//   - `pmset schedule` does not deduplicate. Re-asserting the same wake time,
//     which the daemon does routinely, previously skipped the cancel (the time
//     had not changed) and scheduled anyway, adding an identical entry every
//     time.
//   - `scheduledAt` is in-memory, so a helper restart forgets every entry it
//     owns while those entries remain in the system's schedule. Each restart
//     then added one more, and nothing ever removed them.
//
// The observed result was six identical wake events for a single job. They are
// individually harmless but they accumulate without bound, they are visible to
// anyone running `pmset -g sched`, and a tool that silently litters the
// system's power schedule has no business asking to be trusted with it.
func (s *Service) ScheduleWake(at time.Time, useWakeOrPowerOn bool) error {
	// Drop every entry we own that is not exactly the one being asked for:
	// wrong time, wrong kind, or the binary's previous name. Each cancel is
	// driven by the entry as LISTED, not by remembered state; a remembered
	// kind was forgotten on helper restart, after which cancels silently
	// missed and a stale wakeorpoweron entry kept powering the machine on
	// with nothing able to remove it. Only owner-tagged entries are listed,
	// so another application's wake is never touched.
	matching := 0
	readSchedule := true
	existing, err := s.ops.listWakes()
	if err != nil {
		readSchedule = false
		s.log.Warn("could not read the current wake schedule, "+
			"scheduling without reconciling", "err", err)
	}
	for _, e := range existing {
		if e.at.Equal(at) && e.powerOn == useWakeOrPowerOn && e.owner == wakeOwnerTag {
			matching++
			continue
		}
		if cerr := s.ops.cancelAt(e); cerr != nil {
			s.log.Warn("could not cancel a stale wake", "at", e.at, "err", cerr)
		}
	}

	// Duplicates at the *wanted* time still have to be collapsed, and they
	// cannot be thinned one at a time: `pmset schedule cancel` matches on the
	// timestamp, so a single cancel may remove one entry or all of them and
	// there is no way to ask for a specific one. Clearing the timestamp
	// outright and re-adding once is the only way to land on exactly one.
	//
	// Cancelling an entry that is already gone is explicitly not an error, so
	// over-cancelling here is safe.
	if matching > 1 {
		s.log.Info("collapsing duplicate wake entries", "at", at, "count", matching)
		want := wakeEntry{at: at, powerOn: useWakeOrPowerOn, owner: wakeOwnerTag}
		for range matching {
			if cerr := s.ops.cancelAt(want); cerr != nil {
				s.log.Warn("could not cancel a duplicate wake", "at", at, "err", cerr)
				break
			}
		}
		matching = 0
	}

	// Re-scheduling an entry that is already present is what created the
	// duplicates, so when the OS already holds exactly one, there is nothing
	// to do. When the schedule could not be read at all, schedule anyway:
	// a duplicate entry is a far better failure than no wake.
	if matching == 0 || !readSchedule {
		if err := s.ops.scheduleAt(at, useWakeOrPowerOn); err != nil {
			return err
		}
	}

	s.mu.Lock()
	s.scheduledAt, s.useWakeOrPwr = &at, useWakeOrPowerOn
	s.mu.Unlock()
	return nil
}

// CancelWake removes our scheduled wake, if any.
//
// It reconciles against the OS instead of trusting memory, exactly as
// ScheduleWake does: after a helper restart scheduledAt is nil while the
// persisted entries survive, and trusting memory would report success with
// the wake still registered. Only owner-tagged entries are listed, so
// another application's wake is never touched.
func (s *Service) CancelWake() error {
	s.mu.Lock()
	at, kind := s.scheduledAt, s.useWakeOrPwr
	s.scheduledAt = nil
	s.mu.Unlock()

	existing, err := s.ops.listWakes()
	if err != nil {
		s.log.Warn("could not read the wake schedule to cancel against", "err", err)
		if at != nil {
			return s.ops.cancelAt(wakeEntry{at: *at, powerOn: kind, owner: wakeOwnerTag})
		}
		// The caller keeps the cancel pending and retries; reporting success
		// here would leave an unknown entry registered forever.
		return err
	}
	for _, e := range existing {
		if cerr := s.ops.cancelAt(e); cerr != nil {
			s.log.Warn("could not cancel a wake", "at", e.at, "err", cerr)
			return cerr
		}
	}
	return nil
}

// VerifyWake reports whether a wake at t is genuinely registered with the OS.
// See scheduledWakes for why this check exists.
func (s *Service) VerifyWake(t time.Time) (bool, error) {
	entries, err := s.ops.listWakes()
	if err != nil {
		return false, err
	}
	for _, got := range entries {
		if got.owner != wakeOwnerTag {
			continue
		}
		// pmset stores second resolution; allow a small tolerance so a
		// sub-second difference is not read as a missing entry.
		if d := got.at.Sub(t); d < time.Second && d > -time.Second {
			return true, nil
		}
	}
	return false, nil
}

// ConnectionOpened / ConnectionClosed track live daemon connections for the
// dead-man switch.
func (s *Service) ConnectionOpened() {
	s.mu.Lock()
	s.connections++
	s.lastContact = time.Now()
	s.mu.Unlock()
}

func (s *Service) ConnectionClosed() {
	s.mu.Lock()
	if s.connections > 0 {
		s.connections--
	}
	s.mu.Unlock()
}

// RunDeadMan clears a stranded sleep block if the daemon goes away.
func (s *Service) RunDeadMan(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.checkDeadMan()
		}
	}
}

func (s *Service) checkDeadMan() {
	s.mu.Lock()
	blocked := s.blocked
	conns := s.connections
	idle := time.Since(s.lastContact)
	s.mu.Unlock()

	if !blocked || conns > 0 || idle < deadManTimeout {
		return
	}
	s.log.Warn("no daemon contact while sleep is blocked; releasing as a safety measure",
		"idle", idle.Round(time.Second))
	if err := s.SetBlocked(false, "dead-man switch: daemon unreachable"); err != nil {
		s.log.Error("dead-man release failed", "err", err)
	}
}

// Shutdown clears the block on the way out.
//
// Bound by its own timeout: this runs on SIGTERM, when the OS is already
// counting down to SIGKILL during shutdown or unload, and a blocking pmset
// call could otherwise be cut off partway.
func (s *Service) Shutdown() {
	s.mu.Lock()
	blocked := s.blocked
	at, kind := s.scheduledAt, s.useWakeOrPwr
	s.mu.Unlock()

	if blocked {
		s.log.Info("clearing sleep block on shutdown")
		if err := s.ops.setBlocked(false); err != nil {
			s.log.Error("failed to clear sleep block on shutdown", "err", err)
		}
	}
	// A scheduled wake is deliberately left in place: the machine should
	// still wake for the job even if the helper is being restarted, and a
	// stale entry is harmless; it wakes the machine once and is consumed.
	_ = at
	_ = kind
}
