package daemon

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/detect"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/power"
	"github.com/junnam586/goguma/internal/scan"
	"github.com/junnam586/goguma/internal/schedule"
	"github.com/junnam586/goguma/internal/store"
)

// syncProviders adopts and retires jobs from watched scheduler sources.
//
// This exists because the moment a job is created is almost never a moment
// anyone is thinking about sleep. Someone asks their agent for a morning
// briefing; the agent writes a schedule into its own store; nothing prompts
// anyone to also tell goguma. Without watching, coverage depends on the
// user remembering to re-run `import` after every change, which is exactly
// the kind of silent gap this tool exists to close.
//
// On by default for every adoptable source. Installing goguma is itself the
// statement that jobs should survive sleep, and requiring a second opt-in
// mostly produces installs that quietly do nothing. The safeguard is
// visibility (what was adopted and what it costs is reported) rather than a
// switch the user has to discover.
func (d *Daemon) syncProviders(ctx context.Context) (adopted, updated, retired int) {
	d.mu.RLock()
	sources := effectiveAutoAdopt(d.cfg.AutoAdopt)
	cfg := d.cfg
	d.mu.RUnlock()

	if len(sources) == 0 {
		return 0, 0, 0
	}

	entries, coverage := scan.DiscoverAll(ctx)

	// Miss-risk needs sleep history, but adoption should not be blocked on it:
	// an empty history simply means risk is unknown, which the filter treats
	// as "keep" rather than "discard".
	hist := d.cachedSleepHistory()

	opts := scan.DefaultOptions()
	opts.MinInterval = cfg.MinImportInterval.D()
	keep, _ := scan.Evaluate(entries, hist, time.Now(), opts)

	adopted = d.adoptNew(keep, sources)
	updated, freqRetired := d.updateChanged(entries, sources, cfg)
	retired = d.retireVanished(entries, coverage, sources) + freqRetired
	return adopted, updated, retired
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

	// Merge in the daemon's own observed sleep log. It is written exactly for
	// machines whose OS log is unreadable (no journalctl, log rotated away),
	// but it was write-only: the documented fallback did not exist, and on
	// those machines miss-risk stayed unknowable forever. Overlap with the OS
	// log is harmless; AsleepAt and the replay treat intervals as a union.
	if rec, recErr := d.store.RecordedSleep(); recErr == nil && len(rec) > 0 {
		cutoff := time.Now().Add(-14 * 24 * time.Hour)
		for _, iv := range rec {
			if iv.Wake.After(cutoff) {
				hist.Intervals = append(hist.Intervals, iv)
			}
		}
		sort.Slice(hist.Intervals, func(i, j int) bool {
			return hist.Intervals[i].Sleep.Before(hist.Intervals[j].Sleep)
		})
		if first := rec[0].Sleep; !first.Before(cutoff) &&
			(hist.Since.IsZero() || first.Before(hist.Since)) {
			hist.Since = first
		}
	}

	d.mu.Lock()
	d.sleepHist, d.sleepHistAt = hist, time.Now()
	d.mu.Unlock()
	return hist
}

// adoptNew registers candidates from watched sources that are not yet known.
func (d *Daemon) adoptNew(candidates []scan.Candidate, sources []string) (adopted int) {
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

		// Pick the best detection that needs nothing from the user.
		//
		// Never `mark` here. That one requires the command line to be edited,
		// and adopting a job as mark-detected without editing it produces a
		// job that looks configured and is recorded never-detected on every
		// run, which is a permanent warning about a choice nobody made. It
		// stays a deliberate `import` decision.
		//
		// Pattern is real detection and costs the user nothing, so it wins
		// whenever the pattern identifies this job and nothing else. Distinctive
		// is checked against every other candidate and every registered command,
		// because a pattern that also matches a sibling is worse than none: the
		// daemon releases on the first match to exit, so the fastest sibling
		// closes everyone's window and every duration recorded is wrong.
		//
		// Wake-only is the floor. It observes nothing and holds a bounded
		// window, which is exactly the honest behaviour for a job that cannot
		// be watched, and still the difference between the job running and the
		// machine sleeping through it.
		detection := model.DetectNone
		match := ""
		if pattern := detect.SuggestPattern(c.Command); pattern != "" {
			if detect.Distinctive(pattern, adoptionSiblings(c, candidates, d.store.Jobs())) {
				detection = model.DetectPattern
				match = pattern
			}
		}

		// Still reported, but now as "could be exact" rather than "not
		// covered". The job is already being woken for; `import` upgrades it
		// from a bounded window to its real runtime.
		if c.Wrappable && detection != model.DetectPattern {
			uncovered = append(uncovered, c)
		}

		job := &model.Job{
			ID:        id,
			Name:      c.Name,
			Schedule:  c.Schedule,
			Command:   c.Command,
			Source:    string(c.Source),
			Detection: detection,
			Match:     match,
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
		adopted++
	}

	d.mu.Lock()
	d.uncovered = uncovered
	d.mu.Unlock()
	return adopted
}

// retiredJob is one dropped job, kept only long enough to be reported.
type retiredJob struct {
	Name string
	At   time.Time
}

// retiredNoticeWindow is how long a retirement is worth mentioning to someone
// who never opens the app.
//
// A backstop, not the mechanism. With the menu bar app installed the notice is
// cleared the moment the popover that showed it closes, which is the honest
// answer to "has this been seen". This window exists for a CLI-only machine,
// where nothing can report having displayed anything.
//
// A day. Long enough that someone running `status` in the course of an
// ordinary day still catches it, and short enough that nobody is told the same
// thing on a second morning.
const retiredNoticeWindow = 24 * time.Hour

// ackNotices drops every pending one-off notice.
//
// The time window below is a backstop for someone who only ever uses the CLI.
// With the app installed this is what actually clears them, the moment the
// popover that showed them closes.
func (d *Daemon) ackNotices() {
	d.mu.Lock()
	d.retired = nil
	d.mu.Unlock()
}

// recentRetirements returns the notices still inside the window, pruning the
// rest as it goes.
func (d *Daemon) recentRetirements(now time.Time) []retiredJob {
	d.mu.Lock()
	defer d.mu.Unlock()
	kept := d.retired[:0]
	for _, r := range d.retired {
		if now.Sub(r.At) < retiredNoticeWindow {
			kept = append(kept, r)
		}
	}
	d.retired = kept
	return slices.Clone(kept)
}

// adoptionSiblings is every other command a suggested pattern must not match:
// the rest of this scan, plus everything already registered.
//
// Both halves matter. Checking only the scan misses a collision with a job the
// user added by hand, and checking only the store misses two candidates in the
// same batch that share a pattern, where whichever is adopted first would
// silently claim the other's process.
func adoptionSiblings(self scan.Candidate, batch []scan.Candidate, registered []*model.Job) []string {
	out := make([]string, 0, len(batch)+len(registered))
	for _, c := range batch {
		if c.Name == self.Name && c.Source == self.Source {
			continue
		}
		out = append(out, c.Command)
	}
	for _, j := range registered {
		out = append(out, j.Command)
	}
	// The pattern is derived from this command, so it must be present exactly
	// once for Distinctive's "matches one thing" test to mean what it says.
	return append(out, self.Command)
}

// updateChanged refreshes managed jobs whose source entry still exists but
// now carries a different schedule or command.
//
// A schedule edit happens in the source scheduler, not in goguma, and the
// job's name usually survives it. Adoption skips names it already knows, so
// without this pass the stored copy would keep the old fire time forever:
// the machine wakes at a time nobody means anymore, and sleeps through the
// time they do.
//
// Only jobs goguma adopted itself are updated. A hand-registered job is the
// user's own definition, and a scan never overwrites an explicit human
// decision; retireVanished draws the same line.
func (d *Daemon) updateChanged(entries []scan.Entry, sources []string, cfg config.Config) (updated, retired int) {
	type key struct{ source, id string }
	latest := map[key]scan.Entry{}
	for _, e := range entries {
		latest[key{string(e.Source), model.Slug(e.Name)}] = e
	}

	for _, j := range d.store.Jobs() {
		if !j.Managed || !slices.Contains(sources, j.Source) {
			continue
		}
		e, ok := latest[key{j.Source, j.ID}]
		if !ok {
			continue
		}
		if e.Schedule == j.Schedule && e.Command == j.Command {
			continue
		}

		refreshed := *j
		refreshed.Schedule = e.Schedule
		refreshed.Command = e.Command
		// A source entry that no longer parses keeps the old, working
		// definition instead of replacing it with a broken one. Validate does
		// not parse schedules, so check here, the same way the scheduler will.
		sched, err := schedule.ParseAt(refreshed.Schedule, refreshed.Location(), refreshed.ScheduleAnchor())
		if err != nil {
			d.log.Warn("skipped updating a job to an unparseable schedule",
				"job", j.ID, "schedule", e.Schedule, "err", err)
			continue
		}
		// An update must keep the promise adoption made: goguma never
		// auto-covers a schedule that fires more often than the import
		// interval floor, because waking for it costs more battery than the
		// run is worth. A managed job is goguma's own adoption decision, so
		// when its schedule crosses that line the decision is revisited under
		// the same policy and the job is retired, exactly as if the too
		// frequent schedule had been there on the day of the scan. Keeping
		// the stale schedule instead would wake the machine for fire times
		// the source no longer has. Inclusive comparison for the same reason
		// as scan.Evaluate's.
		if gap := sched.TypicalInterval(time.Now()); gap > 0 && gap <= cfg.MinImportInterval.D() {
			if _, err := d.store.Remove(j.ID); err != nil {
				d.log.Warn("could not retire a job whose schedule became too frequent",
					"job", j.ID, "err", err)
				continue
			}
			d.releaseJob(j.ID)
			d.log.Info("retired a job whose new schedule fires too often to be worth dedicated wakes",
				"job", j.ID, "source", j.Source, "schedule", e.Schedule, "interval", gap)
			d.event(store.Event{
				Kind: store.EventWindowOpened, JobID: j.ID, JobName: j.Name,
				Message: "retired: the new schedule fires every " + model.HumanDuration(gap) +
					", too often to wake for",
			})
			retired++
			continue
		}
		if err := d.store.Put(&refreshed); err != nil {
			d.log.Warn("could not update a changed job", "job", j.ID, "err", err)
			continue
		}

		d.log.Info("updated a job that changed in its source",
			"job", j.ID, "source", j.Source, "schedule", e.Schedule)
		d.event(store.Event{
			Kind: store.EventWindowOpened, JobID: j.ID, JobName: j.Name,
			Message: "definition updated from " + j.Source,
		})
		updated++
	}
	return updated, retired
}

// retireVanished removes managed jobs whose source entry is gone.
//
// Only jobs goguma adopted itself are retired. A hand-registered job is
// left alone even if it looks unmatched, because a human's explicit intent
// should never be undone by a scan that might simply have failed to read a
// source this time.
func (d *Daemon) retireVanished(entries []scan.Entry, coverage []scan.Coverage, sources []string) (retired int) {
	present := map[string]bool{}
	for _, e := range entries {
		present[model.Slug(e.Name)] = true
	}
	// Retire only from sources that were actually read successfully this
	// pass. A clean read returning zero entries is an authoritative statement
	// that everything was deleted or paused; an errored or unavailable source
	// is skipped, so a momentarily unreadable file cannot wipe the user's
	// whole set. Inferring failure from "produced no entries" conflated the
	// two, which meant the last job of a source could never be retired.
	readOK := map[string]bool{}
	for _, c := range coverage {
		if c.Available && c.Err == nil {
			readOK[c.Source] = true
		}
	}

	for _, j := range d.store.Jobs() {
		if !j.Managed || !slices.Contains(sources, j.Source) {
			continue
		}
		if !readOK[j.Source] {
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
		d.mu.Lock()
		d.retired = append(d.retired, retiredJob{Name: j.Name, At: time.Now()})
		d.mu.Unlock()
		d.event(store.Event{
			Kind: store.EventJobRetired, JobID: j.ID, JobName: j.Name,
			Message: "no longer in " + j.Source + "; goguma stopped waking for it",
		})
		d.log.Info("retired a job that no longer exists in its source",
			"job", j.ID, "source", j.Source)
		retired++
	}
	return retired
}

// SyncNow runs a sync immediately, for `goguma sync`.
//
// The real action counts are returned rather than a before/after size delta:
// a rename is one adoption plus one retirement, which a delta reports as
// "up to date" while the job actually lost its identity, its history, and
// its learned ceiling.
func (d *Daemon) SyncNow(ctx context.Context) (adopted, updated, retired int) {
	adopted, updated, retired = d.syncProviders(ctx)
	d.afterJobChange()
	return adopted, updated, retired
}

// effectiveAutoAdopt resolves the configured value.
//
// A nil list means the user has never expressed a preference, which becomes
// every adoptable source. An explicitly empty list is a deliberate "off" and
// is left alone; the two are distinguishable precisely so that turning the
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
// Every source, including crontab, launchd and systemd. Those three used to be
// excluded on the grounds that covering them requires editing a command line,
// but that is only true of `goguma-mark`, which is one of three detection
// modes and the only one that needs a file changed. Pattern matching and
// wake-only need nothing from the user at all.
//
// Excluding them meant the common case failed silently: someone downloads the
// app, it reports itself healthy, and the machine goes on sleeping through the
// crontab that made them install it. A warning pointing at a terminal command
// is not a substitute for the tool doing its job, and the app has no way to
// run that command.
//
// So they are adopted with whatever detection works unattended, and `goguma
// import` remains the way to upgrade a job to exact timing.
func adoptableSources() []string {
	var out []string
	for _, p := range scan.Providers() {
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
// would make `config set auto_adopt off` switch it on, the exact inversion of
// what the user asked for.
//
// "all" is the inverse: it returns nil, restoring the unconfigured default.
// Without it the setting is a one-way door over IPC: a client could turn
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
