package cli

// Tests that the documentation describes the program that actually exists.
//
// Every one of these was a real defect found by reading the docs against the
// code by hand: a README that said goguma would never edit a crontab months
// after it started doing exactly that, a `goguma scheduler` command that was
// registered and documented and missing from `goguma help`, a `--detection
// none` mode the README told people to use and the CLI never mentioned, and an
// advisory key that reached the Homebrew build but not the app bundle, so
// which install route you picked decided whether a feature existed.
//
// Prose drifts from code silently, because nothing fails when it does. These
// fail.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/model"
)

// repoRoot locates the checkout from this file's own path, so the tests work
// from any working directory and in CI.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate this source file")
	}
	return filepath.Join(filepath.Dir(self), "..", "..")
}

// docFiles are the prose files that make claims about behaviour.
var docFiles = []string{
	"README.md",
	"SECURITY.md",
	"Docs/ARCHITECTURE.md",
	"Docs/CODING-AGENTS.md",
	"macos/README.md",
}

func readDoc(t *testing.T, root, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}

// codeSpans returns the contents of every `backticked span` and fenced block,
// which is where the docs name commands and flags. Prose is deliberately not
// searched: "goguma is free and open source" is not a claim about a command.
var codeSpan = regexp.MustCompile("`([^`\n]+)`")

func codeSpans(doc string) []string {
	var out []string
	for _, m := range codeSpan.FindAllStringSubmatch(doc, -1) {
		out = append(out, m[1])
	}
	// Fenced blocks, whose contents are not backticked individually.
	inFence := false
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

var gogumaCall = regexp.MustCompile(`^goguma ([a-z][a-z-]*)`)

// TestDocsOnlyNameRealCommands fails when the docs tell someone to run a
// command that does not exist. A wrong command in a README is worse than no
// README: it is a confident instruction that ends in "unknown command".
func TestDocsOnlyNameRealCommands(t *testing.T) {
	root := repoRoot(t)
	// Real, but dispatched by the switch in Run rather than registered in
	// `commands`, so a plain map lookup does not find them.
	builtIn := map[string]bool{"help": true}
	for _, f := range docFiles {
		for _, span := range codeSpans(readDoc(t, root, f)) {
			m := gogumaCall.FindStringSubmatch(span)
			if m == nil {
				continue
			}
			name := m[1]
			if builtIn[name] {
				continue
			}
			if _, ok := commands[name]; !ok {
				t.Errorf("%s documents `goguma %s`, which is not a registered command (in %q)",
					f, name, span)
			}
		}
	}
}

// TestDocsOnlyNameRealDetectionModes fails when the docs offer a detection
// mode the model does not accept. The README told people to use
// `--detection none` for a year while `goguma help add` listed only two modes,
// so whichever one you read, the other was lying.
func TestDocsOnlyNameRealDetectionModes(t *testing.T) {
	root := repoRoot(t)
	mode := regexp.MustCompile(`--detection ([a-z]+)`)
	seen := map[string]bool{}
	for _, f := range append(docFiles, "internal/cli/jobs.go") {
		body := readDoc(t, root, f)
		for _, m := range mode.FindAllStringSubmatch(body, -1) {
			name := m[1]
			if name == "mode" { // the placeholder in `--detection <mode>`
				continue
			}
			seen[name] = true
			if !model.DetectionMode(name).Valid() {
				t.Errorf("%s offers --detection %s, which model.DetectionMode rejects", f, name)
			}
		}
	}
	// And the other direction: a mode that exists but nothing documents is a
	// mode nobody can find.
	for _, m := range []model.DetectionMode{model.DetectMark, model.DetectPattern, model.DetectNone} {
		if !seen[string(m)] {
			t.Errorf("detection mode %q is valid but no doc or help text mentions it", m)
		}
	}
}

// TestReadmeSafetyNumbersMatchDefaults fails when the README quotes a cutout
// that config.Default() no longer uses. These numbers are the whole safety
// pitch, so a stale one is a promise about someone's laptop in a bag.
func TestReadmeSafetyNumbersMatchDefaults(t *testing.T) {
	d := config.Default()
	readme := readDoc(t, repoRoot(t), "README.md")

	for _, c := range []struct {
		claim string
		want  bool
		desc  string
	}{
		{"above 80°C", d.ThermalCutoutC == 80, "thermal_cutout_c"},
		{"below 10%", d.LowBatteryCutoutPct == 10, "low_battery_cutout_pct"},
		{"five minutes", time.Duration(d.DefaultCeiling) == 5*time.Minute, "default_ceiling"},
	} {
		if !strings.Contains(readme, c.claim) {
			t.Errorf("README no longer contains %q; if the wording changed, update this test "+
				"so %s stays covered", c.claim, c.desc)
			continue
		}
		if !c.want {
			t.Errorf("README says %q but config.Default() disagrees for %s", c.claim, c.desc)
		}
	}
}

var mdLink = regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)

// TestDocLinksResolve fails on a relative link to a file that is not there, or
// to a heading anchor that does not exist. Cross-links are the only way anyone
// navigates between four markdown files, and a dead one is invisible until a
// reader hits it.
func TestDocLinksResolve(t *testing.T) {
	root := repoRoot(t)
	for _, f := range docFiles {
		body := readDoc(t, root, f)
		base := filepath.Dir(filepath.Join(root, f))
		for _, m := range mdLink.FindAllStringSubmatch(body, -1) {
			text, target := m[1], m[2]
			if strings.HasPrefix(target, "http://") ||
				strings.HasPrefix(target, "https://") ||
				strings.HasPrefix(target, "mailto:") {
				continue
			}
			path, frag, _ := strings.Cut(target, "#")
			full := filepath.Join(root, f)
			if path != "" {
				full = filepath.Clean(filepath.Join(base, path))
				if _, err := os.Stat(full); err != nil {
					t.Errorf("%s: [%s](%s) points at a file that does not exist", f, text, target)
					continue
				}
			}
			if frag == "" || !strings.HasSuffix(full, ".md") {
				continue
			}
			if !hasAnchor(t, full, frag) {
				t.Errorf("%s: [%s](%s) points at a heading that does not exist", f, text, target)
			}
		}
	}
}

// hasAnchor renders headings the way GitHub does: lowercase, punctuation
// dropped, spaces to hyphens.
func hasAnchor(t *testing.T, path, want string) bool {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	drop := regexp.MustCompile(`[^\w\s-]`)
	space := regexp.MustCompile(`\s+`)
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "#") {
			continue
		}
		h := strings.ToLower(strings.TrimSpace(strings.TrimLeft(line, "#")))
		h = space.ReplaceAllString(drop.ReplaceAllString(h, ""), "-")
		if h == want {
			return true
		}
	}
	return false
}

// TestBothInstallRoutesInjectTheAdvisoryKey fails when the Homebrew build and
// the app bundle disagree about whether the advisory feed exists.
//
// The app carries its own copy of the daemon, so when only .goreleaser.yaml
// passed the key, whether a user received bug notices depended on whether they
// downloaded the disk image or ran `brew install`. Two install routes the docs
// describe as equivalent, quietly producing different products.
func TestBothInstallRoutesInjectTheAdvisoryKey(t *testing.T) {
	root := repoRoot(t)
	const symbol = "internal/advisory.publicKeyB64"
	const secret = "GOGUMA_ADVISORY_PUBKEY"

	for _, f := range []string{".goreleaser.yaml", "macos/scripts/make-app.sh"} {
		body := readDoc(t, root, f)
		if !strings.Contains(body, symbol) {
			t.Errorf("%s does not inject %s, so binaries built this way can never "+
				"verify an advisory", f, symbol)
		}
		if !strings.Contains(body, secret) {
			t.Errorf("%s does not read %s", f, secret)
		}
	}

	// And the workflow has to hand the secret to both of those steps, or the
	// injection above receives an empty value in CI and silently does nothing.
	wf := readDoc(t, root, ".github/workflows/release.yml")
	if n := strings.Count(wf, secret+":"); n < 2 {
		t.Errorf("release.yml passes %s to %d step(s); both the goreleaser step and the "+
			"app build step need it", secret, n)
	}
}

// TestShippedBinariesMatchTheDocumentedSet fails when the app bundle would
// ship a binary the release does not, or miss one it does.
//
// goguma-advisory is the maintainer tool that generates and signs the notice
// feed. It is deliberately absent from .goreleaser.yaml, but make-app.sh built
// `./cmd/...` and so shipped it inside every copy of the app.
func TestShippedBinariesMatchTheDocumentedSet(t *testing.T) {
	root := repoRoot(t)
	shipped := []string{"goguma", "goguma-daemon", "goguma-helper", "goguma-mark"}
	maintainerOnly := []string{"goguma-advisory"}

	rel := readDoc(t, root, ".goreleaser.yaml")
	app := readDoc(t, root, "macos/scripts/make-app.sh")

	for _, b := range shipped {
		if !strings.Contains(rel, "./cmd/"+b) {
			t.Errorf(".goreleaser.yaml does not build ./cmd/%s", b)
		}
		if !strings.Contains(app, "./cmd/"+b) {
			t.Errorf("make-app.sh does not build ./cmd/%s", b)
		}
	}
	for _, b := range maintainerOnly {
		if strings.Contains(rel, "./cmd/"+b) {
			t.Errorf(".goreleaser.yaml ships %s, which is a maintainer tool", b)
		}
		if strings.Contains(app, "./cmd/"+b) {
			t.Errorf("make-app.sh ships %s inside the app, which is a maintainer tool", b)
		}
	}
	// A wildcard build silently re-adds anything new under cmd/, which is how
	// goguma-advisory got into the bundle in the first place. Comments are
	// stripped first, because the line explaining why the wildcard is gone
	// necessarily contains the wildcard.
	for _, line := range strings.Split(app, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.Contains(line, "./cmd/...") {
			t.Errorf("make-app.sh builds ./cmd/... (%q), so any new command ships inside "+
				"the app whether or not it is meant for users; name the binaries instead",
				strings.TrimSpace(line))
		}
	}
}

// TestDocsDoNotClaimGogumaNeverWrites fails if the old promise comes back.
//
// The README said goguma "will never make that edit on your behalf" long after
// `import --register` started rewriting crontabs and launchd plists. That is
// the most consequential sentence a tool like this can get wrong, because it
// is the one a careful reader relies on.
func TestDocsDoNotClaimGogumaNeverWrites(t *testing.T) {
	root := repoRoot(t)
	forbidden := []string{
		"never make that edit",
		"never edits your crontab",
		"will never edit",
		"never edit your crontab",
	}
	for _, f := range docFiles {
		body := strings.ToLower(readDoc(t, root, f))
		for _, phrase := range forbidden {
			if strings.Contains(body, phrase) {
				t.Errorf("%s claims %q, but internal/cli/crontab_apply.go and "+
					"launchd_apply.go both write on the user's behalf after `import --register`",
					f, phrase)
			}
		}
	}
}

// TestRecordingsAreMadeAgainstSandboxData fails if a docs recording is set up
// to film the machine it runs on.
//
// This is the one class of leak the rest of this file cannot see. Every check
// here reads text, and import.gif published a real username, a private tool's
// path and six real job names for three days without tripping any of them,
// because the leak was pixels. What is checkable is the setup that produced
// it, so that is what this checks: the tape has to build a sandbox HOME and
// point the command at it, and `goguma import` resolves every source it scans
// from HOME.
//
// It cannot prove an image is clean. It can stop the next re-record quietly
// going back to filming a real home directory, which is how the first one
// happened.
func TestRecordingsAreMadeAgainstSandboxData(t *testing.T) {
	root := repoRoot(t)

	for _, f := range []string{"Docs/media/demo-home.py", "Docs/media/demo-daemon.py"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Errorf("%s is missing; the docs media cannot be regenerated without "+
				"filming a real machine", f)
		}
	}

	tape := readDoc(t, root, "Docs/media/import.tape")
	for _, need := range []string{"demo-home.py", "export HOME="} {
		if !strings.Contains(tape, need) {
			t.Errorf("import.tape does not contain %q, so the recording would scan "+
				"whatever machine it runs on", need)
		}
	}
	// The sandbox has to be somewhere that is not a real home and not inside the
	// checkout: the report prints its source path in full, and a path under the
	// repo contains the author's home directory just as surely.
	if !strings.Contains(tape, "HOME=/tmp/") {
		t.Error("import.tape points HOME somewhere other than /tmp; the import report " +
			"prints its source path in full, so a real home or a repo path ends up on screen")
	}

	// And the jobs window, which had the same problem for a different reason.
	mac := readDoc(t, root, "macos/README.md")
	if !strings.Contains(mac, "demo-daemon.py") {
		t.Error("macos/README.md does not mention demo-daemon.py, so the next person " +
			"to regenerate the jobs screenshot will render their own jobs into it")
	}
}

// TestSettingsPlaceholdersMatchDefaults keeps the Settings pane's initial
// values in step with config.Default().
//
// They are only placeholders, shown for the instant before the pane reads the
// real config, so being wrong is nearly invisible in use. It is not invisible
// in a screenshot: the offscreen renderer captures before that read happens,
// so whatever is hardcoded here is what lands in the docs. The battery
// placeholder was 20 against a shipped default of 10, and it went into a
// picture that sat next to a paragraph in the README saying 10.
func TestSettingsPlaceholdersMatchDefaults(t *testing.T) {
	d := config.Default()
	src := readDoc(t, repoRoot(t), "macos/Sources/GogumaUI/Views/SettingsWindowView.swift")

	for _, c := range []struct {
		decl string
		want string
		name string
	}{
		{"@State private var thermalCutout: Double =", fmt.Sprintf("%.0f", d.ThermalCutoutC), "thermal_cutout_c"},
		{"@State private var lowBatteryCutout: Double =", fmt.Sprintf("%d", d.LowBatteryCutoutPct), "low_battery_cutout_pct"},
	} {
		i := strings.Index(src, c.decl)
		if i < 0 {
			t.Errorf("could not find %q in SettingsWindowView.swift", c.decl)
			continue
		}
		rest := src[i+len(c.decl):]
		if end := strings.IndexAny(rest, "\n"); end >= 0 {
			rest = rest[:end]
		}
		if got := strings.TrimSpace(rest); got != c.want {
			t.Errorf("Settings seeds %s with %s, but config.Default() ships %s; "+
				"the difference ends up in every rendered screenshot", c.name, got, c.want)
		}
	}
}

// TestTheTapPushUsesItsOwnToken.
//
// The cask block named the tap repository and never said which credential to
// push with, so goreleaser used the Actions GITHUB_TOKEN, which is scoped to
// this repository alone. v0.1.0 died on it: `403 Resource not accessible by
// integration` on the final step, after every artifact had been built and
// uploaded, which then skipped the app job and left a release with no disk
// image in it. Having the secret set is not enough; it has to be referenced.
func TestTheTapPushUsesItsOwnToken(t *testing.T) {
	cfg := readDoc(t, repoRoot(t), ".goreleaser.yaml")
	i := strings.Index(cfg, "homebrew_casks:")
	if i < 0 {
		t.Fatal("no homebrew_casks block in .goreleaser.yaml")
	}
	block := cfg[i:]
	if !strings.Contains(block, "HOMEBREW_TAP_GITHUB_TOKEN") {
		t.Error("the cask block never references HOMEBREW_TAP_GITHUB_TOKEN, so the push " +
			"will fall back to the Actions token and 403 on the last step of a release")
	}
	if !strings.Contains(block, "token:") {
		t.Error("the cask repository has no token: field")
	}
}

// Every shipped binary is Developer ID signed, and the release can say so.
//
// v0.1.0 shipped four ad-hoc signed binaries, because .goreleaser.yaml had no
// signing step and only the app bundle was ever signed. `codesign --verify
// --strict` passed on all four, which is the trap: an ad-hoc signature is a
// real signature, so the check that was supposed to catch this reported success
// with no identity behind it.
//
// What that cost is specific. `goguma install` runs codesign against the helper
// before copying it in as root and prints who signed it, and SECURITY.md offers
// that line as the one part of the output a tampered copy could not produce.
// For anyone installing from an archive it printed nothing worth reading, so
// the claim held only for people who took the app.
//
// This counts rather than reads, because the regression is a fifth binary added
// without a hook, and a count catches that where an inspection of the four we
// have today would not.
func TestEveryShippedBinaryIsSignedAtRelease(t *testing.T) {
	root := repoRoot(t)
	rel := readDoc(t, root, ".goreleaser.yaml")

	builds := strings.Count(rel, "\n  - id: ")
	hooks := strings.Count(rel, "hooks: &sign_darwin") + strings.Count(rel, "hooks: *sign_darwin")
	if builds == 0 {
		t.Fatal(".goreleaser.yaml declares no builds, so this test is not reading it")
	}
	if hooks != builds {
		t.Errorf("%d builds but %d signing hooks: every build must sign what it produces", builds, hooks)
	}

	// A hook goreleaser cannot execute fails the release at the first binary,
	// which is better than shipping unsigned, but only just.
	script := filepath.Join(root, "macos", "scripts", "sign-binary.sh")
	fi, err := os.Stat(script)
	if err != nil {
		t.Fatalf("the signing hook named by .goreleaser.yaml is missing: %v", err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Errorf("%s is not executable, so goreleaser cannot run it", script)
	}

	// The script exits cleanly when it has no identity, so that nobody's fork
	// is blocked by a certificate they cannot have. The cost of that kindness is
	// that a workflow which forgets to pass one produces ad-hoc archives and
	// nothing anywhere fails. This is the thing that fails.
	wf := readDoc(t, root, ".github/workflows/release.yml")
	if !strings.Contains(wf, "SIGN_ID:") {
		t.Error("release.yml never sets SIGN_ID, so the signing hook would quietly no-op")
	}
	if !strings.Contains(wf, "MACOS_CERTIFICATE_P12") {
		t.Error("release.yml never imports a certificate into the archive job")
	}
}
