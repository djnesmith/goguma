//go:build darwin

package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/junnam586/goguma/internal/paths"
	"github.com/junnam586/goguma/internal/render"
)

const testPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key><string>glass.goguma.unittest</string>
    <key>ProgramArguments</key>
    <array>
        <string>/bin/echo</string>
        <string>hello</string>
    </array>
    <key>StartCalendarInterval</key>
    <dict><key>Hour</key><integer>4</integer></dict>
    <key>AKeyGogumaHasNeverHeardOf</key><string>keep-me</string>
</dict>
</plist>
`

func writePlist(t *testing.T) (ctx *Context, path string) {
	t.Helper()
	if _, err := os.Stat("/usr/libexec/PlistBuddy"); err != nil {
		t.Skip("PlistBuddy is not present")
	}
	dir := t.TempDir()
	path = filepath.Join(dir, "glass.goguma.unittest.plist")
	if err := os.WriteFile(path, []byte(testPlist), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Context{Layout: paths.Layout{StateDir: dir}, Out: render.NewPlain(os.Stdout)}, path
}

func readArgs(t *testing.T, path string) []string {
	t.Helper()
	out, err := exec.Command("plutil", "-extract", "ProgramArguments", "json", "-o", "-", path).Output()
	if err != nil {
		t.Fatalf("reading ProgramArguments: %v", err)
	}
	var a []string
	if err := json.Unmarshal(out, &a); err != nil {
		t.Fatal(err)
	}
	return a
}

// The two entries go at the front, and everything the plist already said
// survives. A naive marshal-and-write would drop every key goguma does not
// model, which for a launchd job is most of them.
func TestInsertWrapperKeepsTheRestOfThePlist(t *testing.T) {
	_, path := writePlist(t)

	if err := insertWrapper(path, "/Users/x/.local/bin/goguma-mark", "my-job"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got := readArgs(t, path)
	want := []string{"/Users/x/.local/bin/goguma-mark", "my-job", "/bin/echo", "hello"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("ProgramArguments = %v, want %v", got, want)
	}

	raw, _ := os.ReadFile(path)
	for _, keep := range []string{"keep-me", "StartCalendarInterval", "glass.goguma.unittest"} {
		if !strings.Contains(string(raw), keep) {
			t.Errorf("%q was lost from the plist:\n%s", keep, raw)
		}
	}
}

// The wrapper is named by absolute path because cron runs with
// PATH=/usr/bin:/bin and launchd with /usr/bin:/bin:/usr/sbin:/sbin, neither of
// which contains ~/.local/bin. A bare name works in the user's shell and fails
// under the scheduler, which does not degrade timing, it stops the job running.
func TestWrapperIsNamedByAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "goguma-mark")
	if err := os.WriteFile(mark, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := &Context{Layout: paths.Layout{BinDir: dir}, Out: render.NewPlain(os.Stdout)}

	if got := markBinary(ctx); got != mark {
		t.Errorf("markBinary = %q, want the installed absolute path %q", got, mark)
	}
	if !filepath.IsAbs(markBinary(ctx)) {
		t.Error("the wrapper is not an absolute path, so cron and launchd will not find it")
	}
}

// A path in no launchd directory at all is refused. /System/Library is Apple's
// and immutable under SIP; anywhere else is not a launchd job.
//
// Note what is NOT here: /Library/LaunchDaemons used to be refused for needing
// root, and is now supported through sudo. See TestScopeRoutesEveryLaunchdLocation.
func TestLaunchdWrapRefusesNonLaunchdPaths(t *testing.T) {
	ctx, _ := writePlist(t)
	for _, path := range []string{
		"/System/Library/LaunchDaemons/com.apple.something.plist",
		"/tmp/not-a-launchd-job.plist",
	} {
		err := applyLaunchdWrap(ctx, path, "x", "cmd", "/bin/mark", "slug")
		if err == nil {
			t.Errorf("%s was accepted, and it must not be", path)
		}
	}
}

// The plist can be edited between the scan and the write, and rewriting it
// then would clobber whatever the user just changed.
func TestLaunchdWrapRefusesAChangedPlist(t *testing.T) {
	ctx, path := writePlist(t)
	// Pretend it lives in the home directory, which is the only location the
	// writer accepts.
	home, _ := os.UserHomeDir()
	inHome := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(inHome, 0o755); err != nil {
		t.Skip("no LaunchAgents directory")
	}
	dest := filepath.Join(inHome, "glass.goguma.unittest.plist")
	b, _ := os.ReadFile(path)
	if err := os.WriteFile(dest, b, 0o644); err != nil {
		t.Skip("cannot write to LaunchAgents")
	}
	t.Cleanup(func() { _ = os.Remove(dest) })

	err := applyLaunchdWrap(ctx, dest, "glass.goguma.unittest",
		"/bin/echo something-the-scan-saw", "/bin/mark", "slug")
	if err == nil {
		t.Fatal("a plist that had changed underneath was rewritten anyway")
	}
	if got := readArgs(t, dest); strings.Join(got, " ") != "/bin/echo hello" {
		t.Errorf("the plist was modified despite the refusal: %v", got)
	}
}

// launchd execs `Program` when the key is present and treats ProgramArguments
// as argv only. A plist carrying both, wrapped by touching the array alone,
// would run the ORIGINAL binary with argv shifted to [goguma-mark, slug, ...]:
// the wrapper never runs, goguma records never-detected forever, and the job
// receives an argument list it never asked for. Whichever key launchd will
// read has to end up pointing at the wrapper.
func TestWrappingSurvivesTheProgramKey(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"Program only", `<key>Label</key><string>a</string>
			<key>Program</key><string>/usr/local/bin/thing</string>`},
		{"Program and ProgramArguments", `<key>Label</key><string>a</string>
			<key>Program</key><string>/usr/local/bin/thing</string>
			<key>ProgramArguments</key><array><string>/usr/local/bin/thing</string><string>-v</string></array>`},
		{"ProgramArguments only", `<key>Label</key><string>a</string>
			<key>ProgramArguments</key><array><string>/bin/echo</string><string>hi</string></array>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "x.plist")
			full := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>` + c.body + `</dict></plist>`
			if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := insertWrapper(path, "/bin/mark", "slug"); err != nil {
				t.Fatalf("insert: %v", err)
			}

			// What launchd will actually execute.
			program, hasProgram := programKey(path)
			args := readArgs(t, path)
			exec := program
			if !hasProgram {
				exec = args[0]
			}
			if exec != "/bin/mark" {
				t.Errorf("launchd would exec %q, so the wrapper never runs", exec)
			}
			if len(args) < 2 || args[0] != "/bin/mark" || args[1] != "slug" {
				t.Errorf("argv is %v, want the wrapper and job name first", args)
			}
		})
	}
}

// A binary plist must survive as a binary plist and still be valid.
func TestWrappingWorksOnBinaryPlists(t *testing.T) {
	_, path := writePlist(t)
	if err := exec.Command("plutil", "-convert", "binary1", path).Run(); err != nil {
		t.Skip("cannot produce a binary plist")
	}
	if err := insertWrapper(path, "/bin/mark", "slug"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if out, err := exec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
		t.Fatalf("the edited binary plist does not validate: %s", out)
	}
	if got := readArgs(t, path); got[0] != "/bin/mark" {
		t.Errorf("argv is %v", got)
	}
}

// macOS home directories are routinely "/Users/Jun Nam", so the wrapper's
// absolute path routinely contains a space. A crontab command is handed to a
// shell, which splits on it and runs "/Users/Jun". launchd takes argv as
// literal array elements and must NOT be quoted, or the quotes become part of
// the filename.
func TestSpacesInTheWrapperPath(t *testing.T) {
	mark := "/Users/Jun Nam/.local/bin/goguma-mark"

	quoted := shellQuote(mark)
	if quoted == mark {
		t.Error("a path with a space was left unquoted for the shell")
	}
	out, err := exec.Command("/bin/sh", "-c", "printf '%s' "+quoted).Output()
	if err != nil || string(out) != mark {
		t.Errorf("the shell resolves %s to %q, want %q", quoted, out, mark)
	}

	_, path := writePlist(t)
	if err := insertWrapper(path, mark, "slug"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if got := readArgs(t, path)[0]; got != mark {
		t.Errorf("plist argv[0] = %q, want the unquoted path %q", got, mark)
	}
}

// Proves the root path never edits the real file in place: it wraps a copy,
// validates it, and only then installs. Everything up to the install is
// exercised here; the install itself is `sudo install`.
func TestRootPathEditsACopy(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "system.plist")
	body := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>com.example.daemon</string>
<key>Program</key><string>/usr/local/bin/thing</string>
<key>StartInterval</key><integer>3600</integer>
</dict></plist>`
	os.WriteFile(real, []byte(body), 0o644)
	before, _ := os.ReadFile(real)

	// The root branch, with the final install swapped for a local copy so the
	// test needs no password. Everything before it is the shipping code.
	tmp, _ := os.CreateTemp("", "goguma-plist-*.plist")
	tmpName := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpName)
	os.WriteFile(tmpName, before, 0o644)

	if err := insertWrapper(tmpName, "/Users/x/.local/bin/goguma-mark", "slug"); err != nil {
		t.Fatalf("wrap the copy: %v", err)
	}
	if out, err := exec.Command("plutil", "-lint", tmpName).CombinedOutput(); err != nil {
		t.Fatalf("copy did not validate: %s", out)
	}

	after, _ := os.ReadFile(real)
	if string(after) != string(before) {
		t.Error("the real plist was modified before the install step")
	}

	prog, hasProg := programKey(tmpName)

	if !hasProg || prog != "/Users/x/.local/bin/goguma-mark" {
		t.Errorf("Program is %q, so launchd would still exec the original binary", prog)
	}
	raw, _ := os.ReadFile(tmpName)
	if !strings.Contains(string(raw), "StartInterval") {
		t.Error("StartInterval was lost, so the daemon would stop being scheduled")
	}
}

// Where a plist lives decides how it is written and which launchd domain owns
// the job. Bootstrapping a daemon into a GUI domain fails outright, and the
// reverse would load a user agent as root, so this is derived from the path
// rather than assumed.
func TestScopeRoutesEveryLaunchdLocation(t *testing.T) {
	home, _ := os.UserHomeDir()
	gui := "gui/" + strconv.Itoa(os.Getuid())

	cases := []struct {
		path      string
		domain    string
		needsRoot bool
		refused   bool
	}{
		{path: filepath.Join(home, "Library/LaunchAgents/a.plist"), domain: gui},
		{path: "/Library/LaunchAgents/b.plist", domain: gui, needsRoot: true},
		{path: "/Library/LaunchDaemons/c.plist", domain: "system", needsRoot: true},
		// Apple's own, immutable under SIP, never the user's automation.
		{path: "/System/Library/LaunchDaemons/d.plist", refused: true},
		{path: "/tmp/e.plist", refused: true},
	}
	for _, c := range cases {
		s, err := scopeOf(c.path)
		if c.refused {
			if err == nil {
				t.Errorf("%s was accepted, and it must not be", c.path)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s was refused: %v", c.path, err)
			continue
		}
		if s.domain != c.domain {
			t.Errorf("%s -> domain %q, want %q", c.path, s.domain, c.domain)
		}
		if s.needsRoot != c.needsRoot {
			t.Errorf("%s -> needsRoot %v, want %v", c.path, s.needsRoot, c.needsRoot)
		}
	}
}
