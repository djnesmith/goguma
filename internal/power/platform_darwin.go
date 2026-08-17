package power

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <stdlib.h>
#include <IOKit/IOKitLib.h>
#include <IOKit/pwr_mgt/IOPMLib.h>
#include <CoreFoundation/CoreFoundation.h>

// wg_assert_idle takes a PreventUserIdleSystemSleep assertion. This is the
// unprivileged half of sleep blocking: it holds off idle sleep on a lid-open
// machine, and the kernel releases it automatically if this process dies, so
// it can never be stranded. It does NOT survive a lid close (Apple's own
// header says "the system may still sleep for lid close"), which is why the
// privileged helper exists.
int wg_assert_idle(const char *reason, unsigned int *outID) {
    CFStringRef r = CFStringCreateWithCString(kCFAllocatorDefault, reason, kCFStringEncodingUTF8);
    if (r == NULL) return -1;
    IOPMAssertionID id = 0;
    IOReturn rc = IOPMAssertionCreateWithName(
        kIOPMAssertPreventUserIdleSystemSleep, kIOPMAssertionLevelOn, r, &id);
    CFRelease(r);
    if (rc != kIOReturnSuccess) return -2;
    *outID = (unsigned int)id;
    return 0;
}

int wg_release_assert(unsigned int id) {
    return IOPMAssertionRelease((IOPMAssertionID)id) == kIOReturnSuccess ? 0 : -1;
}

// wg_lid_closed reads AppleClamshellState from IOPMrootDomain.
// Returns 1 closed, 0 open, -1 unknown (desktop Macs have no clamshell key).
int wg_lid_closed(void) {
    io_registry_entry_t root = IORegistryEntryFromPath(0, "IOService:/IOResources/IOPMrootDomain");
    if (root == MACH_PORT_NULL) return -1;
    CFTypeRef v = IORegistryEntryCreateCFProperty(root, CFSTR("AppleClamshellState"),
                                                  kCFAllocatorDefault, 0);
    IOObjectRelease(root);
    if (v == NULL) return -1;
    int result = -1;
    if (CFGetTypeID(v) == CFBooleanGetTypeID()) {
        result = CFBooleanGetValue((CFBooleanRef)v) ? 1 : 0;
    }
    CFRelease(v);
    return result;
}
*/
import "C"

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

type darwinPlatform struct {
	mu sync.Mutex

	// Battery is read by shelling out to pmset, so it is sampled on a slower
	// cadence than the main tick. It only gates the low-battery cutout, which
	// cannot meaningfully change between 10-second samples, and a subprocess
	// on every tick for the life of the daemon is real waste for no gain.
	battAt  time.Time
	battPct int
	// pmset -g therm, cached on the same cadence as battery for the same
	// reason.
	thermAt   time.Time
	thermWarn bool
	battOnAC  bool
}

// New returns the platform implementation for this OS.
func New() Platform { return &darwinPlatform{} }

func (p *darwinPlatform) Name() string { return "darwin" }

// SupportsClamshellHold is true, but only through the privileged helper;
// this package cannot do it alone.
func (p *darwinPlatform) SupportsClamshellHold() bool { return true }

func (p *darwinPlatform) WakeScheduleSupported() (bool, string) {
	// pmset is present on every macOS install; scheduling itself needs root
	// and is performed by the helper.
	if _, err := exec.LookPath("pmset"); err != nil {
		return false, "pmset not found on PATH"
	}
	return true, ""
}

type darwinAssertion struct {
	id       C.uint
	released bool
	mu       sync.Mutex
}

func (a *darwinAssertion) Release() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.released {
		return nil // idempotent: the daemon may release on both exit and cutout
	}
	a.released = true
	if rc := C.wg_release_assert(a.id); rc != 0 {
		return fmt.Errorf("IOPMAssertionRelease failed for assertion %d", uint32(a.id))
	}
	return nil
}

func (p *darwinPlatform) HoldIdleSleep(reason string) (IdleAssertion, error) {
	cr := C.CString(reason)
	defer C.free(unsafe.Pointer(cr))

	var id C.uint
	if rc := C.wg_assert_idle(cr, &id); rc != 0 {
		return nil, fmt.Errorf("IOPMAssertionCreateWithName failed (code %d)", int(rc))
	}
	return &darwinAssertion{id: id}, nil
}

func (p *darwinPlatform) ReadState() (State, error) {
	st := State{}

	switch C.wg_lid_closed() {
	case 1:
		st.LidClosed = true
	case 0:
		st.LidClosed = false
	default:
		// No clamshell sensor: a desktop, or an external-display setup where
		// the key is absent. Lid-closed is the risky state, so reporting
		// "open" here is the safe default; it only means the lid-gated
		// cutouts stay armed rather than being skipped.
		st.LidClosed = false
	}

	if t, src, ok := readSMCTemp(); ok {
		st.TempC = &t
		st.TempSource = src
	}
	st.ThermalWarn = p.thermalWarning()

	pct, onAC, ok := p.battery()
	if ok {
		st.BatteryPct = pct
		st.OnAC = onAC
	} else {
		// Unknown power source: assume AC. The low-battery cutout exists to
		// stop a battery draining to hard shutdown; with no battery reading
		// there is nothing to protect, and assuming "on battery at 0%" would
		// force-release every hold forever.
		st.OnAC = true
		st.BatteryPct = -1
	}
	return st, nil
}

// battery parses `pmset -g batt`, cached for 30s.
func (p *darwinPlatform) battery() (pct int, onAC bool, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Shorter than the daemon's tick, so every tick sees the real state.
	//
	// This was 30 seconds, which is three ticks: unplugging the charger left
	// goguma believing it was still on AC for up to 40 seconds, so a wake that
	// should have been withheld stayed scheduled and the popover kept saying
	// everything was fine. Plugging in had the same lag in reverse. The user
	// is standing there watching it, and a power source is exactly the input
	// they expect to be reflected at once.
	//
	// The cache is still worth having: `pmset -g batt` is a subprocess, and
	// several callers read state within one tick. At 5s each tick spawns it
	// once, which is about 20ms a tick.
	if time.Since(p.battAt) < 5*time.Second && !p.battAt.IsZero() {
		return p.battPct, p.battOnAC, p.battPct >= 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pmset", "-g", "batt").Output()
	if err != nil {
		return 0, true, false
	}
	pct, onAC, ok = parseBatt(string(out))
	p.battAt = time.Now()
	if ok {
		p.battPct, p.battOnAC = pct, onAC
	} else {
		p.battPct = -1
	}
	return pct, onAC, ok
}

// parseBatt reads the two facts we need out of pmset's battery report:
//
//	Now drawing from 'Battery Power'
//	 -InternalBattery-0 (id=23527523)	78%; discharging; 5:10 remaining present: true
func parseBatt(out string) (pct int, onAC bool, ok bool) {
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "Now drawing from") {
			onAC = strings.Contains(line, "AC Power")
			continue
		}
		i := strings.Index(line, "%")
		if i <= 0 {
			continue
		}
		// Walk back over the digits preceding the percent sign.
		j := i
		for j > 0 && line[j-1] >= '0' && line[j-1] <= '9' {
			j--
		}
		if j == i {
			continue
		}
		n, err := strconv.Atoi(line[j:i])
		if err != nil || n < 0 || n > 100 {
			continue
		}
		return n, onAC, true
	}
	return 0, onAC, false
}

// Close releases cached OS handles.
func Close() { closeSMC() }

// thermalWarning reads the OS's own thermal verdict, cached for 30s.
//
// `pmset -g therm` prints "Note: No thermal warning level has been recorded"
// on a cool machine; under thermal pressure it records a warning level and,
// on throttling machines, a CPU_Speed_Limit under 100. Either is Apple
// saying the machine is hot, independent of which SMC key goguma found.
func (p *darwinPlatform) thermalWarning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.thermAt.IsZero() && time.Since(p.thermAt) < 30*time.Second {
		return p.thermWarn
	}
	p.thermAt, p.thermWarn = time.Now(), false

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pmset", "-g", "therm").Output()
	if err != nil {
		return false
	}
	p.thermWarn = parseTherm(string(out))
	return p.thermWarn
}

func parseTherm(out string) bool {
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "Thermal Warning Level"); ok {
			rest = strings.TrimLeft(rest, " =:")
			if n, err := strconv.Atoi(strings.Fields(rest + " x")[0]); err == nil && n > 0 {
				return true
			}
		}
		if strings.HasPrefix(line, "CPU_Speed_Limit") {
			if i := strings.LastIndexAny(line, "= "); i >= 0 {
				if n, err := strconv.Atoi(strings.TrimSpace(line[i+1:])); err == nil && n < 100 {
					return true
				}
			}
		}
	}
	return false
}

// SleepNow puts the Mac to sleep the same way the Apple menu does.
//
// `pmset sleepnow` rather than IOPMSleepSystem: the IOKit call needs root, and
// the whole point of keeping this out of the helper is that goguma's
// privileged surface stays at the five messages SECURITY.md lists. The command
// overrides idle-sleep assertions, which is exactly what is needed here, since
// the assertions in the way are cloudd's `SystemIsActive` rather than anything
// claiming the machine must stay up.
func (p *darwinPlatform) SleepNow() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pmset", "sleepnow").CombinedOutput()
	if err != nil {
		return fmt.Errorf("pmset sleepnow: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// UserIdle reads HIDIdleTime, which is nanoseconds since the last keyboard or
// pointer event.
//
// Read on demand rather than sampled every tick: `ioreg` walks a device tree
// and ReadState is documented as having to stay cheap.
func (p *darwinPlatform) UserIdle() (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ioreg", "-c", "IOHIDSystem", "-d", "4").Output()
	if err != nil {
		return 0, fmt.Errorf("reading HIDIdleTime: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "HIDIdleTime") {
			continue
		}
		i := strings.LastIndex(line, "=")
		if i < 0 {
			continue
		}
		ns, err := strconv.ParseInt(strings.TrimSpace(line[i+1:]), 10, 64)
		if err != nil {
			continue
		}
		return time.Duration(ns), nil
	}
	// Not found is "cannot tell", never "nobody is there".
	return 0, fmt.Errorf("HIDIdleTime not present in ioreg output")
}

// waketimeRe pulls the seconds out of `kern.waketime`, which prints as
// "{ sec = 1786951887, usec = 427662 } Mon Aug 17 15:31:27 2026".
var waketimeRe = regexp.MustCompile(`sec\s*=\s*(\d+)`)

// LastWakeAt reads kern.waketime, the kernel's own record of the last wake.
func (p *darwinPlatform) LastWakeAt() (time.Time, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sysctl", "-n", "kern.waketime").Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("reading kern.waketime: %w", err)
	}
	m := waketimeRe.FindSubmatch(out)
	if m == nil {
		return time.Time{}, fmt.Errorf("kern.waketime not in the expected form: %q",
			strings.TrimSpace(string(out)))
	}
	sec, err := strconv.ParseInt(string(m[1]), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("kern.waketime seconds unparseable: %w", err)
	}
	// Zero means the machine has not slept since boot, which is a real answer
	// and not an error: nothing has woken, so nothing goguma did woke it.
	if sec == 0 {
		return time.Time{}, nil
	}
	return time.Unix(sec, 0), nil
}

// PowerOnRunsJobs is false on a FileVault machine.
//
// `pmset schedule poweron` genuinely powers the Mac on. What it cannot do is
// get past the unlock screen: with FileVault enabled the volume is still
// locked, macOS has not finished booting, and neither cron nor launchd exists
// yet to run anything. The machine lights up, waits for a password, and the
// job is missed exactly as it would have been while asleep, except now the
// battery has paid for a boot.
//
// FileVault is on by default on current Macs, so this is the common case
// rather than the exotic one.
func (p *darwinPlatform) PowerOnRunsJobs() (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "fdesetup", "status").Output()
	if err != nil {
		// Unknown. Claiming it works would be the damaging direction, so this
		// says nothing rather than reassuring.
		return true, ""
	}
	if strings.Contains(string(out), "FileVault is On") {
		return false, "FileVault is on, so a machine that is powered on stops at the " +
			"unlock screen and no job runs until someone types the password"
	}
	// Even unlocked, a per-user LaunchAgent waits for a login. cron and
	// LaunchDaemons do run, so this is a qualified yes rather than a no.
	return true, ""
}
