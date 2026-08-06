package scan

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Hint is a file that looks like it might define schedules, found by
// searching the filesystem rather than by reading a known registry.
//
// Hints are deliberately NOT jobs. They are never registered automatically and
// never counted as discovered work. They answer one question a registry-based
// scan cannot: "is there a scheduler here that WakeGuard does not know about?"
//
// The distinction matters because finding schedules by searching is unreliable
// in both directions. Mentioning cron is not the same as running cron: a
// README describing how to install a job, a crontab committed to a repository
// for a different machine, a commented-out line, and a test fixture all
// contain valid cron syntax and schedule nothing here. Conversely a scheduler
// storing "0 9 * * *" in a JSON field mentions cron nowhere obvious.
type Hint struct {
	Path string
	// Why describes the structural evidence, not a keyword match.
	Why string
	// Live reports whether this file is known to be in effect. Most hints
	// cannot be verified either way, and saying so is the point — an
	// unverified hint must never be presented as a found job.
	Live Liveness
	// Sample is one matching line so the user can judge it themselves.
	Sample string
}

// Liveness is whether a hint is actually in effect.
type Liveness string

const (
	LiveYes     Liveness = "in effect"
	LiveNo      Liveness = "not installed"
	LiveUnknown Liveness = "cannot tell"
)

// cronLine matches a five-field schedule followed by something command-shaped.
//
// The trailing group is what makes this usable. Requiring only five numeric
// fields matches far too much — "1 2 3 4 5 foo" is syntactically valid cron —
// so the command must additionally look like a command: an absolute path, a
// relative path, or a bare program name possibly with arguments.
var cronLine = regexp.MustCompile(
	`^\s*[\d*/,\-]+\s+[\d*/,\-]+\s+[\d*/,\-]+\s+[\d*/,\-]+\s+[\d*/,\-]+\s+` +
		`([~./]?[\w./\-]*/[\w.\-]+|[a-zA-Z][\w.\-]{2,})(\s|$)`)

// cronDescriptor matches only the real @descriptors.
//
// An earlier version accepted any line beginning with "@", which matched
// Python decorators, email addresses, and assembly directives. The set is
// small and closed, so it is listed rather than guessed at.
var cronDescriptor = regexp.MustCompile(
	`^@(reboot|yearly|annually|monthly|weekly|daily|midnight|hourly|every)\b`)

// scheduleDirNames are directory names that plausibly hold schedule
// definitions. A file is only content-inspected if its name looks like a
// crontab or it sits directly inside one of these.
var scheduleDirNames = map[string]bool{
	"cron": true, "crontab": true, "crontabs": true,
	"cron.d": true, "schedules": true, "timers": true,
}

// skipDirs are never descended into.
//
// Dependency trees, caches, and language runtimes contain enormous numbers of
// cron libraries, fixtures, and vendored examples. Including them buries any
// real finding — an unfiltered search returns hundreds of files on an ordinary
// machine, essentially none of which schedule anything.
var skipDirs = map[string]bool{
	"node_modules": true, ".git": true, "Caches": true, ".cache": true,
	"venv": true, ".venv": true, "site-packages": true, "vendor": true,
	"Library": true, ".Trash": true, "dist": true, "build": true,
	".build": true, "target": true, "__pycache__": true, ".mypy_cache": true,
	"testdata": true, "fixtures": true, "node": true, "bin": true,
	"sbin": true, "lib": true, "share": true, "Applications": true,
	"Pictures": true, "Music": true, "Movies": true, ".npm": true,
	".rustup": true, ".cargo": true, "go": true,
}

// FindSchedulerHints searches roots for files that look like schedule
// definitions.
//
// Scope is deliberately narrow. Content-scanning every readable file is both
// slow and dominated by false positives; a first attempt at that flagged a
// binary executable, a gdb init file, and a maths PDF before reaching any real
// schedule. Only crontab-named files and files inside schedule-shaped
// directories are opened.
func FindSchedulerHints(ctx context.Context, roots []string, maxDepth, maxHints int) []Hint {
	var hints []Hint
	seen := map[string]bool{}

	for _, root := range roots {
		rootDepth := strings.Count(filepath.Clean(root), string(os.PathSeparator))

		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable entries are skipped, not fatal
			}
			if ctx.Err() != nil || len(hints) >= maxHints {
				return filepath.SkipAll
			}

			name := d.Name()
			if d.IsDir() {
				if skipDirs[name] {
					return filepath.SkipDir
				}
				// Hidden directories are skipped except the handful that
				// legitimately hold user configuration.
				if strings.HasPrefix(name, ".") && len(name) > 1 &&
					!allowedHiddenDir(name) {
					return filepath.SkipDir
				}
				if strings.Count(filepath.Clean(path), string(os.PathSeparator))-rootDepth > maxDepth {
					return filepath.SkipDir
				}
				return nil
			}

			if seen[path] || !worthInspecting(path, name) {
				return nil
			}
			if h, ok := inspectFile(path, d, name); ok {
				seen[path] = true
				hints = append(hints, h)
			}
			return nil
		})
	}
	return hints
}

func allowedHiddenDir(name string) bool {
	switch name {
	case ".config", ".local", ".hermes", ".claude", ".openclaude":
		return true
	}
	return false
}

// worthInspecting decides whether a file is even opened.
func worthInspecting(path, name string) bool {
	if isCrontabNamed(name) {
		return true
	}
	// Otherwise only files sitting directly inside a schedule-shaped
	// directory, and only ones with a plausible config extension.
	parent := filepath.Base(filepath.Dir(path))
	if !scheduleDirNames[strings.ToLower(parent)] {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case "", ".txt", ".conf", ".cfg", ".tab", ".cron", ".crontab":
		return true
	}
	return false
}

func isCrontabNamed(name string) bool {
	lower := strings.ToLower(name)
	return lower == "crontab" || strings.HasSuffix(lower, ".cron") ||
		strings.HasSuffix(lower, ".crontab")
}

// inspectFile decides whether a file is worth mentioning.
func inspectFile(path string, d os.DirEntry, name string) (Hint, bool) {
	info, err := d.Info()
	if err != nil || info.Size() > 256<<10 {
		// Large files are logs or data, not schedule definitions.
		return Hint{}, false
	}

	f, err := os.Open(path)
	if err != nil {
		return Hint{}, false
	}
	defer f.Close()

	// Reject binaries before treating the contents as text. An executable
	// with no extension is otherwise happily scanned, and its bytes produce
	// both nonsense matches and unprintable output.
	head := make([]byte, 512)
	n, _ := f.Read(head)
	if bytes.IndexByte(head[:n], 0) >= 0 {
		return Hint{}, false
	}
	if _, err := f.Seek(0, 0); err != nil {
		return Hint{}, false
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 8<<10), 64<<10)
	lines, active := 0, 0
	var sample string

	for sc.Scan() && lines < 500 {
		lines++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if cronLine.MatchString(line) || cronDescriptor.MatchString(line) {
			active++
			if sample == "" {
				sample = line
			}
		}
	}

	switch {
	case active > 0:
		return Hint{
			Path:   path,
			Why:    plural(active, "line") + " with cron-style schedule syntax",
			Live:   LiveUnknown,
			Sample: sample,
		}, true
	case isCrontabNamed(name):
		// A crontab-named file with no active entries is worth surfacing
		// precisely because it looks convincing and schedules nothing.
		return Hint{
			Path: path,
			Why:  "named like a crontab but contains no active entries",
			Live: LiveNo,
		}, true
	}
	return Hint{}, false
}

// VerifyAgainstInstalledCrontab marks hints whose content matches the user's
// actually-installed crontab.
//
// This is the check that separates a live schedule from a dormant file. A
// crontab committed to a project, or written for a different machine, is
// indistinguishable from a real one by content alone; only comparing against
// what is installed settles it.
func VerifyAgainstInstalledCrontab(ctx context.Context, hints []Hint) []Hint {
	installed, err := Crontab(ctx)
	if err != nil || len(installed) == 0 {
		// Nothing is installed, so no cron-syntax hint can be live.
		for i := range hints {
			if hints[i].Live == LiveUnknown {
				hints[i].Live = LiveNo
			}
		}
		return hints
	}

	live := map[string]bool{}
	for _, e := range installed {
		live[e.Schedule+" "+e.Command] = true
	}
	for i := range hints {
		if hints[i].Sample == "" {
			continue
		}
		if live[strings.Join(strings.Fields(hints[i].Sample), " ")] {
			hints[i].Live = LiveYes
		} else if hints[i].Live == LiveUnknown {
			hints[i].Live = LiveNo
		}
	}
	return hints
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return itoa(n) + " " + noun + "s"
}
