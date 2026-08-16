package scan

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Crontab discovers entries in the user's crontab.
//
// Returns no error when there is simply no crontab; that is the normal case
// on macOS, where `crontab -l` exits non-zero with "no crontab for <user>".
// Treating it as a failure would make `import` look broken on a machine whose
// automation all lives in launchd.
func Crontab(ctx context.Context) ([]Entry, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "crontab", "-l").Output()
	if err != nil {
		return nil, nil
	}
	return ParseCrontab(string(out)), nil
}

// ParseCrontab extracts schedulable entries from crontab text.
func ParseCrontab(text string) []Entry {
	var entries []Entry
	lineNo := 0
	for _, raw := range strings.Split(text, "\n") {
		lineNo++
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Environment assignments (PATH=..., MAILTO=...) are not jobs. A bare
		// `=` before any whitespace is the distinguishing feature.
		if eq := strings.IndexByte(line, '='); eq > 0 && !strings.ContainsAny(line[:eq], " \t") {
			continue
		}

		sched, cmd, ok := splitCrontabLine(line)
		if !ok {
			continue
		}
		entries = append(entries, Entry{
			Name:     nameFromCommand(cmd),
			Source:   SourceCrontab,
			Schedule: sched,
			Command:  cmd,
			File:     "crontab",
			Line:     lineNo,
			// The user owns this line and can edit it.
			Wrappable:     true,
			TimeSensitive: !strings.HasPrefix(sched, "@") || sched == "@daily" || sched == "@midnight",
			// @reboot has no fire time to wake for; the machine is by
			// definition already awake when it runs.
			SelfHealing: strings.HasPrefix(sched, "@reboot"),
		})
	}
	return entries
}

// splitCrontabLine separates the schedule from the command.
func splitCrontabLine(line string) (sched, cmd string, ok bool) {
	if strings.HasPrefix(line, "@") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return "", "", false
		}
		return fields[0], strings.TrimSpace(strings.TrimPrefix(line, fields[0])), true
	}

	// Five schedule fields, then the command. Fields can contain commas,
	// ranges, and steps but never spaces, so splitting on whitespace and
	// taking the first five is correct for standard crontab syntax.
	fields := strings.Fields(line)
	if len(fields) < 6 {
		return "", "", false
	}

	// Refuse a line whose sixth token is also a schedule field.
	//
	// Some cron implementations and many task schedulers use a six-field form
	// with leading seconds. Splitting such a line at five fields produces a
	// schedule that is *silently wrong but still valid*: "0 0 9 * * * cmd"
	// becomes "0 0 9 * *", which parses fine and means 00:00 on the 9th of
	// each month rather than 09:00:00 daily. goguma would then wake the
	// machine at confidently incorrect times.
	//
	// A real command essentially never consists only of digits and cron
	// punctuation, so treating that shape as ambiguous costs nothing and
	// converts a wrong answer into a visible one.
	if looksLikeCronField(fields[5]) {
		return "", "", false
	}

	sched = strings.Join(fields[:5], " ")

	// Recover the command with its original internal spacing, which matters
	// because it is what gets rewritten into the crontab.
	rest := line
	for i := range 5 {
		idx := strings.Index(rest, fields[i])
		rest = rest[idx+len(fields[i]):]
	}
	return sched, strings.TrimSpace(rest), true
}

// looksLikeCronField reports whether a token could be a schedule field rather
// than the start of a command.
func looksLikeCronField(tok string) bool {
	if tok == "" {
		return false
	}
	for _, r := range tok {
		switch {
		case r >= '0' && r <= '9':
		case r == '*' || r == '/' || r == ',' || r == '-':
		default:
			return false
		}
	}
	return true
}

// nameFromCommand derives a readable job name from a command line.
func nameFromCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	// Drop shell redirection, which is noise for naming purposes.
	if i := strings.IndexAny(cmd, ">|"); i > 0 {
		cmd = strings.TrimSpace(cmd[:i])
	}
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return "job"
	}

	prog := basename(fields[0])
	// A bare interpreter name says nothing useful; prefer the script it runs.
	if isInterpreter(prog) && len(fields) > 1 {
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "-") {
				continue
			}
			if base := basename(f); base != "" {
				return base
			}
		}
	}

	// A distinctive subcommand makes a much better name than the program
	// alone: "hermes cron run morning-briefing" should not be called "hermes".
	parts := []string{prog}
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "-") || strings.ContainsAny(f, "/><|&$\"'`=") {
			continue
		}
		parts = append(parts, f)
		if len(parts) >= 3 {
			break
		}
	}
	return strings.Join(parts, "-")
}

// basename strips the directory and any script extension.
//
// Applied uniformly whether the script was invoked directly or through an
// interpreter, so "/opt/backup.sh" and "sh -c /opt/backup.sh" produce the same
// job name. Without that, the same job discovered two different ways would get
// two different names and two separate duration histories.
func basename(path string) string {
	base := path
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	for _, ext := range []string{".sh", ".py", ".rb", ".js", ".pl", ".bash", ".zsh"} {
		if strings.HasSuffix(base, ext) {
			return strings.TrimSuffix(base, ext)
		}
	}
	return base
}

func isInterpreter(name string) bool {
	switch name {
	case "sh", "bash", "zsh", "python", "python3", "ruby", "node", "perl", "env":
		return true
	}
	return false
}

// ReplaceCrontabCommand rewrites one line's command, leaving everything else
// byte for byte alone.
//
// This is the transform behind `import` editing the crontab for the user
// instead of printing a line and asking them to paste it. It is deliberately
// pure and deliberately fussy: comments, blank lines, PATH= assignments and
// every other job must survive untouched, and the caller must be able to prove
// what changed before installing it.
//
// `want` is the command the scan saw. It is checked rather than trusted,
// because the crontab can be edited between the scan and the write, and
// rewriting whatever now sits on line N would clobber a job the user just
// added.
func ReplaceCrontabCommand(text string, line int, want, replacement string) (string, error) {
	if line < 1 {
		return "", fmt.Errorf("line %d is not a line number", line)
	}
	lines := strings.Split(text, "\n")
	if line > len(lines) {
		return "", fmt.Errorf("the crontab has %d lines, so line %d is not in it anymore", len(lines), line)
	}

	raw := lines[line-1]
	sched, cmd, ok := splitCrontabLine(strings.TrimSpace(raw))
	if !ok {
		return "", fmt.Errorf("line %d is not a job any more", line)
	}
	if cmd != want {
		return "", fmt.Errorf("line %d now runs %q, not %q; the crontab changed since goguma read it", line, cmd, want)
	}

	// The indent is kept because it is the user's, not ours.
	indent := raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))]
	lines[line-1] = indent + sched + " " + replacement
	return strings.Join(lines, "\n"), nil
}
