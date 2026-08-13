package power

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <stdlib.h>
#include <string.h>
#include <mach/mach.h>
#include <IOKit/IOKitLib.h>

// Apple's SMC user-client protocol. These structures are not in any public
// header, but the layout has been stable for well over a decade and access
// requires no entitlement: the same public IOKit path `powermetrics` and
// every Mac temperature utility uses.

typedef struct { char major; char minor; char build; char reserved[1]; unsigned short release; } SMCVers;
typedef struct { unsigned short version; unsigned short length; unsigned int cpuPLimit, gpuPLimit, memPLimit; } SMCPLimit;
typedef struct { unsigned int dataSize; unsigned int dataType; char dataAttributes; } SMCKeyInfo;

typedef struct {
    unsigned int  key;
    SMCVers       vers;
    SMCPLimit     pLimit;
    SMCKeyInfo    keyInfo;
    char          result;
    char          status;
    char          data8;
    unsigned int  data32;
    unsigned char bytes[32];
} SMCParam;

enum { kSMCUserClientOpen = 0, kSMCHandleYPCEvent = 2 };
enum { kSMCCmdReadBytes = 5, kSMCCmdReadKeyInfo = 9 };

static io_connect_t wg_smc_conn = 0;

// wg_smc_open connects to AppleSMC. Returns 0 on success.
int wg_smc_open(void) {
    if (wg_smc_conn != 0) return 0;
    io_service_t svc = IOServiceGetMatchingService(0, IOServiceMatching("AppleSMC"));
    if (svc == 0) return -1;
    kern_return_t rc = IOServiceOpen(svc, mach_task_self(), 0, &wg_smc_conn);
    IOObjectRelease(svc);
    if (rc != KERN_SUCCESS) { wg_smc_conn = 0; return -2; }
    return 0;
}

void wg_smc_close(void) {
    if (wg_smc_conn != 0) { IOServiceClose(wg_smc_conn); wg_smc_conn = 0; }
}

static kern_return_t wg_smc_call(SMCParam *in, SMCParam *out) {
    size_t outSize = sizeof(SMCParam);
    return IOConnectCallStructMethod(wg_smc_conn, kSMCHandleYPCEvent,
                                     in, sizeof(SMCParam), out, &outSize);
}

// wg_smc_read_temp reads a 4-character SMC key and converts it to celsius.
// Handles the two encodings that carry temperature: sp78 (Intel, signed
// 8.8 fixed point) and flt (Apple silicon, little-endian float32).
//
// Returns 0 on success and writes to *out; negative on failure.
int wg_smc_read_temp(const char *keyStr, double *out) {
    if (wg_smc_conn == 0 && wg_smc_open() != 0) return -1;

    unsigned int key = ((unsigned int)keyStr[0] << 24) | ((unsigned int)keyStr[1] << 16) |
                       ((unsigned int)keyStr[2] << 8)  | ((unsigned int)keyStr[3]);

    SMCParam in, res;
    memset(&in, 0, sizeof(in));
    memset(&res, 0, sizeof(res));

    // Step 1: learn the key's size and type.
    in.key  = key;
    in.data8 = kSMCCmdReadKeyInfo;
    if (wg_smc_call(&in, &res) != KERN_SUCCESS || res.result != 0) return -2;

    unsigned int dataSize = res.keyInfo.dataSize;
    unsigned int dataType = res.keyInfo.dataType;
    if (dataSize == 0 || dataSize > 32) return -3;

    // Step 2: read the bytes.
    memset(&in, 0, sizeof(in));
    in.key = key;
    in.keyInfo.dataSize = dataSize;
    in.data8 = kSMCCmdReadBytes;
    memset(&res, 0, sizeof(res));
    if (wg_smc_call(&in, &res) != KERN_SUCCESS || res.result != 0) return -4;

    const unsigned int TYPE_SP78 = ('s'<<24)|('p'<<16)|('7'<<8)|'8';
    const unsigned int TYPE_FLT  = ('f'<<24)|('l'<<16)|('t'<<8)|' ';

    if (dataType == TYPE_SP78 && dataSize >= 2) {
        // Signed 8.8 fixed point, big-endian.
        signed char whole = (signed char)res.bytes[0];
        unsigned char frac = res.bytes[1];
        *out = (double)whole + ((double)frac / 256.0);
        return 0;
    }
    if (dataType == TYPE_FLT && dataSize >= 4) {
        float f;
        memcpy(&f, res.bytes, 4);
        *out = (double)f;
        return 0;
    }
    return -5;
}
*/
import "C"

import (
	"sync"
	"unsafe"
)

// tempCandidates are SMC keys tried in order until one returns a plausible
// reading.
//
// There is no single portable CPU-temperature key across Mac generations:
// Intel Macs expose TC0P (CPU proximity), while Apple silicon dropped it and
// publishes per-cluster die sensors instead. Probing a candidate list and
// caching the winner keeps one code path working on both without needing to
// detect the architecture, which would be a proxy for the real question
// ("which sensor does this machine actually have?") rather than an answer.
var tempCandidates = []string{
	"TC0P", // Intel: CPU proximity, the key Adrafinil validated
	"TC0D", // Intel: CPU die
	"Tp09", // Apple silicon: P-core cluster die
	"Tp0T", // Apple silicon: SoC die
	"Tp01", // Apple silicon: alternate P-core sensor
	"Tp05",
	"Te05", // Apple silicon: E-core cluster
	"TCHP", // Apple silicon: heat pipe / chassis proximity
}

var (
	smcMu      sync.Mutex
	smcKey     string // the candidate that worked, cached after first success
	smcProbed  bool
	smcUseless bool // no candidate produced a plausible value on this machine
)

// plausibleTemp filters out sensors that exist but report garbage. An SMC
// key can return 0 or a wildly out-of-range value when the sensor is absent
// or unpowered, and feeding that to the thermal cutout would either disable
// the safety valve or trip it constantly.
func plausibleTemp(c float64) bool { return c > 5 && c < 120 }

// readSMCTemp returns the CPU temperature in celsius, and whether a reading
// was obtained. Never returns a value it does not trust: an unavailable
// sensor reports false so the caller can treat thermal safety as unverifiable
// rather than assuming the machine is cool.
func readSMCTemp() (float64, string, bool) {
	smcMu.Lock()
	defer smcMu.Unlock()

	if smcUseless {
		return 0, "", false
	}
	if smcProbed && smcKey != "" {
		if v, ok := readKey(smcKey); ok {
			return v, smcSourceLabel(smcKey), true
		}
		// A previously working key started failing; re-probe rather than
		// silently going blind.
		smcProbed = false
		smcKey = ""
	}

	for _, k := range tempCandidates {
		if v, ok := readKey(k); ok {
			smcKey, smcProbed = k, true
			return v, smcSourceLabel(k), true
		}
	}
	smcProbed = true
	smcUseless = true
	return 0, "", false
}

func readKey(key string) (float64, bool) {
	ck := C.CString(key)
	defer C.free(unsafe.Pointer(ck))

	var out C.double
	if rc := C.wg_smc_read_temp(ck, &out); rc != 0 {
		return 0, false
	}
	v := float64(out)
	if !plausibleTemp(v) {
		return 0, false
	}
	return v, true
}

// closeSMC releases the AppleSMC connection at daemon shutdown.
func closeSMC() {
	smcMu.Lock()
	defer smcMu.Unlock()
	C.wg_smc_close()
}

// smcSourceLabel names the sensor honestly. TCHP is heat-pipe/chassis
// proximity, not the die: it reads tens of degrees under the silicon, so a
// status line claiming a plain CPU reading from it would tell the user the
// thermal valve is armed at a calibration it cannot meet.
func smcSourceLabel(key string) string {
	if key == "TCHP" {
		return "smc:TCHP (chassis proximity)"
	}
	return "smc:" + key
}
