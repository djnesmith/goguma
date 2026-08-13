package power

import (
	"bufio"
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/junnam586/goguma/internal/schedule"
)

// pmsetLogTimeLayout matches the timestamps `pmset -g log` emits, e.g.
// "2026-07-28 19:24:10 +0800".
const pmsetLogTimeLayout = "2006-01-02 15:04:05 -0700"

// SleepHistory reconstructs the machine's recent sleep periods from
// `pmset -g log`, which retains weeks of power-management history.
//
// Each Sleep line carries the duration of the sleep it is entering as a
// trailing "N secs" field, so one line yields a complete interval without
// needing to pair it against a later wake line. Verified against real log
// output: "Sleep … 1071 secs" at 19:24:15 is followed by a wake at 19:42:06,
// exactly 1071 seconds later.
func (p *darwinPlatform) SleepHistory(lookback time.Duration) (*schedule.SleepHistory, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// `pmset -g log` can be tens of megabytes; stream it rather than buffering.
	cmd := exec.CommandContext(ctx, "pmset", "-g", "log")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		_ = stdout.Close()
		_ = cmd.Wait()
	}()

	cutoff := time.Now().Add(-lookback)
	var intervals []schedule.SleepInterval
	earliest := time.Time{}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // some lines are enormous
	for sc.Scan() {
		iv, ts, ok := parseSleepLine(sc.Text())
		// Every parseable timestamp extends coverage, not just Sleep lines:
		// a machine that stayed awake for days then slept once was otherwise
		// credited with a history no older than that one sleep, shrinking
		// Coverage until real risk read as "unknown".
		if !ts.IsZero() && (earliest.IsZero() || ts.Before(earliest)) {
			earliest = ts
		}
		// A later log line proves the machine was awake again by then. A
		// Sleep entry with no recorded duration mid-log is a sleep the
		// machine never woke from normally (battery died; powerd never
		// wrote the trailing secs); leaving it open-ended made AsleepAt
		// read "asleep forever after", swallowing every later interval and
		// scoring every schedule as ~100% missed for up to a fortnight.
		if !ts.IsZero() {
			if n := len(intervals); n > 0 && intervals[n-1].Wake.IsZero() && ts.After(intervals[n-1].Sleep) {
				intervals[n-1].Wake = ts
			}
		}
		if !ok {
			continue
		}
		if iv.Sleep.Before(cutoff) {
			if !iv.Wake.After(cutoff) {
				continue
			}
			// Straddles the window edge: keep the in-window portion. It was
			// dropped whole, and the fires inside it scored "not missed".
			iv.Sleep = cutoff
		}
		intervals = append(intervals, iv)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// Whoever is reading this log is awake, so a still-open trailing sleep
	// ends now at the latest.
	if n := len(intervals); n > 0 && intervals[n-1].Wake.IsZero() {
		intervals[n-1].Wake = time.Now()
	}

	if earliest.IsZero() || earliest.Before(cutoff) {
		earliest = cutoff
	}
	merged := schedule.MergeIntervals(intervals)
	merged = schedule.CoalesceShortGaps(merged, schedule.DarkWakeGap)

	return &schedule.SleepHistory{Intervals: merged, Since: earliest}, nil
}

// parseSleepLine extracts an interval from a pmset log Sleep entry. Returns
// the line's timestamp separately so the caller can track how far back the
// log reaches even for lines it does not use.
func parseSleepLine(line string) (schedule.SleepInterval, time.Time, bool) {
	if len(line) < len(pmsetLogTimeLayout) {
		return schedule.SleepInterval{}, time.Time{}, false
	}
	ts, err := time.Parse(pmsetLogTimeLayout, line[:len(pmsetLogTimeLayout)])
	if err != nil {
		return schedule.SleepInterval{}, time.Time{}, false
	}
	rest := strings.TrimSpace(line[len(pmsetLogTimeLayout):])

	// Match the event column exactly. "Sleep" must not also match
	// "Sleep Service" or the "PM Client Acks" lines that merely mention sleep.
	if !strings.HasPrefix(rest, "Sleep") {
		return schedule.SleepInterval{}, ts, false
	}
	after := strings.TrimPrefix(rest, "Sleep")
	if after != "" && !isSpace(after[0]) {
		return schedule.SleepInterval{}, ts, false
	}
	if !strings.Contains(after, "Entering Sleep state") {
		return schedule.SleepInterval{}, ts, false
	}

	secs, ok := trailingSecs(after)
	if !ok {
		// A sleep with no recorded duration is the one the machine is
		// currently inside, or a truncated log tail. Record it open-ended so
		// it is not silently treated as "awake".
		return schedule.SleepInterval{Sleep: ts}, ts, true
	}
	return schedule.SleepInterval{
		Sleep: ts,
		Wake:  ts.Add(time.Duration(secs) * time.Second),
	}, ts, true
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' }

// trailingSecs pulls the "N secs" duration off the end of a Sleep line.
func trailingSecs(s string) (int, bool) {
	fields := strings.Fields(s)
	for i := len(fields) - 1; i > 0; i-- {
		if fields[i] != "secs" && fields[i] != "sec" {
			continue
		}
		n, err := strconv.Atoi(fields[i-1])
		if err != nil || n < 0 {
			return 0, false
		}
		return n, true
	}
	return 0, false
}
