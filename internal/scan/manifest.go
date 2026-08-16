package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Manifest describes an app's own schedule store, declaratively.
//
// The reason this exists rather than another Go file per app: a scheduler that
// lives inside an application is invisible to `crontab -l` and `launchctl
// list`, and there are far more such applications than goguma will ever have
// providers for. Hermes has a hand-written reader; writing one of those for
// every agent runner, note-taking app and self-hosted runner someone might use
// is not a plan, it is a backlog.
//
// A manifest is the same reader expressed as data. Teaching goguma about a new
// app becomes a JSON file the user can write, and shipping support for one
// becomes a file in this repo rather than a release.
//
// Deliberately not a general query language. Dotted field paths cover every
// on-disk schedule store examined while writing this, and anything they cannot
// express is a signal to write a real provider rather than to grow the format.
type Manifest struct {
	// Name is the source name shown in output, like "crontab" or "hermes".
	Name string `json:"name"`

	// Files are where the schedules live. `~` is expanded and globs are
	// allowed, because apps version their support directories.
	Files []string `json:"files"`

	// Jobs is the dotted path to the array of jobs. Empty means the document
	// is itself the array.
	Jobs string `json:"jobs"`

	// Command is what to record as the job's command line. `{name}` and
	// `{id}` are substituted. It is a label, not something goguma runs: these
	// jobs are executed by the application, not by us.
	Command string `json:"command"`

	Fields ManifestFields `json:"fields"`
}

// ManifestFields maps goguma's idea of a job onto the app's field names. Every
// one is a dotted path into a job object, and every one is optional.
type ManifestFields struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	// Enabled and Disabled are two ways of saying the same thing, because
	// apps do it both ways: a boolean `enabled`, or the presence of something
	// like `paused_at`. Disabled wins when both are set.
	Enabled  string `json:"enabled"`
	Disabled string `json:"disabled"`

	// Exactly one of these describes when the job runs.
	Cron  string `json:"cron"`          // "0 9 * * *"
	Every string `json:"every_minutes"` // a number of minutes
	At    string `json:"at"`            // a one-shot RFC3339 timestamp

	// The scheduler's own record of what it has done, which is what makes
	// these jobs exactly timed rather than held for a fixed window.
	LastRun string `json:"last_run"`
	NextRun string `json:"next_run"`
	Status  string `json:"status"`
	Running string `json:"running"`
}

// Validate reports whether a manifest can actually be used, so a typo fails
// when it is loaded rather than silently discovering nothing forever.
func (m Manifest) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("a manifest needs a name")
	}
	for _, reserved := range []string{"crontab", "launchd", "systemd"} {
		if m.Name == reserved {
			return fmt.Errorf("%q is an OS scheduler goguma already reads", m.Name)
		}
	}
	if len(m.Files) == 0 {
		return fmt.Errorf("%s: no files to read", m.Name)
	}
	if m.Fields.Name == "" {
		return fmt.Errorf("%s: fields.name is required, or every job is nameless", m.Name)
	}
	if m.Fields.Cron == "" && m.Fields.Every == "" && m.Fields.At == "" {
		return fmt.Errorf("%s: needs one of fields.cron, fields.every_minutes or fields.at, "+
			"or there is no schedule to wake for", m.Name)
	}
	return nil
}

// manifestProvider is a Provider driven entirely by a Manifest.
type manifestProvider struct {
	m Manifest
}

func (p *manifestProvider) Name() string { return p.m.Name }

func (p *manifestProvider) Where() string {
	paths := p.m.resolveFiles()
	if len(paths) == 0 {
		return shortenHome(p.m.Files[0])
	}
	out := make([]string, 0, len(paths))
	for _, f := range paths {
		out = append(out, shortenHome(f))
	}
	return strings.Join(out, ", ")
}

func (p *manifestProvider) Available() bool { return len(p.m.resolveFiles()) > 0 }

func (p *manifestProvider) Discover(_ context.Context) ([]Entry, error) {
	var out []Entry
	for _, path := range p.m.resolveFiles() {
		jobs, err := p.m.readJobs(path)
		if err != nil {
			return nil, err
		}
		for _, j := range jobs {
			e, ok := p.m.entryFrom(j, path)
			if ok {
				out = append(out, e)
			}
		}
	}
	return out, nil
}

// ObserveRun makes a manifest source exactly timed rather than held for a
// fixed window, the same way the hand-written hermes reader is. An app that
// records when its jobs ran is telling goguma the one thing it cannot observe
// for itself.
func (p *manifestProvider) ObserveRun(_ context.Context, jobID string) (RunRecord, bool) {
	f := p.m.Fields
	if f.LastRun == "" && f.NextRun == "" && f.Running == "" {
		return RunRecord{}, false
	}
	for _, path := range p.m.resolveFiles() {
		jobs, err := p.m.readJobs(path)
		if err != nil {
			continue
		}
		for _, j := range jobs {
			if p.m.idOf(j) != jobID {
				continue
			}
			rec := RunRecord{
				CompletedAt: timeAt(j, f.LastRun),
				NextRun:     timeAt(j, f.NextRun),
				Running:     boolAt(j, f.Running),
			}
			switch strings.ToLower(stringAt(j, f.Status)) {
			case "ok", "success", "succeeded", "completed", "done":
				rec.Status = "ok"
			case "error", "failed", "failure":
				rec.Status = "error"
			}
			return rec, true
		}
	}
	return RunRecord{}, false
}

// resolveFiles expands ~ and globs, keeping only what exists.
func (m Manifest) resolveFiles() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var out []string
	for _, pattern := range m.Files {
		if strings.HasPrefix(pattern, "~/") {
			pattern = filepath.Join(home, pattern[2:])
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, match := range matches {
			if info, serr := os.Stat(match); serr == nil && !info.IsDir() {
				out = append(out, match)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (m Manifest) readJobs(path string) ([]map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", shortenHome(path), err)
	}
	var doc any
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", shortenHome(path), err)
	}

	node := doc
	if m.Jobs != "" {
		node = lookup(doc, m.Jobs)
	}

	switch v := node.(type) {
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			if obj, ok := item.(map[string]any); ok {
				out = append(out, obj)
			}
		}
		return out, nil
	case map[string]any:
		// Keyed by id, which is the other common shape.
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make([]map[string]any, 0, len(v))
		for _, k := range keys {
			if obj, ok := v[k].(map[string]any); ok {
				if _, has := obj["id"]; !has {
					obj["id"] = k
				}
				out = append(out, obj)
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("%s: %q is not a list of jobs", shortenHome(path), m.Jobs)
}

func (m Manifest) idOf(j map[string]any) string {
	if id := stringAt(j, m.Fields.ID); id != "" {
		return id
	}
	// A document keyed by job id has its key injected as "id" by readJobs, and
	// "id" is the overwhelmingly common spelling anyway, so it is worth trying
	// before falling back to the name.
	if id := stringAt(j, "id"); id != "" {
		return id
	}
	return stringAt(j, m.Fields.Name)
}

func (m Manifest) entryFrom(j map[string]any, path string) (Entry, bool) {
	name := stringAt(j, m.Fields.Name)
	if name == "" {
		return Entry{}, false
	}
	// Disabled wins: an app that records both a boolean and a paused
	// timestamp means the timestamp.
	if m.Fields.Disabled != "" && lookup(j, m.Fields.Disabled) != nil {
		return Entry{}, false
	}
	if m.Fields.Enabled != "" && !boolAt(j, m.Fields.Enabled) {
		return Entry{}, false
	}

	sched := m.scheduleOf(j)
	if sched == "" {
		return Entry{}, false
	}

	command := m.Command
	if command == "" {
		command = m.Name + " " + name
	}
	command = strings.ReplaceAll(command, "{name}", name)
	command = strings.ReplaceAll(command, "{id}", m.idOf(j))

	return Entry{
		Name:     name,
		Source:   Source(m.Name),
		Schedule: sched,
		Command:  command,
		File:     shortenHome(path),
		Label:    m.idOf(j),
		LastRun:  timeAt(j, m.Fields.LastRun),
		NextRun:  timeAt(j, m.Fields.NextRun),
		// The application runs these itself, so there is no command line to
		// put a wrapper in front of. Exact timing comes from ObserveRun.
		Wrappable:     false,
		TimeSensitive: true,
	}, true
}

// scheduleOf renders whichever schedule shape the app uses into the forms
// goguma's parser already reads.
func (m Manifest) scheduleOf(j map[string]any) string {
	if expr := stringAt(j, m.Fields.Cron); expr != "" {
		return expr
	}
	if mins := numberAt(j, m.Fields.Every); mins > 0 {
		return "every " + strconv.Itoa(int(mins)) + "m"
	}
	if at := timeAt(j, m.Fields.At); !at.IsZero() {
		// A one-shot in the past is not a schedule to wake for.
		if at.Before(time.Now()) {
			return ""
		}
		return "at " + at.Format(time.RFC3339)
	}
	return ""
}

// ---- dotted-path lookup ----

// lookup walks a dotted path through decoded JSON. Returns nil for a path
// that does not exist, which every caller treats as "the app does not record
// this", because that is what it means.
func lookup(doc any, path string) any {
	if path == "" {
		return nil
	}
	node := doc
	for _, part := range strings.Split(path, ".") {
		obj, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		node, ok = obj[part]
		if !ok {
			return nil
		}
	}
	return node
}

func stringAt(j map[string]any, path string) string {
	switch v := lookup(j, path).(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func numberAt(j map[string]any, path string) float64 {
	switch v := lookup(j, path).(type) {
	case float64:
		return v
	case string:
		n, err := strconv.ParseFloat(v, 64)
		if err == nil {
			return n
		}
	}
	return 0
}

func boolAt(j map[string]any, path string) bool {
	switch v := lookup(j, path).(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1" || v == "active" || v == "enabled"
	case float64:
		return v != 0
	}
	return false
}

// timeAt reads a timestamp in any of the shapes schedule stores actually use.
func timeAt(j map[string]any, path string) time.Time {
	switch v := lookup(j, path).(type) {
	case string:
		for _, layout := range []string{
			time.RFC3339Nano, time.RFC3339,
			"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02",
		} {
			if t, err := time.Parse(layout, v); err == nil {
				return t
			}
		}
	case float64:
		// Unix seconds, or milliseconds past the year 2286 threshold.
		if v > 1e11 {
			return time.UnixMilli(int64(v))
		}
		if v > 0 {
			return time.Unix(int64(v), 0)
		}
	}
	return time.Time{}
}

// ---- loading ----

// LoadManifests registers a provider for every manifest in dir.
//
// Idempotent: manifest providers already registered are replaced, so a daemon
// that reloads config does not end up scanning the same app twice.
//
// A manifest that does not validate is skipped and reported rather than
// failing the load. One bad file the user is midway through writing must not
// stop goguma reading the sources that do work.
func LoadManifests(dir string) (loaded []string, problems []error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, []error{err}
	}
	sort.Strings(matches)

	var keep []Provider
	for _, path := range matches {
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			problems = append(problems, fmt.Errorf("%s: %w", filepath.Base(path), rerr))
			continue
		}
		var m Manifest
		if jerr := json.Unmarshal(b, &m); jerr != nil {
			problems = append(problems, fmt.Errorf("%s: %w", filepath.Base(path), jerr))
			continue
		}
		if verr := m.Validate(); verr != nil {
			problems = append(problems, fmt.Errorf("%s: %w", filepath.Base(path), verr))
			continue
		}
		keep = append(keep, &manifestProvider{m: m})
		loaded = append(loaded, m.Name)
	}

	providersMu.Lock()
	defer providersMu.Unlock()
	var rest []Provider
	for _, p := range providers {
		if _, isManifest := p.(*manifestProvider); !isManifest {
			rest = append(rest, p)
		}
	}
	providers = append(rest, keep...)
	return loaded, problems
}

// PreviewManifest reads a manifest without registering it, so `scheduler add`
// can prove it works before saving it. A manifest that validates but finds
// nothing is the failure worth catching now rather than a week later.
func PreviewManifest(m Manifest) ([]Entry, error) {
	return (&manifestProvider{m: m}).Discover(context.Background())
}
