package scan

import (
	"context"
	"os/exec"
)

func init() {
	Register(&crontabProvider{})
	Register(&systemdProvider{})
}

// crontabProvider reads the user's crontab.
type crontabProvider struct{}

func (c *crontabProvider) Name() string  { return "crontab" }
func (c *crontabProvider) Where() string { return "crontab -l" }

// Available distinguishes "no crontab exists" from "the crontab is empty".
// The former is the most useful hint `import` can give a user who is not sure
// what they have scheduled: their automation must live somewhere else.
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

// systemdProvider reads user and system timers.
type systemdProvider struct{}

func (s *systemdProvider) Name() string  { return "systemd" }
func (s *systemdProvider) Where() string { return "systemctl list-timers (user and system)" }

func (s *systemdProvider) Available() bool {
	_, err := exec.LookPath("systemctl")
	return err == nil
}

func (s *systemdProvider) Discover(ctx context.Context) ([]Entry, error) {
	return SystemdTimers(ctx)
}

// Discover collects every schedulable entry from every registered provider.
func Discover(ctx context.Context) ([]Entry, error) {
	entries, _ := DiscoverAll(ctx)
	return entries, nil
}
