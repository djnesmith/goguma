package daemon

import (
	"testing"

	"github.com/junnam586/goguma/internal/config"
)

// The GUI used to compute this itself as cutout + rearm margin, which was the
// flat-margin rule from before the floor started rising with a job's measured
// drain. On a default config it displayed 15% while the daemon would wake at
// 11%. The number is reported now, and these two tests are what stop it being
// re-derived on the other side.
func TestReportedWakeFloorIsTheOneTheDaemonApplies(t *testing.T) {
	cfg := config.Default()
	got := wakeFloor(cfg, -1)
	want := cfg.LowBatteryCutoutPct + minWakeMarginPct
	if got != want {
		t.Errorf("baseline wake floor is %d%%, want %d%%", got, want)
	}
}

func TestReportedWakeFloorIsNotTheRearmMargin(t *testing.T) {
	cfg := config.Default()
	// Distinct on the defaults (margin 1 vs rearm 5), and they must stay
	// distinct: they answer different questions. The rearm margin is how far
	// the battery has to recover before holds resume after a cutout fired.
	if cfg.CutoutRearmMarginPct == minWakeMarginPct {
		t.Skip("defaults coincide; the distinction is untestable here")
	}
	if wakeFloor(cfg, -1) == cfg.LowBatteryCutoutPct+cfg.CutoutRearmMarginPct {
		t.Error("wake floor equals cutout+rearm, the formula the GUI got wrong")
	}
}

// A job that costs more than the minimum raises its own floor, which is the
// reason a client cannot state one number for every job.
func TestWakeFloorRisesWithMeasuredDrain(t *testing.T) {
	cfg := config.Default()
	base := wakeFloor(cfg, -1)
	if hungry := wakeFloor(cfg, 4); hungry <= base {
		t.Errorf("a 4%%-drain job floors at %d%%, no higher than the %d%% baseline", hungry, base)
	}
}
