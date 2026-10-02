package power

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// TestHoldDisplaySleepOnDevice takes and releases the real assertion and
// checks both through `pmset -g assertions`, the place a user would look.
func TestHoldDisplaySleepOnDevice(t *testing.T) {
	if testing.Short() {
		t.Skip("on-device assertion test skipped in short mode")
	}
	const name = "goguma test: display assertion"
	held := func() bool {
		out, err := exec.Command("pmset", "-g", "assertions").Output()
		if err != nil {
			t.Fatalf("pmset -g assertions: %v", err)
		}
		mine := "pid " + strconv.Itoa(os.Getpid()) + "("
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, mine) && strings.Contains(line, "PreventUserIdleDisplaySleep") &&
				strings.Contains(line, name) {
				t.Logf("pmset: %s", strings.TrimSpace(line))
				return true
			}
		}
		return false
	}

	a, err := New().HoldDisplaySleep(name)
	if err != nil {
		t.Fatal(err)
	}
	if !held() {
		_ = a.Release()
		t.Fatal("pmset does not list the display assertion after taking it")
	}
	if err := a.Release(); err != nil {
		t.Fatal(err)
	}
	if held() {
		t.Fatal("pmset still lists the display assertion after releasing it")
	}
	if err := a.Release(); err != nil {
		t.Errorf("a second Release should be a no-op, got %v", err)
	}
}
