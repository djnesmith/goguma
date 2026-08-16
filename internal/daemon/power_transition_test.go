package daemon

import (
	"testing"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/power"
)

// Unplugging must put the wake back into the held-back state.
//
// Plugged in, a wake is always safe to schedule: the charge is not going
// anywhere. Unplugged at the same percentage it may not be, and the decision
// has to be re-made rather than inherited from when the cable was in.
func TestUnpluggingWithdrawsTheWake(t *testing.T) {
	cfg := config.Default()
	cfg.LowBatteryCutoutPct = 15

	onAC := power.State{OnAC: true, BatteryPct: 13}
	onBattery := power.State{OnAC: false, BatteryPct: 13}

	if safe, _ := ShouldScheduleWake(onAC, cfg, 0); !safe {
		t.Error("refused to schedule a wake while plugged in at 13%")
	}
	safe, reason := ShouldScheduleWake(onBattery, cfg, 0)
	if safe {
		t.Error("still willing to wake at 13% on battery with a 15% cutout")
	}
	if reason == "" {
		t.Error("withheld the wake without saying why")
	}
	t.Logf("on battery at 13%%: %s", reason)
}

// And plugging back in must release it, in the same tick.
func TestPluggingInRestoresTheWake(t *testing.T) {
	cfg := config.Default()
	cfg.LowBatteryCutoutPct = 15
	if safe, _ := ShouldScheduleWake(power.State{OnAC: false, BatteryPct: 13}, cfg, 0); safe {
		t.Fatal("precondition: should be withheld on battery")
	}
	if safe, _ := ShouldScheduleWake(power.State{OnAC: true, BatteryPct: 13}, cfg, 0); !safe {
		t.Error("plugging in did not restore the wake")
	}
}

// The boundary itself, since the floor is the cutout plus the job's own drain
// and an off-by-one here is a job that silently never runs.
func TestTheWakeFloorBoundary(t *testing.T) {
	cfg := config.Default()
	cfg.LowBatteryCutoutPct = 15

	for _, c := range []struct {
		pct, drain int
		wantSafe   bool
	}{
		{17, 0, true},  // above cutout + minimum margin
		{16, 0, false}, // exactly at the floor is not above it
		{15, 0, false},
		{13, 0, false},
		{23, 8, false}, // an expensive job pushes its own floor up: 15+8=23
		{24, 8, true},
		{25, 2, true},
		{100, 0, true},
	} {
		got, _ := ShouldScheduleWake(
			power.State{OnAC: false, BatteryPct: c.pct}, cfg, c.drain)
		if got != c.wantSafe {
			t.Errorf("at %d%% with %d%%/run drain: safe=%v, want %v",
				c.pct, c.drain, got, c.wantSafe)
		}
	}
}
