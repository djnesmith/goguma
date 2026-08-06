// Package power wraps the OS primitives WakeGuard needs: scheduling a wake
// from sleep, holding sleep off, and reading the machine state the safety
// cutouts act on.
//
// Everything privileged is routed through the helper, not performed here.
// This package is what the *unprivileged* daemon may call directly.
package power

import (
	"time"

	"github.com/junnam/wakeguard/internal/schedule"
)

// IdleAssertion is a held block on idle system sleep. It does not survive a
// lid close — that requires the privileged helper — but it is enough for a
// lid-open machine and needs no privilege at all.
type IdleAssertion interface {
	Release() error
}

// Platform is the per-OS implementation.
type Platform interface {
	// Name identifies the implementation in logs and `status`.
	Name() string

	// HoldIdleSleep blocks idle system sleep until the returned assertion is
	// released. Unprivileged.
	HoldIdleSleep(reason string) (IdleAssertion, error)

	// ReadState samples lid, power source, battery, and temperature. Called
	// on every daemon tick, so it must be cheap and must never block for
	// long — a wedged sample would stall the cutout checks that keep a
	// lid-closed machine safe.
	ReadState() (State, error)

	// SleepHistory reconstructs recent sleep/wake periods from whatever the
	// OS already records, so miss-risk estimates work on the day of install
	// rather than after two weeks of self-observation.
	SleepHistory(lookback time.Duration) (*schedule.SleepHistory, error)

	// SupportsClamshellHold reports whether lid-closed holds are achievable
	// on this platform at all, so the UI can be honest rather than implying
	// a guarantee it cannot keep.
	SupportsClamshellHold() bool

	// WakeScheduleSupported reports whether scheduling a wake works here.
	// On Linux this is genuinely hardware-dependent (PRD §12.3), so it is a
	// runtime probe rather than a compile-time assumption.
	WakeScheduleSupported() (bool, string)
}

// State is a sample of machine conditions.
type State struct {
	LidClosed  bool
	OnAC       bool
	BatteryPct int

	// TempC is nil when no temperature source is available. The thermal
	// cutout must treat "unknown" as "cannot verify safety" rather than as
	// "cool", so this is deliberately a pointer and not a zero value.
	TempC      *float64
	TempSource string
}

// Sampled reports whether a temperature reading was actually obtained.
func (s State) Sampled() bool { return s.TempC != nil }
