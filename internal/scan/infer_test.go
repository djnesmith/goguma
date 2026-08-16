package scan

import (
	"os"
	"path/filepath"
	"testing"
)

func inferFrom(t *testing.T, doc string) Manifest {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "jobs.json")
	if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	m, _, err := InferManifest(p, "app")
	if err != nil {
		t.Fatalf("infer: %v", err)
	}
	return m
}

// An app that supports cron AND intervals AND one-shots stores each in its own
// field, so no single schedule field appears in a majority of the jobs.
// Requiring a majority (which is right for name and id) found no schedule at
// all in hermes's real file: 3 cron jobs, 1 interval and 3 one-offs each put
// their own field under the threshold.
func TestInferFindsScheduleFieldsUsedByAMinority(t *testing.T) {
	m := inferFrom(t, `{"jobs":[
		{"id":"a","name":"one","enabled":true,"schedule":{"kind":"cron","expr":"0 9 * * *"}},
		{"id":"b","name":"two","enabled":true,"schedule":{"kind":"interval","minutes":30}},
		{"id":"c","name":"three","enabled":true,"schedule":{"kind":"once","run_at":"2030-01-01T09:00:00Z"}},
		{"id":"d","name":"four","enabled":true,"schedule":{"kind":"once","run_at":"2030-02-01T09:00:00Z"}},
		{"id":"e","name":"five","enabled":true,"schedule":{"kind":"once","run_at":"2030-03-01T09:00:00Z"}}
	]}`)

	if m.Fields.Cron != "schedule.expr" {
		t.Errorf("cron field = %q, want schedule.expr", m.Fields.Cron)
	}
	if m.Fields.Every != "schedule.minutes" {
		t.Errorf("interval field = %q, want schedule.minutes", m.Fields.Every)
	}
	if m.Fields.At != "schedule.run_at" {
		t.Errorf("one-shot field = %q, want schedule.run_at", m.Fields.At)
	}
}

// Nearly every record carries bookkeeping timestamps, and a substring match
// against the whole path made "created_at" the one-shot schedule field,
// because the path contains "at". So does almost everything else.
func TestInferDoesNotMistakeBookkeepingForASchedule(t *testing.T) {
	m := inferFrom(t, `{"jobs":[
		{"id":"a","name":"one","enabled":true,"cron":"0 9 * * *",
		 "created_at":"2026-08-03T21:39:53Z","updated_at":"2026-08-04T10:00:00Z"},
		{"id":"b","name":"two","enabled":true,"cron":"0 21 * * *",
		 "created_at":"2026-08-03T21:39:53Z","updated_at":"2026-08-04T10:00:00Z"}
	]}`)

	if m.Fields.At != "" {
		t.Errorf("at = %q; created_at and updated_at are not when a job runs", m.Fields.At)
	}
	if m.Fields.Cron != "cron" {
		t.Errorf("cron = %q", m.Fields.Cron)
	}
}

// A cron expression is the one field identifiable from its value rather than
// its name, which is what makes this work on an app whose field names goguma
// has never seen.
func TestInferRecognisesCronByShapeNotName(t *testing.T) {
	m := inferFrom(t, `{"items":[
		{"ref":"a","title":"one","on":true,"whenever":"0 9 * * *"},
		{"ref":"b","title":"two","on":true,"whenever":"*/15 * * * *"}
	]}`)

	if m.Jobs != "items" {
		t.Errorf("jobs path = %q, want items", m.Jobs)
	}
	if m.Fields.Cron != "whenever" {
		t.Errorf("cron = %q, want whenever (found by value shape)", m.Fields.Cron)
	}
	if m.Fields.Name != "title" {
		t.Errorf("name = %q, want title", m.Fields.Name)
	}
	if m.Fields.ID != "ref" {
		t.Errorf("id = %q, want ref", m.Fields.ID)
	}
	if m.Fields.Enabled != "on" {
		t.Errorf("enabled = %q, want on", m.Fields.Enabled)
	}
}

// The inferred manifest has to actually read the file it was inferred from.
// A mapping that validates but finds nothing is the failure worth catching at
// `scheduler add` time rather than a week later.
func TestAnInferredManifestReadsItsOwnFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "jobs.json")
	os.WriteFile(p, []byte(`{"jobs":[
		{"id":"a","name":"one","enabled":true,"schedule":{"expr":"0 9 * * *"}},
		{"id":"b","name":"two","enabled":true,"schedule":{"minutes":30}}
	]}`), 0o600)

	m, notes, err := InferManifest(p, "app")
	if err != nil {
		t.Fatalf("infer: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("the inferred manifest does not validate: %v", err)
	}
	if len(notes) == 0 {
		t.Error("nothing was reported about what it decided, so the user cannot check it")
	}

	found, err := PreviewManifest(m)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("read %d jobs back, want 2", len(found))
	}
}

// A file with no jobs in it must say so rather than saving a manifest that
// will discover nothing forever.
func TestInferRefusesAFileWithNoJobs(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	os.WriteFile(p, []byte(`{"theme":"dark","fontSize":13}`), 0o600)
	if _, _, err := InferManifest(p, "app"); err == nil {
		t.Error("a settings file was accepted as a schedule store")
	}
}
