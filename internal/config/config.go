// Package config holds user-tunable daemon settings, persisted as JSON.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/junnam586/goguma/internal/model"
)

// Config is the daemon's tunables. Every field has a working default, so a
// missing or partial config.json is valid: the daemon must never refuse to
// start because of a config gap.
type Config struct {
	// WakeBuffer is how early to wake the machine before a job's fire time.
	// A cold wake involves display init, filesystem remount, and network
	// re-association; 90s is the PRD default and comfortably covers it.
	WakeBuffer model.Duration `json:"wake_buffer"`

	// DefaultCeiling applies to a job with no usable history yet.
	DefaultCeiling model.Duration `json:"default_ceiling"`

	// WakeOnlyHold is how long to stay awake for a job that cannot be
	// observed at all (detection mode "none"). It is a fixed window rather
	// than a learned one because there is nothing to measure, the price of
	// the machine not being able to see the job.
	WakeOnlyHold model.Duration `json:"wake_only_hold"`

	// MinCeiling floors the learned ceiling. Without it, a job that has only
	// ever taken 200ms would get a sub-second ceiling and be force-released
	// the moment it runs slightly long.
	MinCeiling model.Duration `json:"min_ceiling"`

	// MaxCeiling caps the learned ceiling regardless of history, so one
	// pathological run cannot commit the machine to an all-night hold.
	MaxCeiling model.Duration `json:"max_ceiling"`

	// CeilingMultiplier is applied to the p95 of recent runs. 1.2 gives
	// headroom for a slow run without approaching worst-case padding.
	CeilingMultiplier float64 `json:"ceiling_multiplier"`

	// HistoryWindow is how many recent runs the estimator considers.
	HistoryWindow int `json:"history_window"`

	// MinRunsForEstimate is how many completed runs are needed before the
	// learned ceiling replaces DefaultCeiling. Below this, a single fast run
	// would produce an overconfident ceiling.
	MinRunsForEstimate int `json:"min_runs_for_estimate"`

	// TickInterval is the daemon's main loop period.
	TickInterval model.Duration `json:"tick_interval"`

	// PollInterval is how often the process table is scanned while a
	// pattern-detected window is open. Tighter than TickInterval because it
	// bounds how long we keep holding after a job has already exited.
	PollInterval model.Duration `json:"poll_interval"`

	// WakeReassertInterval is how often the OS wake entry is re-registered.
	// macOS silently drops pmset entries when other apps schedule their own,
	// so set-once-and-forget loses jobs (PRD §5.2).
	WakeReassertInterval model.Duration `json:"wake_reassert_interval"`

	// UseWakeOrPowerOn schedules `wakeorpoweron` instead of `wake`, which
	// also boots a powered-off machine. Off by default: silently powering on
	// a shut-down laptop is a surprise most users would not consent to.
	UseWakeOrPowerOn bool `json:"use_wake_or_power_on"`

	// ThermalCutoutC force-releases every hold above this CPU temperature
	// while the lid is closed. Holding a bagged laptop awake without this is
	// genuinely unsafe (PRD §9).
	ThermalCutoutC float64 `json:"thermal_cutout_c"`

	// LowBatteryCutoutPct force-releases every hold below this charge while
	// on battery with the lid closed, so normal low-power sleep takes over
	// with charge to spare instead of draining to a hard shutdown.
	LowBatteryCutoutPct int `json:"low_battery_cutout_pct"`

	// CutoutRearmMarginC / Pct define how far the hazard must recede before
	// a latched cutout clears. Without hysteresis the system oscillates:
	// release, cool by 0.1°C, re-acquire, cross again.
	CutoutRearmMarginC   float64 `json:"cutout_rearm_margin_c"`
	CutoutRearmMarginPct int     `json:"cutout_rearm_margin_pct"`

	// WebhookURL receives problem events (overrun, wake failure, undetected
	// job, cutout). Empty disables dispatch.
	WebhookURL string `json:"webhook_url,omitempty"`

	// NotifyOnMissedJob posts a native notification when a job's window
	// closed without the job ever being observed.
	NotifyOnMissedJob bool `json:"notify_on_missed_job"`

	// AutoAdopt lists scheduler sources goguma watches continuously and
	// registers new jobs from.
	//
	// Nil means "not configured", which the daemon expands to every adoptable
	// source. Installing goguma is itself the statement that jobs should
	// survive sleep, so requiring a second opt-in mostly produces installs
	// that quietly do nothing, a new silent failure of exactly the kind this
	// tool exists to prevent.
	//
	// The safeguard is visibility, not a switch: what was adopted and what it
	// costs in wakes per day are reported, and an explicit empty list turns it
	// off and is respected.
	//
	// Only sources whose jobs work with no further human action are adoptable.
	// Anything needing a command line edited stays a deliberate decision; see
	// daemon.syncProviders.
	AutoAdopt []string `json:"auto_adopt"`

	// AutoAdoptInterval is how often watched sources are re-read.
	AutoAdoptInterval model.Duration `json:"auto_adopt_interval"`

	// MinImportInterval is the shortest schedule `import` will propose
	// adopting. Anything firing more often than this does not need a wake:
	// it will run on the next natural wake anyway, and waking for it would
	// cost far more battery than the job saves.
	MinImportInterval model.Duration `json:"min_import_interval"`

	// EventLogMaxBytes triggers rotation of events.jsonl.
	EventLogMaxBytes int64 `json:"event_log_max_bytes"`
}

// Default returns the shipped configuration.
func Default() Config {
	return Config{
		WakeBuffer:           model.Duration(90 * time.Second),
		DefaultCeiling:       model.Duration(5 * time.Minute),
		WakeOnlyHold:         model.Duration(3 * time.Minute),
		MinCeiling:           model.Duration(30 * time.Second),
		MaxCeiling:           model.Duration(2 * time.Hour),
		CeilingMultiplier:    1.2,
		HistoryWindow:        20,
		MinRunsForEstimate:   3,
		TickInterval:         model.Duration(10 * time.Second),
		PollInterval:         model.Duration(2 * time.Second),
		WakeReassertInterval: model.Duration(60 * time.Second),
		UseWakeOrPowerOn:     false,
		ThermalCutoutC:       80,
		LowBatteryCutoutPct:  10,
		CutoutRearmMarginC:   5,
		CutoutRearmMarginPct: 5,
		NotifyOnMissedJob:    true,
		AutoAdoptInterval:    model.Duration(2 * time.Minute),
		MinImportInterval:    model.Duration(time.Hour),
		EventLogMaxBytes:     10 << 20,
	}
}

// Normalize repairs out-of-range values in place and returns what it fixed,
// so a hand-edited config produces a visible warning instead of a daemon
// that quietly behaves nothing like the file says.
func (c *Config) Normalize() []string {
	d := Default()
	var fixed []string

	clampDur := func(name string, v *model.Duration, min, max time.Duration, def model.Duration) {
		if *v == 0 {
			*v = def
			return
		}
		if v.D() < min {
			fixed = append(fixed, fmt.Sprintf("%s %s is below the %s minimum; using %s", name, v, model.HumanDuration(min), model.HumanDuration(min)))
			*v = model.Duration(min)
		} else if v.D() > max {
			fixed = append(fixed, fmt.Sprintf("%s %s is above the %s maximum; using %s", name, v, model.HumanDuration(max), model.HumanDuration(max)))
			*v = model.Duration(max)
		}
	}

	clampDur("wake_buffer", &c.WakeBuffer, 10*time.Second, 30*time.Minute, d.WakeBuffer)
	clampDur("default_ceiling", &c.DefaultCeiling, 10*time.Second, 12*time.Hour, d.DefaultCeiling)
	clampDur("wake_only_hold", &c.WakeOnlyHold, 10*time.Second, time.Hour, d.WakeOnlyHold)
	clampDur("min_ceiling", &c.MinCeiling, time.Second, time.Hour, d.MinCeiling)
	clampDur("max_ceiling", &c.MaxCeiling, time.Minute, 24*time.Hour, d.MaxCeiling)
	clampDur("tick_interval", &c.TickInterval, time.Second, 5*time.Minute, d.TickInterval)
	clampDur("poll_interval", &c.PollInterval, 250*time.Millisecond, time.Minute, d.PollInterval)
	clampDur("wake_reassert_interval", &c.WakeReassertInterval, 10*time.Second, 10*time.Minute, d.WakeReassertInterval)
	clampDur("min_import_interval", &c.MinImportInterval, 0, 24*time.Hour, d.MinImportInterval)
	clampDur("auto_adopt_interval", &c.AutoAdoptInterval, 30*time.Second, time.Hour, d.AutoAdoptInterval)

	if c.MinCeiling > c.MaxCeiling {
		fixed = append(fixed, fmt.Sprintf("min_ceiling %s exceeds max_ceiling %s; swapping", c.MinCeiling, c.MaxCeiling))
		c.MinCeiling, c.MaxCeiling = c.MaxCeiling, c.MinCeiling
	}
	if c.CeilingMultiplier < 1.0 || c.CeilingMultiplier > 10 {
		fixed = append(fixed, fmt.Sprintf("ceiling_multiplier %.2f is outside 1.0-10.0; using %.2f", c.CeilingMultiplier, d.CeilingMultiplier))
		c.CeilingMultiplier = d.CeilingMultiplier
	}
	if c.HistoryWindow < 2 || c.HistoryWindow > 1000 {
		c.HistoryWindow = d.HistoryWindow
	}
	if c.MinRunsForEstimate < 1 || c.MinRunsForEstimate > c.HistoryWindow {
		c.MinRunsForEstimate = d.MinRunsForEstimate
	}
	// The thermal range matches what Adrafinil validated as safe on Apple
	// silicon; outside it the cutout is either useless or trips constantly.
	if c.ThermalCutoutC < 70 || c.ThermalCutoutC > 95 {
		fixed = append(fixed, fmt.Sprintf("thermal_cutout_c %.0f is outside the safe 70-95 range; using %.0f", c.ThermalCutoutC, d.ThermalCutoutC))
		c.ThermalCutoutC = d.ThermalCutoutC
	}
	if c.LowBatteryCutoutPct < 5 || c.LowBatteryCutoutPct > 50 {
		fixed = append(fixed, fmt.Sprintf("low_battery_cutout_pct %d is outside 5-50; using %d", c.LowBatteryCutoutPct, d.LowBatteryCutoutPct))
		c.LowBatteryCutoutPct = d.LowBatteryCutoutPct
	}
	if c.CutoutRearmMarginC <= 0 {
		c.CutoutRearmMarginC = d.CutoutRearmMarginC
	}
	// Bounded because it also sets how much headroom above the cutout is
	// required before waking; an unbounded value would push the wake floor
	// past full charge and silently stop the machine ever waking.
	if c.CutoutRearmMarginPct <= 0 || c.CutoutRearmMarginPct > 50 {
		fixed = append(fixed, fmt.Sprintf(
			"cutout_rearm_margin_pct %d is outside 1-50; using %d",
			c.CutoutRearmMarginPct, d.CutoutRearmMarginPct))
		c.CutoutRearmMarginPct = d.CutoutRearmMarginPct
	}
	if c.EventLogMaxBytes < 64<<10 {
		c.EventLogMaxBytes = d.EventLogMaxBytes
	}
	return fixed
}

// Load reads config.json, returning defaults when the file does not exist.
// A malformed file is a hard error: silently reverting to defaults would
// change safety thresholds without telling anyone.
func Load(path string) (Config, []string, error) {
	c := Default()
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c, nil, nil
		}
		return c, nil, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return Default(), nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	warnings := c.Normalize()
	return c, warnings, nil
}

// Save writes config.json atomically, so a crash mid-write cannot leave a
// truncated file that fails to parse on next start.
func Save(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
