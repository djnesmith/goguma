package install

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Signature is what macOS says about a binary this installer is about to place
// as root.
type Signature struct {
	// Valid is whether the signature checks out. A binary that has been
	// modified since it was signed fails here.
	Valid bool
	// Authority is the leaf signing identity, e.g.
	// "Developer ID Application: Some Name (TEAMID)". Empty for an ad-hoc
	// signature, which is what a local build gets.
	Authority string
	// AdHoc is a signature with no identity behind it. Normal when someone has
	// built from source; not what a downloaded copy should ever have.
	AdHoc bool
}

// Describe returns a line for a human, or "" when there is nothing to say.
func (s Signature) Describe() string {
	switch {
	case !s.Valid:
		return ""
	case s.AdHoc:
		return "locally built (ad-hoc signature, no identity)"
	case s.Authority != "":
		return "signed by " + s.Authority
	default:
		return "signed"
	}
}

// authorityLine pulls the leaf authority out of `codesign -dvvv` output. The
// first Authority line is the leaf; the ones after it are the CA chain.
var authorityLine = regexp.MustCompile(`(?m)^Authority=(.+)$`)

// checkSignature asks macOS whether a binary is intact and who signed it.
//
// This exists because the installer's most dangerous moment is copying a
// binary to a root-owned path and loading it as a LaunchDaemon. Everything
// else the tool says about that helper is a claim; this is the one check that
// can be run against the bytes actually being installed, and macOS does it,
// not goguma.
//
// A failure to *run* codesign is not a failure of the binary. It returns an
// error only when the signature is genuinely bad, so a machine without the
// tool degrades to saying nothing rather than refusing to install.
func checkSignature(path string) (Signature, error) {
	if err := exec.Command("codesign", "--verify", "--strict", path).Run(); err != nil {
		var ee *exec.ExitError
		if ok := asExitError(err, &ee); ok {
			// codesign ran and said no. That is the case worth stopping for:
			// the binary does not match what was signed.
			return Signature{Valid: false}, fmt.Errorf(
				"%s does not match its signature; it may have been modified since it was built", path)
		}
		// codesign missing or unrunnable. Nothing learned, nothing claimed.
		return Signature{Valid: false}, nil
	}

	out, err := exec.Command("codesign", "-dvvv", path).CombinedOutput()
	if err != nil {
		return Signature{Valid: true}, nil
	}
	text := string(out)
	if strings.Contains(text, "Signature=adhoc") {
		return Signature{Valid: true, AdHoc: true}, nil
	}
	if m := authorityLine.FindStringSubmatch(text); m != nil {
		return Signature{Valid: true, Authority: strings.TrimSpace(m[1])}, nil
	}
	return Signature{Valid: true}, nil
}

func asExitError(err error, target **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if ok {
		*target = ee
	}
	return ok
}
