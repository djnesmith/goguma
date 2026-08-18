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

// TestTurningTheSettingOffTakesTheConfigurationBackOut.
//
// The reason the daemon owns this rather than the installer. A setting that
// only decided whether to add more would leave "off" meaning "still on, but no
// longer spreading", which is not what anyone reads it as.
func TestTurningTheSettingOffTakesTheConfigurationBackOut(t *testing.T) {
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
	if theirs != 1 {
		t.Errorf("the user's own hook count is %d, want 1", theirs)
	}

	cfg.AgentHooks = false
	d.reconcileAgentHooks(cfg)
	mine, theirs = gogumaEntries(t, path)
	if mine != 0 {
		t.Errorf("%d goguma entries survived the setting being turned off", mine)
	}
	if theirs != 1 {
		t.Errorf("the user's own hook was lost turning the setting off; count is %d", theirs)
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

// TestReconcilingOffWhenNothingIsInstalledWritesNothing. A user who turned this
// off should not have goguma rewriting their agent's config on every start
// merely to confirm it is still absent.
func TestReconcilingOffWhenNothingIsInstalledWritesNothing(t *testing.T) {
	path := withFakeHome(t, `{"model":"opus"}`)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	d := testDaemon(t)
	cfg := config.Default()
	cfg.AgentHooks = false
	d.reconcileAgentHooks(cfg)

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("the config was rewritten even though there was nothing to remove")
	}
	if _, err := os.Stat(path + ".goguma-backup"); err == nil {
		t.Error("a backup was written for a change that was not needed")
	}
}
