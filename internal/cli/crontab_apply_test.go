package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/junnam586/goguma/internal/paths"
	"github.com/junnam586/goguma/internal/render"
)

// fakeCrontab puts a stand-in `crontab` on PATH backed by a file, so the write
// path can be exercised for real without touching the developer's own crontab.
func fakeCrontab(t *testing.T, initial string) string {
	t.Helper()
	dir := t.TempDir()
	store := filepath.Join(dir, "crontab.txt")
	if err := os.WriteFile(store, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"-l\" ]; then cat \"$FAKE_CRONTAB\"; exit 0; fi\n" +
		"if [ \"$1\" = \"-\" ]; then cat > \"$FAKE_CRONTAB\"; exit 0; fi\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "crontab"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CRONTAB", store)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return store
}

func testContext(t *testing.T) *Context {
	t.Helper()
	return &Context{
		Layout: paths.Layout{StateDir: t.TempDir()},
		Out:    render.NewPlain(os.Stdout),
	}
}

const liveCrontab = `# my jobs
PATH=/usr/bin

0 9 * * * /usr/local/bin/calendar-sync
30 2 * * * /opt/backup.sh --full
`

// `import` finishing with "now go and paste this yourself" is a setup command
// that ends in homework, and skipping the homework is punished silently: the
// job is already registered as exactly-timed, so it records never-detected on
// every run afterwards. This is the write that removes the step.
func TestImportInstallsTheWrapperItself(t *testing.T) {
	fakeCrontab(t, liveCrontab)
	ctx := testContext(t)

	if err := applyCrontabWrap(ctx, 4, "/usr/local/bin/calendar-sync",
		"goguma-mark calendar-sync -- /usr/local/bin/calendar-sync"); err != nil {
		t.Fatalf("apply: %v", err)
	}

	live, err := readCrontab()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(live, "0 9 * * * goguma-mark calendar-sync -- /usr/local/bin/calendar-sync") {
		t.Errorf("the wrapper was not installed:\n%s", live)
	}
	// Everything else is someone's working automation.
	for _, keep := range []string{"# my jobs", "PATH=/usr/bin", "30 2 * * * /opt/backup.sh --full"} {
		if !strings.Contains(live, keep) {
			t.Errorf("%q was lost from the crontab:\n%s", keep, live)
		}
	}
}

// There must always be something to put back.
func TestImportBacksUpBeforeWriting(t *testing.T) {
	fakeCrontab(t, liveCrontab)
	ctx := testContext(t)

	if err := applyCrontabWrap(ctx, 4, "/usr/local/bin/calendar-sync",
		"goguma-mark calendar-sync -- /usr/local/bin/calendar-sync"); err != nil {
		t.Fatalf("apply: %v", err)
	}

	b, err := os.ReadFile(ctx.Layout.CrontabBackup())
	if err != nil {
		t.Fatalf("no backup was written: %v", err)
	}
	if string(b) != liveCrontab {
		t.Errorf("the backup is not the crontab as it was:\n%s", b)
	}
}

// A crontab edited between the scan and the write must be refused, not
// clobbered: the line number now points at a job the user just added.
func TestImportRefusesAChangedCrontab(t *testing.T) {
	fakeCrontab(t, liveCrontab)
	ctx := testContext(t)

	err := applyCrontabWrap(ctx, 4, "/usr/local/bin/something-the-scan-saw",
		"goguma-mark x -- /usr/local/bin/something-the-scan-saw")
	if err == nil {
		t.Fatal("a crontab that had changed underneath was rewritten anyway")
	}

	live, _ := readCrontab()
	if live != liveCrontab {
		t.Errorf("the crontab was modified despite the refusal:\n%s", live)
	}
	if _, err := os.Stat(ctx.Layout.CrontabBackup()); err == nil {
		t.Error("a backup was written for a change that never happened")
	}
}
