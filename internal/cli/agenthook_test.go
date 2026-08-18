package cli

import (
	"strings"
	"testing"
)

// TestHookActionReadsWhicheverShapeArrives.
//
// Four harnesses send four payload shapes and change them between versions. The
// flag is authoritative because goguma writes it into the config itself, but
// the payload still has to be read for the session id, which is what keeps two
// agents from sharing one hold.
func TestHookActionReadsWhicheverShapeArrives(t *testing.T) {
	tests := []struct {
		name      string
		p         hookPayload
		flagEvent string
		wantAct   hookAct
		wantKey   string
	}{
		{
			name: "the flag goguma writes", flagEvent: "renew",
			p:       hookPayload{Session: "abc"},
			wantAct: hookRenew, wantKey: "agent-abc",
		},
		{
			name: "stop from the flag", flagEvent: "stop",
			p:       hookPayload{Session: "abc"},
			wantAct: hookStop, wantKey: "agent-abc",
		},
		{
			name:    "the payload's own event name, Claude Code and Codex",
			p:       hookPayload{Event: "Stop", Session: "abc"},
			wantAct: hookStop, wantKey: "agent-abc",
		},
		{
			name:    "Cursor's spelling of both fields",
			p:       hookPayload{EventAlt: "stop", SessionAlt: "xyz"},
			wantAct: hookStop, wantKey: "agent-xyz",
		},
		{
			// The safe direction. An event goguma has never heard of keeps the
			// machine awake rather than dropping the hold under an agent that
			// is still working.
			name:    "an unknown event renews rather than releasing",
			p:       hookPayload{Event: "SomethingNewIn2027", Session: "abc"},
			wantAct: hookRenew, wantKey: "agent-abc",
		},
		{
			// A subagent finishing is not the session finishing, and releasing
			// here would sleep the machine mid-run.
			name:    "a subagent stopping is not the session stopping",
			p:       hookPayload{Event: "SubagentStop", Session: "abc"},
			wantAct: hookRenew, wantKey: "agent-abc",
		},
		{
			name:    "no session id at all still produces a usable key",
			p:       hookPayload{Event: "UserPromptSubmit"},
			wantAct: hookRenew, wantKey: "agent",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			act, key, label := hookAction(tc.p, tc.flagEvent, "", "")
			if act != tc.wantAct {
				t.Errorf("action = %v, want %v", act, tc.wantAct)
			}
			if key != tc.wantKey {
				t.Errorf("key = %q, want %q", key, tc.wantKey)
			}
			if strings.TrimSpace(label) == "" {
				t.Error("label is empty; the hold would have no name in any surface")
			}
		})
	}
}

// TestTwoSessionsGetTwoKeys is what lets one agent finish without releasing
// another's hold.
func TestTwoSessionsGetTwoKeys(t *testing.T) {
	_, a, _ := hookAction(hookPayload{Session: "one"}, "renew", "", "")
	_, b, _ := hookAction(hookPayload{Session: "two"}, "renew", "", "")
	if a == b {
		t.Fatalf("both sessions produced the key %q; one finishing would release the other", a)
	}
}

// TestTheSameSessionAlwaysGetsTheSameKey is the other half: every prompt and
// every tool call in one session has to land on the hold it already has,
// rather than pile up new ones that nothing will close.
func TestTheSameSessionAlwaysGetsTheSameKey(t *testing.T) {
	_, first, _ := hookAction(hookPayload{Session: "abc", Event: "UserPromptSubmit"}, "renew", "", "")
	_, later, _ := hookAction(hookPayload{Session: "abc", Event: "PostToolUse"}, "renew", "", "")
	_, ending, _ := hookAction(hookPayload{Session: "abc", Event: "Stop"}, "stop", "", "")
	if first != later || later != ending {
		t.Errorf("one session produced different keys: %q, %q, %q", first, later, ending)
	}
}

// TestAKeyCannotEscapeItsNamespace. The key reaches a hold id, and a session
// name carrying a path separator or a reserved prefix must not be able to name
// something else.
func TestAKeyCannotEscapeItsNamespace(t *testing.T) {
	for _, nasty := range []string{"../../etc", "__keep_awake__", "a/b/c", "  ", "..", "__run__:1"} {
		_, key, _ := hookAction(hookPayload{Session: nasty}, "renew", "", "")
		if strings.ContainsAny(key, "/\\:") {
			t.Errorf("key %q from session %q carries a path or namespace separator", key, nasty)
		}
		if key == "" {
			t.Errorf("session %q produced an empty key", nasty)
		}
	}
}
