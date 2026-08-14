package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/ipc"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/scan"
)

func candidate(name string, source scan.Source, wrappable bool) scan.Candidate {
	return scan.Candidate{Entry: scan.Entry{
		Name:     name,
		Source:   source,
		Schedule: "0 9 * * *",
		// Application schedulers run their jobs internally, so nothing about
		// them can be wrapped.
		Wrappable: wrappable,
	}}
}

func TestAdoptOnlyFromWatchedSources(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}

	d.adoptNew([]scan.Candidate{
		candidate("watched job", "hermes", false),
		candidate("other job", "someotherapp", false),
	}, []string{"hermes"})

	jobs := d.store.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("adopted %d jobs, want only the watched source's", len(jobs))
	}
	if jobs[0].Name != "watched job" {
		t.Errorf("adopted %q, want the job from the watched source", jobs[0].Name)
	}
}

func TestAdoptsWrappableJobsWithoutEditingAnything(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}

	// A wrappable job is adopted rather than skipped. Editing its command line
	// buys exact timing, but declining to adopt it at all meant the machine
	// slept through it, which is the failure the tool exists to prevent.
	d.adoptNew([]scan.Candidate{
		candidate("crontab job", scan.SourceCrontab, true),
	}, []string{"crontab"})

	jobs := d.store.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("adopted %d jobs, want the crontab entry covered", len(jobs))
	}
	// Never mark: that is the one mode that needs the command line changed,
	// and claiming it without the edit records never-detected on every run.
	if jobs[0].Detection == model.DetectMark {
		t.Error("adopted as mark-detected without the command line being wrapped")
	}
	if !jobs[0].Managed {
		t.Error("an adopted job should be managed so sync can keep it current")
	}
}

func TestAdoptPrefersPatternWhenItIdentifiesOneJob(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}

	// Pattern is real detection and costs the user nothing, so a candidate
	// whose pattern matches only itself should get it rather than falling back
	// to a fixed window.
	c := candidate("restic backup", scan.SourceCrontab, true)
	c.Command = "/usr/local/bin/restic backup /home"
	d.adoptNew([]scan.Candidate{c}, []string{"crontab"})

	jobs := d.store.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("adopted %d jobs, want 1", len(jobs))
	}
	if jobs[0].Detection == model.DetectPattern && jobs[0].Match == "" {
		t.Error("pattern detection with no pattern stored never matches anything")
	}
	if jobs[0].Detection == model.DetectNone && jobs[0].Match != "" {
		t.Error("wake-only job carries a match pattern it will never use")
	}
}

func TestAdoptWillNotGiveTwoJobsTheSamePattern(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}

	// Two candidates sharing a pattern must not both take it. The daemon
	// releases a hold when *a* match exits, so the faster one would close the
	// other's window and every duration recorded would be wrong.
	a := candidate("backup home", scan.SourceCrontab, true)
	a.Command = "/usr/local/bin/restic backup /home"
	b := candidate("backup work", scan.SourceCrontab, true)
	b.Command = "/usr/local/bin/restic backup /work"
	d.adoptNew([]scan.Candidate{a, b}, []string{"crontab"})

	seen := map[string]string{}
	for _, j := range d.store.Jobs() {
		if j.Detection != model.DetectPattern {
			continue
		}
		if prev, dup := seen[j.Match]; dup {
			t.Errorf("%q and %q share pattern %q", prev, j.Name, j.Match)
		}
		seen[j.Match] = j.Name
	}
}

func TestAdoptedJobsAreWakeOnlyAndManaged(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	d.adoptNew([]scan.Candidate{candidate("briefing", "hermes", false)}, []string{"hermes"})

	jobs := d.store.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1", len(jobs))
	}
	j := jobs[0]
	// Wake-only, because an application's internal job cannot be observed and
	// pretending otherwise produces never-detected noise on every run.
	if j.Detection != model.DetectNone {
		t.Errorf("detection = %q, want %q", j.Detection, model.DetectNone)
	}
	// Managed, so it can be retired when it disappears from its source.
	if !j.Managed {
		t.Error("an adopted job must be marked managed so it can be retired later")
	}
}

func TestAdoptIsIdempotent(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	c := []scan.Candidate{candidate("briefing", "hermes", false)}

	d.adoptNew(c, []string{"hermes"})
	d.adoptNew(c, []string{"hermes"})
	d.adoptNew(c, []string{"hermes"})

	if n := len(d.store.Jobs()); n != 1 {
		t.Errorf("got %d jobs after three syncs, want 1", n)
	}
}

func TestRetireRemovesVanishedManagedJobs(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	d.adoptNew([]scan.Candidate{
		candidate("still there", "hermes", false),
		candidate("deleted", "hermes", false),
	}, []string{"hermes"})

	// The source now reports only one of them.
	d.retireVanished([]scan.Entry{
		{Name: "still there", Source: "hermes"},
	}, []scan.Coverage{{Source: "hermes", Available: true}}, []string{"hermes"})

	jobs := d.store.Jobs()
	if len(jobs) != 1 || jobs[0].Name != "still there" {
		t.Errorf("after retirement got %v, want only 'still there'", jobNames(jobs))
	}
}

func TestRetireNeverTouchesHandRegisteredJobs(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	// A job a human registered deliberately. It has the same source name but
	// is not managed, and a scan must never undo an explicit human decision.
	if err := d.store.Add(&model.Job{
		ID: "mine", Name: "mine", Schedule: "0 9 * * *",
		Detection: model.DetectNone, Source: "hermes", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	d.retireVanished([]scan.Entry{{Name: "something else", Source: "hermes"}},
		[]scan.Coverage{{Source: "hermes", Available: true}}, []string{"hermes"})

	if len(d.store.Jobs()) != 1 {
		t.Error("a hand-registered job was retired by a scan")
	}
}

func TestRetireIgnoresASourceThatReturnedNothing(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	d.adoptNew([]scan.Candidate{candidate("briefing", "hermes", false)}, []string{"hermes"})

	// The read failed this pass. Retiring on it would wipe the user's whole
	// set over a transient error, so a failed source retires nothing.
	d.retireVanished(nil,
		[]scan.Coverage{{Source: "hermes", Available: true, Err: errors.New("transient")}},
		[]string{"hermes"})

	if n := len(d.store.Jobs()); n != 1 {
		t.Errorf("got %d jobs; a failed read must not retire anything", n)
	}

	// An unavailable source is the same story.
	d.retireVanished(nil,
		[]scan.Coverage{{Source: "hermes", Available: false}},
		[]string{"hermes"})

	if n := len(d.store.Jobs()); n != 1 {
		t.Errorf("got %d jobs; an unavailable source must not retire anything", n)
	}
}

func TestRetireFollowsACleanReadThatFoundNothing(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	d.adoptNew([]scan.Candidate{candidate("briefing", "hermes", false)}, []string{"hermes"})

	// The user deleted (or paused) their only job in the source. The read
	// succeeded and legitimately found nothing, and that is an authoritative
	// answer: keeping the job means waking the machine for it forever.
	d.retireVanished(nil,
		[]scan.Coverage{{Source: "hermes", Available: true}},
		[]string{"hermes"})

	if n := len(d.store.Jobs()); n != 0 {
		t.Errorf("got %d jobs; the last job of a cleanly-read source must be retirable", n)
	}
}

func TestUpdateFollowsAScheduleChangeInTheSource(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	d.adoptNew([]scan.Candidate{candidate("briefing", "hermes", false)}, []string{"hermes"})

	// The user moved the job an hour earlier in the source scheduler. Same
	// name, so adoption skips it; only an update can follow the change.
	d.updateChanged([]scan.Entry{{
		Name: "briefing", Source: "hermes",
		Schedule: "0 7 * * *", Command: "hermes run briefing",
	}}, []string{"hermes"}, config.Default())

	job, ok := d.store.Job("briefing")
	if !ok {
		t.Fatal("the job vanished during an update")
	}
	if job.Schedule != "0 7 * * *" {
		t.Errorf("schedule is %q, want the source's new 0 7 * * *", job.Schedule)
	}
	if job.Command != "hermes run briefing" {
		t.Errorf("command is %q, want the source's new one", job.Command)
	}
	if !job.Managed {
		t.Error("an update cleared the managed flag")
	}
}

func TestUpdateNeverTouchesHandRegisteredJobs(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	if err := d.store.Add(&model.Job{
		ID: "mine", Name: "mine", Schedule: "0 9 * * *",
		Detection: model.DetectNone, Source: "hermes", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	d.updateChanged([]scan.Entry{{Name: "mine", Source: "hermes", Schedule: "0 7 * * *"}},
		[]string{"hermes"}, config.Default())

	job, _ := d.store.Job("mine")
	if job.Schedule != "0 9 * * *" {
		t.Errorf("schedule is %q; a scan must not rewrite a hand-registered job", job.Schedule)
	}
}

func TestUpdateKeepsTheOldDefinitionWhenTheNewOneIsInvalid(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	d.adoptNew([]scan.Candidate{candidate("briefing", "hermes", false)}, []string{"hermes"})

	d.updateChanged([]scan.Entry{{
		Name: "briefing", Source: "hermes", Schedule: "not a schedule at all",
	}}, []string{"hermes"}, config.Default())

	job, _ := d.store.Job("briefing")
	if job.Schedule != "0 9 * * *" {
		t.Errorf("schedule is %q; an unparseable source entry must not replace a working one", job.Schedule)
	}
}

func TestUpdateRetiresAJobThatBecameTooFrequent(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	d.adoptNew([]scan.Candidate{candidate("briefing", "hermes", false)}, []string{"hermes"})

	// The user moved the job from daily to every 10 minutes. Adoption would
	// have refused this schedule outright (ReasonTooFrequent), so the update
	// path must not smuggle it in: 144 wakes a day is the battery drain the
	// import policy exists to prevent. The adoption decision is revisited
	// and the job retired; it will still run on natural wakes.
	d.updateChanged([]scan.Entry{{
		Name: "briefing", Source: "hermes", Schedule: "every 10m",
	}}, []string{"hermes"}, config.Default())

	if _, ok := d.store.Job("briefing"); ok {
		t.Error("a schedule adoption would refuse was accepted through the update path")
	}
}

// Editing an adopted job is a takeover. Without one, the next sync silently
// reverts the human's schedule back to the source's, and the edit looks like
// it never happened.
func TestEditingAManagedJobStopsSyncFromRevertingIt(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	d.adoptNew([]scan.Candidate{candidate("briefing", "hermes", false)}, []string{"hermes"})

	// The human edits the schedule through the same op the CLI and app use.
	stored, _ := d.store.Job("briefing")
	edited := *stored
	edited.Schedule = "0 8 * * *"
	payload, err := json.Marshal(edited)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.handle(context.Background(), ipc.OpJobsPut, payload); err != nil {
		t.Fatal(err)
	}

	// The source still says 9:00. Sync must not undo the human.
	d.updateChanged([]scan.Entry{{Name: "briefing", Source: "hermes", Schedule: "0 9 * * *"}},
		[]string{"hermes"}, config.Default())

	job, _ := d.store.Job("briefing")
	if job.Schedule != "0 8 * * *" {
		t.Errorf("schedule is %q; sync reverted an explicit human edit", job.Schedule)
	}
	if job.Managed {
		t.Error("an edited job is still marked managed")
	}
}

func TestValidateAutoAdoptAcceptsEverySource(t *testing.T) {
	// Every real source is watchable now, including the three that need a
	// command line edited for exact timing. They are adopted with detection
	// that needs no edit, so refusing to watch them only meant the machine
	// slept through them.
	for _, src := range []string{"crontab", "launchd", "systemd", "hermes"} {
		if !slices.Contains(adoptableSources(), src) {
			continue // not built on this platform
		}
		if _, err := validateAutoAdopt(src); err != nil {
			t.Errorf("validateAutoAdopt(%q) rejected it: %v", src, err)
		}
	}
	if _, err := validateAutoAdopt("nonsense"); err == nil {
		t.Error("an unknown source should be rejected")
	}
	// Turning it off must always be expressible.
	for _, off := range []string{"", "none", "off"} {
		got, err := validateAutoAdopt(off)
		if err != nil || len(got) != 0 {
			t.Errorf("validateAutoAdopt(%q) = %v, %v; want empty and no error", off, got, err)
		}
	}
}

func TestSyncRespectsAnExplicitOff(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	// An explicit empty list is a deliberate "off" and must be honoured, even
	// though the unconfigured default is to watch everything adoptable.
	d.cfg.AutoAdopt = []string{}

	done := make(chan struct{})
	go func() {
		d.syncProviders(t.Context())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("syncProviders should return immediately when nothing is watched")
	}
	if n := len(d.store.Jobs()); n != 0 {
		t.Errorf("adopted %d jobs despite auto_adopt being turned off", n)
	}
}

func jobNames(jobs []*model.Job) []string {
	out := make([]string, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.Name)
	}
	return out
}

func TestTurningAutoAdoptOffIsNotSilentlyReversed(t *testing.T) {
	// nil means "never configured" and expands to every adoptable source, so
	// "off" must produce an empty but non-nil slice. Returning nil here would
	// make `config set auto_adopt off` switch the feature ON.
	for _, off := range []string{"off", "none", ""} {
		got, err := validateAutoAdopt(off)
		if err != nil {
			t.Fatalf("validateAutoAdopt(%q): %v", off, err)
		}
		if got == nil {
			t.Errorf("validateAutoAdopt(%q) returned nil, which means "+
				"'not configured' and would re-enable everything", off)
		}
		if n := len(effectiveAutoAdopt(got)); n != 0 {
			t.Errorf("after turning off, %d sources are still watched", n)
		}
	}
}

func TestUnconfiguredMeansWatchEverythingAdoptable(t *testing.T) {
	// A fresh install has expressed no preference. Installing the tool is the
	// statement that jobs should survive sleep, so it covers what it can.
	got := effectiveAutoAdopt(nil)
	if len(got) != len(adoptableSources()) {
		t.Errorf("unconfigured watches %v, want every adoptable source %v",
			got, adoptableSources())
	}
}

func TestAutoAdoptCanBeRestoredToItsDefault(t *testing.T) {
	// Without an "all" spelling the setting is a one-way door over IPC: a
	// client can turn watching off but never fully back on, only approximate
	// it by naming the sources it happens to know about. That approximation
	// narrows silently as schedulers are added, so a UI toggle would quietly
	// stop covering things it used to.
	for _, on := range []string{"all", "default", "auto", "ALL"} {
		got, err := validateAutoAdopt(on)
		if err != nil {
			t.Fatalf("validateAutoAdopt(%q): %v", on, err)
		}
		if got != nil {
			t.Errorf("validateAutoAdopt(%q) = %v, want nil (the unconfigured default)", on, got)
		}
		if len(effectiveAutoAdopt(got)) != len(adoptableSources()) {
			t.Errorf("%q did not restore watching every adoptable source", on)
		}
	}

	// The round trip that matters: off, then back on, returns to the default
	// rather than to a narrowed list.
	off, _ := validateAutoAdopt("off")
	if len(effectiveAutoAdopt(off)) != 0 {
		t.Fatal("off did not disable watching")
	}
	back, _ := validateAutoAdopt("all")
	if len(effectiveAutoAdopt(back)) != len(adoptableSources()) {
		t.Error("turning it back on did not restore the full default")
	}
}
