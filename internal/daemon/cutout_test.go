package daemon

import (
	"testing"
	"time"

	"github.com/junnam/wakeguard/internal/config"
	"github.com/junnam/wakeguard/internal/model"
	"github.com/junnam/wakeguard/internal/power"
)

func tempOf(c float64) *float64 { return &c }

func TestCutoutOnlyFiresWhileHoldingWithLidClosed(t *testing.T) {
	cfg := config.Default()
	hot := power.State{LidClosed: true, TempC: tempOf(95), BatteryPct: 90, OnAC: true}

	// Not holding: WakeGuard is not the reason the machine is awake, so it has
	// nothing to release and no business intervening.
	if d := EvaluateCutout(hot, cfg, false); d.Fire {
		t.Error("cutout fired while not holding")
	}

	// Lid open: the machine has airflow and the user can see it. The normal
	// system protections apply and a forced release would be unhelpful.
	lidOpen := hot
	lidOpen.LidClosed = false
	if d := EvaluateCutout(lidOpen, cfg, true); d.Fire {
		t.Error("cutout fired with the lid open")
	}

	if d := EvaluateCutout(hot, cfg, true); !d.Fire || d.Kind != model.CutoutThermal {
		t.Errorf("expected a thermal cutout while holding with the lid closed, got %+v", d)
	}
}

func TestThermalCutoutThreshold(t *testing.T) {
	cfg := config.Default() // 80°C

	tests := []struct {
		temp float64
		want bool
	}{
		{79.9, false},
		{80.0, true}, // at the limit, not just above it
		{85.0, true},
	}
	for _, tc := range tests {
		st := power.State{LidClosed: true, TempC: tempOf(tc.temp), OnAC: true, BatteryPct: 100}
		if got := EvaluateCutout(st, cfg, true).Fire; got != tc.want {
			t.Errorf("at %.1f°C: fired = %v, want %v", tc.temp, got, tc.want)
		}
	}
}

func TestUnreadableTemperatureCannotFireThermalCutout(t *testing.T) {
	cfg := config.Default()

	// With no sensor there is nothing to compare against, so the valve cannot
	// fire. The daemon surfaces the missing sensor as a warning instead, so
	// the user knows the protection is inoperative rather than assuming it is
	// armed. Asserted here so a future change cannot quietly start treating
	// "unknown" as "dangerously hot" and force-release every hold.
	st := power.State{LidClosed: true, TempC: nil, OnAC: true, BatteryPct: 100}
	if d := EvaluateCutout(st, cfg, true); d.Fire {
		t.Errorf("cutout fired with no temperature reading: %+v", d)
	}
}

func TestLowBatteryCutout(t *testing.T) {
	cfg := config.Default() // 20%

	t.Run("fires on battery below the threshold", func(t *testing.T) {
		st := power.State{LidClosed: true, OnAC: false, BatteryPct: 20, TempC: tempOf(40)}
		d := EvaluateCutout(st, cfg, true)
		if !d.Fire || d.Kind != model.CutoutLowBattery {
			t.Errorf("expected a low-battery cutout, got %+v", d)
		}
	})

	t.Run("never fires on AC", func(t *testing.T) {
		// On AC there is no drain to protect against, so a low reading is
		// irrelevant.
		st := power.State{LidClosed: true, OnAC: true, BatteryPct: 5, TempC: tempOf(40)}
		if d := EvaluateCutout(st, cfg, true); d.Fire {
			t.Errorf("low-battery cutout fired while on AC: %+v", d)
		}
	})

	t.Run("ignores an unknown battery", func(t *testing.T) {
		// A desktop with no battery reports -1. Treating that as 0% would
		// force-release every hold forever.
		st := power.State{LidClosed: true, OnAC: false, BatteryPct: -1, TempC: tempOf(40)}
		if d := EvaluateCutout(st, cfg, true); d.Fire {
			t.Errorf("cutout fired with an unknown battery level: %+v", d)
		}
	})
}

func TestLatchPreventsOscillation(t *testing.T) {
	cfg := config.Default()
	var latch CutoutLatch
	now := time.Now()

	hot := power.State{LidClosed: true, TempC: tempOf(85), OnAC: true, BatteryPct: 100}
	latch.Engage(EvaluateCutout(hot, cfg, true), 2, now)

	if !latch.Blocking() {
		t.Fatal("latch should block new holds immediately after firing")
	}

	// Still hot: the latch must hold. Without it the job, which is usually
	// still running, would re-acquire within seconds and the system would
	// oscillate release/re-acquire many times a second.
	if latch.Update(hot, cfg) {
		t.Error("latch cleared while still hot")
	}

	// Barely under the threshold is not enough — that is exactly the
	// oscillation case hysteresis exists to prevent.
	marginal := hot
	marginal.TempC = tempOf(cfg.ThermalCutoutC - 0.5)
	if latch.Update(marginal, cfg) {
		t.Error("latch cleared without clearing the hysteresis margin")
	}

	// Comfortably cool: safe to resume.
	cool := hot
	cool.TempC = tempOf(cfg.ThermalCutoutC - cfg.CutoutRearmMarginC - 1)
	if !latch.Update(cool, cfg) {
		t.Error("latch did not clear once the temperature genuinely recovered")
	}
	if latch.Blocking() {
		t.Error("latch still blocking after clearing")
	}
}

func TestOpeningTheLidAlwaysClearsTheLatch(t *testing.T) {
	cfg := config.Default()
	var latch CutoutLatch

	hot := power.State{LidClosed: true, TempC: tempOf(90), OnAC: true, BatteryPct: 100}
	latch.Engage(EvaluateCutout(hot, cfg, true), 1, time.Now())

	// Opening the lid ends the hazard by definition: the machine is out of
	// the bag and the user is present, even if it is still hot.
	openStillHot := hot
	openStillHot.LidClosed = false
	if !latch.Update(openStillHot, cfg) {
		t.Error("opening the lid did not clear the latch")
	}
}

func TestLowBatteryLatchClearsOnAC(t *testing.T) {
	cfg := config.Default()
	var latch CutoutLatch

	low := power.State{LidClosed: true, OnAC: false, BatteryPct: 10, TempC: tempOf(40)}
	latch.Engage(EvaluateCutout(low, cfg, true), 1, time.Now())

	// Plugging in removes the hazard outright, without waiting for the charge
	// to climb past the rearm margin.
	plugged := low
	plugged.OnAC = true
	if !latch.Update(plugged, cfg) {
		t.Error("latch did not clear when the machine was plugged in")
	}
}

func TestThermalTakesPrecedenceOverBattery(t *testing.T) {
	cfg := config.Default()

	// Both hazards at once. Overheating is the more urgent physical risk, so
	// it should be the reported cause.
	st := power.State{LidClosed: true, OnAC: false, BatteryPct: 5, TempC: tempOf(95)}
	d := EvaluateCutout(st, cfg, true)
	if !d.Fire || d.Kind != model.CutoutThermal {
		t.Errorf("expected thermal to take precedence, got %+v", d)
	}
}

// TestWakeIsRefusedOnALowBattery guards the one hazard WakeGuard has that a
// hold-only tool does not.
//
// Adrafinil never wakes anything, so a reactive cutout is enough for it: the
// machine was already awake and the user was just using it. WakeGuard wakes a
// machine that was deliberately put to sleep, possibly into a bag. A cutout
// cannot un-wake a machine — by the time it fires the energy is spent — so on
// a nearly-flat battery the decision has to be made before the wake.
func TestWakeIsRefusedOnALowBattery(t *testing.T) {
	cfg := config.Default() // 20%

	tests := []struct {
		name string
		st   power.State
		want bool
	}{
		{"flat battery, on battery power", power.State{OnAC: false, BatteryPct: 12}, false},
		{"at the release threshold", power.State{OnAC: false, BatteryPct: 20}, false},
		// Just above the release threshold is still refused. Waking costs
		// charge, so arriving at 21% means landing at or under the 20% limit
		// and being cut off before the job finishes — energy spent for a run
		// that did not happen.
		{"just above the release threshold", power.State{OnAC: false, BatteryPct: 21}, false},
		{"at the wake floor", power.State{OnAC: false, BatteryPct: 25}, false},
		{"clear of the wake floor", power.State{OnAC: false, BatteryPct: 26}, true},
		{"healthy battery", power.State{OnAC: false, BatteryPct: 80}, true},
		// On AC there is no drain to protect against, so a low reading is
		// irrelevant and refusing would only lose the job for nothing.
		{"flat but plugged in", power.State{OnAC: true, BatteryPct: 3}, true},
		// A desktop reports -1. Refusing there would disable the tool
		// entirely on machines that have no battery to protect.
		{"no battery present", power.State{OnAC: false, BatteryPct: -1}, true},
	}
	for _, tc := range tests {
		got, reason := ShouldScheduleWake(tc.st, cfg)
		if got != tc.want {
			t.Errorf("%s: ShouldScheduleWake = %v, want %v (reason %q)",
				tc.name, got, tc.want, reason)
		}
		if !got && reason == "" {
			t.Errorf("%s: refused without explaining why", tc.name)
		}
	}
}

func TestTemperatureDoesNotBlockScheduling(t *testing.T) {
	cfg := config.Default()

	// A machine cools while it sleeps, so how hot it is now says nothing about
	// how hot it will be at wake time hours later. Blocking on it would refuse
	// wakes for a hazard that will have passed; the reactive thermal cutout
	// handles the temperature that actually matters, the one after waking.
	hot := power.State{OnAC: true, BatteryPct: 100, TempC: tempOf(95)}
	if ok, reason := ShouldScheduleWake(hot, cfg); !ok {
		t.Errorf("scheduling was refused on temperature alone: %q", reason)
	}
}

// TestWakeFloorSitsAboveTheReleaseThreshold pins the relationship between the
// two battery limits.
//
// If they were equal, a wake scheduled at one point above the cutout would
// spend charge getting the machine up, arrive at or under the limit, and be
// released immediately — burning battery for a job that never completed.
func TestWakeFloorSitsAboveTheReleaseThreshold(t *testing.T) {
	cfg := config.Default()

	if wakeFloor(cfg) <= cfg.LowBatteryCutoutPct {
		t.Fatalf("wake floor %d%% must be above the release threshold %d%%",
			wakeFloor(cfg), cfg.LowBatteryCutoutPct)
	}

	// A machine woken exactly at the floor must still have headroom left after
	// the wake, or the guard achieves nothing.
	atFloor := power.State{OnAC: false, BatteryPct: wakeFloor(cfg)}
	if ok, _ := ShouldScheduleWake(atFloor, cfg); ok {
		t.Error("scheduling was allowed with no headroom above the release threshold")
	}
}

func TestBatteryThresholdIsUserConfigurable(t *testing.T) {
	// Both limits must track a changed threshold together, so lowering the
	// cutout does not silently leave the wake floor behind.
	cfg := config.Default()
	cfg.LowBatteryCutoutPct = 10

	if got, want := wakeFloor(cfg), 10+cfg.CutoutRearmMarginPct; got != want {
		t.Errorf("wake floor = %d%%, want %d%% after lowering the cutout", got, want)
	}
	// A level that was refused at the default must now be allowed.
	st := power.State{OnAC: false, BatteryPct: 20}
	if ok, reason := ShouldScheduleWake(st, cfg); !ok {
		t.Errorf("20%% was refused with a 10%% cutout: %s", reason)
	}
}
