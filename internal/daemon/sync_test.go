package daemon

import (
	"testing"
	"time"

	"github.com/junnam/wakeguard/internal/model"
	"github.com/junnam/wakeguard/internal/scan"
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

func TestAdoptSkipsWrappableJobs(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}

	// A wrappable job needs its command line edited before it can be detected.
	// Adopting one unprompted creates a job that is registered, looks correct,
	// and is recorded as never-detected on every run — a permanent warning
	// about a configuration nobody chose.
	d.adoptNew([]scan.Candidate{
		candidate("crontab job", scan.SourceCrontab, true),
	}, []string{"crontab"})

	if n := len(d.store.Jobs()); n != 0 {
		t.Errorf("adopted %d wrappable jobs; they require a human decision", n)
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
	}, []string{"hermes"})

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
		[]string{"hermes"})

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

	// The source produced no entries at all. That is far more likely to be a
	// momentarily unreadable file than every job having been deleted at once,
	// so retiring on it would wipe the user's whole set over a transient error.
	d.retireVanished(nil, []string{"hermes"})

	if n := len(d.store.Jobs()); n != 1 {
		t.Errorf("got %d jobs; an empty read must not retire anything", n)
	}
}

func TestValidateAutoAdoptRejectsWrappableSources(t *testing.T) {
	// Crontab and launchd entries need a command line edited to be covered
	// properly, which is a decision only the user can make.
	for _, src := range []string{"crontab", "launchd", "systemd"} {
		if _, err := validateAutoAdopt(src); err == nil {
			t.Errorf("validateAutoAdopt(%q) should be rejected", src)
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
