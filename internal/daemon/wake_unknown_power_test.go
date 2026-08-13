package daemon

import (
	"testing"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/power"
)

// TestUnreadablePowerDoesNotLookLikeAFlatBattery guards a failure that silently
// stops every job.
//
// A failed read used to leave the zero-value state in place: 0% and not on AC.
// That is a perfectly plausible reading, so nothing downstream could tell it
// from a real one: the wake guard refused to schedule anything and the
// low-battery cutout fired, both citing a 0% battery that was never measured.
func TestUnreadablePowerDoesNotLookLikeAFlatBattery(t *testing.T) {
	cfg := config.Default()

	// The zero value is the bug: it is a valid-looking flat battery.
	if safe, _ := ShouldScheduleWake(power.State{}, cfg, -1); safe {
		t.Fatal("precondition: a 0% on-battery state should suppress the wake")
	}

	// The sentinel a failed read must produce instead.
	unknown := power.State{BatteryPct: -1}
	if safe, reason := ShouldScheduleWake(unknown, cfg, -1); !safe {
		t.Fatalf("an unknown battery must not suppress the wake, got: %s", reason)
	}
	// `holding: true`; the cutout only has anything to release while a hold
	// is open, which is exactly when a false low-battery reading does damage.
	if d := EvaluateCutout(unknown, cfg, true); d.Fire {
		t.Fatalf("an unknown battery must not fire a cutout, got: %s", d.Detail)
	}
	// And the zero value would have.
	if d := EvaluateCutout(power.State{LidClosed: true}, cfg, true); !d.Fire {
		t.Fatal("precondition: a 0% on-battery state should fire the low-battery cutout")
	}
}
