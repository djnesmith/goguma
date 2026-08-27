package daemon

import (
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/ipc"
)

// TestAgentReportOpensNoHoldWhenAgentHooksIsOff covers the gap between turning
// the setting off and the agents on the machine noticing.
//
// `goguma config set agent_hooks off` takes the hook lines back out of every
// agent's config, but an agent already running read that config when it started
// and keeps firing the hook until it is restarted. The daemon used to honour
// those reports, so a Mac could still be held awake for an agent hours after
// the feature was switched off — with `goguma hooks` correctly reporting
// nothing installed while `status` listed a live agent hold.
func TestAgentReportOpensNoHoldWhenAgentHooksIsOff(t *testing.T) {
	d, _ := runDaemon(t)
	d.cfg.AgentHooks = false
	now := time.Now()

	resp, err := d.StartRun(
		ipc.RunStartReq{Label: "claude-session", Key: "session-abc"}, now)
	if err != nil {
		t.Fatalf("an observed agent report must not error the hook: %v", err)
	}
	if resp.ID != "" {
		t.Fatalf("got hold id %q, want none: the report must not open a hold", resp.ID)
	}
	if got := len(d.holdsSnapshot()); got != 0 {
		t.Fatalf("holds = %d, want 0", got)
	}
}

// TestAgentReportIsReportedWhenAgentHooksIsOff is the other half of the same
// event: no hold, but not silence either.
//
// With the setting off, goguma is the only process on the machine that knows an
// agent is working. Discarding that left the user with no way to know a lid
// close was about to kill a session, which is the decision the manual Keep
// Awake exists to serve.
func TestAgentReportIsReportedWhenAgentHooksIsOff(t *testing.T) {
	d, _ := runDaemon(t)
	d.cfg.AgentHooks = false
	now := time.Now()

	if _, err := d.StartRun(
		ipc.RunStartReq{Label: "claude-session", Key: "session-abc"}, now); err != nil {
		t.Fatal(err)
	}

	sessions := d.agentSessions(now)
	if len(sessions) != 1 {
		t.Fatalf("agent sessions = %d, want 1", len(sessions))
	}
	if sessions[0].Label != "claude-session" {
		t.Fatalf("label = %q, want %q", sessions[0].Label, "claude-session")
	}
	// Reported, and still not holding anything.
	if st := d.Status(); st.Holding || len(st.Holds) != 0 {
		t.Fatalf("status reports holding=%v holds=%d; an observed session must hold nothing",
			st.Holding, len(st.Holds))
	}
}

// TestRepeatedAgentReportsAreOneSession pins that the per-event hook traffic
// collapses to one entry. A harness fires on every prompt and every tool call.
func TestRepeatedAgentReportsAreOneSession(t *testing.T) {
	d, _ := runDaemon(t)
	d.cfg.AgentHooks = false
	now := time.Now()

	for i := range 5 {
		if _, err := d.StartRun(
			ipc.RunStartReq{Label: "claude-session", Key: "session-abc"},
			now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(d.agentSessions(now.Add(5 * time.Minute))); got != 1 {
		t.Fatalf("agent sessions = %d, want 1", got)
	}
}

// TestAgentSessionExpiresWhenItGoesQuiet is the backstop for a harness killed
// outright, which never fires its stop hook. Without expiry the popover would
// claim an agent is working indefinitely.
func TestAgentSessionExpiresWhenItGoesQuiet(t *testing.T) {
	d, _ := runDaemon(t)
	d.cfg.AgentHooks = false
	now := time.Now()

	if _, err := d.StartRun(
		ipc.RunStartReq{Label: "claude-session", Key: "session-abc"}, now); err != nil {
		t.Fatal(err)
	}
	if got := len(d.agentSessions(now.Add(agentLease - time.Minute))); got != 1 {
		t.Fatalf("sessions just inside the lease = %d, want 1", got)
	}
	if got := len(d.agentSessions(now.Add(agentLease + time.Minute))); got != 0 {
		t.Fatalf("sessions past the lease = %d, want 0", got)
	}
}

// TestAgentStopEventClearsTheSession: the ordinary path, where the harness says
// it has finished.
func TestAgentStopEventClearsTheSession(t *testing.T) {
	d, _ := runDaemon(t)
	d.cfg.AgentHooks = false
	now := time.Now()

	if _, err := d.StartRun(
		ipc.RunStartReq{Label: "claude-session", Key: "session-abc"}, now); err != nil {
		t.Fatal(err)
	}
	d.forgetAgentSighting(keyedRunID("session-abc"))

	if got := len(d.agentSessions(now)); got != 0 {
		t.Fatalf("agent sessions = %d, want 0 after the stop event", got)
	}
}

// TestRunHoldsStillWorkWhenAgentHooksIsOff pins the blast radius.
//
// `goguma run` is the other caller of StartRun and is deliberately unkeyed. It
// is a command the user typed, has nothing to do with the agent-hooks setting,
// and must keep holding sleep off regardless of it.
func TestRunHoldsStillWorkWhenAgentHooksIsOff(t *testing.T) {
	d, _ := runDaemon(t)
	d.cfg.AgentHooks = false
	now := time.Now()

	resp, err := d.StartRun(ipc.RunStartReq{Label: "rsync backup"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ID == "" {
		t.Fatal("an unkeyed run hold was dropped; only keyed agent reports are observed")
	}
	if got := len(d.holdsSnapshot()); got != 1 {
		t.Fatalf("holds = %d, want 1", got)
	}
	if got := len(d.agentSessions(now)); got != 0 {
		t.Fatalf("agent sessions = %d, want 0: `goguma run` is not an agent", got)
	}
}

// TestAgentHoldsWorkWhenAgentHooksIsOn keeps the handler intact for the on
// state, so flipping the setting back restores the auto-hold and nothing else.
func TestAgentHoldsWorkWhenAgentHooksIsOn(t *testing.T) {
	d, _ := runDaemon(t)
	d.cfg.AgentHooks = true
	now := time.Now()

	first, err := d.StartRun(
		ipc.RunStartReq{Label: "claude-session", Key: "session-abc"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" {
		t.Fatal("agent hold was not opened with agent_hooks on")
	}
	// Nothing is merely observed while the setting is on; it is a real hold.
	if got := len(d.agentSessions(now)); got != 0 {
		t.Fatalf("agent sessions = %d, want 0 while holding for real", got)
	}

	second, err := d.StartRun(
		ipc.RunStartReq{Label: "claude-session", Key: "session-abc"}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("second report opened %q, want the existing %q", second.ID, first.ID)
	}
	if got := len(d.holdsSnapshot()); got != 1 {
		t.Fatalf("holds = %d, want 1", got)
	}
}

// TestReportAfterTheFlipDoesNotRenewAnOpenHold is the reported defect itself,
// and the one case the first version of these tests left unpinned.
//
// A session holding while the setting was on keeps firing its hook afterwards.
// The gate has to sit before the renewal branch, or each of those reports would
// push the hold's deadline forward and the Mac would never sleep.
func TestReportAfterTheFlipDoesNotRenewAnOpenHold(t *testing.T) {
	d, _ := runDaemon(t)
	d.cfg.AgentHooks = true
	now := time.Now()

	opened, err := d.StartRun(
		ipc.RunStartReq{Label: "claude-session", Key: "session-abc"}, now)
	if err != nil {
		t.Fatal(err)
	}

	d.mu.RLock()
	before := d.holds[opened.ID].fireAt
	d.mu.RUnlock()

	d.cfg.AgentHooks = false
	resp, err := d.StartRun(
		ipc.RunStartReq{Label: "claude-session", Key: "session-abc"},
		now.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if resp.ID != "" {
		t.Fatalf("got hold id %q, want none: the report must not be honoured", resp.ID)
	}

	d.mu.RLock()
	after := d.holds[opened.ID].fireAt
	d.mu.RUnlock()
	if !after.Equal(before) {
		t.Fatalf("the hold's deadline moved from %v to %v; the report renewed it", before, after)
	}
}

// TestSwitchingAgentHooksOffReleasesOpenAgentHolds: declining to renew is not
// enough on its own, because a keyed hold survives its full lease without one.
// The user asked for the machine to stop being held; fifteen more minutes of
// being held is not that.
func TestSwitchingAgentHooksOffReleasesOpenAgentHolds(t *testing.T) {
	d, _ := runDaemon(t)
	d.cfg.AgentHooks = true
	now := time.Now()

	if _, err := d.StartRun(
		ipc.RunStartReq{Label: "claude-session", Key: "session-abc"}, now); err != nil {
		t.Fatal(err)
	}
	// An unkeyed `goguma run` hold must survive: it is not an agent's.
	if _, err := d.StartRun(ipc.RunStartReq{Label: "rsync backup"}, now); err != nil {
		t.Fatal(err)
	}
	if got := len(d.holdsSnapshot()); got != 2 {
		t.Fatalf("holds = %d, want 2 before the flip", got)
	}

	released := d.releaseAgentHolds(now.Add(time.Minute))
	if released != 1 {
		t.Fatalf("released %d holds, want 1", released)
	}
	holds := d.holdsSnapshot()
	if len(holds) != 1 {
		t.Fatalf("holds = %d, want 1: the wrapped command must keep holding", len(holds))
	}
}
