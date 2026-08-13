package power

import (
	"testing"
	"time"
)

// TestOnDeviceProbe is a diagnostic rather than an assertion-heavy unit test.
// The PRD is explicit that the platform primitives must be verified on real
// hardware rather than assumed to work, and the two that can silently degrade
// are SMC temperature (key names differ across Mac generations) and sleep-log
// parsing (the format is undocumented). This prints what the current machine
// actually reports, so a regression shows up in test output rather than as a
// safety valve that quietly stopped working.
//
// Skipped under -short so CI on hardware without these sensors stays green.
func TestOnDeviceProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("on-device probe skipped in short mode")
	}
	p := New()

	st, err := p.ReadState()
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	t.Logf("lid_closed=%v on_ac=%v battery=%d%%", st.LidClosed, st.OnAC, st.BatteryPct)
	if st.TempC != nil {
		t.Logf("temperature: %.1f°C via %s", *st.TempC, st.TempSource)
		if !plausibleTemp(*st.TempC) {
			t.Errorf("temperature %.1f°C is outside the plausible range", *st.TempC)
		}
	} else {
		t.Logf("temperature: unavailable; thermal cutout will report unverifiable")
	}

	h, err := p.SleepHistory(14 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("SleepHistory: %v", err)
	}
	var asleep time.Duration
	for _, iv := range h.Intervals {
		if !iv.Wake.IsZero() {
			asleep += iv.Wake.Sub(iv.Sleep)
			if iv.Wake.Before(iv.Sleep) {
				t.Errorf("interval ends before it starts: %s -> %s", iv.Sleep, iv.Wake)
			}
		}
	}
	coverage := h.Coverage(time.Now())
	pct := 0.0
	if coverage > 0 {
		pct = 100 * asleep.Seconds() / coverage.Seconds()
	}
	t.Logf("sleep history: %d intervals over %s, %s asleep (%.0f%% of the window)",
		len(h.Intervals), coverage.Round(time.Hour), asleep.Round(time.Minute), pct)

	if asleep > coverage && coverage > 0 {
		t.Errorf("recorded sleep %s exceeds the %s window; intervals overlap",
			asleep.Round(time.Minute), coverage.Round(time.Hour))
	}
}

func TestParseBatt(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantPct int
		wantAC  bool
		wantOK  bool
	}{
		{
			name:    "on battery",
			in:      "Now drawing from 'Battery Power'\n -InternalBattery-0 (id=23527523)\t78%; discharging; 5:10 remaining present: true\n",
			wantPct: 78, wantAC: false, wantOK: true,
		},
		{
			name:    "on AC charging",
			in:      "Now drawing from 'AC Power'\n -InternalBattery-0 (id=123)\t100%; charged; 0:00 remaining present: true\n",
			wantPct: 100, wantAC: true, wantOK: true,
		},
		{
			name:    "desktop with no battery",
			in:      "Now drawing from 'AC Power'\n",
			wantPct: 0, wantAC: true, wantOK: false,
		},
		{
			name:    "single digit charge",
			in:      "Now drawing from 'Battery Power'\n -InternalBattery-0 (id=1)\t7%; discharging; 0:12 remaining present: true\n",
			wantPct: 7, wantAC: false, wantOK: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pct, ac, ok := parseBatt(tc.in)
			if ok != tc.wantOK || ac != tc.wantAC || (ok && pct != tc.wantPct) {
				t.Errorf("parseBatt() = (%d, %v, %v), want (%d, %v, %v)",
					pct, ac, ok, tc.wantPct, tc.wantAC, tc.wantOK)
			}
		})
	}
}

func TestParseSleepLine(t *testing.T) {
	// Real lines captured from `pmset -g log` on macOS 26.5.
	const sleepLine = "2026-07-28 19:24:15 +0800 Sleep               \tEntering Sleep state due to 'Sleep Service Back to Sleep':TCPKeepAlive=active Using Batt (Charge:45%) 1071 secs "
	iv, _, ok := parseSleepLine(sleepLine)
	if !ok {
		t.Fatal("expected a Sleep line to parse")
	}
	if got := iv.Wake.Sub(iv.Sleep); got != 1071*time.Second {
		t.Errorf("interval length = %s, want 1071s", got)
	}

	// A DarkWake line must not be read as a sleep, or every sleep period
	// would be split by its own maintenance wakes.
	const darkWake = "2026-07-28 19:24:08 +0800 DarkWake            \tDarkWake from Deep Idle [CDNP] : due to rtc/SleepService Using BATT (Charge:45%) 2 secs    "
	if _, _, ok := parseSleepLine(darkWake); ok {
		t.Error("DarkWake line was misparsed as a Sleep interval")
	}

	// Lines that merely mention sleep in another column must be ignored.
	const acks = "2026-07-28 19:24:12 +0800 PM Client Acks      \tDelays to Sleep notifications: [mDNSResponder is slow(2851 ms)] "
	if _, _, ok := parseSleepLine(acks); ok {
		t.Error("PM Client Acks line was misparsed as a Sleep interval")
	}
}
