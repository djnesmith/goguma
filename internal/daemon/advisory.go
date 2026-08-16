package daemon

import (
	"context"
	"time"

	"github.com/junnam586/goguma/internal/advisory"
)

// advisoryInterval is how often the feed is fetched.
//
// Once a day, and jittered by where in the day the daemon happened to start,
// because a fixed hour would have every install on earth hit the same static
// file within the same minute.
const advisoryInterval = 24 * time.Hour

// checkAdvisories fetches the signed notice feed, if this build can.
//
// Every failure is silent. The network being down, the site having moved, and
// a signature that does not verify all mean the same thing from where the user
// sits: no notice today. Only a bad signature is logged, because that is the
// one case where something may actually be wrong rather than merely absent,
// and even then it changes nothing the daemon does.
func (d *Daemon) checkAdvisories(ctx context.Context) {
	d.mu.RLock()
	on := d.cfg.AdvisoryChecks
	d.mu.RUnlock()
	if !on || !advisory.Enabled() {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	feed, err := advisory.NewClient("", advisory.PublicKey()).Fetch(ctx)
	if err != nil {
		d.log.Debug("no advisory today", "err", err)
		return
	}

	d.mu.Lock()
	d.advisory = &feed
	d.mu.Unlock()
}

// advisoryWarnings turns the current feed into whatever is worth telling the
// user, which is usually nothing.
func (d *Daemon) advisoryWarnings() []advisoryNotice {
	d.mu.RLock()
	feed, version := d.advisory, d.version
	d.mu.RUnlock()
	if feed == nil {
		return nil
	}

	var out []advisoryNotice
	// The notice first: a known bug matters more than a version number.
	if feed.AppliesTo(version) {
		n := advisoryNotice{Message: feed.Notice}
		if feed.NoticeURL != "" {
			n.Message += " (" + feed.NoticeURL + ")"
		}
		out = append(out, n)
	}
	if feed.UpdateAvailable(version) {
		out = append(out, advisoryNotice{
			Message: "goguma " + feed.Latest + " is out; this is " + version,
			Fix:     "brew upgrade goguma",
		})
	}
	return out
}

type advisoryNotice struct {
	Message string
	Fix     string
}
