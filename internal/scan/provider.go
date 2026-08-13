package scan

import (
	"context"
	"sort"
	"sync"
)

// Provider discovers scheduled jobs from one source.
//
// The OS schedulers (crontab, launchd, systemd) are only part of the picture.
// Plenty of tools run their own scheduler entirely inside their own config
// (Hermes, n8n, Jenkins, self-hosted runners), and those jobs are invisible to
// `crontab -l` and `launchctl list`. A user who does not know exactly what
// they have scheduled is precisely the person whose most important jobs live
// in one of these, so discovery has to be extensible rather than hardcoded to
// the three OS mechanisms.
type Provider interface {
	// Name identifies the source in output ("crontab", "hermes").
	Name() string

	// Available reports whether this source exists on the machine at all, so
	// `import` can distinguish "found nothing" from "did not look".
	Available() bool

	// Where describes what was searched, shown in the coverage report.
	Where() string

	// Discover returns the entries this source knows about.
	Discover(ctx context.Context) ([]Entry, error)
}

var (
	providersMu sync.RWMutex
	providers   []Provider
)

// Register adds a provider. Called from init in each provider's file, so
// supporting a new scheduler is one new file and no edits elsewhere.
func Register(p Provider) {
	providersMu.Lock()
	defer providersMu.Unlock()
	providers = append(providers, p)
}

// Providers returns the registered providers, sorted by name for stable
// output.
func Providers() []Provider {
	providersMu.RLock()
	defer providersMu.RUnlock()
	out := append([]Provider(nil), providers...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Coverage records what a scan actually looked at.
//
// This exists because the most dangerous output `import` can produce is a
// confident "nothing needs goguma" when the real answer is "your jobs live
// somewhere I did not check". Reporting coverage turns an unqualified
// all-clear into an auditable one.
type Coverage struct {
	Source    string
	Where     string
	Available bool
	Found     int
	Err       error
}

// DiscoverAll runs every registered provider, returning the entries and a
// per-source coverage report.
//
// A provider that fails does not abort the scan (one broken source should not
// hide every other source's jobs), but its error is recorded and surfaced.
func DiscoverAll(ctx context.Context) ([]Entry, []Coverage) {
	var (
		all      []Entry
		coverage []Coverage
	)
	for _, p := range Providers() {
		c := Coverage{Source: p.Name(), Where: p.Where(), Available: p.Available()}
		if !c.Available {
			coverage = append(coverage, c)
			continue
		}
		entries, err := p.Discover(ctx)
		c.Err, c.Found = err, len(entries)
		coverage = append(coverage, c)
		all = append(all, entries...)
	}
	return all, coverage
}
