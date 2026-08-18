package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/power"
)

func tempOf(c float64) *float64 { return &c }

func TestCutoutOnlyFiresWhileHoldingWithLidClosed(t *testing.T) {
	cfg := config.Default()
	hot := power.State{LidClosed: true, TempC: tempOf(95), BatteryPct: 90, OnAC: true}

	// Not holding: goguma is not the reason the machine is awake, so it has
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
	cfg := config.Default()

	t.Run("fires on battery below the threshold", func(t *testing.T) {
		// Relative to the configured cutout, not a literal. Written as 20 it
		// passed only because that happened to be the default, and went red
		// the moment the default moved.
		st := power.State{
			LidClosed: true, OnAC: false,
			BatteryPct: cfg.LowBatteryCutoutPct, TempC: tempOf(40),
		}
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

	// Barely under the threshold is not enough; that is exactly the
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

// TestWakeIsRefusedOnALowBattery guards the one hazard goguma has that a
// hold-only tool does not.
//
// Adrafinil never wakes anything, so a reactive cutout is enough for it: the
// machine was already awake and the user was just using it. goguma wakes a
// machine that was deliberately put to sleep, possibly into a bag. A cutout
// cannot un-wake a machine (by the time it fires the energy is spent), so on
// a nearly-flat battery the decision has to be made before the wake.
func TestWakeIsRefusedOnALowBattery(t *testing.T) {
	cfg := config.Default()

	// -1 is "never run on battery, nothing measured", which is the normal case
	// and the one that gets the baseline.
	const unmeasured = -1

	tests := []struct {
		name  string
		st    power.State
		drain int
		want  bool
	}{
		{"flat battery", power.State{OnAC: false, BatteryPct: 5}, unmeasured, false},
		{"at the release threshold", power.State{OnAC: false, BatteryPct: 10}, unmeasured, false},
		// One point of hysteresis above the cutout, and no more. A job with no
		// measured cost is almost certainly seconds of held-awake time, which
		// is a fraction of a percent; refusing it at 20% protected nothing.
		{"one above the cutout", power.State{OnAC: false, BatteryPct: 11}, unmeasured, false},
		{"clear of the floor", power.State{OnAC: false, BatteryPct: 12}, unmeasured, true},
		{"well above the old floor", power.State{OnAC: false, BatteryPct: 20}, unmeasured, true},
		// A job measured at 8% a run needs 8 points of room, not one.
		{"expensive job, plenty of charge", power.State{OnAC: false, BatteryPct: 40}, 8, true},
		{"expensive job, not enough room", power.State{OnAC: false, BatteryPct: 17}, 8, false},
		{"expensive job, exactly at its floor", power.State{OnAC: false, BatteryPct: 18}, 8, false},
		{"expensive job, one clear", power.State{OnAC: false, BatteryPct: 19}, 8, true},
		// On AC there is no drain to protect against, however costly the job.
		{"flat but plugged in", power.State{OnAC: true, BatteryPct: 3}, 30, true},
		// A desktop reports -1. Refusing there would disable the tool entirely
		// on machines that have no battery to protect.
		{"no battery present", power.State{OnAC: false, BatteryPct: -1}, unmeasured, true},
	}
	for _, tc := range tests {
		got, reason := ShouldScheduleWake(tc.st, cfg, tc.drain)
		if got != tc.want {
			t.Errorf("%s: ShouldScheduleWake = %v, want %v (reason %q)",
				tc.name, got, tc.want, reason)
		}
		if !got && reason == "" {
			t.Errorf("%s: refused without explaining why", tc.name)
		}
	}
}

// TestAnExpensiveJobIsRefusedSoonerThanACheapOne is the whole point of the
// change: the floor is the job's own cost, not one number for the machine.
func TestAnExpensiveJobIsRefusedSoonerThanACheapOne(t *testing.T) {
	cfg := config.Default()
	st := power.State{OnAC: false, BatteryPct: 15}

	if ok, _ := ShouldScheduleWake(st, cfg, -1); !ok {
		t.Error("a job with no measured drain was refused at 15%")
	}
	if ok, _ := ShouldScheduleWake(st, cfg, 1); !ok {
		t.Error("a job costing 1%% was refused at 15%")
	}
	if ok, reason := ShouldScheduleWake(st, cfg, 9); ok {
		t.Errorf("a job costing 9%% was allowed at 15%%, which lands under the cutout (%q)", reason)
	}
}

// TestTheRefusalSaysWhichLimitWasHit matters because the two cases call for
// different actions: a low battery wants charging, an expensive job wants a
// look at whether it should be waking the machine at all.
func TestTheRefusalSaysWhichLimitWasHit(t *testing.T) {
	cfg := config.Default()

	_, cheap := ShouldScheduleWake(power.State{OnAC: false, BatteryPct: 10}, cfg, -1)
	if strings.Contains(cheap, "per run") {
		t.Errorf("a plain low battery blamed the job: %q", cheap)
	}
	_, costly := ShouldScheduleWake(power.State{OnAC: false, BatteryPct: 15}, cfg, 9)
	if !strings.Contains(costly, "per run") {
		t.Errorf("an expensive job was refused without saying so: %q", costly)
	}
}

func TestTemperatureDoesNotBlockScheduling(t *testing.T) {
	cfg := config.Default()

	// A machine cools while it sleeps, so how hot it is now says nothing about
	// how hot it will be at wake time hours later. Blocking on it would refuse
	// wakes for a hazard that will have passed; the reactive thermal cutout
	// handles the temperature that actually matters, the one after waking.
	hot := power.State{OnAC: true, BatteryPct: 100, TempC: tempOf(95)}
	if ok, reason := ShouldScheduleWake(hot, cfg, -1); !ok {
		t.Errorf("scheduling was refused on temperature alone: %q", reason)
	}
}

// TestWakeFloorSitsAboveTheReleaseThreshold pins the relationship between the
// two battery limits.
//
// If they were equal, a wake scheduled at the cutout would spend charge
// getting the machine up, arrive at the limit, and be released immediately,
// burning battery for a job that never completed.
func TestWakeFloorSitsAboveTheReleaseThreshold(t *testing.T) {
	cfg := config.Default()

	for _, drain := range []int{-1, 0, 1, 5, 40} {
		if got := wakeFloor(cfg, drain); got <= cfg.LowBatteryCutoutPct {
			t.Errorf("wake floor %d%% at drain %d%% must be above the release threshold %d%%",
				got, drain, cfg.LowBatteryCutoutPct)
		}
	}

	atFloor := power.State{OnAC: false, BatteryPct: wakeFloor(cfg, -1)}
	if ok, _ := ShouldScheduleWake(atFloor, cfg, -1); ok {
		t.Error("scheduling was allowed with no headroom above the release threshold")
	}
}

func TestBatteryThresholdIsUserConfigurable(t *testing.T) {
	// Both limits must track a changed threshold together, so lowering the
	// cutout does not silently leave the wake floor behind.
	cfg := config.Default()
	cfg.LowBatteryCutoutPct = 10

	cfg.LowBatteryCutoutPct = 30
	if got, want := wakeFloor(cfg, -1), 30+minWakeMarginPct; got != want {
		t.Errorf("wake floor = %d%%, want %d%% after raising the cutout", got, want)
	}
	// A level that is fine at the default must now be refused.
	st := power.State{OnAC: false, BatteryPct: 25}
	if ok, _ := ShouldScheduleWake(st, cfg, -1); ok {
		t.Error("25%% was allowed with a 30%% cutout")
	}
}

// popoverReasonCols is roughly how many characters of the popover's reason card
// fit on one line, measured off a rendered capture at the shipped font: the
// longest line that held was "asleep to preserve charge (holds are released",
// at 44.
const popoverReasonCols = 44

// TestSuppressedWakeReasonsFitTheCard keeps the held-back reason to two lines.
//
// It is not fussiness. The reason is shown verbatim, because it carries numbers
// that a paraphrase would lose, and at three lines the last one held two words:
// "at 15%)" on a line of its own under a full-width paragraph. Format.noWidow
// cannot fix that, and reading its implementation shows why: it binds the final
// two words with a non-breaking space, so an over-long sentence moves the pair
// down together instead of leaving one behind. The orphan gets wider, not
// rarer. The only real fix is a shorter sentence, and the only way that stays
// fixed is a test that fails when one grows back.
func TestSuppressedWakeReasonsFitTheCard(t *testing.T) {
	cfg := config.Default()

	cases := []struct {
		name    string
		st      power.State
		drain   int
		wantSub string
	}{
		{
			name:    "flat battery, ordinary job",
			st:      power.State{OnAC: false, BatteryPct: 9},
			drain:   -1,
			wantSub: "staying asleep",
		},
		{
			name:    "a job that costs more than the margin",
			st:      power.State{OnAC: false, BatteryPct: 19},
			drain:   12,
			wantSub: "per run",
		},
		{
			// Three-digit-free but wide: every number at its longest.
			name:    "the widest numbers these can carry",
			st:      power.State{OnAC: false, BatteryPct: 100},
			drain:   100,
			wantSub: "per run",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := ShouldScheduleWake(tc.st, cfg, tc.drain)
			if ok {
				t.Fatalf("expected the wake to be held back, got ok with reason %q", reason)
			}
			if !strings.Contains(reason, tc.wantSub) {
				t.Errorf("reason %q does not mention %q", reason, tc.wantSub)
			}
			limit := 2 * popoverReasonCols
			if len(reason) > limit {
				t.Errorf("reason is %d characters, over the %d that fit two lines of the popover card:\n  %q\n"+
					"a third line here holds only the last word or two, which reads as a layout bug",
					len(reason), limit, reason)
			}
		})
	}
}
