// Package detect answers "is this job running right now?".
//
// Two mechanisms, deliberately unequal:
//
//   - Mark (exact). The job is wrapped in `goguma-mark`, which tells the
//     daemon the moment it starts and the moment it exits, with a real exit
//     code. Requires editing the crontab line, and is worth it.
//
//   - Pattern (best effort). The daemon scans the process table for a regexp
//     match. Requires no change to the user's setup, but cannot see exit
//     codes, cannot distinguish two concurrent runs, and silently observes
//     nothing at all if the pattern is wrong.
//
// The PRD calls pattern detection out as the project's main reliability risk.
// The mitigation here is not to make matching cleverer; it is to make its
// failures loud: a window that closes having never matched is recorded as
// OutcomeNeverDetected and surfaced as a warning with the exact command to
// fix it.
package detect

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Process is one entry from the process table.
type Process struct {
	PID     int
	Command string
}

// Matcher tests process command lines against a compiled pattern.
type Matcher struct {
	re  *regexp.Regexp
	raw string
}

// Compile prepares a match pattern.
func Compile(pattern string) (*Matcher, error) {
	p := strings.TrimSpace(pattern)
	if p == "" {
		return nil, fmt.Errorf("empty match pattern")
	}
	re, err := regexp.Compile(p)
	if err != nil {
		return nil, fmt.Errorf("invalid match pattern %q: %w", pattern, err)
	}
	return &Matcher{re: re, raw: p}, nil
}

func (m *Matcher) Pattern() string { return m.raw }

// Find returns every live process matching the pattern.
func (m *Matcher) Find(procs []Process) []Process {
	self := os.Getpid()
	var out []Process
	for _, p := range procs {
		// Never match the daemon itself. The registered pattern is stored in
		// jobs.json and frequently appears in the daemon's own command line
		// or in a `goguma` CLI invocation, which would otherwise look like
		// the job running and hold the machine awake indefinitely.
		if p.PID == self {
			continue
		}
		if isGogumaProcess(p.Command) {
			continue
		}
		if m.re.MatchString(p.Command) {
			out = append(out, p)
		}
	}
	return out
}

// isGogumaProcess filters goguma's own processes out of match results.
//
// Without this, `goguma add --match "backup"` self-matches the moment the
// user runs `goguma list` in a terminal, and the daemon concludes the
// backup job is running. `goguma-mark` is excluded too: it is the wrapper,
// not the job, and its own command line contains the wrapped command verbatim.
func isGogumaProcess(cmd string) bool {
	for _, name := range []string{
		"goguma-daemon", "goguma-helper", "goguma-mark",
	} {
		if strings.Contains(cmd, name) {
			return true
		}
	}
	// A bare `goguma <subcommand>` invocation, but not a job whose command
	// merely mentions the word.
	fields := strings.Fields(cmd)
	if len(fields) > 0 {
		base := fields[0]
		if i := strings.LastIndexByte(base, '/'); i >= 0 {
			base = base[i+1:]
		}
		if base == "goguma" {
			return true
		}
	}
	return false
}

// SuggestPattern derives a match pattern from a command line.
//
// Anchored on the program and the *last* identifier-looking argument, because
// that is where task runners put the thing that distinguishes one job from
// another: `hermes cron run <job>`, `npm run <script>`, `make <target>`,
// `just <recipe>`, `rake <task>`. Taking the first arguments instead (which
// this did) produced the same pattern for every job of a given runner:
// `hermes cron run a` and `hermes cron run b` both became "hermes.*cron.*run",
// so each job's pattern matched all of its siblings, and the first to exit
// released everyone's hold.
//
// Quoted text is dropped entirely. A prompt is prose, not an identifier, and
// scraping words out of it produced patterns like "claude.*my.*unread" that
// are neither stable nor unique.
//
// An empty result means "no usable pattern" and must be treated as a refusal,
// not as a pattern that happens to match everything.
func SuggestPattern(command string) string {
	// Order matters: unwrap `sh -c "..."` first (its payload lives inside the
	// quotes), THEN drop quoted prose, THEN split off the last shell segment.
	// Splitting before quote-stripping let separators inside quoted prose
	// win: `claude -p "fetch news; summarize stories"` anchored the pattern
	// on words scraped from the prompt, which the contract above forbids.
	cmd := lastShellSegment(stripQuoted(stripShellWrapping(command)))
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}

	prog := base(fields[0])
	if prog == "" {
		return ""
	}
	parts := []string{regexp.QuoteMeta(prog)}

	// Positional arguments only.
	//
	// A runner names the job positionally: `hermes cron run <job>`,
	// `npm run <script>`, `make <target>`. Flags configure the runner, and
	// their *values* are especially dangerous: `--output-format json` yielded
	// "claude.*json", which matches an interactive Claude session (its command
	// line carries `--output-format stream-json`). Releasing a job's hold when
	// the user's editor session exits is exactly the failure a pattern is
	// supposed to prevent.
	//
	// Dropping them costs the odd usable case (`--agent inbox-triage` becomes
	// a bare program name), but a bare name is refused by Distinctive and falls
	// back to wrapping, which is the safe direction to be wrong in.
	var ids []string
	prevWasFlag := false
	for _, f := range fields[1:] {
		isFlag := strings.HasPrefix(f, "-")
		if !isFlag && !prevWasFlag && isIdentifierArg(f) {
			ids = append(ids, f)
		}
		// `--flag=value` carries its value inline, so it consumes nothing.
		prevWasFlag = isFlag && !strings.Contains(f, "=")
	}

	// The last one distinguishes; one earlier one adds context without
	// pinning the whole command line (which differs between the crontab text
	// and what the process table shows).
	switch len(ids) {
	case 0:
		// Program alone. Legal, but rarely unique; `Distinctive` is what
		// decides whether it may actually be used.
	case 1:
		parts = append(parts, regexp.QuoteMeta(ids[0]))
	default:
		parts = append(parts, regexp.QuoteMeta(ids[len(ids)-2]), regexp.QuoteMeta(ids[len(ids)-1]))
	}
	return strings.Join(parts, ".*")
}

// Distinctive reports whether pattern identifies exactly one of commands.
//
// A pattern that matches two of a user's jobs is worse than no pattern: the
// daemon releases a hold when *a* match exits, so the fastest sibling ends
// everyone's window and every duration recorded is wrong. Callers must refuse
// a pattern this rejects rather than registering it hopefully.
func Distinctive(pattern string, commands []string) bool {
	if pattern == "" {
		return false
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	hits := 0
	for _, c := range commands {
		if re.MatchString(c) {
			hits++
		}
	}
	return hits == 1
}

// isIdentifierArg reports whether an argument is a name rather than a flag, a
// path, a redirection, or a fragment of prose.
func isIdentifierArg(f string) bool {
	if f == "" || strings.HasPrefix(f, "-") {
		return false
	}
	if strings.ContainsAny(f, `/><|&$"'`+"`") {
		return false
	}
	// A bare number is a count or a port, not a name.
	if strings.IndexFunc(f, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
		return false
	}
	return true
}

func base(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// lastShellSegment returns the final command of a `cd x && real-command` or
// `a; b` chain, which is the one that actually does the work. Without this the
// pattern anchored on `cd`, which is in every such line and identifies nothing.
func lastShellSegment(command string) string {
	seps := []string{"&&", ";", "||"}
	out := command
	for _, sep := range seps {
		if i := strings.LastIndex(out, sep); i >= 0 {
			out = out[i+len(sep):]
		}
	}
	return strings.TrimSpace(out)
}

// stripQuoted removes single- and double-quoted runs. Prompt text is prose:
// it is long, it changes, and words scraped from it make a pattern that is
// neither stable nor unique.
func stripQuoted(command string) string {
	var b strings.Builder
	var quote rune
	for _, r := range command {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// stripShellWrapping unwraps the `/bin/sh -c "..."` form that crontab and
// launchd entries frequently use, so the suggested pattern describes the real
// program rather than the shell.
func stripShellWrapping(command string) string {
	fields := strings.Fields(command)
	for i := 0; i < len(fields)-1; i++ {
		base := fields[i]
		if j := strings.LastIndexByte(base, '/'); j >= 0 {
			base = base[j+1:]
		}
		if (base == "sh" || base == "bash" || base == "zsh") && fields[i+1] == "-c" {
			rest := strings.Join(fields[i+2:], " ")
			return strings.Trim(rest, `"'`)
		}
	}
	return command
}
