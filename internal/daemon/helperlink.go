package daemon

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/junnam586/goguma/internal/ipc"
	"github.com/junnam586/goguma/internal/paths"
)

// helperLink talks to the privileged helper.
//
// Connections are made per call rather than held open. The helper's dead-man
// switch measures time since last contact, and the daemon re-pushes its state
// on a timer well inside that window, so a short-lived connection per
// operation is both simpler and self-healing: a helper restart is picked up on
// the next call instead of leaving a dead connection to detect and rebuild.
//
// Calls are made from a single pump goroutine rather than from whoever wants
// the state changed. A helper mid-restart accepts a connection and then never
// answers, and HelperTimeout is 45s because the helper is allowed 10s for a
// slow pmset, so a push issued from the control loop froze the whole daemon
// for three quarters of a minute. That is not a hypothetical: three of them
// back to back stalled the loop for 2m20s, which crossed the 60s wall-clock
// threshold in `detectSleepGap` and was recorded as the machine having slept,
// while the helper's own dead-man switch released the sleep block because the
// daemon had gone quiet. One stuck socket read turned into a fabricated sleep
// interval, fake missed runs written against it, and a hold dropped mid-job.
type helperLink struct {
	log *slog.Logger

	mu            sync.Mutex
	connected     bool
	version       string
	lastErr       error
	wantBlocked   bool
	blockReason   string
	scheduledAt   *time.Time
	scheduledKind bool // UseWakeOrPowerOn for scheduledAt
	cancelPending bool // a cancel was issued and has not been confirmed
	lastPush      time.Time
	// wantFull asks the next reconcile to refresh the version and re-assert
	// the wake schedule, not just the sleep block.
	wantFull bool

	// dirty wakes the pump. Buffered at one and written without blocking, so
	// a caller never waits and a burst while a call is in flight collapses
	// into a single follow-up carrying the latest desired state.
	dirty chan struct{}
	// done closes when the pump has stopped, so shutdown can have the last
	// word on the sleep block rather than racing a push already in flight.
	done chan struct{}
}

func newHelperLink(log *slog.Logger) *helperLink {
	return &helperLink{
		log:   log,
		dirty: make(chan struct{}, 1),
		done:  make(chan struct{}),
	}
}

// Push records the desired sleep-block state and returns immediately.
//
// This is what the control loop calls. It never touches the socket, so a
// helper that has stopped answering costs the loop nothing.
func (h *helperLink) Push(blocked bool, reason string) {
	h.mu.Lock()
	h.wantBlocked, h.blockReason = blocked, reason
	h.mu.Unlock()
	h.nudge()
}

// Repush asks for a full reconcile: refresh the version, re-assert the sleep
// block, and re-register the wake schedule.
//
// The wake half is why this is separate from an ordinary push. macOS can drop
// pmset entries when other applications register their own, so set-once-and-
// forget loses jobs (PRD §5.2), but re-asserting on every two-second push
// would hammer pmset for nothing.
func (h *helperLink) Repush() {
	h.mu.Lock()
	h.wantFull = true
	h.mu.Unlock()
	h.nudge()
}

func (h *helperLink) nudge() {
	select {
	case h.dirty <- struct{}{}:
	default:
	}
}

// pump is the only goroutine that reconciles state with the helper.
//
// Being the only one is the point: calls are serialised without a lock that
// anyone else can be caught behind, and a call that takes the full timeout
// delays nothing except the next reconcile.
func (h *helperLink) pump(ctx context.Context) {
	defer close(h.done)
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.dirty:
		}
		h.reconcile()
	}
}

// reconcile brings the helper into line with the desired state.
func (h *helperLink) reconcile() {
	h.mu.Lock()
	full := h.wantFull
	h.wantFull = false
	want, reason := h.wantBlocked, h.blockReason
	at, kind, cancel := h.scheduledAt, h.scheduledKind, h.cancelPending
	h.mu.Unlock()

	if full {
		// Refresh the cached version while we are talking to it anyway.
		//
		// Status is the only call that returns the helper's version, and
		// nothing used to call it, so `h.version` stayed empty for the life of
		// the daemon and every surface that reports it printed a blank:
		// `goguma version` said "helper" with nothing after it, and doctor said
		// "connected, version ". That is worse than cosmetic here, because this
		// package goes out of its way to support a daemon and helper at
		// different versions, and the skew it tolerates was the one thing it
		// could not show.
		_, _ = h.Status()
	}

	if err := h.call(ipc.OpHelperSetSleepBlocked, ipc.SetSleepBlockedReq{
		Blocked: want, Reason: reason,
	}, nil); err != nil {
		// Not fatal: the unprivileged idle assertion still holds a lid-open
		// machine awake. Only clamshell holds are lost, which the status
		// output reports rather than silently pretending to cover.
		h.log.Debug("helper sleep-block push failed", "err", err)
		return
	}
	h.mu.Lock()
	h.lastPush = time.Now()
	h.mu.Unlock()

	// A cancel that failed is retried here, the same way a lost schedule is
	// re-asserted below. Without this, one dropped IPC call leaves the wake
	// registered with the OS forever: the machine keeps waking for a job
	// that was skipped, removed, or disabled.
	if cancel {
		if h.call(ipc.OpHelperCancelWake, nil, nil) == nil {
			h.mu.Lock()
			h.cancelPending = false
			h.mu.Unlock()
		}
		return
	}

	// The kind must ride along: re-asserting without it downgrades a
	// wakeorpoweron entry to wake, and the helper then records a kind the OS
	// entry does not have, so later cancels stop matching.
	if full && at != nil && at.After(time.Now()) {
		_ = h.call(ipc.OpHelperScheduleWake, ipc.ScheduleWakeReq{At: *at, UseWakeOrPowerOn: kind}, nil)
	}
}

// Settle waits for the pump to stop, so a final synchronous SetBlocked is the
// last thing the helper hears.
//
// Bounded, because the reason a push is still in flight is almost always a
// helper that has stopped answering, and in that case the block cannot be
// cleared from here anyway; the helper's dead-man switch is what releases it.
func (h *helperLink) Settle(limit time.Duration) {
	t := time.NewTimer(limit)
	defer t.Stop()
	select {
	case <-h.done:
	case <-t.C:
		h.log.Warn("a helper push was still in flight at shutdown")
	}
}

func (h *helperLink) call(op ipc.Op, payload, out any) error {
	err := ipc.DoTimeout(paths.HelperSocket, ipc.HelperTimeout, op, payload, out)

	h.mu.Lock()
	defer h.mu.Unlock()
	if err != nil {
		// An application-level rejection means the helper answered: the
		// link is up. Treating it as lost contact flipped status to "the
		// privileged helper isn't running, run goguma install" after every
		// refused request; an older helper rejecting a newer op did that on
		// every single wake registration, forever, on a skew the installer
		// explicitly supports.
		var app *ipc.AppError
		if errors.As(err, &app) {
			if !h.connected {
				h.log.Info("connected to the privileged helper")
			}
			h.connected, h.lastErr = true, nil
			return err
		}
		if h.connected {
			h.log.Warn("lost contact with the privileged helper", "err", err)
		}
		h.connected, h.lastErr = false, err
		return err
	}
	if !h.connected {
		h.log.Info("connected to the privileged helper")
	}
	h.connected, h.lastErr = true, nil
	return nil
}

// Status queries the helper, refreshing the cached version string.
func (h *helperLink) Status() (ipc.HelperStatusResp, error) {
	var resp ipc.HelperStatusResp
	if err := h.call(ipc.OpHelperStatus, nil, &resp); err != nil {
		return resp, err
	}
	h.mu.Lock()
	h.version = resp.Version
	h.mu.Unlock()
	return resp, nil
}

// SetBlocked records the desired state and pushes it to the helper.
//
// The desired state is remembered even when the push fails, so a helper that
// comes back later is brought into line by the next re-push rather than the
// daemon believing sleep is blocked when it is not.
func (h *helperLink) SetBlocked(blocked bool, reason string) error {
	h.mu.Lock()
	h.wantBlocked, h.blockReason = blocked, reason
	h.mu.Unlock()

	err := h.call(ipc.OpHelperSetSleepBlocked, ipc.SetSleepBlockedReq{
		Blocked: blocked, Reason: reason,
	}, nil)
	if err == nil {
		h.mu.Lock()
		h.lastPush = time.Now()
		h.mu.Unlock()
	}
	return err
}

// ScheduleWake asks the helper to register an OS wake.
func (h *helperLink) ScheduleWake(at time.Time, useWakeOrPowerOn bool) error {
	err := h.call(ipc.OpHelperScheduleWake, ipc.ScheduleWakeReq{
		At: at, UseWakeOrPowerOn: useWakeOrPowerOn,
	}, nil)
	if err != nil {
		return err
	}
	h.mu.Lock()
	// A new schedule supersedes any cancel still in flight: the helper's
	// ScheduleWake reconciles against the OS and removes stale entries.
	h.scheduledAt, h.scheduledKind = &at, useWakeOrPowerOn
	h.cancelPending = false
	h.mu.Unlock()
	return nil
}

// VerifyWake asks the helper whether the OS genuinely holds a wake at t.
func (h *helperLink) VerifyWake(at time.Time) (bool, error) {
	var resp ipc.VerifyWakeResp
	if err := h.call(ipc.OpHelperVerifyWake, ipc.VerifyWakeReq{At: at}, &resp); err != nil {
		return false, err
	}
	return resp.Registered, nil
}

// CancelWake clears any scheduled wake.
//
// The intent is remembered before the call is attempted, so a cancel the
// helper never heard is retried by Repush instead of being lost, exactly as
// SetBlocked remembers its desired state.
func (h *helperLink) CancelWake() error {
	h.mu.Lock()
	h.scheduledAt = nil
	h.cancelPending = true
	h.mu.Unlock()
	err := h.call(ipc.OpHelperCancelWake, nil, nil)
	if err == nil {
		h.mu.Lock()
		h.cancelPending = false
		h.mu.Unlock()
	}
	return err
}

func (h *helperLink) Connected() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.connected
}

func (h *helperLink) Version() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.version
}

func (h *helperLink) ScheduledAt() *time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.scheduledAt
}

func (h *helperLink) LastError() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastErr
}
