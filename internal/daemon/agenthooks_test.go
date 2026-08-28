package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/junnam586/goguma/internal/agenthooks"
	"github.com/junnam586/goguma/internal/config"
)

// withFakeHome points the harness lookups at a sandbox, so these never touch
// the machine's real agent configuration.
func withFakeHome(t *testing.T, seed string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "settings.json")
}

func gogumaEntries(t *testing.T, path string) (mine, theirs int) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("config is no longer valid JSON: %v\n%s", err, b)
	}
	for _, h := range agenthooks.Harnesses {
		if h.ID != "claude-code" {
			continue
		}
		for _, c := range agenthooks.CommandsIn(doc, h) {
			if strings.Contains(c, agenthooks.Marker) {
				mine++
			} else {
				theirs++
			}
		}
	}
	return mine, theirs
}

// TestTurningTheSettingOffKeepsTheHooksInstalled.
//
// This asserted the opposite until the passive display existed, and the old
// behaviour is what broke it: switching off took the hook lines back out, so
// agents stopped reporting, so goguma had nothing to show and could not say an
// agent was working. It had deafened itself.
//
// `agent_hooks off` means "do not hold sleep off for agents". Whether a report
// opens a hold is decided in StartRun; the hooks are the signal and stay put.
func TestTurningTheSettingOffKeepsTheHooksInstalled(t *testing.T) {
	path := withFakeHome(t, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"my-notifier"}]}]}}`)
	d := testDaemon(t)

	cfg := config.Default()
	if !cfg.AgentHooks {
		t.Fatal("agent_hooks is not on by default; the seamless path depends on it")
	}

	d.reconcileAgentHooks(cfg)
	mine, theirs := gogumaEntries(t, path)
	if mine == 0 {
		t.Fatal("nothing was configured with the setting on")
	}
	installed := mine
	if theirs != 1 {
		t.Errorf("the user's own hook count is %d, want 1", theirs)
	}

	cfg.AgentHooks = false
	d.reconcileAgentHooks(cfg)
	mine, theirs = gogumaEntries(t, path)
	if mine != installed {
		t.Errorf("goguma entries went from %d to %d turning the setting off; "+
			"off must keep listening, or nothing can be reported", installed, mine)
	}
	if theirs != 1 {
		t.Errorf("the user's own hook was lost turning the setting off; count is %d", theirs)
	}
}

// TestReconcilingOffFromScratchStillInstalls: a machine that has never had the
// hooks, with the setting already off, still gets them. Otherwise the passive
// display would work only for people who had once had the setting on.
func TestReconcilingOffFromScratchStillInstalls(t *testing.T) {
	path := withFakeHome(t, `{"model":"opus"}`)
	d := testDaemon(t)

	cfg := config.Default()
	cfg.AgentHooks = false
	d.reconcileAgentHooks(cfg)

	mine, _ := gogumaEntries(t, path)
	if mine == 0 {
		t.Fatal("no hooks installed with the setting off; agents would never report")
	}
}

// TestReconcilingRepeatedlyIsStable, because it runs on every daemon start.
func TestReconcilingRepeatedlyIsStable(t *testing.T) {
	path := withFakeHome(t, `{"model":"opus"}`)
	d := testDaemon(t)
	cfg := config.Default()

	for i := 0; i < 5; i++ {
		d.reconcileAgentHooks(cfg)
	}
	mine, _ := gogumaEntries(t, path)
	if mine != 3 {
		t.Errorf("goguma entries = %d after five reconciles, want 3", mine)
	}

	b, _ := os.ReadFile(path)
	var doc map[string]any
	_ = json.Unmarshal(b, &doc)
	if doc["model"] != "opus" {
		t.Error("an unrelated setting was lost")
	}
}

// TestReconcilingOffWhenAlreadyInstalledWritesNothing.
//
// This used to assert that reconciling with the setting off wrote nothing at
// all, which was true when off meant "take the hooks out" and is false now that
// it means "keep listening, do not hold". The property worth keeping is the one
// underneath it: the daemon must not rewrite somebody else's config on every
// start merely to confirm it already says what it should.
func TestReconcilingOffWhenAlreadyInstalledWritesNothing(t *testing.T) {
	path := withFakeHome(t, `{"model":"opus"}`)
	d := testDaemon(t)
	cfg := config.Default()
	cfg.AgentHooks = false

	// First pass installs.
	d.reconcileAgentHooks(cfg)
	if mine, _ := gogumaEntries(t, path); mine == 0 {
		t.Fatal("nothing was installed on the first pass")
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	// Second pass has nothing to do.
	d.reconcileAgentHooks(cfg)
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("the config was rewritten even though it was already correct")
	}
	// A backup is written only when the file is, so its mtime is the second
	// witness that nothing happened on the idle pass.
	if bk, err := os.Stat(path + ".goguma-backup"); err == nil {
		if bk.ModTime().After(before.ModTime()) {
			t.Error("a backup was written for a pass that changed nothing")
		}
	}
}

// TestOptOutMarkerStopsTheReconcile.
//
// The daemon reinstalls the hooks on every start, because they are the signal
// and `agent_hooks` only decides whether that signal opens a hold. That would
// undo `goguma hooks remove` at the next login and make it a command with a
// success message and no effect, so the CLI records the removal and this is
// what reads it.
func TestOptOutMarkerStopsTheReconcile(t *testing.T) {
	path := withFakeHome(t, `{"model":"opus"}`)
	d := testDaemon(t)
	cfg := config.Default()

	if err := os.WriteFile(d.store.Layout().HooksOptOut(), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	d.reconcileAgentHooks(cfg)

	if mine, _ := gogumaEntries(t, path); mine != 0 {
		t.Fatalf("%d hooks were installed despite the opt-out marker", mine)
	}

	// Clearing it lets the reconcile run again, which is what `hooks install`
	// relies on.
	if err := os.Remove(d.store.Layout().HooksOptOut()); err != nil {
		t.Fatal(err)
	}
	d.reconcileAgentHooks(cfg)
	if mine, _ := gogumaEntries(t, path); mine == 0 {
		t.Fatal("nothing was installed after the opt-out was cleared")
	}
}
