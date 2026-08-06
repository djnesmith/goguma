package scan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
)

func init() {
	Register(&crontabProvider{})
	Register(&launchdProvider{})
}

// crontabProvider reads the user's crontab.
type crontabProvider struct{}

func (c *crontabProvider) Name() string  { return "crontab" }
func (c *crontabProvider) Where() string { return "crontab -l" }

// Available reports whether a crontab exists at all.
//
// On macOS the usual answer is no, and that is worth distinguishing from "a
// crontab exists but is empty": the first means the user's automation must
// live somewhere else, which is the single most useful hint `import` can give
// someone who is not sure what they have scheduled.
func (c *crontabProvider) Available() bool {
	if _, err := exec.LookPath("crontab"); err != nil {
		return false
	}
	out, err := exec.Command("crontab", "-l").Output()
	return err == nil && len(out) > 0
}

func (c *crontabProvider) Discover(ctx context.Context) ([]Entry, error) {
	return Crontab(ctx)
}

// launchdProvider reads user and system LaunchAgents and LaunchDaemons.
type launchdProvider struct{}

func (l *launchdProvider) Name() string { return "launchd" }

func (l *launchdProvider) Where() string {
	return "~/Library/LaunchAgents, /Library/LaunchAgents, /Library/LaunchDaemons"
}

func (l *launchdProvider) Available() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	for _, dir := range LaunchAgentDirs(home) {
		if entries, err := filepath.Glob(filepath.Join(dir, "*.plist")); err == nil && len(entries) > 0 {
			return true
		}
	}
	return false
}

func (l *launchdProvider) Discover(ctx context.Context) ([]Entry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	entries, err := Launchd(ctx, home)
	if err != nil {
		return nil, err
	}
	running := LoadedLabels(ctx)
	for i := range entries {
		if running[entries[i].Label] {
			entries[i].AlwaysRunning = true
		}
	}
	return entries, nil
}

// Discover collects every schedulable entry from every registered provider.
func Discover(ctx context.Context) ([]Entry, error) {
	entries, _ := DiscoverAll(ctx)
	return entries, nil
}
