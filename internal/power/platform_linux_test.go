package power

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mustParseJournalTS(t *testing.T, s string) time.Time {
	t.Helper()
	ts, ok := parseJournalTime(s)
	if !ok {
		t.Fatalf("test fixture timestamp %q does not parse", s)
	}
	return ts
}

// TestParseJournalLine works from lines in the shapes journalctl actually
// emits. The wording of the systemd-sleep messages changed twice across
// releases, so a parser that only knows one of them would silently reconstruct
// no sleep at all on half the distros in use.
func TestParseJournalLine(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantEvent  bool
		wantResume bool
		wantTS     string
	}{
		{
			name:      "kernel suspend entry",
			line:      "2026-07-28T23:11:04+0800 thinkpad kernel: PM: suspend entry (deep)",
			wantEvent: true, wantResume: false, wantTS: "2026-07-28T23:11:04+0800",
		},
		{
			name:      "kernel suspend entry s2idle",
			line:      "2026-07-28T23:11:04+0800 thinkpad kernel: PM: suspend entry (s2idle)",
			wantEvent: true, wantResume: false, wantTS: "2026-07-28T23:11:04+0800",
		},
		{
			name:      "kernel suspend exit",
			line:      "2026-07-29T07:02:19+0800 thinkpad kernel: PM: suspend exit",
			wantEvent: true, wantResume: true, wantTS: "2026-07-29T07:02:19+0800",
		},
		{
			name:      "systemd 255 sleep operation",
			line:      "2026-07-28T23:11:03+0800 thinkpad systemd-sleep[9142]: Performing sleep operation 'suspend'...",
			wantEvent: true, wantResume: false, wantTS: "2026-07-28T23:11:03+0800",
		},
		{
			name:      "systemd 255 return from sleep operation",
			line:      "2026-07-29T07:02:20+0800 thinkpad systemd-sleep[9142]: System returned from sleep operation 'suspend'.",
			wantEvent: true, wantResume: true, wantTS: "2026-07-29T07:02:20+0800",
		},
		{
			name:      "systemd 248 sleep state",
			line:      "2026-07-28T23:11:03+0800 thinkpad systemd-sleep[912]: Entering sleep state 'suspend'...",
			wantEvent: true, wantResume: false, wantTS: "2026-07-28T23:11:03+0800",
		},
		{
			name:      "systemd 248 return from sleep state",
			line:      "2026-07-29T07:02:20+0800 thinkpad systemd-sleep[912]: System returned from sleep state 'suspend'.",
			wantEvent: true, wantResume: true, wantTS: "2026-07-29T07:02:20+0800",
		},
		{
			name:      "older logind suspending",
			line:      "2026-07-28T23:11:02+0800 thinkpad systemd-logind[712]: Suspending system...",
			wantEvent: true, wantResume: false, wantTS: "2026-07-28T23:11:02+0800",
		},
		{
			name:      "older systemd-sleep resumed",
			line:      "2026-07-29T07:02:20+0800 thinkpad systemd-sleep[912]: System resumed.",
			wantEvent: true, wantResume: true, wantTS: "2026-07-29T07:02:20+0800",
		},
		{
			name:      "hibernation entry",
			line:      "2026-07-28T23:11:04+0800 thinkpad kernel: PM: hibernation entry",
			wantEvent: true, wantResume: false, wantTS: "2026-07-28T23:11:04+0800",
		},
		{
			name:      "systemd 249 offset with colon",
			line:      "2026-07-29T07:02:19+08:00 thinkpad kernel: PM: suspend exit",
			wantEvent: true, wantResume: true, wantTS: "2026-07-29T07:02:19+08:00",
		},
		{
			// The ACPI preparation line precedes every suspend and mentions a
			// sleep state. Matching it would double-count the same suspend.
			name: "acpi preparation line is not an event",
			line: "2026-07-28T23:10:59+0800 thinkpad kernel: ACPI: PM: Preparing to enter system sleep state S3",
		},
		{
			name: "lid closed is not an event on its own",
			line: "2026-07-28T23:11:02+0800 thinkpad systemd-logind[712]: Lid closed.",
		},
		{
			name: "target stop is not an event",
			line: "2026-07-29T07:02:21+0800 thinkpad systemd[1]: Stopped target Sleep.",
		},
		{
			name: "journal boot separator has no timestamp",
			line: "-- Boot 3f8c9b2a4d5e4f0a9c1b2d3e4f5a6b7c --",
		},
		{
			name: "empty line",
			line: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev, ts, ok := parseJournalLine(tc.line)
			if ok != tc.wantEvent {
				t.Fatalf("parseJournalLine(%q) event = %v, want %v", tc.line, ok, tc.wantEvent)
			}
			if !tc.wantEvent {
				return
			}
			if ev.resume != tc.wantResume {
				t.Errorf("resume = %v, want %v", ev.resume, tc.wantResume)
			}
			want := mustParseJournalTS(t, tc.wantTS)
			if !ev.at.Equal(want) || !ts.Equal(want) {
				t.Errorf("timestamp = %s / %s, want %s", ev.at, ts, want)
			}
		})
	}
}

// TestParseJournalLineReportsTimestampForNonEvents guards the mechanism that
// clamps a history's Since to how far back the journal actually reaches: it
// works off the timestamps of lines that are not events.
func TestParseJournalLineReportsTimestampForNonEvents(t *testing.T) {
	const line = "2026-07-20T04:00:01+0800 thinkpad systemd[1]: Started Daily apt upgrade."
	_, ts, ok := parseJournalLine(line)
	if ok {
		t.Fatal("an ordinary log line was misread as a sleep event")
	}
	if want := mustParseJournalTS(t, "2026-07-20T04:00:01+0800"); !ts.Equal(want) {
		t.Errorf("timestamp = %s, want %s", ts, want)
	}
}

func TestBuildSleepIntervals(t *testing.T) {
	base := time.Date(2026, 7, 28, 23, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return base.Add(d) }

	t.Run("simple pair", func(t *testing.T) {
		got := buildSleepIntervals([]journalEvent{
			{at: at(0)},
			{at: at(8 * time.Hour), resume: true},
		})
		if len(got) != 1 || !got[0].Sleep.Equal(at(0)) || !got[0].Wake.Equal(at(8*time.Hour)) {
			t.Fatalf("got %+v, want one 8h interval", got)
		}
	})

	t.Run("duplicate edges from both journal queries", func(t *testing.T) {
		// One suspend produces a kernel line and a systemd-sleep line a moment
		// apart, and both queries observe both resume lines too.
		got := buildSleepIntervals([]journalEvent{
			{at: at(0)},
			{at: at(time.Second)},
			{at: at(8 * time.Hour), resume: true},
			{at: at(8*time.Hour + time.Second), resume: true},
		})
		if len(got) != 1 {
			t.Fatalf("got %d intervals, want 1: %+v", len(got), got)
		}
		if !got[0].Sleep.Equal(at(0)) || !got[0].Wake.Equal(at(8*time.Hour)) {
			t.Errorf("got %+v, want the outermost edges", got[0])
		}
	})

	t.Run("out of order input is sorted", func(t *testing.T) {
		got := buildSleepIntervals([]journalEvent{
			{at: at(8 * time.Hour), resume: true},
			{at: at(0)},
		})
		if len(got) != 1 || !got[0].Wake.Equal(at(8*time.Hour)) {
			t.Fatalf("got %+v, want one interval", got)
		}
	})

	t.Run("resume with no suspend in window is ignored", func(t *testing.T) {
		got := buildSleepIntervals([]journalEvent{
			{at: at(time.Minute), resume: true},
			{at: at(time.Hour)},
			{at: at(2 * time.Hour), resume: true},
		})
		if len(got) != 1 || !got[0].Sleep.Equal(at(time.Hour)) {
			t.Fatalf("got %+v, want only the complete pair", got)
		}
	})

	t.Run("suspend with no resume is dropped", func(t *testing.T) {
		// A machine that failed to resume and was power-cycled. Closing this
		// at any later timestamp would mark the whole gap as sleep and inflate
		// every risk estimate after it.
		got := buildSleepIntervals([]journalEvent{
			{at: at(0)},
			{at: at(2 * time.Hour), resume: true},
			{at: at(5 * time.Hour)},
		})
		if len(got) != 1 || !got[0].Wake.Equal(at(2*time.Hour)) {
			t.Fatalf("got %+v, want only the closed interval", got)
		}
	})

	t.Run("no events", func(t *testing.T) {
		if got := buildSleepIntervals(nil); len(got) != 0 {
			t.Fatalf("got %+v, want none", got)
		}
	})

	t.Run("input is not mutated", func(t *testing.T) {
		in := []journalEvent{
			{at: at(8 * time.Hour), resume: true},
			{at: at(0)},
		}
		buildSleepIntervals(in)
		if !in[0].at.Equal(at(8 * time.Hour)) {
			t.Error("buildSleepIntervals reordered its argument")
		}
	})
}

// TestSleepHistoryWithoutJournalctl pins the contract the daemon depends on: a
// machine with no journal is a normal configuration, not a failure. Returning
// an error here would turn a non-systemd distro or a user without journal
// access into a startup fault instead of a history that reports itself as
// covering nothing.
func TestSleepHistoryWithoutJournalctl(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	h, err := (&linuxPlatform{}).SleepHistory(14 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("SleepHistory with no journalctl: %v", err)
	}
	if len(h.Intervals) != 0 {
		t.Errorf("got %+v, want no intervals", h.Intervals)
	}
	if h.Since.IsZero() {
		t.Fatal("Since must be set even on an empty history, or risk estimates cannot be clamped")
	}
	if d := time.Since(h.Since); d < 13*24*time.Hour || d > 15*24*time.Hour {
		t.Errorf("Since is %s ago, want the requested ~14d lookback", d)
	}
}

// TestSleepHistoryReconstruction exercises the whole path against a stand-in
// journalctl. It is the only test that covers the two queries being merged,
// and the duplicate edges that merging produces: every real suspend is logged
// once by the kernel and once by systemd-sleep, and both queries here return
// both, so a naive pairing would report four intervals instead of two.
func TestSleepHistoryReconstruction(t *testing.T) {
	now := time.Now()
	oldest := now.Add(-72 * time.Hour)
	sleep1, wake1 := now.Add(-30*time.Hour), now.Add(-23*time.Hour)
	sleep2, wake2 := now.Add(-9*time.Hour), now.Add(-2*time.Hour)
	stamp := func(t time.Time) string { return t.Format("2006-01-02T15:04:05-0700") }

	script := fmt.Sprintf(`cat <<'END_OF_JOURNAL'
%s host systemd[1]: Started Daily apt upgrade.
%s host kernel: ACPI: PM: Preparing to enter system sleep state S3
%s host kernel: PM: suspend entry (deep)
%s host systemd-sleep[91]: Performing sleep operation 'suspend'...
%s host kernel: PM: suspend exit
%s host systemd-sleep[91]: System returned from sleep operation 'suspend'.
%s host systemd-logind[712]: Lid closed.
%s host kernel: PM: suspend entry (s2idle)
%s host kernel: PM: suspend exit
END_OF_JOURNAL`,
		stamp(oldest), stamp(sleep1.Add(-time.Second)), stamp(sleep1),
		stamp(sleep1.Add(time.Second)), stamp(wake1), stamp(wake1.Add(time.Second)),
		stamp(sleep2.Add(-2*time.Second)), stamp(sleep2), stamp(wake2))

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "journalctl"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	h, err := (&linuxPlatform{}).SleepHistory(14 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("SleepHistory: %v", err)
	}
	if len(h.Intervals) != 2 {
		t.Fatalf("got %d intervals, want 2: %+v", len(h.Intervals), h.Intervals)
	}
	for i, iv := range h.Intervals {
		if d := iv.Wake.Sub(iv.Sleep); d < 6*time.Hour+59*time.Minute || d > 7*time.Hour+time.Minute {
			t.Errorf("interval %d lasts %s, want ~7h", i, d)
		}
	}
	// Since must reflect how far the journal actually reaches, not the
	// requested lookback, or a three-day-old journal would speak for two weeks.
	if d := h.Since.Sub(oldest); d < -2*time.Second || d > 2*time.Second {
		t.Errorf("Since = %s, want the oldest journal line at %s", h.Since, oldest)
	}
}

// writeSysfs builds a fake sysfs attribute inside a test's temp directory.
func writeSysfs(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadPowerSupply(t *testing.T) {
	t.Run("laptop on battery", func(t *testing.T) {
		root := t.TempDir()
		writeSysfs(t, filepath.Join(root, "BAT0", "type"), "Battery\n")
		writeSysfs(t, filepath.Join(root, "BAT0", "capacity"), "78\n")
		writeSysfs(t, filepath.Join(root, "BAT0", "status"), "Discharging\n")
		writeSysfs(t, filepath.Join(root, "AC", "type"), "Mains\n")
		writeSysfs(t, filepath.Join(root, "AC", "online"), "0\n")

		pct, onAC, ok := readPowerSupply(root)
		if !ok || pct != 78 || onAC {
			t.Errorf("got (%d, %v, %v), want (78, false, true)", pct, onAC, ok)
		}
	})

	t.Run("laptop on mains named ADP1", func(t *testing.T) {
		root := t.TempDir()
		writeSysfs(t, filepath.Join(root, "BAT1", "type"), "Battery\n")
		writeSysfs(t, filepath.Join(root, "BAT1", "capacity"), "100\n")
		writeSysfs(t, filepath.Join(root, "ADP1", "type"), "Mains\n")
		writeSysfs(t, filepath.Join(root, "ADP1", "online"), "1\n")

		pct, onAC, ok := readPowerSupply(root)
		if !ok || pct != 100 || !onAC {
			t.Errorf("got (%d, %v, %v), want (100, true, true)", pct, onAC, ok)
		}
	})

	t.Run("adapter with no type attribute falls back to its name", func(t *testing.T) {
		root := t.TempDir()
		writeSysfs(t, filepath.Join(root, "BAT0", "type"), "Battery\n")
		writeSysfs(t, filepath.Join(root, "BAT0", "capacity"), "50\n")
		writeSysfs(t, filepath.Join(root, "ACAD", "online"), "1\n")

		_, onAC, ok := readPowerSupply(root)
		if !ok || !onAC {
			t.Errorf("got (onAC=%v, ok=%v), want both true", onAC, ok)
		}
	})

	t.Run("peripheral battery is ignored", func(t *testing.T) {
		// A flat wireless mouse must not fire the low-battery cutout on a
		// laptop that is itself nearly full.
		root := t.TempDir()
		writeSysfs(t, filepath.Join(root, "BAT0", "type"), "Battery\n")
		writeSysfs(t, filepath.Join(root, "BAT0", "capacity"), "91\n")
		writeSysfs(t, filepath.Join(root, "hid-mouse-battery", "type"), "Battery\n")
		writeSysfs(t, filepath.Join(root, "hid-mouse-battery", "scope"), "Device\n")
		writeSysfs(t, filepath.Join(root, "hid-mouse-battery", "capacity"), "4\n")

		pct, _, ok := readPowerSupply(root)
		if !ok || pct != 91 {
			t.Errorf("got (%d, ok=%v), want (91, true)", pct, ok)
		}
	})

	t.Run("two packs report the lower charge", func(t *testing.T) {
		root := t.TempDir()
		writeSysfs(t, filepath.Join(root, "BAT0", "type"), "Battery\n")
		writeSysfs(t, filepath.Join(root, "BAT0", "capacity"), "88\n")
		writeSysfs(t, filepath.Join(root, "BAT1", "type"), "Battery\n")
		writeSysfs(t, filepath.Join(root, "BAT1", "capacity"), "12\n")

		pct, _, ok := readPowerSupply(root)
		if !ok || pct != 12 {
			t.Errorf("got (%d, ok=%v), want (12, true)", pct, ok)
		}
	})

	t.Run("charging pack implies mains when no adapter is exposed", func(t *testing.T) {
		root := t.TempDir()
		writeSysfs(t, filepath.Join(root, "BAT0", "type"), "Battery\n")
		writeSysfs(t, filepath.Join(root, "BAT0", "capacity"), "40\n")
		writeSysfs(t, filepath.Join(root, "BAT0", "status"), "Charging\n")

		_, onAC, ok := readPowerSupply(root)
		if !ok || !onAC {
			t.Errorf("got (onAC=%v, ok=%v), want both true", onAC, ok)
		}
	})

	t.Run("not charging on mains stays on mains", func(t *testing.T) {
		// Charge thresholds park a full-enough pack at "Not charging" while
		// plugged in; that must not be read as running on battery.
		root := t.TempDir()
		writeSysfs(t, filepath.Join(root, "BAT0", "type"), "Battery\n")
		writeSysfs(t, filepath.Join(root, "BAT0", "capacity"), "60\n")
		writeSysfs(t, filepath.Join(root, "BAT0", "status"), "Not charging\n")
		writeSysfs(t, filepath.Join(root, "AC", "type"), "Mains\n")
		writeSysfs(t, filepath.Join(root, "AC", "online"), "1\n")

		_, onAC, ok := readPowerSupply(root)
		if !ok || !onAC {
			t.Errorf("got (onAC=%v, ok=%v), want both true", onAC, ok)
		}
	})

	t.Run("energy pair when capacity is absent", func(t *testing.T) {
		root := t.TempDir()
		writeSysfs(t, filepath.Join(root, "BAT0", "type"), "Battery\n")
		writeSysfs(t, filepath.Join(root, "BAT0", "energy_now"), "22000000\n")
		writeSysfs(t, filepath.Join(root, "BAT0", "energy_full"), "44000000\n")

		pct, _, ok := readPowerSupply(root)
		if !ok || pct != 50 {
			t.Errorf("got (%d, ok=%v), want (50, true)", pct, ok)
		}
	})

	t.Run("desktop with no battery reports unknown", func(t *testing.T) {
		root := t.TempDir()
		writeSysfs(t, filepath.Join(root, "AC", "type"), "Mains\n")
		writeSysfs(t, filepath.Join(root, "AC", "online"), "1\n")

		pct, onAC, ok := readPowerSupply(root)
		if ok {
			t.Fatalf("got ok=true (%d%%), want unknown so the caller assumes AC", pct)
		}
		if !onAC {
			t.Error("the unknown case must report AC, matching darwin's contract")
		}
	})

	t.Run("missing directory reports unknown", func(t *testing.T) {
		pct, onAC, ok := readPowerSupply(filepath.Join(t.TempDir(), "absent"))
		if ok || !onAC || pct != 0 {
			t.Errorf("got (%d, %v, %v), want (0, true, false)", pct, onAC, ok)
		}
	})
}

func TestLidClosedIn(t *testing.T) {
	root := t.TempDir()
	glob := filepath.Join(root, "*", "state")

	if _, known := lidClosedIn(glob); known {
		t.Error("an absent ACPI lid button must report unknown, not open")
	}

	writeSysfs(t, filepath.Join(root, "LID0", "state"), "state:      open\n")
	closed, known := lidClosedIn(glob)
	if !known || closed {
		t.Errorf("got (closed=%v, known=%v), want (false, true)", closed, known)
	}

	writeSysfs(t, filepath.Join(root, "LID0", "state"), "state:      closed\n")
	closed, known = lidClosedIn(glob)
	if !known || !closed {
		t.Errorf("got (closed=%v, known=%v), want (true, true)", closed, known)
	}
}

func TestReadCelsius(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		writeSysfs(t, path, content)
		return path
	}

	if v, ok := readCelsius(write("milli", "48250\n")); !ok || v < 48.2 || v > 48.3 {
		t.Errorf("millidegrees: got (%v, %v), want ~48.25", v, ok)
	}
	// A driver reporting whole degrees is unambiguous, because a value that is
	// plausible as degrees is never plausible as millidegrees.
	if v, ok := readCelsius(write("degrees", "52\n")); !ok || v != 52 {
		t.Errorf("whole degrees: got (%v, %v), want 52", v, ok)
	}
	// An unpopulated zone reads 0 and must not be handed to the thermal cutout.
	if _, ok := readCelsius(write("zero", "0\n")); ok {
		t.Error("a 0 reading was accepted; the thermal cutout would be disabled by it")
	}
	if _, ok := readCelsius(write("absurd", "2000000\n")); ok {
		t.Error("a 2000°C reading was accepted; the thermal cutout would fire constantly")
	}
	if _, ok := readCelsius(write("garbage", "n/a\n")); ok {
		t.Error("a non-numeric reading was accepted")
	}
	if _, ok := readCelsius(filepath.Join(dir, "absent")); ok {
		t.Error("a missing file was accepted")
	}
}

// TestThermalCandidatesOrdering checks that the CPU package sensor is tried
// before the chassis sensor, and that hwmon is only reached after every
// thermal zone. Getting this backwards would not fail loudly; it would just
// silently monitor the wrong part of the machine.
func TestThermalCandidatesOrdering(t *testing.T) {
	thermal := t.TempDir()
	hwmon := t.TempDir()

	writeSysfs(t, filepath.Join(thermal, "thermal_zone0", "type"), "acpitz\n")
	writeSysfs(t, filepath.Join(thermal, "thermal_zone0", "temp"), "42000\n")
	writeSysfs(t, filepath.Join(thermal, "thermal_zone1", "type"), "x86_pkg_temp\n")
	writeSysfs(t, filepath.Join(thermal, "thermal_zone1", "temp"), "55000\n")
	writeSysfs(t, filepath.Join(thermal, "thermal_zone2", "type"), "iwlwifi_1\n")
	writeSysfs(t, filepath.Join(thermal, "thermal_zone2", "temp"), "38000\n")
	writeSysfs(t, filepath.Join(hwmon, "hwmon0", "name"), "coretemp\n")
	writeSysfs(t, filepath.Join(hwmon, "hwmon0", "temp1_input"), "56000\n")

	got := thermalCandidates(thermal, hwmon)
	if len(got) != 4 {
		t.Fatalf("got %d candidates, want 4: %+v", len(got), got)
	}
	if got[0].label != "thermal_zone1:x86_pkg_temp" {
		t.Errorf("first candidate = %q, want the CPU package zone", got[0].label)
	}
	if got[1].label != "thermal_zone0:acpitz" {
		t.Errorf("second candidate = %q, want the ACPI zone", got[1].label)
	}
	if got[2].label != "thermal_zone2:iwlwifi_1" {
		t.Errorf("third candidate = %q, want the unranked zone last among zones", got[2].label)
	}
	if got[3].label != "hwmon0:coretemp" {
		t.Errorf("fourth candidate = %q, want hwmon after all zones", got[3].label)
	}
	if want := filepath.Join(thermal, "thermal_zone1", "temp"); got[0].path != want {
		t.Errorf("first candidate path = %q, want %q", got[0].path, want)
	}
}

func TestThermalCandidatesEmpty(t *testing.T) {
	dir := t.TempDir()
	if got := thermalCandidates(filepath.Join(dir, "absent"), filepath.Join(dir, "gone")); len(got) != 0 {
		t.Errorf("got %+v, want no candidates when neither class directory exists", got)
	}
}

func TestRTCWakealarmPaths(t *testing.T) {
	root := t.TempDir()
	// rtc0 has no alarm support, so no wakealarm attribute is created for it.
	writeSysfs(t, filepath.Join(root, "rtc0", "name"), "rtc-efi\n")
	writeSysfs(t, filepath.Join(root, "rtc1", "wakealarm"), "\n")

	got := rtcWakealarmPaths(root)
	if len(got) != 1 || got[0] != filepath.Join(root, "rtc1", "wakealarm") {
		t.Errorf("got %v, want only rtc1's wakealarm", got)
	}
	if got := rtcWakealarmPaths(filepath.Join(root, "absent")); len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

// One failed probe pass must be a cooldown, not a life sentence: a sensor
// that vanishes across suspend/resume comes back, and the thermal cutout
// staying blind forever is exactly backwards for a safety valve.
func TestThermalProbeRecoversAfterAFailedPass(t *testing.T) {
	oldThermal, oldHwmon := thermalRoot, hwmonRoot
	defer func() { thermalRoot, hwmonRoot = oldThermal, oldHwmon }()
	thermalRoot, hwmonRoot = t.TempDir(), t.TempDir()
	tempMu.Lock()
	tempPath, tempLabel, tempProbed, tempRecheckAt = "", "", false, time.Time{}
	tempMu.Unlock()

	if _, _, ok := readThermalTemp(); ok {
		t.Fatal("empty sysfs produced a temperature reading")
	}

	// The sensor re-appears (resume completed).
	zone := filepath.Join(thermalRoot, "thermal_zone0")
	if err := os.MkdirAll(zone, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zone, "type"), []byte("x86_pkg_temp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zone, "temp"), []byte("45000\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Still inside the cooldown: blind, by design, so a sensorless machine
	// is not re-enumerated on every tick.
	if _, _, ok := readThermalTemp(); ok {
		t.Fatal("a reading appeared inside the cooldown")
	}

	tempMu.Lock()
	tempRecheckAt = time.Now().Add(-time.Second)
	tempMu.Unlock()

	v, _, ok := readThermalTemp()
	if !ok || v != 45 {
		t.Fatalf("sensor did not recover after the cooldown: v=%v ok=%v", v, ok)
	}
}
