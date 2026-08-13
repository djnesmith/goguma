package scan

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// SystemdTimers lists user and system timers.
//
// `systemctl list-timers` is used rather than parsing unit files because it
// resolves the full timer graph (including drop-ins, templated units, and
// the timer-to-service mapping), which reimplementing would get subtly wrong.
func SystemdTimers(ctx context.Context) ([]Entry, error) {
	var out []Entry
	for _, scope := range []string{"--user", "--system"} {
		entries, err := listTimers(ctx, scope)
		if err != nil {
			continue // a machine may legitimately have no user bus
		}
		out = append(out, entries...)
	}
	return out, nil
}

func listTimers(ctx context.Context, scope string) ([]Entry, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "systemctl", scope, "list-timers",
		"--all", "--no-pager", "--no-legend", "--output=json")
	raw, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	entries := parseTimers(string(raw), scope == "--system")
	// The listing has no recurrence, only the next occurrence, so without
	// this read-back every timer carried an empty schedule and Evaluate
	// filtered all of them as "no scheduled time to wake for": the systemd
	// provider found everything and kept nothing.
	for i := range entries {
		expr, serr := SystemdSchedule(ctx, entries[i].Label, scope == "--user")
		if serr != nil || expr == "" {
			continue
		}
		entries[i].Schedule = CronFromOnCalendar(expr)
	}
	return entries, nil
}

// parseTimers reads `systemctl list-timers --output=json`.
//
// The schedule is read back from each timer unit's OnCalendar property rather
// than from the listing, because the listing only shows the *next* occurrence,
// which is a point in time and not a recurring schedule.
func parseTimers(jsonText string, system bool) []Entry {
	type timerRow struct {
		Unit      string `json:"unit"`
		Activates string `json:"activates"`
	}
	var rows []timerRow
	if err := jsonDecode(jsonText, &rows); err != nil {
		return nil
	}

	var out []Entry
	for _, r := range rows {
		if r.Unit == "" {
			continue
		}
		e := Entry{
			Name:   strings.TrimSuffix(r.Unit, ".timer"),
			Source: SourceSystemd,
			Label:  r.Unit,
			// Distribution-shipped timers under /usr/lib are the OS vendor's,
			// not the user's automation.
			SystemOwned: system && isDistroTimer(r.Unit),
			Command:     r.Activates,
		}
		out = append(out, e)
	}
	return out
}

func isDistroTimer(unit string) bool {
	for _, prefix := range []string{
		"systemd-", "apt-", "dnf-", "man-db", "logrotate", "fstrim",
		"e2scrub", "snapd", "packagekit",
	} {
		if strings.HasPrefix(unit, prefix) {
			return true
		}
	}
	return false
}

// SystemdSchedule reads a timer's OnCalendar expression.
//
// systemd calendar syntax is not cron syntax, so the caller must translate.
// Returning it verbatim rather than guessing a cron equivalent keeps a
// mistranslation from silently scheduling wakes at the wrong times.
func SystemdSchedule(ctx context.Context, unit string, user bool) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	scope := "--system"
	if user {
		scope = "--user"
	}
	out, err := exec.CommandContext(ctx, "systemctl", scope, "show", unit,
		"--property=TimersCalendar", "--value").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// jsonDecode is a thin indirection so the import stays local to this file.
func jsonDecode(s string, v any) error {
	return decodeJSON([]byte(s), v)
}

var _ = time.Second
