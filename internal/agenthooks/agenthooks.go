package agenthooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A Harness is a coding agent goguma can be told about by, rather than have to
// watch for.
//
// Watching is not on the table. An agent waiting on a model is a process
// blocked on a socket and is indistinguishable from one doing nothing; measured
// across four of them, the busiest CPU figure belonged to an idle session. The
// work is not local, so there is nothing local to see. Every one of these
// Harnesses runs shell commands at its own lifecycle events, which is the
// Harness saying what no observer could work out.
type Harness struct {
	// ID is what the user types; Name is what they read.
	ID, Name string
	// dir is the Harness's own config directory, and its presence is what
	// "installed" means here. Absent means the tool is not on this machine.
	dir string
	// file is the config to edit, inside dir.
	file string
	// nested is the shape of the events map.
	//
	// Claude Code and Codex both take hooks.<Event> = [ {hooks: [{type, command}]} ].
	// Cursor takes version:1 with hooks.<event> = [ {command} ]. Same idea,
	// two spellings, and writing one into the other's file produces a config
	// the tool silently ignores.
	nested bool
	// version is written at the top level when non-zero (Cursor requires it).
	version int
	// renewOn fires while the agent is working; stopOn when it has finished.
	renewOn, stopOn []string
}

// Harnesses is the set goguma knows how to configure.
//
// Each entry's shape is taken from that vendor's own documentation, and the
// Claude Code one was additionally checked against a working config on a real
// machine. Nothing is guessed: a wrong shape here writes a broken config into
// somebody's agent, which is a far worse failure than the sleep it prevents.
var Harnesses = []Harness{
	{
		ID: "claude-code", Name: "Claude Code",
		dir: "~/.claude", file: "settings.json", nested: true,
		renewOn: []string{"UserPromptSubmit", "PostToolUse"},
		stopOn:  []string{"Stop"},
	},
	{
		ID: "codex", Name: "Codex CLI",
		dir: "~/.codex", file: "hooks.json", nested: true,
		renewOn: []string{"UserPromptSubmit", "PostToolUse"},
		stopOn:  []string{"Stop"},
	},
	{
		ID: "cursor", Name: "Cursor",
		dir: "~/.cursor", file: "hooks.json", nested: false, version: 1,
		renewOn: []string{"beforeSubmitPrompt", "afterFileEdit"},
		stopOn:  []string{"stop"},
	},
	{
		// Gemini CLI takes Claude Code's nesting exactly —
		// hooks.<Event> = [ {hooks: [{type, command}]} ] — so it needs no new
		// writer, only the right event names.
		//
		// The events are not the same words for the same moments, and picking
		// them by name similarity would have been wrong. `BeforeAgent` and
		// `AfterAgent` fire once per turn, which is where Claude Code's
		// `UserPromptSubmit` and `Stop` sit; `AfterTool` fires per tool call,
		// like `PostToolUse`. `SessionStart` and `SessionEnd` are the *process*
		// boundaries, not the work boundaries, so opening a hold on
		// `SessionStart` would hold sleep off for a session left sitting at an
		// idle prompt.
		//
		// `SessionEnd` is still a stop, as a backstop for a CLI that quits
		// mid-turn. It is known not to fire on some versions
		// (google-gemini/gemini-cli#16697), which costs nothing here: it is the
		// second of two stops, and the lease expires on its own regardless.
		ID: "gemini-cli", Name: "Gemini CLI",
		dir: "~/.gemini", file: "settings.json", nested: true,
		renewOn: []string{"BeforeAgent", "AfterTool"},
		stopOn:  []string{"AfterAgent", "SessionEnd"},
	},
}

func (h Harness) Path() string { return filepath.Join(ExpandHome(h.dir), h.file) }

// present reports whether this Harness is installed, judged by its own config
// directory rather than by a running process: an agent that is not running at
// this second is still one to configure.
func (h Harness) Present() bool {
	st, err := os.Stat(ExpandHome(h.dir))
	return err == nil && st.IsDir()
}

func ExpandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// Marker identifies goguma's own entries.
//
// The command string is the marker. Anything containing it is ours to add,
// replace or remove, and everything else in the file is somebody's own work and
// is never touched. A separate marker field would be tidier and would also be
// silently dropped by any tool that rewrites its own config.
const Marker = "goguma agent-hook"

// hookCommands returns the command for a renew event and for a stop event.
//
// Absolute, because a hook runs with whatever environment the Harness gives it,
// which frequently does not include ~/.local/bin on PATH. A bare `goguma` there
// fails silently and the whole feature does nothing, with no error anywhere.
func Commands(binDir string) (renew, stop string) {
	bin := filepath.Join(binDir, "goguma")
	return bin + " agent-hook --event renew", bin + " agent-hook --event stop"
}

// State is what one Harness looks like right now.
type State struct {
	H Harness
	// Found is whether the agent is on this machine at all. Named Found rather
	// than Present because Harness already has a Present method and a field
	// shadowing it reads as a bug at every call site.
	Found     bool
	Installed bool
	// stale is an installed entry whose command no longer matches, which
	// happens when goguma moves. Reported separately because the fix is the
	// same command but the message is not "already done".
	Stale bool
	Err   error
}

// inspect reads one Harness's config without changing it.
func Inspect(h Harness, binDir string) State {
	st := State{H: h, Found: h.Present()}
	if !st.Found {
		return st
	}
	doc, err := ReadConfig(h.Path())
	if err != nil {
		st.Err = err
		return st
	}
	renew, stop := Commands(binDir)
	want := map[string]bool{renew: true, stop: true}

	found, matching := 0, 0
	for _, cmd := range CommandsIn(doc, h) {
		if !strings.Contains(cmd, Marker) {
			continue
		}
		found++
		if want[cmd] {
			matching++
		}
	}
	st.Installed = found > 0
	st.Stale = found > 0 && matching < len(h.renewOn)+len(h.stopOn)
	return st
}

// readJSONFile reads a config, treating "not there" as an empty document so a
// Harness with no config yet is configured rather than skipped.
func ReadConfig(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return map[string]any{}, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON, so goguma will not edit it: %w", path, err)
	}
	return doc, nil
}

// commandsIn lists every hook command in a document, whoever wrote it.
func CommandsIn(doc map[string]any, h Harness) []string {
	var out []string
	hooks, _ := doc["hooks"].(map[string]any)
	for _, v := range hooks {
		blocks, _ := v.([]any)
		for _, b := range blocks {
			block, _ := b.(map[string]any)
			if block == nil {
				continue
			}
			if h.nested {
				inner, _ := block["hooks"].([]any)
				for _, i := range inner {
					e, _ := i.(map[string]any)
					if c, ok := e["command"].(string); ok {
						out = append(out, c)
					}
				}
				continue
			}
			if c, ok := block["command"].(string); ok {
				out = append(out, c)
			}
		}
	}
	return out
}

// applyHooks returns the document with goguma's entries set, leaving everything
// else exactly as it was.
//
// The rule throughout: read what is there, drop only entries carrying goguma's
// marker, append fresh ones. A user's own hooks on the same event survive
// untouched and keep their order, which matters because hooks on one event run
// in the order they appear.
func Apply(doc map[string]any, h Harness, binDir string, remove bool) map[string]any {
	renew, stop := Commands(binDir)

	if h.version != 0 {
		if _, ok := doc["version"]; !ok {
			doc["version"] = h.version
		}
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}

	set := func(event, cmd string) {
		blocks, _ := hooks[event].([]any)
		kept := make([]any, 0, len(blocks))
		for _, b := range blocks {
			if blockIsGoguma(b, h) {
				continue // ours: replaced below, or dropped when removing
			}
			kept = append(kept, b)
		}
		if !remove {
			kept = append(kept, newHookBlock(h, cmd))
		}
		if len(kept) == 0 {
			delete(hooks, event)
			return
		}
		hooks[event] = kept
	}

	for _, e := range h.renewOn {
		set(e, renew)
	}
	for _, e := range h.stopOn {
		set(e, stop)
	}

	if len(hooks) == 0 {
		delete(doc, "hooks")
	} else {
		doc["hooks"] = hooks
	}
	return doc
}

func blockIsGoguma(b any, h Harness) bool {
	block, _ := b.(map[string]any)
	if block == nil {
		return false
	}
	if h.nested {
		inner, _ := block["hooks"].([]any)
		for _, i := range inner {
			e, _ := i.(map[string]any)
			if c, ok := e["command"].(string); ok && strings.Contains(c, Marker) {
				return true
			}
		}
		return false
	}
	c, _ := block["command"].(string)
	return strings.Contains(c, Marker)
}

func newHookBlock(h Harness, cmd string) map[string]any {
	if h.nested {
		return map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": cmd}},
		}
	}
	return map[string]any{"command": cmd}
}

// writeJSONFile writes a config back, keeping a copy of the old one first and
// putting it back if the result does not parse.
//
// The same shape as `goguma import --register`, and for the same reason: this
// is goguma editing a file that belongs to another program, and the only
// acceptable version of that is one that cannot leave the file worse than it
// found it.
func WriteConfig(path string, doc map[string]any) (backup string, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if old, err := os.ReadFile(path); err == nil {
		backup = path + ".goguma-backup"
		if err := os.WriteFile(backup, old, 0o600); err != nil {
			return "", fmt.Errorf("couldn't back up %s: %w", path, err)
		}
	}

	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return backup, err
	}
	b = append(b, '\n')

	tmp := path + ".goguma-tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return backup, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return backup, err
	}

	// Verify by reading it back. A config that does not parse is a broken
	// agent, so the old one goes back rather than being left for the user to
	// discover the next time they open the tool.
	if _, err := ReadConfig(path); err != nil {
		if backup != "" {
			if b, rerr := os.ReadFile(backup); rerr == nil {
				_ = os.WriteFile(path, b, 0o600)
			}
		}
		return backup, fmt.Errorf("the rewritten %s did not verify and the original was restored: %w", path, err)
	}
	return backup, nil
}

func SortedIDs() []string {
	ids := make([]string, 0, len(Harnesses))
	for _, h := range Harnesses {
		ids = append(ids, h.ID)
	}
	sort.Strings(ids)
	return ids
}
