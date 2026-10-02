package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/ipc"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/power"
)

func setKeepDisplay(t *testing.T, d *Daemon, on string) {
	t.Helper()
	if _, err := d.setConfig(ipc.ConfigSetReq{Key: "keep_display_awake", Value: on}); err != nil {
		t.Fatal(err)
	}
}

// heldDisplays counts display assertions taken and not yet released.
func heldDisplays(p *fakePlatform) int {
	n := 0
	for _, a := range p.displays {
		if a.released == 0 {
			n++
		}
	}
	return n
}

func TestKeepDisplayAwakeIsOffByDefault(t *testing.T) {
	d, plat := keepAwakeDaemon(t)
	if config.Default().KeepDisplayAwake {
		t.Fatal("keep_display_awake defaults to on; it must be opt-in")
	}
	if _, err := d.KeepAwake(time.Hour, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(plat.displays) != 0 {
		t.Errorf("took %d display assertions with the setting off, want 0", len(plat.displays))
	}
}

func TestKeepDisplayAwakeDoesNothingWithoutAWindow(t *testing.T) {
	d, plat := keepAwakeDaemon(t)
	setKeepDisplay(t, d, "on")
	if len(plat.displays) != 0 {
		t.Errorf("took %d display assertions with no keep-awake open, want 0", len(plat.displays))
	}

	// A job's hold is not a keep-awake, and the setting says nothing about it.
	d.mu.Lock()
	d.holds["job-1"] = &hold{job: &model.Job{ID: "job-1"}}
	d.syncKeepAwakeDisplayLocked()
	d.mu.Unlock()
	if len(plat.displays) != 0 {
		t.Errorf("a job hold took a display assertion")
	}
}

func TestKeepDisplayAwakeTogglesLive(t *testing.T) {
	d, plat := keepAwakeDaemon(t)
	now := time.Now()
	resp, err := d.KeepAwake(time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}

	setKeepDisplay(t, d, "on")
	if got := heldDisplays(plat); got != 1 {
		t.Fatalf("holding %d display assertions after switching on mid-window, want 1", got)
	}
	setKeepDisplay(t, d, "on")
	if len(plat.displays) != 1 {
		t.Errorf("switching on twice took %d display assertions, want 1", len(plat.displays))
	}

	setKeepDisplay(t, d, "off")
	if got := heldDisplays(plat); got != 0 {
		t.Fatalf("holding %d display assertions after switching off, want 0", got)
	}

	// The window itself is untouched both ways: same deadline, same idle
	// assertion, never restarted.
	d.mu.RLock()
	until := d.keepAwakeUntilLocked()
	d.mu.RUnlock()
	if until == nil || !until.Equal(*resp.Until) {
		t.Errorf("window until = %v after toggling, want %s", until, resp.Until)
	}
	if len(plat.assertions) != 1 || plat.assertions[0].released != 0 {
		t.Error("toggling the display setting touched the idle assertion")
	}
}

func TestKeepDisplayAwakeEndsWithTheWindow(t *testing.T) {
	start := time.Now()
	tests := []struct {
		name string
		end  func(d *Daemon)
	}{
		{"cancel", func(d *Daemon) {
			if _, err := d.KeepAwake(0, start.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
		}},
		{"expiry", func(d *Daemon) {
			d.enforceCeilings(start.Add(time.Hour+time.Second), config.Default())
		}},
		{"cutout", func(d *Daemon) {
			hot := power.State{LidClosed: true, TempC: tempOf(95), OnAC: true, BatteryPct: 100}
			d.evaluateCutouts(hot, config.Default(), start.Add(time.Minute))
		}},
		{"sleep now", func(d *Daemon) { d.SleepNow() }},
		{"shutdown", func(d *Daemon) { d.shutdown() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, plat := keepAwakeDaemon(t)
			setKeepDisplay(t, d, "on")
			if _, err := d.KeepAwake(time.Hour, start); err != nil {
				t.Fatal(err)
			}
			if got := heldDisplays(plat); got != 1 {
				t.Fatalf("holding %d display assertions with the window open, want 1", got)
			}
			tt.end(d)
			if got := heldDisplays(plat); got != 0 {
				t.Errorf("holding %d display assertions after %s, want 0", got, tt.name)
			}
		})
	}
}

func TestKeepDisplayAwakeMovesToAReplacementWindow(t *testing.T) {
	d, plat := keepAwakeDaemon(t)
	now := time.Now()
	setKeepDisplay(t, d, "on")
	if _, err := d.KeepAwake(time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.KeepAwake(2*time.Hour, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(plat.displays) != 2 || plat.displays[0].released != 1 {
		t.Fatal("the replaced window's display assertion was not released")
	}
	if got := heldDisplays(plat); got != 1 {
		t.Errorf("holding %d display assertions after a replacement, want 1", got)
	}
}

func TestKeepDisplayAwakePersists(t *testing.T) {
	d, _ := keepAwakeDaemon(t)
	setKeepDisplay(t, d, "on")
	cfg, _, err := config.Load(d.store.Layout().ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.KeepDisplayAwake {
		t.Error("keep_display_awake did not survive a reload from disk")
	}
}

func TestKeepDisplayAwakeFailureLeavesTheWindowRunning(t *testing.T) {
	d, plat := keepAwakeDaemon(t)
	setKeepDisplay(t, d, "on")
	plat.displayErr = errors.New("IOKit said no")
	if _, err := d.KeepAwake(time.Hour, time.Now()); err != nil {
		t.Fatalf("a failed display assertion failed the keep-awake: %v", err)
	}
	if len(d.holds) != 1 || len(plat.assertions) != 1 || plat.assertions[0].released != 0 {
		t.Fatal("the keep-awake did not keep holding idle sleep when the display assertion failed")
	}

	// Flipping the setting again is the retry.
	plat.displayErr = nil
	setKeepDisplay(t, d, "off")
	setKeepDisplay(t, d, "on")
	if got := heldDisplays(plat); got != 1 {
		t.Errorf("holding %d display assertions after a retry, want 1", got)
	}
}
