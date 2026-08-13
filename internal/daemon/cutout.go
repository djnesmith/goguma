package daemon

import (
	"fmt"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/power"
)

// CutoutDecision is the outcome of evaluating the safety valves.
type CutoutDecision struct {
	Fire   bool
	Kind   model.CutoutKind
	Detail string
}

// EvaluateCutout decides whether to force-release every hold.
//
// Both valves are gated on the lid being closed, because that is the only
// state where holding sleep off is genuinely dangerous: the machine may be in
// a bag with no airflow, and the user cannot see that anything is wrong. With
// the lid open, a hot or draining machine is visible and the normal system
// protections apply.
//
// Written as a pure function over sampled state so the policy can be unit
// tested without a Mac, an SMC, or a real battery.
func EvaluateCutout(st power.State, cfg config.Config, holding bool) CutoutDecision {
	if !holding || !st.LidClosed {
		return CutoutDecision{}
	}

	// Thermal. An unavailable sensor is deliberately NOT treated as safe, but
	// it also cannot fire a cutout; there is nothing to compare. The daemon
	// surfaces the missing sensor as a warning instead, so the user knows the
	// valve is inoperative rather than believing it is protecting them.
	if st.TempC != nil && *st.TempC >= cfg.ThermalCutoutC {
		return CutoutDecision{
			Fire: true,
			Kind: model.CutoutThermal,
			Detail: fmt.Sprintf("CPU reached %.1f°C with the lid closed (limit %.0f°C)",
				*st.TempC, cfg.ThermalCutoutC),
		}
	}
	// The OS's own thermal warning fires the valve regardless of the degree
	// reading. The threshold above is calibrated for die sensors; a machine
	// whose die keys are unreadable falls back to a chassis-proximity sensor
	// that reads tens of degrees cooler, so a bagged lid-closed laptop could
	// peg its silicon while the number stayed under every permitted limit.
	if st.ThermalWarn {
		return CutoutDecision{
			Fire:   true,
			Kind:   model.CutoutThermal,
			Detail: "the OS reported a thermal warning with the lid closed",
		}
	}

	// Low battery. Only on battery: on AC there is no drain to protect
	// against. Because the block is global, a lid-closed machine held awake
	// on battery would otherwise run down to a hard shutdown rather than
	// entering normal low-power sleep with charge to spare.
	if !st.OnAC && st.BatteryPct >= 0 && st.BatteryPct <= cfg.LowBatteryCutoutPct {
		return CutoutDecision{
			Fire: true,
			Kind: model.CutoutLowBattery,
			Detail: fmt.Sprintf("battery fell to %d%% on battery power with the lid closed (limit %d%%)",
				st.BatteryPct, cfg.LowBatteryCutoutPct),
		}
	}
	return CutoutDecision{}
}

// ShouldScheduleWake decides whether it is safe to wake the machine at all.
//
// The cutouts are reactive: they release a hold once conditions have already
// gone bad. That is sufficient for a tool that only holds a machine which is
// already awake, but goguma does something more dangerous; it wakes a
// machine the user deliberately put to sleep, possibly into a closed bag,
// possibly at 3am with nobody watching.
//
// A cutout cannot un-wake a machine. By the time it fires the wake has already
// happened and the energy is already spent, so on a nearly-flat battery the
// sequence is: wake, immediately release, and end up closer to a hard shutdown
// with the job not run either. Refusing the wake in the first place is
// strictly better: the machine stays asleep, the charge is preserved, and the
// job is missed exactly as it would have been anyway.
//
// Only battery is checked, not temperature. A machine cools while it sleeps,
// so its temperature at scheduling time says nothing useful about its
// temperature hours later at wake time. Charge only ever falls while asleep,
// which makes it the one signal that remains meaningful.
// The wake floor sits above the release threshold, but only by what the job
// is actually going to cost.
//
// Waking at exactly the cutout level is self-defeating: the wake itself costs
// charge, so the machine arrives at or under the limit, the cutout fires, and
// the hold is released before the job can finish. Energy spent for a run that
// did not happen.
//
// But a flat margin above the cutout refuses far more than it needs to. Almost
// every job here is seconds to a couple of minutes of held-awake time, which is
// a fraction of a percent; refusing to wake at 24% for a 57-second sync
// protected nobody from anything. So the margin is the job's own measured
// drain, and only a job that genuinely eats charge pushes the floor up.
//
// drainPct is what the job has been observed to consume, or -1 when it has
// never run on battery and there is nothing to read. Unknown gets the minimum,
// deliberately: the baseline is the answer until evidence says otherwise.
func wakeFloor(cfg config.Config, drainPct int) int {
	margin := minWakeMarginPct
	if drainPct > margin {
		margin = drainPct
	}
	return cfg.LowBatteryCutoutPct + margin
}

// minWakeMarginPct is the smallest gap kept above the cutout.
//
// Not zero, because a machine sitting exactly on the threshold would wake,
// trip the cutout, sleep, and wake again for the next job, oscillating on the
// boundary and spending more on the flapping than on the work. One point is
// enough hysteresis for that, and is itself larger than a typical job's drain.
const minWakeMarginPct = 1

func ShouldScheduleWake(st power.State, cfg config.Config, drainPct int) (bool, string) {
	// An unknown battery level (-1, e.g. a desktop) is not a reason to refuse.
	if st.OnAC || st.BatteryPct < 0 {
		return true, ""
	}
	floor := wakeFloor(cfg, drainPct)
	if st.BatteryPct > floor {
		return true, ""
	}
	// Says which of the two reasons applies, because they call for different
	// actions: a low battery wants charging, an expensive job wants a look at
	// whether it should be waking the machine at all.
	if drainPct > minWakeMarginPct {
		return false, fmt.Sprintf(
			"battery is at %d%% and this job has been using about %d%% per run, "+
				"which would take it under the %d%% floor where holds are released",
			st.BatteryPct, drainPct, cfg.LowBatteryCutoutPct)
	}
	return false, fmt.Sprintf(
		"battery is at %d%% on battery power, staying asleep to preserve charge "+
			"(holds are released at %d%%)",
		st.BatteryPct, cfg.LowBatteryCutoutPct)
}

// CutoutLatch suppresses re-acquisition after a cutout fires.
//
// Without it the system oscillates: the cutout releases the hold, the job is
// still inside its window so the next tick re-opens it, the hazard is still
// present, and it fires again, several times a second in the worst case.
// The latch holds until the hazard genuinely recedes by a margin (simple
// hysteresis) or the lid opens, at which point the danger is over by
// definition.
type CutoutLatch struct {
	active *model.Cutout
}

// Active returns the latched cutout, or nil.
func (l *CutoutLatch) Active() *model.Cutout { return l.active }

// Engage latches a fired cutout.
func (l *CutoutLatch) Engage(d CutoutDecision, released int, now time.Time) *model.Cutout {
	c := &model.Cutout{
		Kind:     d.Kind,
		FiredAt:  now,
		Detail:   d.Detail,
		Latched:  true,
		Released: released,
	}
	l.active = c
	return c
}

// Update clears the latch when conditions have recovered, returning true if
// it cleared on this call.
func (l *CutoutLatch) Update(st power.State, cfg config.Config) bool {
	if l.active == nil {
		return false
	}
	// Opening the lid ends the hazard outright: the machine is out of the bag
	// and the user is present.
	if !st.LidClosed {
		l.active = nil
		return true
	}

	switch l.active.Kind {
	case model.CutoutThermal:
		// A warning-fired cutout may exist on a machine with no readable die
		// sensor at all, so a missing reading cannot be required for release
		// there; when a reading exists it must be genuinely cool, as before.
		cooled := st.TempC == nil || *st.TempC <= cfg.ThermalCutoutC-cfg.CutoutRearmMarginC
		if !st.ThermalWarn && cooled {
			l.active = nil
			return true
		}
	case model.CutoutLowBattery:
		if st.OnAC || st.BatteryPct >= cfg.LowBatteryCutoutPct+cfg.CutoutRearmMarginPct {
			l.active = nil
			return true
		}
	}
	return false
}

// Blocking reports whether new holds should be refused right now.
func (l *CutoutLatch) Blocking() bool { return l.active != nil }
