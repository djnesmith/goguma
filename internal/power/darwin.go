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
// it can never be stranded. It does NOT survive a lid close — Apple's own
// header says "the system may still sleep for lid close" — which is why the
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
	battAt   time.Time
	battPct  int
	battOnAC bool
}

// New returns the platform implementation for this OS.
func New() Platform { return &darwinPlatform{} }

func (p *darwinPlatform) Name() string { return "darwin" }

// SupportsClamshellHold is true, but only through the privileged helper —
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
		// "open" here is the safe default — it only means the lid-gated
		// cutouts stay armed rather than being skipped.
		st.LidClosed = false
	}

	if t, src, ok := readSMCTemp(); ok {
		st.TempC = &t
		st.TempSource = src
	}

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

	if time.Since(p.battAt) < 30*time.Second && !p.battAt.IsZero() {
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
