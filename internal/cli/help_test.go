package cli

import (
	"testing"
)

func TestWantsHelp(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"no arguments", nil, false},
		{"long form", []string{"--help"}, true},
		{"short form", []string{"-h"}, true},
		{"single dash long form", []string{"-help"}, true},
		{"after another flag", []string{"--json", "--help"}, true},
		{"after a positional", []string{"some-job", "--help"}, true},
		{"ordinary flags only", []string{"--json"}, false},
		{"a job named help", []string{"help"}, false},
		{"escaped by a double dash", []string{"--", "--help"}, false},
		{"escaped, with a flag first", []string{"--json", "--", "-h"}, false},
		{"substring must not match", []string{"--help-me"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wantsHelp(c.args); got != c.want {
				t.Errorf("wantsHelp(%q) = %v, want %v", c.args, got, c.want)
			}
		})
	}
}

// TestHelpNeverRunsTheCommand is the regression guard for the bug this
// interception exists to prevent.
//
// The commands that take no flags never looked at their arguments, so asking
// what they did was indistinguishable from telling them to do it:
// `goguma pause --help` returned a paused daemon and a success message. A
// state-changing command is exactly the kind a user is most likely to ask
// about before running, so this must hold for every registered command, not
// just the ones that happen to parse a flag set today.
func TestHelpNeverRunsTheCommand(t *testing.T) {
	for name, cmd := range commands {
		t.Run(name, func(t *testing.T) {
			if cmd.Usage == "" {
				t.Fatalf("%s has no usage text, so --help would print nothing", name)
			}

			ran := false
			restore := cmd.Run
			commands[name] = &Command{
				Name:    cmd.Name,
				Summary: cmd.Summary,
				Usage:   cmd.Usage,
				Hidden:  cmd.Hidden,
				Run: func(*Context, []string) error {
					ran = true
					return nil
				},
			}
			t.Cleanup(func() {
				commands[name] = &Command{
					Name: cmd.Name, Summary: cmd.Summary, Usage: cmd.Usage,
					Hidden: cmd.Hidden, Run: restore,
				}
			})

			for _, flag := range []string{"--help", "-h"} {
				ran = false
				if code := Main([]string{name, flag}); code != 0 {
					t.Errorf("`goguma %s %s` exited %d, want 0", name, flag, code)
				}
				if ran {
					t.Errorf("`goguma %s %s` executed the command instead of describing it",
						name, flag)
				}
			}
		})
	}
}
