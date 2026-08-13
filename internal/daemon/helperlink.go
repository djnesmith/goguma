package daemon

import (
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
	unavailable   bool // helper is not installed at all
	unavailableAt time.Time
}

func newHelperLink(log *slog.Logger) *helperLink {
	return &helperLink{log: log}
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

// Repush re-asserts the current desired state.
//
// This is the self-healing path for three real failure modes: a pmset call
// that silently failed, a helper that restarted and lost its state, and the
// kernel resetting SleepDisabled across a sleep/wake cycle. The helper's set
// is idempotent, so this is a no-op when nothing was lost and a repair when
// something was.
func (h *helperLink) Repush() {
	// Refresh the cached version while we are talking to it anyway.
	//
	// Status is the only call that returns the helper's version, and nothing
	// used to call it, so `h.version` stayed empty for the life of the daemon
	// and every surface that reports it printed a blank: `goguma version` said
	// "helper" with nothing after it, and doctor said "connected, version ".
	// That is worse than cosmetic here, because this package goes out of its
	// way to support a daemon and helper at different versions, and the skew it
	// tolerates was the one thing it could not show.
	_, _ = h.Status()

	h.mu.Lock()
	want, reason, at, kind, cancel := h.wantBlocked, h.blockReason, h.scheduledAt, h.scheduledKind, h.cancelPending
	h.mu.Unlock()

	if err := h.call(ipc.OpHelperSetSleepBlocked, ipc.SetSleepBlockedReq{
		Blocked: want, Reason: reason,
	}, nil); err != nil {
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

	// Re-assert the wake schedule too. macOS can drop pmset entries when
	// other applications register their own, so set-once-and-forget loses
	// jobs (PRD §5.2). The kind must ride along: re-asserting without it
	// downgrades a wakeorpoweron entry to wake, and the helper then records
	// a kind the OS entry does not have, so later cancels stop matching.
	if at != nil && at.After(time.Now()) {
		_ = h.call(ipc.OpHelperScheduleWake, ipc.ScheduleWakeReq{At: *at, UseWakeOrPowerOn: kind}, nil)
	}
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
