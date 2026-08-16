package cli

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/junnam586/goguma/internal/detect"
	"github.com/junnam586/goguma/internal/ipc"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/schedule"
)

// stdinReader is shared so a prompt does not lose buffered input between
// questions, which bufio.NewScanner(os.Stdin) per call would.
var stdinReader = bufio.NewReader(os.Stdin)

// interactivePossible reports whether prompting makes sense.
//
// A piped or redirected stdin means this is a script, where blocking on a
// question would hang a cron line or a CI step forever. Those callers get the
// usage error instead.
func interactivePossible() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func ask(prompt string) (string, error) {
	fmt.Fprint(os.Stdout, prompt)
	line, err := stdinReader.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("cancelled")
	}
	return strings.TrimSpace(line), nil
}

// promptForJob fills in whatever the user did not pass as flags.
//
// Deliberately conversational about the schedule: the whole reason someone
// runs `add` without flags is that they know when they want the job to run but
// not how to express it in cron. Whatever they type is echoed back as both the
// cron expression and the next concrete fire time, so a misreading is caught
// before it is saved rather than at 3am.
func promptForJob(ctx *Context, name, cron, detection, match, command *string) error {
	r := ctx.Out

	r.Line(r.Bold("Registering a job for goguma to wake the machine for."))
	r.Line(r.Muted("goguma doesn't run the job, whatever runs it now still does."))
	r.Blank()

	if *name == "" {
		v, err := ask("  What should this job be called?  ")
		if err != nil {
			return err
		}
		if v == "" {
			return fmt.Errorf("a name is required")
		}
		*name = v
	}

	if *cron == "" {
		r.Line(r.Muted("  When should it run? Plain English is fine, for example:"))
		for _, ex := range schedule.HumanExamples()[:4] {
			r.Printf("    %s\n", r.Muted(ex))
		}
		for {
			v, err := ask("  When?  ")
			if err != nil {
				return err
			}
			if v == "" {
				return fmt.Errorf("a schedule is required")
			}
			// Plain Parse, not ParseAt: there is no job yet, so there is no
			// CreatedAt to anchor an interval schedule to. The process-start
			// anchor is within seconds of the CreatedAt `add` is about to
			// assign, which is close enough for a preview and, unlike the
			// call time, does not move while the user is still typing.
			s, perr := schedule.Parse(v, time.Local)
			if perr != nil {
				r.Printf("    %s %s\n", r.Warn(r.Sym().Warn),
					"I couldn't read that as a schedule.")
				r.Printf("    %s\n", r.Muted("try: "+strings.Join(schedule.HumanExamples(), " · ")))
				continue
			}
			// Confirm the interpretation before it is committed.
			next := s.Next(time.Now())
			r.Printf("    %s %s  %s\n", r.Good(r.Sym().OK), s.Display,
				r.Muted("("+s.Expr+")"))
			r.Printf("      %s\n", r.Muted("next run "+next.Format("Mon 2 Jan 15:04")+
				"  "+model.HumanUntil(next, time.Now())))
			*cron = s.Expr
			break
		}
	}

	if *command == "" {
		v, err := ask("  What command does it run? (optional, press enter to skip)  ")
		if err != nil {
			return err
		}
		*command = v
	}

	// Detection is the choice with real consequences, so it is explained in
	// terms of what the user gets rather than by mode name.
	if *detection == "mark" && *match == "" {
		r.Blank()
		r.Line(r.Muted("  How should goguma tell when the job is running?"))
		// Padded on the unstyled label: a styled string carries escape bytes
		// that %-24s would count, collapsing the column.
		opt := func(key, label, note string) {
			pad := strings.Repeat(" ", max(26-len(label), 1))
			r.Printf("    %s%s%s\n", r.Accent(key+" "+label), pad, r.Muted(note))
		}
		opt("[1]", "it announces itself", "exact, one edit to how the job is launched")
		opt("[2]", "watch for a process", "no edits, but can't see exit codes")
		opt("[3]", "just wake, don't watch", "for jobs run inside another app")
		v, err := ask("  Choice [1/2/3, default 1]:  ")
		if err != nil {
			return err
		}
		switch v {
		case "", "1":
			*detection = string(model.DetectMark)
		case "2":
			*detection = string(model.DetectPattern)
			if err := promptForMatch(ctx, *command, match); err != nil {
				return err
			}
		case "3":
			*detection = string(model.DetectNone)
		default:
			return fmt.Errorf("please answer 1, 2, or 3")
		}
	}
	r.Blank()
	return nil
}

// promptForMatch collects a process pattern and tests it live.
//
// Testing at configuration time is the whole point: a pattern that matches
// nothing is the single most common way a job ends up registered, looking
// correct, and silently never detected.
func promptForMatch(ctx *Context, command string, match *string) error {
	r := ctx.Out

	suggested := ""
	if command != "" {
		suggested = detect.SuggestPattern(command)
	}
	prompt := "  Pattern to watch for:  "
	if suggested != "" {
		r.Printf("    %s\n", r.Muted("suggested from the command: "+suggested))
		prompt = "  Pattern [enter to accept the suggestion]:  "
	}

	v, err := ask(prompt)
	if err != nil {
		return err
	}
	if v == "" {
		v = suggested
	}
	if v == "" {
		return fmt.Errorf("a match pattern is required for process watching")
	}
	*match = v

	// A pattern that also matches another registered job's command is worse
	// than none: the daemon releases a hold when *a* match exits, so the
	// fastest sibling ends this job's window and every recorded duration is
	// wrong. Import checks its whole scan for this; the interactive path
	// checked nothing, so registering two sibling jobs one at a time
	// accepted the collision. Best-effort: with the daemon down the check
	// degrades to nothing, same as every other daemon-backed nicety here.
	var jobs ipc.JobsListResp
	if err := callDaemon(ctx, ipc.OpJobsList, nil, &jobs); err == nil {
		if re, reErr := regexp.Compile(v); reErr == nil {
			for _, jv := range jobs.Jobs {
				if jv.Job.Command == "" || jv.Job.Command == command {
					continue
				}
				if re.MatchString(jv.Job.Command) {
					return fmt.Errorf(
						"that pattern also matches the job %q (%s); a shared pattern "+
							"would release this hold when the wrong job exits",
						jv.Job.Name, jv.Job.Command)
				}
			}
		}
	}

	var resp ipc.MatchTestResp
	if err := callDaemon(ctx, ipc.OpTestMatch, ipc.MatchTestReq{Pattern: v}, &resp); err != nil {
		return nil // the daemon will validate on save
	}
	switch {
	case !resp.Valid:
		return fmt.Errorf("that isn't a valid pattern: %s", resp.Error)
	case len(resp.Matches) == 0:
		r.Printf("    %s\n", r.Muted(
			"nothing matches right now, expected if the job isn't running"))
	default:
		r.Printf("    %s matches %d process(es) right now\n",
			r.Good(r.Sym().OK), len(resp.Matches))
	}
	return nil
}
