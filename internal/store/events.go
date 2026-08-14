package store

import (
	"encoding/json"
	"os"
	"time"

	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/schedule"
)

// EventKind classifies entries in the append-only event log. These strings
// appear in events.jsonl and in webhook payloads, so they are a contract.
type EventKind string

const (
	EventWakeScheduled EventKind = "wake_scheduled"
	EventWakeFailed    EventKind = "wake_failed"
	EventWindowOpened  EventKind = "window_opened"
	EventJobStarted    EventKind = "job_started"
	EventJobFinished   EventKind = "job_finished"
	EventHoldReleased  EventKind = "hold_released"
	EventCeilingHit    EventKind = "ceiling_hit"
	EventNeverDetected EventKind = "never_detected"
	EventCutout        EventKind = "cutout"
	EventSlept         EventKind = "machine_slept"
	EventWoke          EventKind = "machine_woke"
	EventDaemonStart   EventKind = "daemon_start"
	EventDaemonStop    EventKind = "daemon_stop"
	EventHelperState   EventKind = "helper_state"
	// EventJobRetired: a job goguma had adopted vanished from the scheduler
	// that created it, so goguma stopped waking for it.
	EventJobRetired EventKind = "job_retired"
)

// Event is one line of events.jsonl, the audit trail for every wake, hold,
// and release cycle that §8 of the PRD requires.
type Event struct {
	At      time.Time `json:"at"`
	Kind    EventKind `json:"kind"`
	JobID   string    `json:"job_id,omitempty"`
	JobName string    `json:"job_name,omitempty"`
	Message string    `json:"message,omitempty"`

	// Duration is the measured runtime for job_finished events, and the hold
	// length for hold_released.
	Duration model.Duration `json:"duration,omitempty"`
	Outcome  model.Outcome  `json:"outcome,omitempty"`

	// Power captures the machine context at the moment of the event, which is
	// what makes a cutout entry diagnosable after the fact.
	LidClosed  *bool    `json:"lid_closed,omitempty"`
	TempC      *float64 `json:"temp_c,omitempty"`
	BatteryPct *int     `json:"battery_pct,omitempty"`
}

// LogEvent appends to events.jsonl, rotating first if it has grown past the
// configured cap. Logging failures are returned but callers deliberately
// treat them as non-fatal: losing an audit line must never stop the daemon
// from releasing a sleep hold.
func (s *Store) LogEvent(e Event, maxBytes int64) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.layout.EventsFile()
	if fi, err := os.Stat(path); err == nil && maxBytes > 0 && fi.Size() >= maxBytes {
		// Single-generation rotation: enough to bound disk use, and the
		// previous file stays available for a post-mortem.
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(b)
	return err
}

// RecordSleepInterval appends an observed sleep period to the daemon's own
// sleep log.
//
// This supplements the OS log rather than replacing it: `pmset -g log` gives
// history back to before goguma was installed (so miss-risk works on day
// one), while this file is portable to platforms with no equivalent system
// log and survives the OS log being rotated away.
func (s *Store) RecordSleepInterval(iv schedule.SleepInterval) error {
	b, err := json.Marshal(iv)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.OpenFile(s.layout.SleepLogFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// RecordedSleep reads back the daemon's own observed sleep intervals.
func (s *Store) RecordedSleep() ([]schedule.SleepInterval, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, err := os.ReadFile(s.layout.SleepLogFile())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []schedule.SleepInterval
	for _, line := range splitLines(b) {
		var iv schedule.SleepInterval
		if err := json.Unmarshal(line, &iv); err != nil {
			continue
		}
		out = append(out, iv)
	}
	return out, nil
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			if i > start {
				out = append(out, b[start:i])
			}
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}
