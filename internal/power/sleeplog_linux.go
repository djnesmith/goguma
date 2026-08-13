package power

import (
	"bufio"
	"context"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/junnam586/goguma/internal/schedule"
)

// journalTimeLayouts are the timestamp formats `journalctl --output=short-iso`
// has emitted across versions: the zone offset gained a colon in systemd 249,
// and short-iso-precise adds microseconds. All are accepted so the parser is
// not silently version-locked to whatever the developer happened to test on.
var journalTimeLayouts = []string{
	"2006-01-02T15:04:05-0700",
	"2006-01-02T15:04:05-07:00",
	"2006-01-02T15:04:05.000000-0700",
	"2006-01-02T15:04:05.000000-07:00",
}

// journalSinceLayout is the timestamp format journalctl accepts for --since.
const journalSinceLayout = "2006-01-02 15:04:05"

// journalTimeout bounds each journalctl invocation. A 14-day kernel journal is
// large, and this runs on the daemon's behalf, so it is streamed rather than
// buffered and capped rather than trusted to finish.
const journalTimeout = 20 * time.Second

// suspendEnterMarkers and suspendResumeMarkers are message substrings that
// mark the two edges of a sleep period.
//
// Several are listed for each edge because no single one is portable: the
// kernel's PM lines are present on every kernel but need journal read access,
// while the systemd-sleep wording changed twice (systemd 248 moved from
// "Suspending system..." to "Entering sleep state 'suspend'...", and 255 moved
// again to "Performing sleep operation..."). Matching a set means a distro
// outside the tested range degrades to fewer matches rather than to none.
//
// Hibernation is included: a hibernated machine is just as unable to run a
// cron job as a suspended one, which is the only property the risk estimate
// cares about.
var suspendEnterMarkers = []string{
	"PM: suspend entry",
	"PM: hibernation entry",
	"Suspending system",
	"Entering sleep state",
	"Performing sleep operation",
}

var suspendResumeMarkers = []string{
	"PM: suspend exit",
	"PM: hibernation exit",
	"System resumed",
	"System returned from sleep state",
	"System returned from sleep operation",
}

// journalEvent is one suspend or resume marker recovered from the journal.
type journalEvent struct {
	at     time.Time
	resume bool
}

// SleepHistory reconstructs the machine's recent sleep periods from the
// systemd journal.
//
// Unlike macOS, where `pmset -g log` records each sleep's duration on the line
// that starts it, Linux only logs the two edges, so intervals have to be built
// by pairing a suspend against the resume that follows it.
//
// A machine with no journal (a non-systemd distro, a container, or a user
// without journal read access) yields an empty history rather than an error.
// That is deliberate: the daemon records its own observed sleep intervals as
// it runs, and an empty history simply means risk estimates report themselves
// as not confident until enough of those accumulate. Reporting an error here
// would turn a normal, recoverable condition into a startup failure. The user
// still sees the consequence, because MissRisk.Confident stays false and the
// UI renders that as "unknown" rather than as "no risk".
func (p *linuxPlatform) SleepHistory(lookback time.Duration) (*schedule.SleepHistory, error) {
	cutoff := time.Now().Add(-lookback)

	if _, err := exec.LookPath("journalctl"); err != nil {
		return &schedule.SleepHistory{Since: cutoff}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), journalTimeout)
	defer cancel()

	var events []journalEvent
	earliest := time.Time{}
	for _, args := range journalQueries(cutoff) {
		evs, first := collectJournalEvents(ctx, args)
		events = append(events, evs...)
		if !first.IsZero() && (earliest.IsZero() || first.Before(earliest)) {
			earliest = first
		}
	}

	var intervals []schedule.SleepInterval
	for _, iv := range buildSleepIntervals(events) {
		if iv.Sleep.Before(cutoff) {
			continue
		}
		intervals = append(intervals, iv)
	}

	// Never claim to cover a window the journal does not reach back to: a
	// three-day-old journal must not let a risk estimate speak for two weeks.
	if earliest.IsZero() || earliest.Before(cutoff) {
		earliest = cutoff
	}

	merged := schedule.MergeIntervals(intervals)
	merged = schedule.CoalesceShortGaps(merged, schedule.DarkWakeGap)

	return &schedule.SleepHistory{Intervals: merged, Since: earliest}, nil
}

// journalQueries returns the journalctl invocations to run, as separate
// commands.
//
// They cannot be combined. journalctl ANDs matches on different fields, so
// `-k -u systemd-logind.service` asks for entries that are simultaneously
// kernel messages and logind unit messages, which is nothing. Running both and
// merging the results also means a machine where one source is unreadable
// (kernel messages need journal group membership on many distros) still gets
// whatever the other source provides.
func journalQueries(since time.Time) [][]string {
	stamp := since.Format(journalSinceLayout)
	base := []string{"--since=" + stamp, "--output=short-iso", "--no-pager"}

	kernel := append(append([]string{}, base...), "-k")
	units := append(append([]string{}, base...),
		"-u", "systemd-logind.service",
		"-u", "systemd-suspend.service",
		"-u", "systemd-hibernate.service",
		"-u", "systemd-suspend-then-hibernate.service")

	return [][]string{kernel, units}
}

// collectJournalEvents runs one journalctl query and returns the sleep events
// it yielded plus the earliest timestamp seen on any line, which is how far
// back this query's data actually reaches.
//
// Failures are reported as no data rather than propagated. Every way this can
// fail (journalctl missing the units, the journal being unreadable by this
// user, a non-persistent journal wiped at boot) is a normal configuration,
// not a fault, and SleepHistory's contract is to degrade to "unknown".
func collectJournalEvents(ctx context.Context, args []string) ([]journalEvent, time.Time) {
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, time.Time{}
	}
	if err := cmd.Start(); err != nil {
		return nil, time.Time{}
	}
	defer func() {
		_ = stdout.Close()
		_ = cmd.Wait()
	}()

	var events []journalEvent
	earliest := time.Time{}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		ev, ts, ok := parseJournalLine(sc.Text())
		if !ts.IsZero() && (earliest.IsZero() || ts.Before(earliest)) {
			earliest = ts
		}
		if ok {
			events = append(events, ev)
		}
	}
	return events, earliest
}

// parseJournalLine extracts a suspend or resume marker from one short-iso
// journal line, for example:
//
//	2026-07-28T23:11:04+0800 thinkpad kernel: PM: suspend entry (deep)
//	2026-07-29T07:02:19+0800 thinkpad systemd-sleep[9142]: System returned from sleep operation 'suspend'.
//
// The line's timestamp is returned separately, and is valid even when the line
// is not an event, so the caller can measure how far back the journal reaches
// from lines it otherwise ignores.
func parseJournalLine(line string) (journalEvent, time.Time, bool) {
	sp := strings.IndexByte(line, ' ')
	if sp <= 0 {
		return journalEvent{}, time.Time{}, false
	}
	ts, ok := parseJournalTime(line[:sp])
	if !ok {
		// Journal separators such as "-- Boot 8f3c… --" and "-- No entries --"
		// land here and are correctly ignored.
		return journalEvent{}, time.Time{}, false
	}

	// Everything up to the first ": " is "hostname tag[pid]", which no message
	// text can contain. Stripping it means a line that merely quotes another
	// message cannot be misread as the event itself.
	msg := line[sp+1:]
	if i := strings.Index(msg, ": "); i >= 0 {
		msg = msg[i+2:]
	}

	// Resume markers are checked first only for symmetry; the two sets share
	// no substring, so the order does not affect the result.
	if containsAny(msg, suspendResumeMarkers) {
		return journalEvent{at: ts, resume: true}, ts, true
	}
	if containsAny(msg, suspendEnterMarkers) {
		return journalEvent{at: ts}, ts, true
	}
	return journalEvent{}, ts, false
}

func containsAny(s string, markers []string) bool {
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// parseJournalTime accepts the several shapes journalctl's ISO timestamps take.
func parseJournalTime(field string) (time.Time, bool) {
	for _, layout := range journalTimeLayouts {
		if t, err := time.Parse(layout, field); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// buildSleepIntervals pairs suspend markers with the resume markers that
// follow them.
//
// Duplicate edges are expected rather than exceptional: one suspend produces
// both a kernel "PM: suspend entry" and a systemd-sleep message a fraction of
// a second apart, and both queries observe it. A marker that arrives while the
// machine is already recorded as asleep (or a resume with nothing open) is
// therefore ignored rather than treated as a new period.
//
// A suspend with no matching resume is dropped. It is tempting to close it at
// the next log line instead, but the case that produces a dangling suspend is
// a machine that failed to resume and was power-cycled, and closing it at the
// following line would then record every hour until the reboot as sleep. That
// error runs in the dangerous direction: it inflates every risk estimate after
// it, making goguma claim jobs are being missed when they are not. Losing
// one interval is the cheaper mistake, and the daemon's own recorded intervals
// cover the gap.
func buildSleepIntervals(events []journalEvent) []schedule.SleepInterval {
	if len(events) == 0 {
		return nil
	}
	sorted := make([]journalEvent, len(events))
	copy(sorted, events)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].at.Before(sorted[j].at) })

	var out []schedule.SleepInterval
	var open time.Time
	for _, ev := range sorted {
		if ev.resume {
			if open.IsZero() {
				// The suspend that this resume closes predates the window.
				continue
			}
			if ev.at.After(open) {
				out = append(out, schedule.SleepInterval{Sleep: open, Wake: ev.at})
			}
			open = time.Time{}
			continue
		}
		if open.IsZero() {
			open = ev.at
		}
	}
	return out
}
