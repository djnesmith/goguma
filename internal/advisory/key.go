package advisory

import (
	"crypto/ed25519"
	"encoding/base64"
)

// publicKeyB64 is the key advisories must be signed with.
//
// Set at build time so the repository never contains a key that looks
// authoritative but is not:
//
//	go build -ldflags "-X github.com/junnam586/goguma/internal/advisory.publicKeyB64=<key>"
//
// Empty is the safe default and the state of any build that did not set it:
// `Verify` refuses everything, so a binary with no key compiled in shows no
// advisories rather than trusting whatever the network hands it.
//
// The private half never touches this repository or any build machine. See
// `goguma-advisory` in cmd/ for how a feed is signed.
var publicKeyB64 = ""

// PublicKey returns the compiled-in key, or nil when there is none.
func PublicKey() ed25519.PublicKey {
	if publicKeyB64 == "" {
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(publicKeyB64)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil
	}
	return ed25519.PublicKey(b)
}

// Enabled reports whether this build can check advisories at all.
func Enabled() bool { return PublicKey() != nil }
