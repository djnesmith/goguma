package daemon

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/junnam/wakeguard/internal/model"
	"github.com/junnam/wakeguard/internal/power"
	"github.com/junnam/wakeguard/internal/scan"
	"github.com/junnam/wakeguard/internal/schedule"
	"github.com/junnam/wakeguard/internal/store"
)

// syncProviders adopts and retires jobs from watched scheduler sources.
//
// This exists because the moment a job is created is almost never a moment
// anyone is thinking about sleep. Someone asks their agent for a morning
// briefing; the agent writes a schedule into its own store; nothing prompts
// anyone to also tell WakeGuard. Without watching, coverage depends on the
// user remembering to re-run `import` after every change — which is exactly
// the kind of silent gap this tool exists to close.
//
// On by default for every adoptable source. Installing WakeGuard is itself the
// statement that jobs should survive sleep, and requiring a second opt-in
// mostly produces installs that quietly do nothing. The safeguard is
// visibility — what was adopted and what it costs is reported — rather than a
// switch the user has to discover.
func (d *Daemon) syncProviders(ctx context.Context) {
	d.mu.RLock()
	sources := effectiveAutoAdopt(d.cfg.AutoAdopt)
	cfg := d.cfg
	d.mu.RUnlock()

	if len(sources) == 0 {
		return
	}

	entries, _ := scan.DiscoverAll(ctx)

	// Miss-risk needs sleep history, but adoption should not be blocked on it
	// — an empty history simply means risk is unknown, which the filter treats
	// as "keep" rather than "discard".
	hist := d.cachedSleepHistory()

	opts := scan.DefaultOptions()
	opts.MinInterval = cfg.MinImportInterval.D()
	keep, _ := scan.Evaluate(entries, hist, time.Now(), opts)

	d.adoptNew(keep, sources)
	d.retireVanished(entries, sources)
}

// sleepHistoryTTL is how long a reconstructed sleep history is reused.
//
// The underlying read is expensive and the data spans two weeks, so a stale
// half hour changes no decision it feeds. The daemon also records sleep gaps
// itself as they happen, so recent sleep is not invisible in the meantime.
const sleepHistoryTTL = 30 * time.Minute

// cachedSleepHistory returns the sleep history, re-reading only when stale.
func (d *Daemon) cachedSleepHistory() *schedule.SleepHistory {
	d.mu.RLock()
	cached, at := d.sleepHist, d.sleepHistAt
	d.mu.RUnlock()

	if cached != nil && time.Since(at) < sleepHistoryTTL {
		return cached
	}

	hist, err := d.plat.SleepHistory(14 * 24 * time.Hour)
	if err != nil || hist == nil {
		if cached != nil {
			return cached // a failed refresh should not discard good data
		}
		hist = &schedule.SleepHistory{}
	}

	d.mu.Lock()
	d.sleepHist, d.sleepHistAt = hist, time.Now()
	d.mu.Unlock()
	return hist
}

// adoptNew registers candidates from watched sources that are not yet known.
func (d *Daemon) adoptNew(candidates []scan.Candidate, sources []string) {
	var uncovered []scan.Candidate
	existing := map[string]bool{}
	for _, j := range d.store.Jobs() {
		existing[j.ID] = true
	}

	for _, c := range candidates {
		if !slices.Contains(sources, string(c.Source)) {
			continue
		}
		id := model.Slug(c.Name)
		if existing[id] {
			continue
		}

		// Only adopt what can work with no further human action.
		//
		// A wrappable job needs its command line edited to be detectable.
		// Adopting one automatically would create a job that is registered,
		// looks correct, and is recorded as never-detected on every single
		// run — generating a permanent warning about a configuration nobody
		// chose. Those stay a deliberate `import` decision.
		if c.Wrappable {
			// Not adopted — but no longer forgotten. Skipping silently meant a
			// user with a crontab full of jobs saw a healthy WakeGuard and a
			// Mac that still slept through every one of them.
			uncovered = append(uncovered, c)
			continue
		}

		job := &model.Job{
			ID:        id,
			Name:      c.Name,
			Schedule:  c.Schedule,
			Command:   c.Command,
			Source:    string(c.Source),
			Detection: model.DetectNone,
			Enabled:   true,
			Managed:   true,
			CreatedAt: time.Now(),
		}
		if err := job.Validate(); err != nil {
			d.log.Warn("skipped adopting an invalid job", "job", c.Name, "err", err)
			continue
		}
		if err := d.store.Add(job); err != nil {
			d.log.Warn("could not adopt job", "job", c.Name, "err", err)
			continue
		}

		d.log.Info("adopted a new job from a watched scheduler",
			"job", id, "source", c.Source, "schedule", c.Schedule)
		d.event(store.Event{
			Kind: store.EventWindowOpened, JobID: id, JobName: c.Name,
			Message: "adopted automatically from " + string(c.Source),
		})
		existing[id] = true
	}

	d.mu.Lock()
	d.uncovered = uncovered
	d.mu.Unlock()
}

// retireVanished removes managed jobs whose source entry is gone.
//
// Only jobs WakeGuard adopted itself are retired. A hand-registered job is
// left alone even if it looks unmatched, because a human's explicit intent
// should never be undone by a scan that might simply have failed to read a
// source this time.
func (d *Daemon) retireVanished(entries []scan.Entry, sources []string) {
	present := map[string]bool{}
	sawSource := map[string]bool{}
	for _, e := range entries {
		present[model.Slug(e.Name)] = true
		sawSource[string(e.Source)] = true
	}

	for _, j := range d.store.Jobs() {
		if !j.Managed || !slices.Contains(sources, j.Source) {
			continue
		}
		// If the source produced nothing at all this pass, treat it as a read
		// failure rather than as every job having been deleted. Otherwise a
		// momentarily unreadable file would retire the user's whole set.
		if !sawSource[j.Source] {
			continue
		}
		if present[j.ID] {
			continue
		}

		if _, err := d.store.Remove(j.ID); err != nil {
			d.log.Warn("could not retire a vanished job", "job", j.ID, "err", err)
			continue
		}
		d.releaseJob(j.ID)
		d.log.Info("retired a job that no longer exists in its source",
			"job", j.ID, "source", j.Source)
	}
}

// SyncNow runs a sync immediately, for `wakeguard sync`.
func (d *Daemon) SyncNow(ctx context.Context) int {
	before := len(d.store.Jobs())
	d.syncProviders(ctx)
	after := len(d.store.Jobs())
	d.afterJobChange()
	return after - before
}

// effectiveAutoAdopt resolves the configured value.
//
// A nil list means the user has never expressed a preference, which becomes
// every adoptable source. An explicitly empty list is a deliberate "off" and
// is left alone — the two are distinguishable precisely so that turning the
// feature off is not silently undone on the next start.
func effectiveAutoAdopt(configured []string) []string {
	if configured == nil {
		return adoptableSources()
	}
	return slices.Clone(configured)
}

// EffectiveAutoAdopt is the exported form, for the CLI to report what is
// actually being watched rather than what is literally in the file.
func EffectiveAutoAdopt(configured []string) []string {
	return effectiveAutoAdopt(configured)
}

// adoptableSources lists sources that can be watched.
//
// A source is adoptable only if its jobs need no further human action, which
// in practice means the application runs them itself. Crontab and launchd
// entries are excluded: covering those properly requires editing a command
// line, which is a decision only the user can make.
func adoptableSources() []string {
	var out []string
	for _, p := range scan.Providers() {
		switch p.Name() {
		case "crontab", "launchd", "systemd":
			continue
		}
		out = append(out, p.Name())
	}
	return out
}

// AdoptableSources is the exported form, for CLI help and validation.
func AdoptableSources() []string { return adoptableSources() }

// validateAutoAdopt rejects sources that cannot be watched, so a typo or an
// unsupported choice fails at configuration time rather than silently doing
// nothing forever.
//
// Turning the feature off returns an empty but NON-nil slice. nil means "never
// configured" and expands to every adoptable source, so returning nil here
// would make `config set auto_adopt off` switch it on — the exact inversion of
// what the user asked for.
//
// "all" is the inverse: it returns nil, restoring the unconfigured default.
// Without it the setting is a one-way door over IPC — a client could turn
// watching off but never fully back on, only approximate it by naming the
// sources it happened to know about. That approximation silently narrows as
// schedulers are added, so a UI toggle would quietly stop covering things it
// used to.
func validateAutoAdopt(v string) ([]string, error) {
	if strings.TrimSpace(v) == "" {
		return []string{}, nil
	}
	if s := strings.ToLower(strings.TrimSpace(v)); s == "all" || s == "default" || s == "auto" {
		return nil, nil
	}
	allowed := adoptableSources()
	var out []string
	for _, s := range strings.Split(v, ",") {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		if s == "none" || s == "off" {
			return []string{}, nil
		}
		if !slices.Contains(allowed, s) {
			return nil, errUnadoptable(s, allowed)
		}
		out = append(out, s)
	}
	return out, nil
}

var _ = power.State{}
