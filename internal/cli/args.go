package cli

import "strings"

// hoistFlags reorders arguments so flags parse regardless of where they appear.
//
// Go's flag package stops parsing at the first non-flag argument, so
// `history morning-briefing --json` silently treats `--json` as a second
// positional and drops it. The command then prints a human table while the
// caller believes it asked for JSON; a script parsing that output fails in a
// way that points nowhere near the actual cause.
//
// Users reasonably write the subject before the options, so rather than
// requiring `history --json morning-briefing`, flags are hoisted ahead of
// positionals before parsing.
//
// Everything after a bare `--` is left untouched and returned as positional,
// which is what allows `goguma-mark <job> -- <command with its own flags>`
// to pass a command line through unmolested.
func hoistFlags(args []string) (flags, positional []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]

		if a == "--" {
			positional = append(positional, args[i+1:]...)
			return flags, positional
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}

		flags = append(flags, a)
		// A flag written as `--limit 20` takes the next argument with it.
		// `--limit=20` is self-contained and must not swallow one.
		if !strings.Contains(a, "=") && takesValue(a) && i+1 < len(args) {
			next := args[i+1]
			if !strings.HasPrefix(next, "-") {
				flags = append(flags, next)
				i++
			}
		}
	}
	return flags, positional
}

// valueFlags are the flags that consume a following argument.
//
// Listed explicitly rather than inferred, because guessing from whether the
// next token looks like a value would mis-handle `--json morning-briefing`:
// a boolean flag would swallow the job name and the command would then report
// that no job was given.
var valueFlags = map[string]bool{
	"limit": true, "name": true, "cron": true, "tz": true,
	"detection": true, "match": true, "command": true,
	"max-runtime": true, "wake-buffer": true, "socket": true, "log": true,
}

func takesValue(flag string) bool {
	name := strings.TrimLeft(flag, "-")
	if i := strings.IndexByte(name, '='); i >= 0 {
		name = name[:i]
	}
	return valueFlags[name]
}

// splitRef pulls the leading subject off a command's arguments and returns the
// remaining arguments in an order the flag package can parse.
func splitRef(args []string) (ref string, rest []string) {
	flags, positional := hoistFlags(args)
	if len(positional) > 0 {
		ref = positional[0]
		positional = positional[1:]
	}
	return ref, append(flags, positional...)
}
