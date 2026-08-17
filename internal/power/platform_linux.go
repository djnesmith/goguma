package power

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sysfs and procfs roots. Named rather than inlined so the parsing helpers
// can be pointed at a fixture tree in tests; none of this code can be
// exercised on the developer's machine otherwise. The thermal pair are vars
// because readThermalTemp reads them directly and its probe lifecycle is
// tested through them.
var (
	thermalRoot = "/sys/class/thermal"
	hwmonRoot   = "/sys/class/hwmon"
)

const (
	powerSupplyRoot = "/sys/class/power_supply"
	rtcRoot         = "/sys/class/rtc"

	// lidStateGlob is the ACPI lid button's state file. The directory name in
	// the middle varies by firmware (LID, LID0, C1FB, …), hence the glob.
	lidStateGlob = "/proc/acpi/button/lid/*/state"
)

// linuxPlatform implements Platform on systemd/logind distributions.
//
// Everything here stays inside what an unprivileged user can do: reading
// sysfs and procfs, and spawning subprocesses that hold logind inhibitor
// locks. Anything needing root goes through the helper.
type linuxPlatform struct {
	mu sync.Mutex

	// Power-supply state is read from sysfs, which is cheap, but it still
	// costs a directory walk plus half a dozen file opens on every sample.
	// It only feeds the low-battery cutout, which cannot meaningfully change
	// between 10-second ticks, so it is cached on the same slower cadence the
	// darwin implementation uses for pmset.
	battAt   time.Time
	battPct  int
	battOnAC bool
}

// New returns the platform implementation for this OS.
func New() Platform { return &linuxPlatform{} }

func (p *linuxPlatform) Name() string { return "linux" }

// SupportsClamshellHold reports true, but the guarantee is weaker than the
// name suggests and weaker than on macOS, so the honest picture matters here.
//
// Linux has no clamshell-specific API. What exists is logind's inhibitor
// framework, and a *sleep* block inhibitor on its own does NOT stop a
// lid-close suspend on a stock systemd install: logind.conf ships with
// LidSwitchIgnoreInhibited=yes, which explicitly tells logind to suspend on
// lid close even while sleep inhibitors are held. The lock that does cover it
// is "handle-lid-switch", which is why the helper takes
// sleep:idle:handle-lid-switch rather than the sleep:idle the PRD sketches.
//
// Two paths remain outside our control even then: a desktop environment that
// takes over lid handling and implements its own policy (GNOME, KDE and Xfce
// all do this), and anything writing directly to /sys/power/state. The first
// usually still routes its suspend through logind's Suspend() method, which
// our sleep inhibitor does block; the second does not exist in normal use.
//
// None of this has been verified on real hardware yet; see the notes in the
// helper. Reporting true is the useful answer for the UI, but it is a claim
// about the mechanism being available, not about it having been observed to
// hold a specific laptop awake with the lid shut.
func (p *linuxPlatform) SupportsClamshellHold() bool { return true }

// WakeScheduleSupported probes rather than assumes.
//
// RTC wake is the one primitive on Linux that is genuinely hardware- and
// firmware-dependent (PRD §12.3): plenty of machines expose an RTC with no
// alarm IRQ, and plenty more accept an alarm that the firmware then declines
// to act on. This can only rule out the cases that are detectable from
// userspace: a machine that passes this check can still fail to wake, and the
// daemon's own wake verification is what catches that.
func (p *linuxPlatform) WakeScheduleSupported() (bool, string) {
	if _, err := exec.LookPath("rtcwake"); err != nil {
		return false, "rtcwake not found on PATH (install util-linux)"
	}
	alarms := rtcWakealarmPaths(rtcRoot)
	if len(alarms) == 0 {
		return false, "no RTC device exposes a wakealarm attribute; this machine's RTC has no alarm support"
	}
	for _, path := range alarms {
		if _, err := os.ReadFile(path); err == nil {
			return true, ""
		}
	}
	return false, fmt.Sprintf("%s exists but is not readable; the daemon cannot verify scheduled wakes", alarms[0])
}

// rtcWakealarmPaths lists the RTC devices that advertise an alarm. The
// wakealarm attribute is only created when the driver supports alarms, so its
// presence is the probe.
func rtcWakealarmPaths(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "rtc") {
			continue
		}
		path := filepath.Join(root, e.Name(), "wakealarm")
		if _, err := os.Stat(path); err == nil {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

// inhibitWho tags our logind locks. `systemd-inhibit --list` prints this
// verbatim, so a user debugging "why won't my laptop sleep" sees who to blame.
const inhibitWho = "goguma"

// inhibitStartGrace is how long HoldIdleSleep waits to see whether the
// inhibitor process survives its own startup.
//
// systemd-inhibit reports a refused lock (no logind, no D-Bus, polkit denial)
// by exiting, not by failing to spawn. Without this pause the daemon would
// believe it holds a lock that never existed, which is the one failure mode
// that must not be silent: it would let the machine sleep through a job while
// reporting everything as fine. A few hundred milliseconds is paid once per
// job window, not per tick.
//
// It is a best-effort check, not a guarantee. On a heavily loaded machine the
// child can outlive this window and still be gone moments later, so Release is
// the backstop that reports a lock which lapsed at any point during the hold.
const inhibitStartGrace = 300 * time.Millisecond

// inhibitStopTimeout bounds how long Release waits for the child to notice its
// keepalive pipe closed before escalating to SIGKILL.
const inhibitStopTimeout = 5 * time.Second

// linuxAssertion is a held logind inhibitor lock.
//
// The lock is owned by a child `systemd-inhibit` process rather than by this
// one. That is a deliberate tradeoff and it is worth being clear about the
// cost: logind's Inhibit() method hands the lock back as a file descriptor
// over D-Bus, so taking it in-process means implementing D-Bus (SASL
// handshake, type-signature marshalling and SCM_RIGHTS fd passing), which is
// several hundred lines of wire protocol that cannot be added here without a
// dependency, and goguma has committed to none beyond cron and x/sys.
//
// The subprocess buys back the property that actually matters. The child's
// stdin is a pipe whose write end this process holds, and the child is `cat`,
// which exits the moment that pipe reaches EOF. Closing it, or dying for any
// reason including SIGKILL, closes the fd and the kernel tears the whole chain
// down: cat exits, systemd-inhibit exits, logind drops the lock. So the lock
// cannot be stranded by a crashed daemon, which is the same guarantee macOS
// gives IOPMAssertion holders and the reason the helper's dead-man switch is
// not needed for this half of the design.
type linuxAssertion struct {
	mu       sync.Mutex
	released bool

	cmd  *exec.Cmd
	hold *os.File      // write end of the keepalive pipe
	done chan struct{} // closed once the child has been reaped
	err  error         // the child's exit status; read only after done is closed
}

var (
	holdsMu   sync.Mutex
	liveHolds = map[*linuxAssertion]struct{}{}
)

// HoldIdleSleep takes an unprivileged sleep:idle block inhibitor.
//
// "idle" is the lock that matches this method's contract; "sleep" is included
// because it also blocks the Suspend() call a desktop environment makes when
// it decides the session is idle, which "idle" alone does not. Neither covers
// lid close (see SupportsClamshellHold), which is exactly the split the
// Platform interface documents.
func (p *linuxPlatform) HoldIdleSleep(reason string) (IdleAssertion, error) {
	if _, err := exec.LookPath("systemd-inhibit"); err != nil {
		return nil, fmt.Errorf("systemd-inhibit not found on PATH: %w", err)
	}
	if strings.TrimSpace(reason) == "" {
		reason = "goguma is holding the machine awake for a scheduled job"
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("creating inhibitor keepalive pipe: %w", err)
	}

	// Deliberately not exec.CommandContext: this process is meant to outlive
	// the call that starts it. Its lifetime is bounded by the keepalive pipe
	// instead, which is a stronger bound than a context because it survives
	// this process being killed.
	cmd := exec.Command("systemd-inhibit",
		"--what=sleep:idle",
		"--who="+inhibitWho,
		"--why="+reason,
		"--mode=block",
		"cat")
	cmd.Stdin = pr

	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return nil, fmt.Errorf("starting systemd-inhibit: %w", err)
	}
	// Only the child needs the read end; keeping it open here would mean the
	// pipe never reaches EOF and the child would outlive us.
	_ = pr.Close()

	a := &linuxAssertion{cmd: cmd, hold: pw, done: make(chan struct{})}
	go func() {
		a.err = cmd.Wait()
		close(a.done)
	}()

	select {
	case <-a.done:
		_ = pw.Close()
		return nil, fmt.Errorf("systemd-inhibit exited immediately, so no lock was taken: %s", exitDetail(a.err))
	case <-time.After(inhibitStartGrace):
	}

	holdsMu.Lock()
	liveHolds[a] = struct{}{}
	holdsMu.Unlock()
	return a, nil
}

// Release drops the lock. Idempotent: the daemon may release on both a cutout
// and its own shutdown path.
func (a *linuxAssertion) Release() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.released {
		return nil
	}
	a.released = true

	holdsMu.Lock()
	delete(liveHolds, a)
	holdsMu.Unlock()

	// A child that is already gone means the lock lapsed at some unknown point
	// during the hold window. That is reported rather than swallowed: the
	// daemon asked for sleep to be held and it was not, and a job may have
	// been slept through as a result.
	select {
	case <-a.done:
		_ = a.hold.Close()
		return fmt.Errorf("systemd-inhibit exited before the hold was released, so idle sleep was not held for the whole window: %s", exitDetail(a.err))
	default:
	}

	_ = a.hold.Close()
	select {
	case <-a.done:
		return nil
	case <-time.After(inhibitStopTimeout):
	}

	// The child ignored EOF on stdin. Killing it is safe (logind drops the
	// lock when the process dies), and leaving it alive would hold sleep off
	// indefinitely, which is the worst outcome available here.
	if err := a.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("killing unresponsive systemd-inhibit (pid %d): %w", a.cmd.Process.Pid, err)
	}
	<-a.done
	return fmt.Errorf("systemd-inhibit (pid %d) did not exit when its keepalive pipe closed; killed it", a.cmd.Process.Pid)
}

// exitDetail renders a child's exit status for an error message, including the
// case where it exited cleanly (which is itself the anomaly being reported).
func exitDetail(err error) string {
	if err == nil {
		return "exited with status 0"
	}
	return err.Error()
}

// Close releases anything this process is still holding.
//
// The keepalive pipe already guarantees the kernel cleans up if the daemon
// dies outright; this is the orderly path, so shutdown is deterministic rather
// than depending on the child noticing EOF at some later moment.
func Close() {
	holdsMu.Lock()
	holds := make([]*linuxAssertion, 0, len(liveHolds))
	for a := range liveHolds {
		holds = append(holds, a)
	}
	holdsMu.Unlock()

	for _, a := range holds {
		_ = a.Release()
	}
}

func (p *linuxPlatform) ReadState() (State, error) {
	st := State{}

	if closed, known := lidClosedIn(lidStateGlob); known {
		st.LidClosed = closed
	}
	// An undeterminable lid reports open. There is no second portable source:
	// sysfs exposes no lid attribute, logind's LidClosed property is only
	// reachable over D-Bus, and the evdev SW_LID switch needs read access to
	// /dev/input, which the unprivileged daemon does not have. Reporting open
	// is the safe direction; it keeps the lid-gated cutouts armed instead of
	// skipping them, but it also means the cutouts will never fire on a
	// machine whose lid we cannot see, so the hazard they guard against is
	// simply not detected there.

	if t, src, ok := readThermalTemp(); ok {
		st.TempC = &t
		st.TempSource = src
	}

	pct, onAC, ok := p.battery()
	if ok {
		st.BatteryPct = pct
		st.OnAC = onAC
	} else {
		// Unknown power source: assume AC, matching darwin. The low-battery
		// cutout exists to stop a battery draining to a hard shutdown; with no
		// battery reading there is nothing to protect, and reporting "on
		// battery at 0%" would force-release every hold forever.
		st.OnAC = true
		st.BatteryPct = -1
	}
	return st, nil
}

// lidClosedIn reads the ACPI lid button state, whose file contains a single
// line such as "state:      open".
//
// This interface is old and is absent on plenty of modern machines (ARM
// laptops never had it, and some x86 firmware stopped exposing it), so
// "unknown" is a common answer rather than an edge case.
func lidClosedIn(glob string) (closed, known bool) {
	matches, err := filepath.Glob(glob)
	if err != nil || len(matches) == 0 {
		return false, false
	}
	for _, path := range matches {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		s := strings.ToLower(string(b))
		switch {
		case strings.Contains(s, "closed"):
			return true, true
		case strings.Contains(s, "open"):
			return false, true
		}
	}
	return false, false
}

// battery samples the power supplies, cached for 30s.
func (p *linuxPlatform) battery() (pct int, onAC bool, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if time.Since(p.battAt) < 30*time.Second && !p.battAt.IsZero() {
		return p.battPct, p.battOnAC, p.battPct >= 0
	}

	pct, onAC, ok = readPowerSupply(powerSupplyRoot)
	p.battAt = time.Now()
	if ok {
		p.battPct, p.battOnAC = pct, onAC
	} else {
		p.battPct = -1
	}
	return pct, onAC, ok
}

// readPowerSupply walks /sys/class/power_supply and returns the machine's
// charge level and whether it is on mains power.
//
// Devices are classified by their "type" attribute rather than by name,
// because the name is firmware-chosen and varies (BAT0, BAT1, CMB0; AC, ADP1,
// ACAD). The classic name patterns are still accepted as a fallback for
// drivers that leave "type" empty.
func readPowerSupply(root string) (pct int, onAC bool, ok bool) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, true, false
	}

	pct = -1
	charging := false
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		typ := readSysfsString(filepath.Join(dir, "type"))

		switch {
		case strings.EqualFold(typ, "Battery"):
			// Wireless mice, keyboards and headsets appear here too and would
			// otherwise trip the low-battery cutout on a plugged-in laptop
			// because someone's mouse is flat. scope=Device marks them.
			if strings.EqualFold(readSysfsString(filepath.Join(dir, "scope")), "Device") {
				continue
			}
			p, got := batteryPercent(dir)
			if !got {
				continue
			}
			// With more than one pack, take the lower reading. It is the
			// conservative direction for a safety cutout: firing early on a
			// dual-battery machine costs a released hold, firing late costs a
			// hard shutdown mid-job.
			if pct < 0 || p < pct {
				pct = p
			}
			if strings.EqualFold(readSysfsString(filepath.Join(dir, "status")), "Charging") {
				charging = true
			}

		case strings.EqualFold(typ, "Mains"), isMainsName(e.Name()):
			// Only "Mains" counts. Laptops charged over USB-C sometimes expose
			// the charger as type "USB", but so does a phone plugged into a
			// USB port, and a false "on AC" would silently disable the
			// low-battery cutout. Wrongly believing we are on battery only
			// costs an early release, so that is the error to prefer.
			if v, err := readSysfsInt(filepath.Join(dir, "online")); err == nil && v == 1 {
				onAC = true
			}
		}
	}

	// Some machines expose no mains device at all. A pack that reports itself
	// as actively charging is unambiguous evidence of external power. The
	// converse is not true (ThinkPads with charge thresholds sit at "Not
	// charging" while plugged in), so only the positive case is used.
	if !onAC && charging {
		onAC = true
	}

	if pct < 0 {
		return 0, true, false
	}
	return pct, onAC, true
}

// isMainsName matches the conventional AC-adapter device names, used only when
// the driver did not populate "type".
func isMainsName(name string) bool {
	n := strings.ToUpper(name)
	return strings.HasPrefix(n, "AC") || strings.HasPrefix(n, "ADP")
}

// batteryPercent reads a pack's charge level.
//
// "capacity" is the direct answer and is present on current kernels. The
// energy_*/charge_* pairs are the fallback for drivers that report raw µWh or
// µAh without deriving a percentage, which was common enough historically that
// ignoring them would blind the cutout on older hardware.
func batteryPercent(dir string) (int, bool) {
	if n, err := readSysfsInt(filepath.Join(dir, "capacity")); err == nil {
		return clampPercent(n), true
	}
	for _, pair := range [][2]string{
		{"energy_now", "energy_full"},
		{"charge_now", "charge_full"},
	} {
		now, err := readSysfsInt(filepath.Join(dir, pair[0]))
		if err != nil {
			continue
		}
		full, err := readSysfsInt(filepath.Join(dir, pair[1]))
		if err != nil || full <= 0 {
			continue
		}
		return clampPercent(now * 100 / full), true
	}
	return 0, false
}

func clampPercent(n int64) int {
	switch {
	case n < 0:
		return 0
	case n > 100:
		// A pack can read slightly over full right after a charge cycle.
		return 100
	default:
		return int(n)
	}
}

// plausibleTemp filters out sensors that exist but report garbage. A thermal
// zone can be registered by a driver that never populates it, and feeding a 0°C
// or 2000°C reading to the thermal cutout would either disable the safety valve
// or trip it constantly.
func plausibleTemp(c float64) bool { return c > 5 && c < 120 }

// thermalZonePreference ranks thermal_zone "type" values from most to least
// representative of CPU temperature. Zones outside this list are still tried,
// just last: a machine with only a battery or wifi zone is better served by a
// distant reading than by none, and plausibleTemp still guards the value.
var thermalZonePreference = []string{
	"x86_pkg_temp", // Intel package sensor: the closest analogue to macOS TC0P
	"cpu-thermal",  // ARM SoCs (Raspberry Pi, most aarch64 laptops)
	"cpu_thermal",  // same, underscore spelling used by some device trees
	"coretemp",     // Intel per-core, exposed as a zone on some kernels
	"acpitz",       // ACPI thermal zone: universal, but often reports the chassis
}

// hwmonPreference ranks hwmon device names, used when no usable thermal zone
// exists. Ordered CPU-first for the same reason as the zone list.
var hwmonPreference = []string{"coretemp", "k10temp", "zenpower", "cpu_thermal", "acpitz"}

type tempCandidate struct {
	path  string
	label string
}

// Cached winning sensor, mirroring how the darwin implementation caches the
// SMC key that worked. Probing every zone on every tick would be pure waste on
// a machine with a dozen of them.
var (
	tempMu     sync.Mutex
	tempPath   string
	tempLabel  string
	tempProbed bool
	// tempRecheckAt backs off re-enumeration after a fully failed probe.
	//
	// A cooldown, deliberately not a permanent verdict: sensors vanish
	// transiently (sysfs mid-resume, a driver reloading), and one bad pass
	// on the first post-resume tick used to disable the thermal cutout for
	// the daemon's whole life. The valve staying blind forever over a
	// moment's flap is exactly backwards for a safety mechanism.
	tempRecheckAt time.Time
)

// tempRecheckInterval is how long a fully failed probe pass suppresses
// re-enumeration. Long enough that a genuinely sensorless machine is not
// re-scanned every tick; short enough that a resume flap heals itself.
const tempRecheckInterval = 10 * time.Minute

// readThermalTemp returns the CPU temperature in celsius and whether a reading
// was obtained. It never returns a value it does not trust: an unavailable
// sensor reports false so the caller treats thermal safety as unverifiable
// rather than assuming the machine is cool.
func readThermalTemp() (float64, string, bool) {
	tempMu.Lock()
	defer tempMu.Unlock()

	if !tempRecheckAt.IsZero() && time.Now().Before(tempRecheckAt) {
		return 0, "", false
	}
	if tempProbed && tempPath != "" {
		if v, ok := readCelsius(tempPath); ok {
			return v, tempLabel, true
		}
		// A sensor that worked and then stopped means the device was
		// hot-unplugged or the driver unloaded. Re-probe rather than silently
		// going blind for the rest of the daemon's life.
		tempProbed, tempPath, tempLabel = false, "", ""
	}

	for _, c := range thermalCandidates(thermalRoot, hwmonRoot) {
		if v, ok := readCelsius(c.path); ok {
			tempPath, tempLabel, tempProbed = c.path, c.label, true
			tempRecheckAt = time.Time{}
			return v, c.label, true
		}
	}
	tempRecheckAt = time.Now().Add(tempRecheckInterval)
	return 0, "", false
}

// thermalCandidates lists every temperature file worth trying, best first.
func thermalCandidates(thermal, hwmon string) []tempCandidate {
	var out []tempCandidate

	out = append(out, rankedCandidates(thermal, thermalZonePreference,
		func(name string) bool { return strings.HasPrefix(name, "thermal_zone") },
		"type", "temp")...)

	out = append(out, rankedCandidates(hwmon, hwmonPreference,
		func(name string) bool { return strings.HasPrefix(name, "hwmon") },
		"name", "temp1_input")...)

	return out
}

// rankedCandidates scans a sysfs class directory and orders its devices by how
// well their identifying attribute matches a preference list.
func rankedCandidates(root string, prefer []string, include func(string) bool, idAttr, valueAttr string) []tempCandidate {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	type ranked struct {
		rank int
		name string
		c    tempCandidate
	}
	var found []ranked
	for _, e := range entries {
		if !include(e.Name()) {
			continue
		}
		dir := filepath.Join(root, e.Name())
		id := readSysfsString(filepath.Join(dir, idAttr))

		rank := len(prefer)
		for i, want := range prefer {
			if strings.EqualFold(id, want) {
				rank = i
				break
			}
		}
		label := e.Name()
		if id != "" {
			label += ":" + id
		}
		found = append(found, ranked{
			rank: rank,
			name: e.Name(),
			c:    tempCandidate{path: filepath.Join(dir, valueAttr), label: label},
		})
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].rank != found[j].rank {
			return found[i].rank < found[j].rank
		}
		return found[i].name < found[j].name
	})

	out := make([]tempCandidate, 0, len(found))
	for _, f := range found {
		out = append(out, f.c)
	}
	return out
}

// readCelsius reads a sysfs temperature file.
//
// Both thermal_zone/temp and hwmon/tempN_input are specified as millidegrees,
// and that is tried first. A handful of out-of-tree drivers report whole
// degrees instead, so that is tried second, unambiguously, because the two
// plausible ranges (5000..120000 and 5..120) cannot overlap.
func readCelsius(path string) (float64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0, false
	}
	if c := float64(n) / 1000; plausibleTemp(c) {
		return c, true
	}
	if c := float64(n); plausibleTemp(c) {
		return c, true
	}
	return 0, false
}

// readSysfsString reads a sysfs attribute, returning "" when it is missing.
//
// Absence is the normal case in sysfs (an attribute file only exists when the
// driver implements it), so this deliberately does not distinguish "missing"
// from "empty". Callers treat both as "this device did not tell us".
func readSysfsString(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readSysfsInt(path string) (int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", path, err)
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return 0, fmt.Errorf("reading %s: %w", path, errors.New("attribute is empty"))
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing %s: %w", path, err)
	}
	return n, nil
}

// SleepNow suspends the machine via systemd, which is already how this
// platform inhibits and schedules.
func (p *linuxPlatform) SleepNow() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "suspend").CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl suspend: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// UserIdle has no portable source here.
//
// X11 has XScreenSaver, Wayland has nothing standard, and a headless server
// has no concept of it at all. Reporting a wrong idle time would be worse than
// reporting none: the caller treats an error as "someone may be there" and
// declines to sleep, which is the safe direction. In practice the problem this
// exists for is a macOS one, where a scheduled wake is classified as
// user-initiated and the desktop stack wakes up with it.
func (p *linuxPlatform) UserIdle() (time.Duration, error) {
	return 0, ErrUnsupported
}

// LastWakeAt has no cheap equivalent here.
//
// /sys/power has no last-wake timestamp, and the journal query that would
// answer it costs about what `pmset -g log` costs on macOS, which is the very
// thing this exists to avoid. Unsupported means the caller falls back to the
// tick-gap heuristic, which is what every platform used before.
func (p *linuxPlatform) LastWakeAt() (time.Time, error) {
	return time.Time{}, ErrUnsupported
}

// PowerOnRunsJobs: an encrypted root filesystem raises the same question here,
// but the answer depends on the initramfs and whether the key is in the TPM,
// which is not worth guessing at. Unqualified yes, and the warning stays quiet.
func (p *linuxPlatform) PowerOnRunsJobs() (bool, string) { return true, "" }
