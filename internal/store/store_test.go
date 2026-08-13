package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/paths"
)

func testStore(t *testing.T) (*Store, paths.Layout) {
	t.Helper()
	l := paths.Layout{StateDir: t.TempDir()}
	l.LogDir = l.StateDir
	if err := l.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	return New(l), l
}

// TestCorruptJobFileIsNeverOverwritten is the data-loss guard.
//
// A daemon that starts with an empty job list and then persists would replace
// the user's unreadable-but-recoverable file with an empty one, destroying the
// exact data they need in order to repair it.
func TestCorruptJobFileIsNeverOverwritten(t *testing.T) {
	s, l := testStore(t)

	const corrupt = `{ "version": 1, "jobs": [ {"id":"precious","name":"precious"`
	if err := os.WriteFile(l.JobsFile(), []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}

	// Loading must succeed so the daemon can start and report the problem,
	// rather than crash-looping with no explanation.
	if err := s.Load(); err != nil {
		t.Fatalf("Load returned a fatal error; the daemon could not start: %v", err)
	}
	if s.LoadError() == nil {
		t.Fatal("a corrupt file did not record a load error")
	}

	// Every mutation must refuse rather than write.
	job := &model.Job{ID: "new", Name: "new", Schedule: "@daily", Detection: model.DetectNone}
	if err := s.Add(job); err == nil {
		t.Error("Add succeeded against an unreadable file")
	}
	if _, err := s.SetEnabled("new", false); err == nil {
		t.Error("SetEnabled succeeded against an unreadable file")
	}

	got, err := os.ReadFile(l.JobsFile())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != corrupt {
		t.Errorf("the corrupt file was modified:\n got %q\nwant %q", got, corrupt)
	}
}

func TestAGoodFileStillWrites(t *testing.T) {
	s, l := testStore(t)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if s.LoadError() != nil {
		t.Fatalf("a missing file should not be an error: %v", s.LoadError())
	}
	if err := s.Add(&model.Job{
		ID: "x", Name: "x", Schedule: "@daily", Detection: model.DetectNone, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(l.JobsFile()); err != nil {
		t.Errorf("jobs.json was not written: %v", err)
	}
}

func TestOneBadJobDoesNotHideTheOthers(t *testing.T) {
	s, l := testStore(t)
	// Valid JSON, but one entry fails validation. The rest must survive: a
	// single bad hand-edit should not take every other job offline.
	const mixed = `{"version":1,"jobs":[
	  {"id":"good","name":"good","schedule":"@daily","detection":"none","enabled":true},
	  {"id":"bad","name":"bad","schedule":"","detection":"none","enabled":true}
	]}`
	if err := os.WriteFile(l.JobsFile(), []byte(mixed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if s.LoadError() != nil {
		t.Errorf("valid JSON should not set a load error: %v", s.LoadError())
	}
	jobs := s.Jobs()
	if len(jobs) != 1 || jobs[0].ID != "good" {
		t.Errorf("got %d jobs, want just the valid one", len(jobs))
	}
	if len(s.InvalidJobs()) != 1 {
		t.Error("the invalid entry should be reportable so the user can fix it")
	}
}

func TestAGroupSurvivesAWriteAndReload(t *testing.T) {
	s, l := testStore(t)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(&model.Job{
		ID: "backup", Name: "backup", Schedule: "@daily",
		Detection: model.DetectNone, Group: "  Nightly   work ", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Read it back through a fresh store, so this covers the file rather than
	// the in-memory map.
	reloaded := New(l)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	job, ok := reloaded.Job("backup")
	if !ok {
		t.Fatal("the job did not survive the reload")
	}
	if job.Group != "Nightly work" {
		t.Errorf("group = %q, want the normalised name", job.Group)
	}
}

// TestGroupsListsOnlyRealNames covers the contract callers depend on: the
// empty group is the absence of a name, so it is theirs to render, not
// something the store invents an entry for.
func TestGroupsListsOnlyRealNames(t *testing.T) {
	s, _ := testStore(t)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	add := func(id, group string) {
		t.Helper()
		if err := s.Add(&model.Job{
			ID: id, Name: id, Schedule: "@daily",
			Detection: model.DetectNone, Group: group, Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	add("a", "zebra")
	add("b", "alpha")
	add("c", "zebra") // duplicate: one heading, not two
	add("d", "")      // ungrouped: no heading at all

	got := s.Groups()
	want := []string{"alpha", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("Groups() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Groups() = %v, want %v", got, want)
		}
	}
}

// TestJobsWithNoGroupKeyLoadUngrouped is the backward-compatibility guard for
// the jobs.json already on disk, where no entry has the field.
func TestJobsWithNoGroupKeyLoadUngrouped(t *testing.T) {
	s, l := testStore(t)
	const legacy = `{"version":1,"jobs":[
	  {"id":"one","name":"one","schedule":"@daily","detection":"none","enabled":true},
	  {"id":"two","name":"two","schedule":"@hourly","detection":"none","enabled":true}
	]}`
	if err := os.WriteFile(l.JobsFile(), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	jobs := s.Jobs()
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs, want both; a missing group key dropped one", len(jobs))
	}
	for _, j := range jobs {
		if j.Group != "" {
			t.Errorf("job %q was loaded into group %q, want ungrouped", j.ID, j.Group)
		}
	}
	if g := s.Groups(); len(g) != 0 {
		t.Errorf("Groups() = %v, want none for an entirely ungrouped file", g)
	}
}

func TestHistorySurvivesATruncatedLine(t *testing.T) {
	s, l := testStore(t)
	// A hard kill mid-append leaves a partial final line. Losing one run is
	// acceptable; losing the whole history would silently reset the job to a
	// cold-start ceiling.
	path := filepath.Join(l.HistoryDir(), "j.jsonl")
	const partial = `{"job_id":"j","outcome":"ok","duration":"2s"}
{"job_id":"j","outcome":`
	if err := os.WriteFile(path, []byte(partial), 0o600); err != nil {
		t.Fatal(err)
	}
	runs, err := s.Runs("j")
	if err != nil {
		t.Fatalf("a truncated line made the whole history unreadable: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("got %d runs, want the 1 complete one", len(runs))
	}
}

// An entry that fails Validate is quarantined, not erased: the next persist
// must write it back verbatim. Dropping it meant one unrelated job change
// after a hand-edit typo silently destroyed the broken job's schedule,
// pattern, and timezone, the very data needed to repair it.
func TestAnInvalidEntrySurvivesThePersistCycle(t *testing.T) {
	layout := paths.Layout{StateDir: t.TempDir()}
	layout.LogDir = layout.StateDir
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	broken := `{"version":1,"jobs":[
	  {"id":"good","name":"good","schedule":"0 9 * * *","detection":"none","enabled":true},
	  {"id":"typo","name":"typo","schedule":"0 2 * * *","detection":"pattern","match":"restic (","enabled":true}
	]}`
	if err := os.WriteFile(layout.JobsFile(), []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}

	s := New(layout)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if len(s.Jobs()) != 1 {
		t.Fatalf("loaded %d jobs, want 1 valid", len(s.Jobs()))
	}

	// An unrelated mutation persists the store.
	if err := s.Add(&model.Job{
		ID: "new", Name: "new", Schedule: "0 5 * * *",
		Detection: model.DetectNone, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	// The broken entry must still be in the file, still reported invalid.
	b, err := os.ReadFile(layout.JobsFile())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"typo"`) || !strings.Contains(string(b), "restic (") {
		t.Fatal("the invalid entry was erased by an unrelated persist; " +
			"the user's broken-but-repairable job is gone")
	}
	if errs := s.InvalidJobs(); len(errs) != 1 {
		t.Errorf("InvalidJobs reports %d entries, want the quarantined one", len(errs))
	}
}
