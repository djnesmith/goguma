package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LaunchAgentDirs are the user-controllable locations scanned by default.
//
// /System/Library/* is deliberately excluded: it is Apple's, immutable under
// SIP, and never the user's automation.
func LaunchAgentDirs(home string) []string {
	return []string{
		filepath.Join(home, "Library", "LaunchAgents"),
		"/Library/LaunchAgents",
		"/Library/LaunchDaemons",
	}
}

// launchdPlist is the subset of launchd.plist keys that determine whether an
// entry is a schedulable, missable job.
type launchdPlist struct {
	Label            string          `json:"Label"`
	Program          string          `json:"Program"`
	ProgramArguments []string        `json:"ProgramArguments"`
	StartInterval    int             `json:"StartInterval"`
	StartCalendar    json.RawMessage `json:"StartCalendarInterval"`
	RunAtLoad        bool            `json:"RunAtLoad"`
	KeepAlive        json.RawMessage `json:"KeepAlive"`
	WatchPaths       []string        `json:"WatchPaths"`
	QueueDirectories []string        `json:"QueueDirectories"`
	Sockets          json.RawMessage `json:"Sockets"`
	MachServices     json.RawMessage `json:"MachServices"`
	Disabled         bool            `json:"Disabled"`
}

// calendarInterval mirrors a StartCalendarInterval dictionary. Pointers
// distinguish "not specified" (a wildcard) from an explicit zero, which is a
// real value for both Minute and Hour.
type calendarInterval struct {
	Minute  *int `json:"Minute"`
	Hour    *int `json:"Hour"`
	Day     *int `json:"Day"`
	Weekday *int `json:"Weekday"`
	Month   *int `json:"Month"`
}

// Launchd discovers scheduled launchd entries.
func Launchd(ctx context.Context, home string) ([]Entry, error) {
	var entries []Entry
	for _, dir := range LaunchAgentDirs(home) {
		files, err := filepath.Glob(filepath.Join(dir, "*.plist"))
		if err != nil {
			continue
		}
		for _, f := range files {
			e, ok := readLaunchdEntry(ctx, f)
			if !ok {
				continue
			}
			entries = append(entries, e)
		}
	}
	return entries, nil
}

func readLaunchdEntry(ctx context.Context, path string) (Entry, bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// plutil handles all three plist encodings (XML, binary, JSON) and is
	// always present on macOS, which is far more robust than parsing the
	// XML ourselves and silently failing on binary plists.
	out, err := exec.CommandContext(ctx, "plutil", "-convert", "json", "-o", "-", path).Output()
	if err != nil {
		return Entry{}, false
	}
	var p launchdPlist
	if err := json.Unmarshal(out, &p); err != nil {
		return Entry{}, false
	}
	if p.Disabled {
		return Entry{}, false
	}

	label := p.Label
	if label == "" {
		label = strings.TrimSuffix(filepath.Base(path), ".plist")
	}
	return entryFromPlist(p, label, path), true
}

func entryFromPlist(p launchdPlist, label, path string) Entry {
	e := Entry{
		Name:        nameFromLabel(label),
		Source:      SourceLaunchd,
		Label:       label,
		File:        path,
		Command:     commandOf(p),
		SystemOwned: isSystemOwned(label),
		// ProgramArguments is editable, so the wrapper can be inserted.
		Wrappable: true,
	}

	// A resident service is never "missed": it is already running when the
	// machine wakes, so there is nothing to wake up for.
	e.AlwaysRunning = len(p.KeepAlive) > 0 && string(p.KeepAlive) != "false" ||
		len(p.Sockets) > 0 || len(p.MachServices) > 0

	switch {
	case len(p.StartCalendar) > 0:
		e.Schedule = cronFromCalendar(p.StartCalendar)
		// A calendar entry names a wall-clock time, so the time is the point.
		e.TimeSensitive = true
		// Apple's launchd.plist(5) is explicit: "Unlike cron which skips job
		// invocations when the computer is asleep, launchd will start the job
		// the next time the computer wakes up." So a calendar job is deferred,
		// not lost; goguma's contribution here is punctuality, not
		// survival. Marking it keeps the recommendation honest instead of
		// implying these runs disappear.
		e.SelfHealing = true

	case p.StartInterval > 0:
		e.Schedule = fmt.Sprintf("every %ds", p.StartInterval)
		e.SelfHealing = true

	case len(p.WatchPaths) > 0 || len(p.QueueDirectories) > 0:
		// Triggered by filesystem activity, not by a clock. There is no fire
		// time to wake for.
		e.Schedule = ""

	case p.RunAtLoad:
		// Runs at login or boot, when the machine is awake by definition.
		e.Schedule = ""
	}
	return e
}

func commandOf(p launchdPlist) string {
	if len(p.ProgramArguments) > 0 {
		return strings.Join(p.ProgramArguments, " ")
	}
	return p.Program
}

// isSystemOwned identifies OS-vendor entries.
func isSystemOwned(label string) bool {
	for _, prefix := range []string{
		"com.apple.", "com.openssh.", "org.cups.", "org.ntp.",
		"com.vix.cron", "org.postfix.", "org.apache.",
	} {
		if strings.HasPrefix(label, prefix) {
			return true
		}
	}
	return false
}

// nameFromLabel turns a reverse-DNS label into a readable name.
func nameFromLabel(label string) string {
	parts := strings.Split(label, ".")
	if len(parts) <= 2 {
		return label
	}
	// The trailing components carry the meaning: com.google.keystone.agent
	// reads better as "keystone-agent" than as the full label.
	tail := parts[len(parts)-2:]
	if len(parts) >= 3 && (tail[0] == "com" || tail[0] == "org" || tail[0] == "io") {
		tail = parts[len(parts)-1:]
	}
	return strings.Join(tail, "-")
}

// cronFromCalendar renders a StartCalendarInterval as a cron expression.
//
// launchd treats missing keys as wildcards, matching crontab semantics, so
// the translation is direct. An array of dictionaries means several fire
// times. When the dictionaries differ in exactly one field the union is a
// plain cron list ("0 2,14 * * *" for 02:00 and 14:00), which is the common
// shape by far, and dropping its extra fires silently lost the overnight
// one, the only fire goguma matters for. A union cron cannot express (the
// dictionaries differ in several fields) falls back to the first entry,
// because inventing an approximation would schedule wakes at wrong times.
func cronFromCalendar(raw json.RawMessage) string {
	var single calendarInterval
	if err := json.Unmarshal(raw, &single); err == nil {
		return cronOf(single)
	}
	var many []calendarInterval
	if err := json.Unmarshal(raw, &many); err == nil && len(many) > 0 {
		if merged, ok := mergeCalendar(many); ok {
			return merged
		}
		return cronOf(many[0])
	}
	return ""
}

// mergeCalendar joins an array of calendar intervals into one cron line when
// they are identical in every field but one, and that field is set in all of
// them. Returns ok=false otherwise.
func mergeCalendar(many []calendarInterval) (string, bool) {
	fields := func(c calendarInterval) [5]*int {
		return [5]*int{c.Minute, c.Hour, c.Day, c.Month, c.Weekday}
	}
	same := func(a, b *int) bool {
		if a == nil || b == nil {
			return a == b
		}
		return *a == *b
	}

	base := fields(many[0])
	varying := -1
	for _, c := range many[1:] {
		f := fields(c)
		diffs := 0
		for i := range f {
			if same(f[i], base[i]) {
				continue
			}
			diffs++
			if varying == -1 {
				varying = i
			}
			if varying != i {
				return "", false
			}
		}
		if diffs > 1 {
			return "", false
		}
	}
	if varying == -1 {
		return cronOf(many[0]), true // duplicates of one fire time
	}

	seen := map[int]bool{}
	vals := make([]int, 0, len(many))
	for _, c := range many {
		v := fields(c)[varying]
		if v == nil {
			return "", false // a wildcard cannot join a comma list
		}
		if !seen[*v] {
			seen[*v] = true
			vals = append(vals, *v)
		}
	}
	sort.Ints(vals)
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		parts = append(parts, fmt.Sprintf("%d", v))
	}

	out := []string{}
	for i, v := range base {
		if i == varying {
			out = append(out, strings.Join(parts, ","))
			continue
		}
		if v == nil {
			out = append(out, "*")
			continue
		}
		out = append(out, fmt.Sprintf("%d", *v))
	}
	return strings.Join(out, " "), true
}

func cronOf(c calendarInterval) string {
	f := func(v *int) string {
		if v == nil {
			return "*"
		}
		return fmt.Sprintf("%d", *v)
	}
	// launchd Weekday: 0 and 7 are both Sunday, same as cron.
	return strings.Join([]string{
		f(c.Minute), f(c.Hour), f(c.Day), f(c.Month), f(c.Weekday),
	}, " ")
}

// LoadedLabels returns launchd labels that currently have a running process.
//
// Used to mark entries as always-running even when their plist does not say
// KeepAlive: a service with a live pid is demonstrably resident regardless
// of how it was configured.
func LoadedLabels(ctx context.Context) map[string]bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "launchctl", "list").Output()
	if err != nil {
		return nil
	}
	running := map[string]bool{}
	for i, line := range strings.Split(string(out), "\n") {
		if i == 0 {
			continue // header: PID Status Label
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if fields[0] != "-" {
			running[fields[2]] = true
		}
	}
	return running
}
