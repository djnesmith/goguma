package config

import (
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/model"
)

// Zero means "no schedule is too frequent to cover", which is a choice, not an
// absence. The shared clamp reads a zero duration as "unset" and replaces it
// with the default, so `config set min_import_interval 0s` reported success
// and left the old value in place.
func TestMinImportIntervalCanBeSetToZero(t *testing.T) {
	c := Default()
	c.MinImportInterval = 0

	c.Normalize()
	if c.MinImportInterval != 0 {
		t.Errorf("min_import_interval came back as %v; an explicit zero was overwritten",
			c.MinImportInterval)
	}
}

// The other durations keep the old behaviour: zero there means the field was
// missing from the file, and a zero wake buffer is not something to honour.
func TestZeroWakeBufferStillFallsBackToTheDefault(t *testing.T) {
	c := Default()
	c.WakeBuffer = 0

	c.Normalize()
	if c.WakeBuffer == 0 {
		t.Error("a zero wake buffer was honoured; it should fall back to the default")
	}
}

// Out of range is still corrected.
func TestMinImportIntervalIsCappedAtADay(t *testing.T) {
	c := Default()
	c.MinImportInterval = model.Duration(48 * time.Hour)

	warnings := c.Normalize()
	if c.MinImportInterval.D() != 24*time.Hour {
		t.Errorf("48h was not capped: got %v", c.MinImportInterval)
	}
	if len(warnings) == 0 {
		t.Error("capping a value silently; the user should be told")
	}
}
