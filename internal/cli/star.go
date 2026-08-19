package cli

import "github.com/junnam586/goguma/internal/render"

// repoURL is where a star goes.
const repoURL = "https://github.com/junnam586/goguma"

// printStarAsk adds one quiet line inviting a star.
//
// The menu bar app asks in its popover header, so the command line asks too;
// somebody who never opens the app should not be the only person never asked.
//
// Three rules keep it from becoming nagging. It is muted, so it sits below
// everything the command was actually run to say. It is last, so a reader who
// has what they came for has already stopped. And it is silent whenever the
// output is not a terminal, which is the important one: `goguma status` is run
// from scripts, cron lines and status bars, and a line about GitHub in the
// middle of somebody's monitoring output is not a request, it is a defect.
func printStarAsk(r *render.Renderer) {
	if !r.Color() {
		return
	}
	r.Blank()
	// The lead-in muted, the address accented, which is how the updates link
	// two lines above is set. One URL coloured and the other not read as one
	// being a link and the other being punctuation.
	r.Printf("  %s\n", r.Muted("★ like goguma? a star helps people find it:"))
	r.Printf("  %s\n", r.Accent(repoURL))
}
