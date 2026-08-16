package advisory

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func keypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func serve(t *testing.T, f Feed) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(f)
	}))
	t.Cleanup(s.Close)
	return s
}

func TestAGenuineAdvisoryIsAccepted(t *testing.T) {
	pub, priv := keypair(t)
	f := Feed{Latest: "0.2.0", Notice: "0.1.x misses jobs on Intel Macs."}
	f.Signature = Sign(f, priv)

	got, err := NewClient(serve(t, f).URL, pub).Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got.Notice != f.Notice || got.Latest != f.Latest {
		t.Errorf("got %+v", got)
	}
}

// The entire security model. Anyone who can serve bytes at that URL, or sit in
// front of it, must not be able to put words in goguma's mouth.
func TestATamperedAdvisoryIsRefused(t *testing.T) {
	pub, priv := keypair(t)
	honest := Feed{Latest: "0.2.0", Notice: "All is well."}
	honest.Signature = Sign(honest, priv)

	for _, tampered := range []struct {
		name string
		f    Feed
	}{
		{"notice rewritten", Feed{Latest: honest.Latest, Notice: "Run this script as root.", Signature: honest.Signature}},
		{"version rewritten", Feed{Latest: "9.9.9", Notice: honest.Notice, Signature: honest.Signature}},
		{"url added", Feed{Latest: honest.Latest, Notice: honest.Notice, NoticeURL: "https://evil.example", Signature: honest.Signature}},
		{"signature stripped", Feed{Latest: honest.Latest, Notice: honest.Notice}},
		{"signature garbage", Feed{Latest: honest.Latest, Notice: honest.Notice, Signature: "not base64!!"}},
	} {
		t.Run(tampered.name, func(t *testing.T) {
			if _, err := NewClient(serve(t, tampered.f).URL, pub).Fetch(context.Background()); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

// A feed signed by a different key is someone else's feed.
func TestAnotherKeysAdvisoryIsRefused(t *testing.T) {
	pub, _ := keypair(t)
	_, otherPriv := keypair(t)
	f := Feed{Latest: "0.2.0", Notice: "trust me"}
	f.Signature = Sign(f, otherPriv)

	if _, err := NewClient(serve(t, f).URL, pub).Fetch(context.Background()); err == nil {
		t.Fatal("a feed signed by an unrelated key was accepted")
	}
}

// A notice about a bug must stop appearing once the reader has the fix.
func TestANoticeStopsAtTheVersionThatFixedIt(t *testing.T) {
	f := Feed{Notice: "Intel Macs miss jobs.", AffectsBelow: "0.2.0"}
	for _, c := range []struct {
		installed string
		want      bool
	}{
		{"0.1.0", true},
		{"0.1.9", true},
		{"0.2.0", false},
		{"0.3.0", false},
		{"dev", false}, // a working tree is not nagged
	} {
		if got := f.AppliesTo(c.installed); got != c.want {
			t.Errorf("installed %s: applies = %v, want %v", c.installed, got, c.want)
		}
	}
}

func TestUpdateDetection(t *testing.T) {
	f := Feed{Latest: "0.2.1"}
	for _, c := range []struct {
		installed string
		want      bool
	}{
		{"0.1.0", true},
		{"0.2.0", true},
		{"0.2.1", false},
		{"1.0.0", false},
		{"dev", false},
		{"v0.2.0", true},
		{"0.2.1-rc1", false},
	} {
		if got := f.UpdateAvailable(c.installed); got != c.want {
			t.Errorf("installed %q: update = %v, want %v", c.installed, got, c.want)
		}
	}
}

// A payload bigger than the limit must not be read into memory before the
// signature has had a chance to reject it.
func TestAnEnormousFeedIsBounded(t *testing.T) {
	pub, _ := keypair(t)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"notice":"`))
		chunk := make([]byte, 4096)
		for i := range chunk {
			chunk[i] = 'a'
		}
		for range 1000 { // 4MB, far past maxBody
			w.Write(chunk)
		}
	}))
	defer s.Close()
	if _, err := NewClient(s.URL, pub).Fetch(context.Background()); err == nil {
		t.Fatal("an unbounded body was accepted")
	}
}

// A server that is down, moved, or serving nonsense is not a problem with the
// user's machine and must simply produce no advisory.
func TestAFailedFetchIsJustNoAdvisory(t *testing.T) {
	pub, _ := keypair(t)
	for _, c := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"404", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) }},
		{"500", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }},
		{"html", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("<html>")) }},
		{"empty", func(w http.ResponseWriter, _ *http.Request) {}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := httptest.NewServer(c.handler)
			defer s.Close()
			if _, err := NewClient(s.URL, pub).Fetch(context.Background()); err == nil {
				t.Error("accepted")
			}
		})
	}
}

// The signature must cover the exact bytes, not a re-encoding, or it verifies
// only on the machine that produced it.
func TestSignedPayloadIsStableAcrossEncodings(t *testing.T) {
	f := Feed{Latest: "0.2.0", Notice: "hello", NoticeURL: "https://x", AffectsBelow: "0.2.0"}
	first := string(f.signedPayload())

	var round Feed
	b, _ := json.Marshal(f)
	_ = json.Unmarshal(b, &round)
	if string(round.signedPayload()) != first {
		t.Error("the signed bytes changed across a JSON round trip")
	}
}
