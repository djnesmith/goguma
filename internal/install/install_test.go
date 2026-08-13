package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/junnam586/goguma/internal/paths"
)

func testLayout(t *testing.T) paths.Layout {
	t.Helper()
	root := t.TempDir()
	return paths.Layout{
		StateDir:      filepath.Join(root, "state"),
		LogDir:        filepath.Join(root, "logs"),
		BinDir:        filepath.Join(root, "bin"),
		UnitDir:       filepath.Join(root, "units"),
		SystemUnitDir: filepath.Join(root, "system-units"),
		DaemonService: "test.goguma.absent.daemon",
		HelperService: "test.goguma.absent.helper",
		HelperBinary:  filepath.Join(root, "libexec", "goguma-helper"),
	}
}

// TestArtifactsCoverEveryInstalledBinary is the inventory check the package doc
// promises: "every file this package creates is listed in Artifacts, and
// uninstall removes exactly that list". A binary missing from this list is one
// uninstall silently leaves behind.
func TestArtifactsCoverEveryInstalledBinary(t *testing.T) {
	l := testLayout(t)
	got := Artifacts(l)

	for _, name := range []string{"goguma", "goguma-mark", "goguma-daemon"} {
		want := filepath.Join(l.BinDir, name)
		if !contains(got, want) {
			t.Errorf("Artifacts() is missing %s; uninstall would leave it behind", want)
		}
	}
	if len(got) <= 3 {
		t.Errorf("Artifacts() = %d entries, expected the platform's units and helper too", len(got))
	}
	for _, a := range got {
		if a == "" {
			t.Error("Artifacts() contains an empty path")
		}
		if !filepath.IsAbs(a) {
			t.Errorf("Artifacts() entry %q is not absolute; uninstall resolves it against an unknown cwd", a)
		}
	}
}

// TestArtifactsNeverIncludesTheStateDirectory is the guard for the bug this
// package shipped with.
//
// Uninstall deletes everything Artifacts lists, unconditionally and without
// consulting the purge flag. If the state directory were ever added to that
// inventory, a plain uninstall would delete the user's jobs and duration
// history no matter what they asked for, and the flag would become decorative.
func TestArtifactsNeverIncludesTheStateDirectory(t *testing.T) {
	l := testLayout(t)
	for _, a := range Artifacts(l) {
		if a == l.StateDir || strings.HasPrefix(a, l.StateDir+string(filepath.Separator)) {
			t.Errorf("Artifacts() includes %q, inside the state directory; "+
				"uninstall removes this list unconditionally", a)
		}
	}
}

func TestCopyFilePreservesTheExecutableBit(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("#!/bin/sh\necho hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "nested", "dst")

	if err := copyFile(src, dst, 0o755); err != nil {
		t.Fatalf("copyFile: %v", err)
	}

	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("destination missing: %v", err)
	}
	// A daemon copied without +x installs cleanly and then fails to launch,
	// which surfaces as "the daemon did not start" rather than as a bad copy.
	if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("mode = %v, want the executable bit set", fi.Mode().Perm())
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "#!/bin/sh\necho hi\n" {
		t.Errorf("contents = %q, want the source contents", got)
	}
}

// TestCopyFileLeavesNoTempOnFailure covers the half-written-binary case the
// temp-then-rename dance exists to prevent.
func TestCopyFileLeavesNoTempOnFailure(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "dst")

	if err := copyFile(filepath.Join(dir, "does-not-exist"), dst, 0o755); err == nil {
		t.Fatal("copyFile succeeded with a missing source")
	}
	if _, err := os.Stat(dst + ".new"); !os.IsNotExist(err) {
		t.Error("a .new temp file survived a failed copy")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("a destination was created despite the copy failing")
	}
}

func TestCopyFileReplacesAnExistingBinary(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(src, dst, 0o755); err != nil {
		t.Fatalf("copyFile over an existing file: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("contents = %q, want the replacement", got)
	}
}

func TestFindBinaryReportsHowToBuildAMissingComponent(t *testing.T) {
	_, err := findBinary("goguma-definitely-not-real")
	if err == nil {
		t.Fatal("findBinary found a component that does not exist")
	}
	// The message is the whole value here: a missing helper reads as a broken
	// install unless it says the binary was simply never built.
	if !strings.Contains(err.Error(), "go build") {
		t.Errorf("error %q does not tell the user how to build it", err)
	}
}

func TestFindBinarySearchesTheExtraDirectories(t *testing.T) {
	dir := t.TempDir()
	name := "goguma-fake-component"
	staged := filepath.Join(dir, name)
	if err := os.WriteFile(staged, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// The helper is installed to libexec, never beside the CLI, so a re-run of
	// `goguma install` from the installed location can only find it if BinDir
	// is searched as well as the executable's own directory.
	got, err := findBinary(name, dir)
	if err != nil {
		t.Fatalf("findBinary with an extra dir: %v", err)
	}
	if got != staged {
		t.Errorf("findBinary = %q, want %q", got, staged)
	}
}

func TestExecutableAtRejectsDirectories(t *testing.T) {
	dir := t.TempDir()
	if _, ok := executableAt(dir); ok {
		t.Error("executableAt accepted a directory as a binary")
	}
	if _, ok := executableAt(filepath.Join(dir, "nope")); ok {
		t.Error("executableAt accepted a missing path")
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
