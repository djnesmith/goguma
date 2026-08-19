package render

import (
	"fmt"
	"strings"
	"time"
)

// Waiter shows that something is being waited for, and how far along the wait
// is, on one line that rewrites itself.
//
// Setup spends up to twenty seconds each on two services, and printed nothing
// while it did. Twenty seconds of a still cursor after a password prompt reads
// as a hang, and the honest thing to show is that the wait is bounded and
// moving: the bar is the share of the allowance used, so a full one means
// "about to give up" rather than "nearly done".
//
// Silent when the output is not a terminal. Carriage returns in a log file or a
// CI transcript produce one unreadable line, and `goguma install` is run from
// scripts.
type Waiter struct {
	r     *Renderer
	label string
	steps int
	done  int
	live  bool
}

// NewWaiter starts a wait of at most steps ticks.
func (r *Renderer) NewWaiter(label string, steps int) *Waiter {
	w := &Waiter{r: r, label: label, steps: max(steps, 1), live: r.Color()}
	w.draw()
	return w
}

// Tick advances the bar by one.
func (w *Waiter) Tick() {
	if w == nil {
		return
	}
	w.done++
	w.draw()
}

// Done clears the line, leaving the caller to say what happened.
func (w *Waiter) Done() {
	if w == nil || !w.live {
		return
	}
	fmt.Fprintf(w.r.w, "\r%s\r", strings.Repeat(" ", w.width()+2))
}

func (w *Waiter) width() int { return len(w.label) + 2 + barCells + 8 }

// barCells is how many cells the bar itself occupies.
const barCells = 16

func (w *Waiter) draw() {
	if !w.live {
		return
	}
	filled := w.done * barCells / w.steps
	if filled > barCells {
		filled = barCells
	}
	bar := strings.Repeat("▪", filled) + strings.Repeat("▫", barCells-filled)
	fmt.Fprintf(w.r.w, "\r%s %s %s",
		w.r.Muted(w.label),
		w.r.Accent(bar),
		w.r.Muted(fmt.Sprintf("%ds", w.done/2)),
	)
}

// WaitFor runs check every interval until it succeeds or the allowance runs
// out, drawing a bar as it goes. It reports whether check ever succeeded.
func (r *Renderer) WaitFor(label string, attempts int, interval time.Duration, check func() bool) bool {
	w := r.NewWaiter(label, attempts)
	defer w.Done()
	for range attempts {
		if check() {
			return true
		}
		time.Sleep(interval)
		w.Tick()
	}
	return false
}
