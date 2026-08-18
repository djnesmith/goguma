package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/junnam586/goguma/internal/ipc"
	"github.com/junnam586/goguma/internal/model"
)

// hookPayload is the part of a harness's hook JSON this needs.
//
// Every field is optional. Four harnesses send four shapes, they change between
// versions, and a hook that fails when a field it did not expect turns up is a
// hook that breaks somebody's agent. Anything missing falls back to a flag or a
// default, and nothing here is ever required.
type hookPayload struct {
	Event   string `json:"hook_event_name"`
	Session string `json:"session_id"`
	CWD     string `json:"cwd"`

	// Cursor and Codex name these differently; both are read so one command
	// covers all of them.
	EventAlt   string `json:"event"`
	SessionAlt string `json:"sessionId"`
}

// stopEvents are the hook events that mean the agent has finished.
//
// Lowercased before lookup, so `Stop`, `stop`, `sessionEnd` and `SessionEnd`
// all land here. Everything not in this set counts as activity, which is the
// safe direction: an unrecognised event renews the hold rather than dropping
// it, so a harness that adds an event goguma has not heard of keeps the machine
// awake instead of letting it sleep under a working agent.
var stopEvents = map[string]bool{
	"stop":         true,
	"sessionend":   true,
	"session_end":  true,
	"agentstop":    true,
	"subagentstop": false, // a subagent finishing is not the session finishing
}

var cmdAgentHook = &Command{
	Name:    "agent-hook",
	Summary: "report an agent's activity from a harness hook",
	Hidden:  true,
	Usage: `goguma agent-hook [--event start|stop] [--key <name>] [--label <name>]

Called by a coding agent's own hooks, not by hand. Reads the harness's hook JSON
on stdin and holds sleep off while that session is working, releasing when it
stops.

'goguma hooks install' writes the configuration that calls this. See
'goguma help hooks'.

Each session holds separately, keyed by the session id the harness reports, so
two agents running at once do not release each other and the machine stays awake
until the last one finishes.

The hold is leased. If the harness exits without firing its stop hook, or the
machine loses power, the hold lapses by itself rather than being left on.

This never fails and never prints to stdout: a hook that errors or speaks out of
turn can break the agent it is attached to, which would be a far worse bug than
the sleep it is preventing.`,
	Run: func(ctx *Context, args []string) error {
		fs := flag.NewFlagSet("agent-hook", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		event := fs.String("event", "", "start or stop, when the payload does not say")
		key := fs.String("key", "", "session key, when the payload does not carry one")
		label := fs.String("label", "", "what to call this in goguma's own surfaces")
		if err := fs.Parse(args); err != nil {
			return nil // never fail a hook
		}

		p := readHookPayload(os.Stdin)
		act, k, name := hookAction(p, *event, *key, *label)

		if act == hookStop {
			_ = callDaemon(ctx, ipc.OpRunEnd, ipc.RunEndReq{Key: k}, nil)
			return nil
		}
		var resp ipc.RunStartResp
		_ = callDaemon(ctx, ipc.OpRunStart, ipc.RunStartReq{Label: name, Key: k}, &resp)
		return nil
	},
}

type hookAct int

const (
	hookRenew hookAct = iota
	hookStop
)

// readHookPayload reads the harness's JSON, tolerating anything.
//
// A hook is handed its payload on stdin, but not every harness sends one and a
// user may run this by hand. Reading a terminal would hang forever holding the
// agent open, so that case reads nothing at all.
func readHookPayload(f *os.File) hookPayload {
	var p hookPayload
	if f == nil {
		return p
	}
	if st, err := f.Stat(); err != nil || st.Mode()&os.ModeCharDevice != 0 {
		return p
	}
	b, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil || len(b) == 0 {
		return p
	}
	_ = json.Unmarshal(b, &p) // a shape we don't know is not an error
	return p
}

// hookAction decides what this invocation means.
func hookAction(p hookPayload, flagEvent, flagKey, flagLabel string) (hookAct, string, string) {
	ev := strings.ToLower(strings.TrimSpace(flagEvent))
	if ev == "" {
		ev = strings.ToLower(strings.TrimSpace(firstNonEmpty(p.Event, p.EventAlt)))
	}

	act := hookRenew
	if stopEvents[strings.ReplaceAll(ev, "-", "")] {
		act = hookStop
	}

	// The key decides which hold this lands on, so a missing session id must
	// not silently merge two agents into one. It falls back to a constant only
	// as a last resort, which is still correct: one shared hold released by
	// whichever session stops last is worse than nothing holding at all, but
	// both are bounded by the lease.
	k := strings.TrimSpace(flagKey)
	if k == "" {
		k = strings.TrimSpace(firstNonEmpty(p.Session, p.SessionAlt))
	}
	// Slugged, and prefixed, because this becomes part of a hold id. A session
	// name is whatever the harness chose to call it, and without this a name
	// carrying a path separator or one of goguma's own reserved prefixes could
	// address a hold that is not its own.
	if k != "" {
		k = model.Slug("agent-" + k)
	}
	if k == "" {
		k = "agent"
	}

	name := strings.TrimSpace(flagLabel)
	if name == "" && p.CWD != "" {
		name = filepath.Base(p.CWD)
	}
	if name == "" {
		name = "coding agent"
	}
	return act, k, fmt.Sprintf("%s (agent)", name)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
