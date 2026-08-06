package daemon

import (
	"strings"
	"testing"

	"github.com/junnam/wakeguard/internal/config"
	"github.com/junnam/wakeguard/internal/model"
	"github.com/junnam/wakeguard/internal/power"
	"github.com/junnam/wakeguard/internal/scan"
)

// TestUncoveredJobsAreReported guards the quietest failure WakeGuard had.
//
// Adoption declines wrappable candidates — crontab and launchd entries — on
// the grounds that adopting one blind would warn forever about a config nobody
// chose. Fair, but it said nothing at all, so a user whose jobs were *all*
// crontab saw a healthy WakeGuard and a Mac that still slept through every
// one of them. A missing job is the failure this product exists to prevent;
// it must never be the silent case.
func TestUncoveredJobsAreReported(t *testing.T) {
	d := testDaemon(t)
	d.mu.Lock()
	d.uncovered = []scan.Candidate{
		{Entry: scan.Entry{Name: "nightly-backup", Source: scan.SourceCrontab, Wrappable: true}},
		{Entry: scan.Entry{Name: "reindex", Source: scan.SourceCrontab, Wrappable: true}},
	}
	d.mu.Unlock()

	d.refreshWarnings(power.State{BatteryPct: -1}, config.Default())

	w := findWarning(d, model.WarnUncovered)
	if w == nil {
		t.Fatal("two uncovered jobs produced no warning; a user cannot act on silence")
	}
	if !strings.Contains(w.Message, "2 scheduled jobs") {
		t.Errorf("message does not say how many: %q", w.Message)
	}
	for _, name := range []string{"nightly-backup", "reindex"} {
		if !strings.Contains(w.Message, name) {
			t.Errorf("message does not name %q: %q", name, w.Message)
		}
	}
	if w.Fix == "" {
		t.Error("the warning offers no way to fix it")
	}
}

// Nothing uncovered must produce no warning at all — an empty state that
// nags is how people learn to ignore warnings.
func TestNothingUncoveredIsSilent(t *testing.T) {
	d := testDaemon(t)
	d.refreshWarnings(power.State{BatteryPct: -1}, config.Default())
	if w := findWarning(d, model.WarnUncovered); w != nil {
		t.Errorf("warned with nothing uncovered: %q", w.Message)
	}
}

// Singular reads as English, not as "1 scheduled jobs are".
func TestSingularUncoveredReadsCorrectly(t *testing.T) {
	d := testDaemon(t)
	d.mu.Lock()
	d.uncovered = []scan.Candidate{{Entry: scan.Entry{Name: "solo", Source: scan.SourceCrontab, Wrappable: true}}}
	d.mu.Unlock()
	d.refreshWarnings(power.State{BatteryPct: -1}, config.Default())

	w := findWarning(d, model.WarnUncovered)
	if w == nil {
		t.Fatal("no warning")
	}
	for _, want := range []string{"1 scheduled job", " is not being woken", "it is missed"} {
		if !strings.Contains(w.Message, want) {
			t.Errorf("singular missing %q: %q", want, w.Message)
		}
	}
	for _, bad := range []string{"jobs", "they are"} {
		if strings.Contains(w.Message, bad) {
			t.Errorf("singular used the plural %q: %q", bad, w.Message)
		}
	}
}

func findWarning(d *Daemon, kind model.WarningKind) *model.Warning {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for i := range d.warnings {
		if d.warnings[i].Kind == kind {
			return &d.warnings[i]
		}
	}
	return nil
}
