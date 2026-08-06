package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func hintPaths(hs []Hint) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, filepath.Base(h.Path))
	}
	return out
}

func TestHintsFindCrontabNamedFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "project/crontab", "0 9 * * * /opt/briefing.sh\n")

	hints := FindSchedulerHints(context.Background(), []string{root}, 5, 20)
	if len(hints) != 1 {
		t.Fatalf("got %d hints %v, want 1", len(hints), hintPaths(hints))
	}
	if hints[0].Sample == "" {
		t.Error("a matching line should be sampled so the user can judge it")
	}
}

func TestHintsIgnoreBinaries(t *testing.T) {
	root := t.TempDir()
	// An extension-less executable. A first implementation happily scanned
	// these and reported unprintable bytes as a schedule sample.
	writeFile(t, root, "cron/uvx", "\x7fELF\x02\x01\x01\x00\x00\x00binary\x00garbage")

	hints := FindSchedulerHints(context.Background(), []string{root}, 5, 20)
	if len(hints) != 0 {
		t.Errorf("binary file was reported as a scheduler: %v", hintPaths(hints))
	}
}

func TestHintsIgnoreDecoratorsAndAddresses(t *testing.T) {
	root := t.TempDir()
	// "@staticmethod" and an email both begin with @, which an earlier
	// implementation accepted as a cron descriptor.
	writeFile(t, root, "cron/notes.txt",
		"@staticmethod\ndef f():\n    pass\ncontact @someone or me@example.com\n")

	hints := FindSchedulerHints(context.Background(), []string{root}, 5, 20)
	if len(hints) != 0 {
		t.Errorf("non-schedule @ lines were reported: %v", hintPaths(hints))
	}
}

func TestHintsAcceptRealDescriptors(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "jobs.cron", "@daily /opt/backup.sh\n")

	hints := FindSchedulerHints(context.Background(), []string{root}, 5, 20)
	if len(hints) != 1 {
		t.Fatalf("got %d hints, want 1 for a real @daily descriptor", len(hints))
	}
}

func TestHintsIgnoreFilesOutsideScheduleDirectories(t *testing.T) {
	root := t.TempDir()
	// Valid cron syntax, but in a random text file that is not crontab-named
	// and not inside a schedule-shaped directory. Opening every such file is
	// what produced dozens of false positives.
	writeFile(t, root, "docs/readme.txt", "0 9 * * * /opt/thing.sh\n")

	hints := FindSchedulerHints(context.Background(), []string{root}, 5, 20)
	if len(hints) != 0 {
		t.Errorf("a file outside any schedule directory was reported: %v", hintPaths(hints))
	}
}

func TestHintsIgnoreFiveNumbersThatAreNotCron(t *testing.T) {
	root := t.TempDir()
	// Five numeric fields alone are not enough — the remainder must look like
	// a command, or ordinary tabular data matches.
	writeFile(t, root, "cron/data.txt", "1 2 3 4 5\n10 20 30 40 50\n")

	hints := FindSchedulerHints(context.Background(), []string{root}, 5, 20)
	if len(hints) != 0 {
		t.Errorf("numeric data was reported as schedules: %v", hintPaths(hints))
	}
}

func TestDormantCrontabIsReportedAsNotInstalled(t *testing.T) {
	root := t.TempDir()
	// The real-world case: a crontab committed to a repository, meant for a
	// different machine, with every entry still commented out. It looks
	// entirely convincing and schedules nothing here.
	writeFile(t, root, "agent/crontab", `# Personal agent cron entries.
# Install on the droplet with: crontab ./crontab
# (no entries yet)
`)
	hints := FindSchedulerHints(context.Background(), []string{root}, 5, 20)
	if len(hints) != 1 {
		t.Fatalf("got %d hints, want the dormant crontab surfaced", len(hints))
	}
	if hints[0].Live != LiveNo {
		t.Errorf("Live = %q, want %q — an uninstalled file must not read as active",
			hints[0].Live, LiveNo)
	}
}

func TestHintsSkipDependencyTrees(t *testing.T) {
	root := t.TempDir()
	// Dependency trees are full of cron libraries and fixtures. Including
	// them buries every real finding.
	for _, dir := range []string{"node_modules/cron", "venv/cron", ".git/cron"} {
		writeFile(t, root, filepath.Join(dir, "crontab"), "0 9 * * * /opt/x.sh\n")
	}
	hints := FindSchedulerHints(context.Background(), []string{root}, 6, 20)
	if len(hints) != 0 {
		t.Errorf("dependency-tree files were reported: %v", hintPaths(hints))
	}
}

func TestHintsRespectTheCap(t *testing.T) {
	root := t.TempDir()
	for i := range 30 {
		writeFile(t, root, filepath.Join("d"+itoa(i), "crontab"), "0 9 * * * /opt/x.sh\n")
	}
	hints := FindSchedulerHints(context.Background(), []string{root}, 5, 5)
	if len(hints) > 5 {
		t.Errorf("got %d hints, cap was 5", len(hints))
	}
}

func TestVerifyMarksUninstalledWhenNoCrontabExists(t *testing.T) {
	// With no installed crontab, nothing found on disk can be in effect.
	in := []Hint{{Path: "/x/crontab", Live: LiveUnknown, Sample: "0 9 * * * /opt/x.sh"}}
	got := VerifyAgainstInstalledCrontab(context.Background(), in)
	if got[0].Live == LiveYes {
		t.Error("a hint was marked live despite no crontab being installed")
	}
}
