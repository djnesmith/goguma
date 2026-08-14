package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/power"
	"github.com/junnam586/goguma/internal/scan"
)

// A job leaving its scheduler used to be logged and nothing else, so the list
// silently got shorter and the machine silently stopped waking for something.
func TestARetiredJobIsReportedToTheUser(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	d.adoptNew([]scan.Candidate{candidate("briefing", "hermes", false)}, []string{"hermes"})

	// The source no longer lists it.
	d.retireVanished(nil, []scan.Coverage{{Source: "hermes", Available: true}}, []string{"hermes"})
	d.refreshWarnings(power.State{BatteryPct: -1}, config.Default())

	w := findWarning(d, model.WarnRetired)
	if w == nil {
		t.Fatal("a job was retired and nothing told the user")
	}
	if !strings.Contains(w.Message, "briefing") {
		t.Errorf("the notice does not name the job: %q", w.Message)
	}
}

// It is news for a few days, then it stops being news.
func TestARetirementStopsBeingReported(t *testing.T) {
	d := testDaemon(t)
	d.mu.Lock()
	d.retired = []retiredJob{{Name: "old", At: time.Now().Add(-retiredNoticeWindow - time.Hour)}}
	d.mu.Unlock()

	d.refreshWarnings(power.State{BatteryPct: -1}, config.Default())
	if findWarning(d, model.WarnRetired) != nil {
		t.Error("a retirement older than the notice window is still being reported")
	}
	if n := len(d.recentRetirements(time.Now())); n != 0 {
		t.Errorf("%d stale notices left in memory; they should be pruned", n)
	}
}

// Acknowledging is what actually clears the notice on a machine with the app.
// The time window is only a backstop for a CLI-only install.
func TestAcknowledgingClearsTheNotice(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	d.adoptNew([]scan.Candidate{candidate("briefing", "hermes", false)}, []string{"hermes"})
	d.retireVanished(nil, []scan.Coverage{{Source: "hermes", Available: true}}, []string{"hermes"})

	d.refreshWarnings(power.State{BatteryPct: -1}, config.Default())
	if findWarning(d, model.WarnRetired) == nil {
		t.Fatal("no notice to acknowledge")
	}

	d.ackNotices()
	d.refreshWarnings(power.State{BatteryPct: -1}, config.Default())
	if w := findWarning(d, model.WarnRetired); w != nil {
		t.Errorf("still reporting after acknowledgement: %q", w.Message)
	}
}
