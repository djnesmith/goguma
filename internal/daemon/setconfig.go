package daemon

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/ipc"
	"github.com/junnam586/goguma/internal/model"
)

// configSetters maps the keys accepted by `goguma config set` to their
// parsers.
//
// An explicit table rather than reflection over the struct: it keeps the
// user-facing key names decoupled from Go field names, gives each key a
// purpose-built error message, and makes the settable surface obvious to a
// reader; reflection would silently expose every field the moment one is
// added, including ones that must not be changed at runtime.
var configSetters = map[string]func(*config.Config, string) error{
	"wake_buffer":     func(c *config.Config, v string) error { return setDur(&c.WakeBuffer, v) },
	"default_ceiling": func(c *config.Config, v string) error { return setDur(&c.DefaultCeiling, v) },
	"wake_only_hold":  func(c *config.Config, v string) error { return setDur(&c.WakeOnlyHold, v) },
	"min_ceiling":     func(c *config.Config, v string) error { return setDur(&c.MinCeiling, v) },
	"max_ceiling":     func(c *config.Config, v string) error { return setDur(&c.MaxCeiling, v) },
	"tick_interval":   func(c *config.Config, v string) error { return setDur(&c.TickInterval, v) },
	"poll_interval":   func(c *config.Config, v string) error { return setDur(&c.PollInterval, v) },
	"min_import_interval": func(c *config.Config, v string) error {
		return setDur(&c.MinImportInterval, v)
	},
	"wake_reassert_interval": func(c *config.Config, v string) error {
		return setDur(&c.WakeReassertInterval, v)
	},

	"ceiling_multiplier": func(c *config.Config, v string) error {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("ceiling_multiplier must be a number like 1.2")
		}
		c.CeilingMultiplier = f
		return nil
	},
	"history_window": func(c *config.Config, v string) error {
		return setInt(&c.HistoryWindow, v, "history_window")
	},
	"min_runs_for_estimate": func(c *config.Config, v string) error {
		return setInt(&c.MinRunsForEstimate, v, "min_runs_for_estimate")
	},
	"thermal_cutout_c": func(c *config.Config, v string) error {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("thermal_cutout_c must be a temperature in celsius, 70-95")
		}
		c.ThermalCutoutC = f
		return nil
	},
	"low_battery_cutout_pct": func(c *config.Config, v string) error {
		return setInt(&c.LowBatteryCutoutPct, v, "low_battery_cutout_pct")
	},
	"cutout_rearm_margin_pct": func(c *config.Config, v string) error {
		return setInt(&c.CutoutRearmMarginPct, v, "cutout_rearm_margin_pct")
	},
	"cutout_rearm_margin_c": func(c *config.Config, v string) error {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("cutout_rearm_margin_c must be a number of degrees")
		}
		c.CutoutRearmMarginC = f
		return nil
	},

	"webhook_url": func(c *config.Config, v string) error {
		v = strings.TrimSpace(v)
		if v != "" && !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
			return fmt.Errorf("webhook_url must start with http:// or https://")
		}
		c.WebhookURL = v
		return nil
	},
	"notify_on_missed_job": func(c *config.Config, v string) error {
		return setBool(&c.NotifyOnMissedJob, v, "notify_on_missed_job")
	},
	"use_wake_or_power_on": func(c *config.Config, v string) error {
		return setBool(&c.UseWakeOrPowerOn, v, "use_wake_or_power_on")
	},
	"auto_adopt": func(c *config.Config, v string) error {
		list, err := validateAutoAdopt(v)
		if err != nil {
			return err
		}
		c.AutoAdopt = list
		return nil
	},
	"auto_adopt_interval": func(c *config.Config, v string) error {
		return setDur(&c.AutoAdoptInterval, v)
	},
	"sleep_after_wake": func(c *config.Config, v string) error {
		return setBool(&c.SleepAfterWake, v, "sleep_after_wake")
	},
	"advisory_checks": func(c *config.Config, v string) error {
		return setBool(&c.AdvisoryChecks, v, "advisory_checks")
	},
	"agent_hooks": func(c *config.Config, v string) error {
		return setBool(&c.AgentHooks, v, "agent_hooks")
	},
}

// errUnadoptable explains why a source cannot be watched, listing the ones
// that can, rather than failing with a bare rejection.
func errUnadoptable(got string, allowed []string) error {
	if len(allowed) == 0 {
		return fmt.Errorf("no scheduler on this machine supports automatic adoption")
	}
	return fmt.Errorf(
		"%q can't be watched automatically. Only schedulers that run their own "+
			"jobs qualify, because everything else needs its command line edited "+
			"to be detectable. Available: %s",
		got, strings.Join(allowed, ", "))
}

// ConfigKeys lists the settable keys, for help text and shell completion.
func ConfigKeys() []string {
	keys := make([]string, 0, len(configSetters))
	for k := range configSetters {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (d *Daemon) setConfig(req ipc.ConfigSetReq) (ipc.ConfigResp, error) {
	key := strings.TrimSpace(strings.ToLower(req.Key))
	set, ok := configSetters[key]
	if !ok {
		return ipc.ConfigResp{}, fmt.Errorf("unknown setting %q; valid keys: %s",
			req.Key, strings.Join(ConfigKeys(), ", "))
	}

	d.mu.Lock()
	next := d.cfg
	d.mu.Unlock()

	if err := set(&next, req.Value); err != nil {
		return ipc.ConfigResp{}, err
	}
	// Normalize before persisting so an out-of-range value is corrected and
	// reported now, rather than silently clamped on the next daemon start.
	warnings := next.Normalize()

	if err := config.Save(d.store.Layout().ConfigFile(), next); err != nil {
		return ipc.ConfigResp{}, fmt.Errorf("saving config: %w", err)
	}

	d.mu.Lock()
	d.cfg, d.cfgWarn = next, warnings
	d.mu.Unlock()

	d.log.Info("configuration changed", "key", key, "value", req.Value)

	// Applied here rather than only on the next start, so the toggle in the
	// menu bar app does what it says the moment it is flipped.
	//
	// The reconcile is the cheap half: hooks are installed in both states, so
	// it almost always finds them already correct. Releasing the open holds is
	// the half that has to happen now.
	if key == "agent_hooks" {
		// Switching off releases what agents are already holding, rather than
		// only declining to open more. A keyed hold survives fifteen minutes
		// without renewal, so without this the Mac stays awake for up to that
		// long after the user asked it to stop — and for the whole of that
		// window `goguma hooks` correctly reports nothing installed while
		// `status` still lists a live agent hold, the exact contradiction the
		// setting exists to remove.
		if !next.AgentHooks {
			d.releaseAgentHolds(time.Now())
		}
		d.bg.Add(1)
		go func() {
			defer d.bg.Done()
			d.reconcileAgentHooks(next)
		}()
	}

	return ipc.ConfigResp{
		Config:           next,
		Warnings:         warnings,
		WakeFloorBasePct: wakeFloor(next, -1),
	}, nil
}

func setDur(dst *model.Duration, v string) error {
	d, err := model.ParseDuration(v)
	if err != nil {
		return err
	}
	*dst = model.Duration(d)
	return nil
}

func setInt(dst *int, v, name string) error {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fmt.Errorf("%s must be a whole number", name)
	}
	*dst = n
	return nil
}

func setBool(dst *bool, v, name string) error {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "on", "1":
		*dst = true
	case "false", "no", "off", "0":
		*dst = false
	default:
		return fmt.Errorf("%s must be true or false", name)
	}
	return nil
}
