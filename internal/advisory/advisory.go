// Package advisory fetches signed notices from goguma's own site.
//
// The problem it solves: a bug that only appears on hardware or an OS the
// author does not have reaches people who then have no way to find out. They
// installed a tool that promises to wake their Mac, it silently stops doing
// that, and nothing tells them a fix exists.
//
// What this deliberately is NOT is remote configuration. The payload can say
// two things: a version number, and a sentence. It cannot change a setting,
// disable a job, or run anything. That restriction is the whole design: goguma
// runs a root helper that controls whether a laptop is allowed to sleep, and a
// server able to reach into that is a remote control channel into people's
// machines. A hijacked endpoint here can, at worst, print a lie.
//
// Nothing identifying is sent. The request is a plain GET of a static file
// with no query string, no headers beyond the default, and no body, so the
// operator of the endpoint learns exactly what any web server learns from a
// file being fetched, and nothing about the machine or its jobs.
package advisory

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultURL is where the feed lives. A static file, so it is cheap to host
// and there is no server to compromise beyond the CDN.
const DefaultURL = "https://getgoguma.com/advisories.json"

// maxBody bounds what is read, because a signature is only checked after the
// bytes have been accepted and an unbounded read is a denial of service.
const maxBody = 64 * 1024

// Feed is the on-the-wire document.
type Feed struct {
	// Latest is the newest released version, like "0.2.1". Optional.
	Latest string `json:"latest"`

	// Notice is a sentence to show the user, or empty. Kept deliberately
	// small: this is a line in a status output, not a newsletter.
	Notice string `json:"notice"`

	// NoticeURL is where to read more, shown alongside Notice.
	NoticeURL string `json:"notice_url"`

	// AffectsBelow limits Notice to installs older than this version, so a
	// warning about a bug does not keep being shown to people who already
	// took the fix. Empty means everyone.
	AffectsBelow string `json:"affects_below"`

	// Signature is base64 Ed25519 over the canonical form of the fields
	// above. It is not part of what is signed.
	Signature string `json:"signature"`
}

// signedPayload is the exact byte sequence a signature covers.
//
// Built field by field rather than by re-marshalling the JSON, because two
// JSON encoders disagree about key order and whitespace, and a signature over
// "whatever the encoder produced" is a signature that verifies on the machine
// that made it and nowhere else.
func (f Feed) signedPayload() []byte {
	return []byte(strings.Join([]string{
		"goguma-advisory-v1",
		f.Latest,
		f.Notice,
		f.NoticeURL,
		f.AffectsBelow,
	}, "\n"))
}

// Verify checks the signature against a public key.
func (f Feed) Verify(pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("no usable public key is compiled in")
	}
	if f.Signature == "" {
		return fmt.Errorf("the advisory is unsigned")
	}
	sig, err := base64.StdEncoding.DecodeString(f.Signature)
	if err != nil {
		return fmt.Errorf("the advisory's signature is not valid base64: %w", err)
	}
	if !ed25519.Verify(pub, f.signedPayload(), sig) {
		return fmt.Errorf("the advisory's signature does not match; it was not written by goguma")
	}
	return nil
}

// Sign produces the Signature field. Used by the tool that publishes the feed
// and by the tests; goguma itself only ever verifies.
func Sign(f Feed, priv ed25519.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, f.signedPayload()))
}

// Client fetches and verifies.
type Client struct {
	URL   string
	Pub   ed25519.PublicKey
	HTTP  *http.Client
	nowFn func() time.Time
}

func NewClient(url string, pub ed25519.PublicKey) *Client {
	if url == "" {
		url = DefaultURL
	}
	return &Client{
		URL: url,
		Pub: pub,
		// Short, and no retry. This is never urgent: a missed check is
		// retried tomorrow, and a hung one must not hold a goroutine open
		// against a daemon that is meant to be invisible.
		HTTP:  &http.Client{Timeout: 10 * time.Second},
		nowFn: time.Now,
	}
}

// Fetch returns the verified feed, or an error.
//
// A failure here is never surfaced as a problem with the user's machine. The
// network being down, the site being moved, and a signature that does not
// check out are all the same thing from where the user sits: no advisory
// today. Only a signature failure is worth a log line, because it is the one
// that could mean something is wrong rather than merely absent.
func (c *Client) Fetch(ctx context.Context) (Feed, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return Feed{}, err
	}
	// No User-Agent beyond Go's default, no query string, no cookies. The
	// request must not become a way to count installs by the back door.
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Feed{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Feed{}, fmt.Errorf("advisory feed returned %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return Feed{}, err
	}
	var f Feed
	if err := json.Unmarshal(body, &f); err != nil {
		return Feed{}, fmt.Errorf("advisory feed is not readable: %w", err)
	}
	if err := f.Verify(c.Pub); err != nil {
		return Feed{}, err
	}
	return f, nil
}

// AppliesTo reports whether this feed's notice is meant for a given installed
// version, so a warning about a bug stops appearing once it is fixed.
func (f Feed) AppliesTo(installed string) bool {
	if f.Notice == "" {
		return false
	}
	if f.AffectsBelow == "" {
		return true
	}
	return Older(installed, f.AffectsBelow)
}

// UpdateAvailable reports whether Latest is newer than what is installed.
func (f Feed) UpdateAvailable(installed string) bool {
	return f.Latest != "" && Older(installed, f.Latest)
}

// Older compares dotted numeric versions. A version that cannot be parsed
// (a dev build, say) is never treated as older, because nagging a developer
// about their own working tree is noise.
func Older(a, b string) bool {
	av, aok := parseVersion(a)
	bv, bok := parseVersion(b)
	if !aok || !bok {
		return false
	}
	for i := range 3 {
		if av[i] != bv[i] {
			return av[i] < bv[i]
		}
	}
	return false
}

func parseVersion(s string) ([3]int, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	// Drop any pre-release or build suffix: "0.2.0-rc1" compares as 0.2.0.
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n := 0
		if p == "" {
			return [3]int{}, false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return [3]int{}, false
			}
			n = n*10 + int(r-'0')
		}
		out[i] = n
	}
	return out, true
}
