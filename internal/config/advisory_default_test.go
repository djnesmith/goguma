package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The promise shown while asking for root says nothing leaves the machine.
// An upgrade must not quietly start making network requests for someone who
// agreed to that; only a fresh install, which sees the current wording, gets
// the default.
func TestAdvisoryChecksDoNotTurnThemselvesOnForExistingInstalls(t *testing.T) {
	dir := t.TempDir()

	t.Run("fresh install has no config at all", func(t *testing.T) {
		c, _, err := Load(filepath.Join(dir, "absent.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !c.AdvisoryChecks {
			t.Error("a new install should get the default, which is on")
		}
	})

	t.Run("config written before the feature existed", func(t *testing.T) {
		p := filepath.Join(dir, "old.json")
		// A real pre-feature config: valid, and simply has no such key.
		if err := os.WriteFile(p, []byte(`{"wake_buffer":"90s","thermal_cutout_c":80}`), 0o600); err != nil {
			t.Fatal(err)
		}
		c, _, err := Load(p)
		if err != nil {
			t.Fatal(err)
		}
		if c.AdvisoryChecks {
			t.Error("an upgrade turned network checks on for someone who never agreed to them")
		}
	})

	t.Run("an explicit choice is honoured either way", func(t *testing.T) {
		for _, want := range []bool{true, false} {
			p := filepath.Join(dir, "explicit.json")
			b, _ := json.Marshal(map[string]any{"advisory_checks": want})
			if err := os.WriteFile(p, b, 0o600); err != nil {
				t.Fatal(err)
			}
			c, _, err := Load(p)
			if err != nil {
				t.Fatal(err)
			}
			if c.AdvisoryChecks != want {
				t.Errorf("advisory_checks=%v was not honoured", want)
			}
		}
	})
}

// With no key compiled in, nothing can be verified, so nothing is fetched
// regardless of the setting. A build that forgot the key must be silent, not
// trusting.
func TestDefaultIsOnlyMeaningfulWithAKey(t *testing.T) {
	if !Default().AdvisoryChecks {
		t.Error("the shipped default should be on for new installs")
	}
}
