// Package power wraps the OS primitives goguma needs: scheduling a wake
// from sleep, holding sleep off, and reading the machine state the safety
// cutouts act on.
//
// Everything privileged is routed through the helper, not performed here.
// This package is what the *unprivileged* daemon may call directly.
package power

import (
	"errors"
	"time"

	"github.com/junnam586/goguma/internal/schedule"
)

// IdleAssertion is a held block on idle system sleep. It does not survive a
// lid close (that requires the privileged helper), but it is enough for a
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
	// long; a wedged sample would stall the cutout checks that keep a
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

	// SleepNow asks the machine to sleep immediately.
	//
	// Needed because goguma cannot ask for a quiet wake. A scheduled wake is
	// classified by macOS as user-initiated, so the whole machine comes up and
	// iCloud, Spotlight and the rest take their own assertions; measured on one
	// Mac, a 31-second job left it awake for six hours after goguma had already
	// released. Its own maintenance wakes are a different, constrained class
	// that returns to sleep in 45 seconds, and third parties cannot request
	// one. So the only thing in a position to undo a goguma wake is goguma.
	SleepNow() error

	// PowerOnRunsJobs reports whether booting this machine from off would
	// actually get scheduled jobs run, and if not, why not.
	//
	// `use_wake_or_power_on` schedules `wakeorpoweron` instead of `wake`, and
	// the setting reads as though powering on were equivalent to waking. It is
	// not. A FileVault machine boots to an unlock screen with the OS not yet
	// running, so nothing fires until someone types a password, and the toggle
	// silently promises something it cannot do. Reported rather than guessed,
	// because the answer differs per machine.
	PowerOnRunsJobs() (bool, string)

	// LastWakeAt reports when the machine last woke from sleep.
	//
	// The OS's own record, not an inference. goguma used to decide "did I wake
	// this machine" by watching for a gap in its own ticks, which misses a wake
	// out of a light sleep: the daemon keeps ticking through it, sees no gap,
	// and concludes it woke nothing. Measured on a real run, that was wrong for
	// two wakes out of three, and each time it left the machine awake that
	// goguma had itself brought up.
	//
	// Cheap on purpose, because this is consulted whenever a window opens.
	// `pmset -g log` carries the same truth and takes about seven seconds.
	LastWakeAt() (time.Time, error)

	// UserIdle reports how long since the last keyboard or pointer event.
	//
	// Separate from ReadState because it is only consulted before sleeping the
	// machine, and ReadState runs on every tick where it would be pure cost.
	// Returning an error means "cannot tell", which callers must treat as
	// "someone may be there" rather than as zero.
	UserIdle() (time.Duration, error)
}

// ErrUnsupported is returned by Platform operations a system cannot perform.
var ErrUnsupported = errors.New("not supported on this platform")

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

	// ThermalWarn is the OS's own thermal warning (pmset -g therm on macOS).
	// It exists because the degree threshold is calibrated for die sensors,
	// and on machines whose die keys are unreadable the probe falls back to
	// a chassis-proximity sensor that reads tens of degrees cooler: the
	// enclosure can be dangerously hot while the number stays under every
	// permitted threshold. The OS's verdict does not depend on which sensor
	// goguma happened to find.
	ThermalWarn bool
}

// Sampled reports whether a temperature reading was actually obtained.
func (s State) Sampled() bool { return s.TempC != nil }
