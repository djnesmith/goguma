package helper

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

// Mechanism names how sleep is actually being blocked, so `status` can report
// the truth rather than a generic success.
const Mechanism = "systemd-logind block inhibitor (sleep:idle:handle-lid-switch)"

// inhibitWhat is the set of logind locks the helper holds.
//
// The composition matters and is not the obvious one. A "sleep" block
// inhibitor alone does NOT stop a lid-close suspend on a stock systemd
// install: logind.conf ships with LidSwitchIgnoreInhibited=yes, which tells
// logind to suspend on lid close regardless of sleep inhibitors. The lock that
// covers lid close is "handle-lid-switch", which stops logind acting on the
// switch at all. "sleep" is still needed for the D-Bus Suspend() path a
// desktop environment or `systemctl suspend` uses, and "idle" for logind's own
// idle timer. All three, or the block has a hole in it.
const inhibitWhat = "sleep:idle:handle-lid-switch"

// inhibitWho and inhibitWhy are what a user sees in `systemd-inhibit --list`
// when they wonder why their laptop will not sleep. inhibitWho is also how
// readSleepBlocked recognises our lock, so it must stay a single word.
const (
	// inhibitWho must differ from the daemon's unprivileged inhibitor WHO
	// ("goguma" in internal/power). The liveness check greps the inhibitor
	// list for this exact name; sharing one name let the daemon's weaker
	// idle lock satisfy the check for the helper's lid-closed block after a
	// logind restart invalidated both, so the block was never re-taken.
	inhibitWho = "goguma-helper"
	inhibitWhy = "goguma is holding the machine awake for a scheduled job"
)

// systemdTimeout bounds every systemd-inhibit and rtcwake invocation. A wedged
// subprocess must not be able to deadlock the helper's policy lock; if that
// happened while blocked, the block could never be cleared.
const systemdTimeout = 10 * time.Second

// inhibitStartGrace is how long we wait to see whether a freshly spawned
// inhibitor survives. systemd-inhibit reports a refused lock by exiting rather
// than by failing to spawn, so without this pause the helper would report
// success for a block that does not exist.
//
// It is best-effort: a loaded machine can let the child outlive this window and
// die immediately after. readSleepBlocked is the backstop, since it asks logind
// rather than trusting the spawn, and the daemon's re-push then repairs it.
const inhibitStartGrace = 300 * time.Millisecond

// inhibitStopTimeout bounds how long we wait for the child to notice its
// keepalive pipe closed before escalating to SIGKILL.
const inhibitStopTimeout = 5 * time.Second

// The single inhibitor this helper holds. Guarded by inhibitMu, which the
// exported entry points take for the whole of a state change so two concurrent
// requests cannot leave two children running.
var (
	inhibitMu   sync.Mutex
	inhibitCmd  *exec.Cmd
	inhibitHold *os.File      // write end of the child's keepalive pipe
	inhibitDone chan struct{} // closed once the child has been reaped
	inhibitErr  error         // child's exit status; read only after inhibitDone
)

// setSleepBlocked applies or clears the root-level block on sleep.
//
// Two mechanisms were available and the choice between them is the important
// decision in this file.
//
// Masking sleep.target/suspend.target works and is what a lot of scripts do,
// but it writes persistent state into /etc/systemd/system: a helper that is
// SIGKILLed, or a machine force-powered-off, comes back up with sleep still
// masked and no process left that knows to undo it. That is precisely the
// stranded-block failure the dead-man switch in service.go exists to guard
// against on macOS, where pmset's setting has the same persistence problem.
//
// A logind block inhibitor has the opposite property. The lock lives only as
// long as the file descriptor holding it, so it cannot survive a reboot and
// cannot outlive the process that took it. The lock here is owned by a child
// `systemd-inhibit` process whose stdin is a pipe this helper holds the write
// end of; the child is `cat`, which exits on EOF. If the helper exits for any
// reason including SIGKILL, the kernel closes that fd, cat exits,
// systemd-inhibit exits and logind drops the lock. Nothing can be stranded.
//
// The subprocess exists because logind hands the lock back as a file
// descriptor over D-Bus, and taking it in-process would mean implementing the
// D-Bus wire protocol (SASL, type-signature marshalling, SCM_RIGHTS fd
// passing), which cannot be done here without adding a dependency the project
// has deliberately not taken.
func setSleepBlocked(blocked bool) error {
	inhibitMu.Lock()
	defer inhibitMu.Unlock()

	if !blocked {
		return stopInhibitor()
	}
	if inhibitorHeld() {
		// Re-assertion must be a no-op while the lock is genuinely held. The
		// daemon re-pushes its state every few seconds, and tearing the child
		// down to start a fresh one each time would leave a window in which
		// the machine could suspend, the opposite of what re-assertion is for.
		return nil
	}
	// Clear any dead bookkeeping first so a child that exited on its own does
	// not leak a zombie or a stale pipe.
	if err := stopInhibitor(); err != nil {
		return err
	}
	return startInhibitor()
}

// startInhibitor spawns the child that owns the lock. Caller holds inhibitMu.
func startInhibitor() error {
	if _, err := exec.LookPath("systemd-inhibit"); err != nil {
		return fmt.Errorf("systemd-inhibit not found on PATH: %w", err)
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("creating inhibitor keepalive pipe: %w", err)
	}

	// Deliberately not exec.CommandContext: this process is meant to outlive
	// the call. Its lifetime is bounded by the keepalive pipe instead, which
	// is a stronger bound than a context because it survives us being killed.
	cmd := exec.Command("systemd-inhibit",
		"--what="+inhibitWhat,
		"--who="+inhibitWho,
		"--why="+inhibitWhy,
		"--mode=block",
		"cat")
	cmd.Stdin = pr

	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return fmt.Errorf("starting systemd-inhibit: %w", err)
	}
	// Only the child needs the read end; holding it here would mean the pipe
	// never reaches EOF and the child would outlive us.
	_ = pr.Close()

	done := make(chan struct{})
	inhibitCmd, inhibitHold, inhibitDone, inhibitErr = cmd, pw, done, nil
	go func() {
		err := cmd.Wait()
		inhibitMu.Lock()
		if inhibitDone == done {
			inhibitErr = err
		}
		inhibitMu.Unlock()
		close(done)
	}()

	select {
	case <-done:
		exit := "exited with status 0"
		if inhibitErr != nil {
			exit = inhibitErr.Error()
		}
		_ = pw.Close()
		inhibitCmd, inhibitHold, inhibitDone = nil, nil, nil
		// The usual causes are logind not running, or polkit refusing
		// org.freedesktop.login1.inhibit-block-sleep. Either way no lock was
		// taken, and reporting success would let the daemon believe a job
		// window is protected when it is not.
		return fmt.Errorf("systemd-inhibit exited immediately, so no lock was taken: %s", exit)
	case <-time.After(inhibitStartGrace):
	}
	return nil
}

// stopInhibitor releases the lock and clears our bookkeeping. Caller holds
// inhibitMu. Releasing when nothing is held is not an error: the daemon
// re-pushes "unblocked" routinely.
func stopInhibitor() error {
	cmd, hold, done := inhibitCmd, inhibitHold, inhibitDone
	inhibitCmd, inhibitHold, inhibitDone = nil, nil, nil
	if cmd == nil {
		return nil
	}

	_ = hold.Close()
	select {
	case <-done:
		return nil
	case <-time.After(inhibitStopTimeout):
	}

	// The child ignored EOF on stdin. Killing it is safe (logind drops the
	// lock when the holder dies), and leaving it alive would hold sleep off
	// indefinitely with nothing tracking it, which is the worst outcome here.
	if err := cmd.Process.Kill(); err != nil {
		return fmt.Errorf("killing unresponsive systemd-inhibit (pid %d): %w", cmd.Process.Pid, err)
	}
	<-done
	return fmt.Errorf("systemd-inhibit (pid %d) did not exit when its keepalive pipe closed; killed it", cmd.Process.Pid)
}

// inhibitorHeld reports whether the lock is genuinely in force. Caller holds
// inhibitMu.
//
// Both halves are checked because they can disagree. The child can die on its
// own, and logind can drop the lock while the child lives: a logind restart
// invalidates every inhibitor fd without notifying the holders. Asking logind
// as well means that case self-heals on the daemon's next re-push instead of
// leaving the machine unprotected until someone reads the status output.
func inhibitorHeld() bool {
	if inhibitCmd == nil {
		return false
	}
	select {
	case <-inhibitDone:
		return false
	default:
	}
	held, err := inhibitorListed()
	if err != nil {
		// logind could not be asked. Keep the child we have: dropping a lock
		// that is probably still valid, on the strength of a failed query, is
		// worse than carrying a possibly stale belief for one interval.
		return true
	}
	return held
}

// inhibitorListed asks logind whether our block lock is registered.
//
// No --no-pager flag is passed because systemd only pages when stdout is a
// terminal, and older systemd-inhibit does not accept the flag at all.
func inhibitorListed() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), systemdTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "systemd-inhibit", "--list").Output()
	if err != nil {
		if ctx.Err() != nil {
			return false, fmt.Errorf("systemd-inhibit --list timed out after %s", systemdTimeout)
		}
		return false, fmt.Errorf("systemd-inhibit --list failed: %w", err)
	}
	return parseInhibitList(string(out)), nil
}

// parseInhibitList looks for our lock in `systemd-inhibit --list` output, e.g.
//
//	WHO       UID USER PID  COMM ... WHY                        MODE
//	goguma 0   root 4711 cat  ... goguma is holding ...   block
//
// It matches on the first and last columns rather than counting fields,
// because the WHY column contains spaces and the table's column set has
// changed between systemd versions.
func parseInhibitList(out string) bool {
	for line := range strings.SplitSeq(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasSuffix(trimmed, "block") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) > 0 && fields[0] == inhibitWho {
			return true
		}
	}
	return false
}

// readSleepBlocked reports whether our block is currently held.
//
// Like the darwin implementation, this asks the system rather than trusting
// the helper's own memory, and for the same reason: the answer can change
// underneath us. Here the specific hazard is a logind restart, which silently
// invalidates every inhibitor lock while the processes holding them keep
// running perfectly happily.
func readSleepBlocked() (bool, error) {
	return inhibitorListed()
}

// rtcwakeTimeout bounds RTC operations, which talk to firmware and can hang.
const rtcwakeTimeout = 10 * time.Second

// rtcRoot is where the kernel exposes RTC devices and their alarms.
const rtcRoot = "/sys/class/rtc"

// scheduleWake sets an RTC alarm at t without suspending.
//
// `-m no` means "program the alarm and return" rather than the default of
// suspending immediately, which is what makes rtcwake usable as a scheduler
// rather than as a sleep command.
//
// useWakeOrPowerOn is ignored, and cannot be honoured: the RTC has exactly one
// alarm with one behaviour. On most firmware that alarm will also power on a
// machine that is fully off, so Linux effectively always behaves like macOS's
// `wakeorpoweron`. The opt-in that exists on macOS to avoid surprising a user
// by booting a shut-down laptop therefore cannot be expressed here.
//
// No -l/-u flag is passed. rtcwake reads /etc/adjtime to learn whether the
// hardware clock runs in UTC or local time, and overriding that guess would
// shift the alarm by the timezone offset on dual-boot machines that keep the
// RTC in local time. Getting it wrong is not silent: scheduledWakes reads the
// alarm back and the daemon's wake verification compares it against what was
// requested.
func scheduleWake(t time.Time, useWakeOrPowerOn bool) error {
	_ = useWakeOrPowerOn

	ctx, cancel := context.WithTimeout(context.Background(), rtcwakeTimeout)
	defer cancel()

	epoch := strconv.FormatInt(t.Unix(), 10)
	out, err := exec.CommandContext(ctx, "rtcwake", "-m", "no", "-t", epoch).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("rtcwake -m no -t %s timed out after %s", epoch, rtcwakeTimeout)
		}
		return fmt.Errorf("rtcwake -m no -t %s failed: %w: %s", epoch, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// cancelWake clears the RTC alarm.
//
// The RTC has a single alarm slot with no notion of ownership, unlike pmset's
// owner-tagged entries, so the alarm currently programmed may not be ours;
// the firmware or another tool can have overwritten it. The alarm is read back
// first and only cleared when it matches the one being cancelled, so a
// cancellation cannot silently delete somebody else's wake. An alarm that is
// no longer registered is not an error: the normal case after the machine
// actually woke is that the kernel already consumed it.
func cancelWake(e wakeEntry) error {
	t := e.at

	paths := rtcWakealarmPaths(rtcRoot)
	if len(paths) == 0 {
		return nil
	}

	var firstErr error
	for _, path := range paths {
		set, ok, err := readWakealarm(path)
		if err != nil {
			// Unreadable is not proof of absence, so fall through to clearing
			// it: leaving a stale alarm would wake the machine for a job that
			// is no longer scheduled.
			if firstErr == nil {
				firstErr = err
			}
		} else if !ok {
			continue // nothing programmed
		} else if d := set.Sub(t); d >= time.Second || d <= -time.Second {
			continue // somebody else's alarm
		}
		if err := clearWakealarm(path); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// clearWakealarm disables an alarm.
//
// Writing 0 to the sysfs attribute is preferred over `rtcwake -m disable`: it
// is a single write with no subprocess, no timezone interpretation and no
// dependency on util-linux being installed. rtcwake remains the fallback for
// kernels that reject the write.
func clearWakealarm(path string) error {
	if err := os.WriteFile(path, []byte("0\n"), 0o644); err == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), rtcwakeTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "rtcwake", "-m", "disable").CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("rtcwake -m disable timed out after %s", rtcwakeTimeout)
		}
		return fmt.Errorf("clearing %s and rtcwake -m disable both failed: %w: %s",
			path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// scheduledWakes reads back the alarms the kernel actually has programmed.
//
// This read-back is the mitigation for the same class of problem PRD §5.2
// describes on macOS, and it carries extra weight here. It is the only way to
// notice that rtcwake and the kernel disagree about whether the hardware clock
// runs in UTC, which would otherwise place the alarm hours away from the
// requested time and produce a job that silently never runs.
//
// It cannot detect the harder failure: firmware that accepts an alarm and then
// declines to act on it. Only a real suspend/resume test on the specific
// machine can rule that out.
func scheduledWakes() ([]wakeEntry, error) {
	paths := rtcWakealarmPaths(rtcRoot)
	if len(paths) == 0 {
		return nil, errors.New("no RTC device exposes a wakealarm attribute")
	}

	var entries []wakeEntry
	var firstErr error
	for _, path := range paths {
		at, ok, err := readWakealarm(path)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if ok {
			// The RTC has no kind or owner; a listed alarm is simply what
			// is programmed. It is tagged as ours because cancel matching
			// already guards against clearing somebody else's alarm.
			entries = append(entries, wakeEntry{at: at, owner: wakeOwnerTag})
		}
	}
	if len(entries) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return entries, nil
}

// readWakealarm reads one RTC alarm. The attribute holds seconds since the
// epoch, and is empty (not zero) when no alarm is programmed, though some
// drivers write a literal 0 instead.
func readWakealarm(path string) (time.Time, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("reading %s: %w", path, err)
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return time.Time{}, false, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parsing %s: %w", path, err)
	}
	if n <= 0 {
		return time.Time{}, false, nil
	}
	return time.Unix(n, 0), true, nil
}

// rtcWakealarmPaths lists RTC devices that advertise an alarm. The wakealarm
// attribute only exists when the driver supports alarms, so its presence is
// the capability probe. All of them are listed rather than assuming rtc0,
// because the wake-capable device is not always the first one.
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
