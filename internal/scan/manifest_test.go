package scan

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeJSON(t *testing.T, dir, name string, v any) string {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The format has to be able to express a scheduler that already works, or it
// is not a general mechanism, it is a guess. Hermes has a hand-written reader
// and a real on-disk shape: jobs under a "jobs" key, a nested schedule object
// that is one of cron/interval/once, `paused_at` for disabled, and its own
// last_run_at. If a manifest can read that file, it can read most app
// schedulers.
func TestAManifestCanExpressHermes(t *testing.T) {
	dir := t.TempDir()
	soon := time.Now().Add(48 * time.Hour).Format(time.RFC3339)
	path := writeJSON(t, dir, "jobs.json", map[string]any{
		"jobs": []any{
			map[string]any{
				"id": "aaa", "name": "digest", "enabled": true,
				"schedule":    map[string]any{"kind": "cron", "expr": "0 9,21 * * *"},
				"last_run_at": "2026-08-15T09:01:15+08:00",
				"last_status": "ok",
			},
			map[string]any{
				"id": "bbb", "name": "uptime", "enabled": true,
				"schedule": map[string]any{"kind": "interval", "minutes": 30},
			},
			map[string]any{
				"id": "ccc", "name": "one-off", "enabled": true,
				"schedule": map[string]any{"kind": "once", "run_at": soon},
			},
			map[string]any{
				"id": "ddd", "name": "paused one", "enabled": true,
				"schedule":  map[string]any{"kind": "cron", "expr": "0 9 * * *"},
				"paused_at": "2026-08-01T00:00:00Z",
			},
			map[string]any{
				"id": "eee", "name": "switched off", "enabled": false,
				"schedule": map[string]any{"kind": "cron", "expr": "0 9 * * *"},
			},
		},
	})

	m := Manifest{
		Name:    "hermes-via-manifest",
		Files:   []string{path},
		Jobs:    "jobs",
		Command: "hermes cron run {name}",
		Fields: ManifestFields{
			ID: "id", Name: "name", Enabled: "enabled", Disabled: "paused_at",
			Cron: "schedule.expr", Every: "schedule.minutes", At: "schedule.run_at",
			LastRun: "last_run_at", NextRun: "next_run_at", Status: "last_status",
		},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	p := &manifestProvider{m: m}
	if !p.Available() {
		t.Fatal("the file exists but the provider reports it does not")
	}
	got, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	bySchedule := map[string]string{}
	for _, e := range got {
		bySchedule[e.Name] = e.Schedule
	}
	if len(got) != 3 {
		t.Fatalf("found %d jobs, want 3 (paused and disabled must be skipped): %v", len(got), bySchedule)
	}
	if bySchedule["digest"] != "0 9,21 * * *" {
		t.Errorf("cron schedule = %q", bySchedule["digest"])
	}
	if bySchedule["uptime"] != "every 30m" {
		t.Errorf("interval schedule = %q", bySchedule["uptime"])
	}
	if bySchedule["one-off"] == "" {
		t.Error("a future one-shot was dropped")
	}
	for _, e := range got {
		if e.Command == "" || e.Source != Source("hermes-via-manifest") {
			t.Errorf("entry %q has command %q source %q", e.Name, e.Command, e.Source)
		}
		if e.Wrappable {
			t.Errorf("%q was marked wrappable; the app runs it, there is no command line to edit", e.Name)
		}
	}
}

// The scheduler's own record of when a job ran is what makes these exactly
// timed instead of held for a fixed window. Without it there is nothing to
// observe: no process appears and none exits.
func TestAManifestReportsRunState(t *testing.T) {
	dir := t.TempDir()
	path := writeJSON(t, dir, "jobs.json", map[string]any{
		"jobs": []any{map[string]any{
			"id": "aaa", "name": "digest", "enabled": true,
			"schedule":    map[string]any{"expr": "0 9 * * *"},
			"last_run_at": "2026-08-15T09:01:15Z",
			"next_run_at": "2026-08-16T09:00:00Z",
			"last_status": "success",
			"in_flight":   false,
		}},
	})
	p := &manifestProvider{m: Manifest{
		Name: "app", Files: []string{path}, Jobs: "jobs",
		Fields: ManifestFields{
			ID: "id", Name: "name", Enabled: "enabled", Cron: "schedule.expr",
			LastRun: "last_run_at", NextRun: "next_run_at",
			Status: "last_status", Running: "in_flight",
		},
	}}

	rec, ok := p.ObserveRun(context.Background(), "aaa")
	if !ok {
		t.Fatal("no run record for a job that has one")
	}
	if rec.CompletedAt.IsZero() || rec.NextRun.IsZero() {
		t.Errorf("timestamps not read: %+v", rec)
	}
	if !rec.Succeeded() {
		t.Errorf("status %q was not normalised to ok", rec.Status)
	}
	if _, ok := p.ObserveRun(context.Background(), "nope"); ok {
		t.Error("a run record was invented for an unknown job")
	}
}

// The other common on-disk shape: an object keyed by job id rather than an
// array. The key becomes the id.
func TestAManifestReadsJobsKeyedByID(t *testing.T) {
	dir := t.TempDir()
	path := writeJSON(t, dir, "tasks.json", map[string]any{
		"tasks": map[string]any{
			"task-1": map[string]any{"title": "morning", "cron": "0 8 * * *", "on": true},
			"task-2": map[string]any{"title": "evening", "cron": "0 20 * * *", "on": true},
		},
	})
	p := &manifestProvider{m: Manifest{
		Name: "app", Files: []string{path}, Jobs: "tasks",
		Fields: ManifestFields{Name: "title", Cron: "cron", Enabled: "on"},
	}}
	got, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("found %d jobs, want 2", len(got))
	}
	if got[0].Label != "task-1" {
		t.Errorf("the map key was not used as the id: %q", got[0].Label)
	}
}

// A manifest that cannot work must say so when it is loaded, not discover
// nothing forever.
func TestManifestValidationCatchesTypos(t *testing.T) {
	cases := map[string]Manifest{
		"no name":     {Files: []string{"/x"}, Fields: ManifestFields{Name: "n", Cron: "c"}},
		"no files":    {Name: "a", Fields: ManifestFields{Name: "n", Cron: "c"}},
		"no name map": {Name: "a", Files: []string{"/x"}, Fields: ManifestFields{Cron: "c"}},
		"no schedule": {Name: "a", Files: []string{"/x"}, Fields: ManifestFields{Name: "n"}},
		"shadows an OS scheduler": {
			Name: "crontab", Files: []string{"/x"},
			Fields: ManifestFields{Name: "n", Cron: "c"},
		},
	}
	for name, m := range cases {
		if err := m.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// One half-written file must not stop goguma reading the sources that work.
func TestLoadingSkipsBadManifestsAndReportsThem(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not json"), 0o600)
	os.WriteFile(filepath.Join(dir, "invalid.json"), []byte(`{"name":"x"}`), 0o600)
	writeJSON(t, dir, "good.json", Manifest{
		Name: "good-app", Files: []string{"/nonexistent"},
		Fields: ManifestFields{Name: "name", Cron: "cron"},
	})

	loaded, problems := LoadManifests(dir)
	if len(loaded) != 1 || loaded[0] != "good-app" {
		t.Errorf("loaded = %v, want just the good one", loaded)
	}
	if len(problems) != 2 {
		t.Errorf("problems = %v, want one per bad file", problems)
	}

	// Loading twice must not register the same app twice.
	LoadManifests(dir)
	n := 0
	for _, p := range Providers() {
		if p.Name() == "good-app" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("good-app is registered %d times after two loads", n)
	}
}
