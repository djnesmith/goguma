package agenthooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func claudeHarness(t *testing.T) Harness {
	t.Helper()
	for _, h := range Harnesses {
		if h.ID == "claude-code" {
			return h
		}
	}
	t.Fatal("claude-code Harness is gone")
	return Harness{}
}

func cursorHarness(t *testing.T) Harness {
	t.Helper()
	for _, h := range Harnesses {
		if h.ID == "cursor" {
			return h
		}
	}
	t.Fatal("cursor Harness is gone")
	return Harness{}
}

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var d map[string]any
	if err := json.Unmarshal([]byte(s), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

// TestInstallKeepsWhatWasAlreadyThere is the property that matters most.
//
// This edits a file belonging to another program, one that a user has very
// likely already put their own work into. Dropping somebody's formatter or
// audit hook to add a sleep hold would be a far worse bug than the sleep.
func TestInstallKeepsWhatWasAlreadyThere(t *testing.T) {
	h := claudeHarness(t)
	before := decode(t, `{
		"model": "opus",
		"permissions": {"allow": ["Bash"]},
		"hooks": {
			"UserPromptSubmit": [{"hooks": [{"type": "command", "command": "my-indexer"}]}],
			"Stop": [{"matcher": "x", "hooks": [{"type": "command", "command": "notify-me"}]}]
		}
	}`)

	after := Apply(before, h, "/opt/bin", false)

	// Unrelated settings are untouched.
	if after["model"] != "opus" {
		t.Error("an unrelated top-level setting was lost")
	}
	if _, ok := after["permissions"]; !ok {
		t.Error("permissions were lost")
	}

	cmds := CommandsIn(after, h)
	for _, want := range []string{"my-indexer", "notify-me"} {
		if !containsCmd(cmds, want) {
			t.Errorf("the user's own hook %q was dropped", want)
		}
	}
	if !containsCmd(cmds, "/opt/bin/goguma agent-hook --event renew") {
		t.Error("goguma's renew hook was not added")
	}
	if !containsCmd(cmds, "/opt/bin/goguma agent-hook --event stop") {
		t.Error("goguma's stop hook was not added")
	}

	// And the user's matcher survives, which is what decides when their hook
	// runs at all.
	hooks := after["hooks"].(map[string]any)
	stop := hooks["Stop"].([]any)
	var sawMatcher bool
	for _, b := range stop {
		if m, ok := b.(map[string]any)["matcher"]; ok && m == "x" {
			sawMatcher = true
		}
	}
	if !sawMatcher {
		t.Error("the user's matcher was dropped")
	}
}

// TestInstallingTwiceChangesNothingTheSecondTime: install runs on every
// `goguma install`, and a user may run that repeatedly.
func TestInstallingTwiceChangesNothingTheSecondTime(t *testing.T) {
	h := claudeHarness(t)
	doc := decode(t, `{"hooks": {"UserPromptSubmit": [{"hooks": [{"type": "command", "command": "mine"}]}]}}`)

	once := Apply(doc, h, "/opt/bin", false)
	first, _ := json.Marshal(once)
	twice := Apply(once, h, "/opt/bin", false)
	second, _ := json.Marshal(twice)

	if string(first) != string(second) {
		t.Errorf("a second install changed the file again:\n first: %s\nsecond: %s", first, second)
	}
	n := 0
	for _, c := range CommandsIn(twice, h) {
		if strings.Contains(c, Marker) {
			n++
		}
	}
	if want := len(h.renewOn) + len(h.stopOn); n != want {
		t.Errorf("goguma has %d entries after two installs, want %d", n, want)
	}
}

// TestRemoveLeavesTheFileAsItWasFound. An uninstall that leaves debris behind
// is a reason not to try the thing in the first place.
func TestRemoveLeavesTheFileAsItWasFound(t *testing.T) {
	h := claudeHarness(t)
	src := `{
		"model": "opus",
		"hooks": {
			"UserPromptSubmit": [{"hooks": [{"type": "command", "command": "mine"}]}],
			"Stop": [{"hooks": [{"type": "command", "command": "notify"}]}]
		}
	}`
	original := decode(t, src)
	working := decode(t, src)

	after := Apply(Apply(working, h, "/opt/bin", false), h, "/opt/bin", true)
	if !reflect.DeepEqual(original, after) {
		a, _ := json.Marshal(original)
		b, _ := json.Marshal(after)
		t.Errorf("remove did not restore the file\n  was: %s\n  now: %s", a, b)
	}
}

// TestRemoveDropsAnEventItAddedEntirely: goguma adds PostToolUse, which the
// user had no entry for. Removing must take the whole event away rather than
// leave an empty array behind.
func TestRemoveDropsAnEventItAddedEntirely(t *testing.T) {
	h := claudeHarness(t)
	doc := map[string]any{}
	after := Apply(Apply(doc, h, "/opt/bin", false), h, "/opt/bin", true)
	if len(after) != 0 {
		b, _ := json.Marshal(after)
		t.Errorf("remove left something behind in an untouched file: %s", b)
	}
}

// TestCursorGetsItsOwnShape. Two Harnesses, two spellings of the same idea, and
// writing one into the other's file produces a config the tool ignores in
// silence, which is the worst way for this to fail.
func TestCursorGetsItsOwnShape(t *testing.T) {
	h := cursorHarness(t)
	doc := Apply(map[string]any{}, h, "/opt/bin", false)

	if doc["version"] != 1 {
		t.Errorf("version = %v, want 1; Cursor requires it", doc["version"])
	}
	hooks := doc["hooks"].(map[string]any)
	blocks := hooks["stop"].([]any)
	block := blocks[0].(map[string]any)
	if _, nested := block["hooks"]; nested {
		t.Error("Cursor got the nested Claude Code shape, which it does not read")
	}
	if c, _ := block["command"].(string); !strings.Contains(c, "--event stop") {
		t.Errorf("stop command = %q", c)
	}
}

// TestAnExistingVersionIsNotOverwritten: if Cursor moves to version 2, goguma
// must not quietly downgrade the file.
func TestAnExistingVersionIsNotOverwritten(t *testing.T) {
	h := cursorHarness(t)
	doc := Apply(map[string]any{"version": float64(2)}, h, "/opt/bin", false)
	if doc["version"] != float64(2) {
		t.Errorf("version = %v, want the file's own 2", doc["version"])
	}
}

// TestUnparseableConfigIsRefusedRatherThanReplaced. A file goguma cannot read
// is a file it must not write: replacing it would destroy settings that a
// human, or a newer version of that tool, put there.
func TestUnparseableConfigIsRefusedRatherThanReplaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	broken := []byte("{ this is not json")
	if err := os.WriteFile(path, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadConfig(path); err == nil {
		t.Fatal("a broken config was read as if it were fine")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(broken) {
		t.Error("the broken file was modified; it must be left exactly as found")
	}
}

// TestWriteKeepsABackup, so a user can always get back what they had.
func TestWriteKeepsABackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"model":"opus"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	backup, err := WriteConfig(path, map[string]any{"model": "sonnet"})
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Fatal("no backup was kept")
	}
	b, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "opus") {
		t.Errorf("backup does not hold the original: %s", b)
	}
}

// TestHookCommandsAreAbsolute. A hook runs with whatever environment its
// Harness provides, and that frequently has no ~/.local/bin on PATH. A bare
// `goguma` there fails silently: no hold, no error, no clue anywhere.
func TestHookCommandsAreAbsolute(t *testing.T) {
	renew, stop := Commands("/opt/bin")
	for _, c := range []string{renew, stop} {
		if !strings.HasPrefix(c, "/") {
			t.Errorf("hook command is not absolute: %q", c)
		}
		if !strings.Contains(c, Marker) {
			t.Errorf("hook command %q does not carry the marker that identifies it as goguma's", c)
		}
	}
	if renew == stop {
		t.Error("renew and stop are the same command")
	}
}

func containsCmd(cmds []string, want string) bool {
	for _, c := range cmds {
		if c == want {
			return true
		}
	}
	return false
}
