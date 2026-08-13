package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/junnam586/goguma/internal/model"
)

// SourceHermes identifies jobs from the Hermes agent's own scheduler.
const SourceHermes Source = "hermes"

func init() { Register(&hermesProvider{}) }

// hermesProvider reads Hermes's cron store.
//
// Hermes keeps its schedules entirely in its own state directory and runs them
// from its gateway process, so none of them appear in crontab or launchd. On a
// machine where Hermes is the user's main automation, an OS-only scan reports
// "nothing scheduled here" while every job the user actually cares about sits
// in this file.
//
// It is also the best-instrumented source available: Hermes records
// last_run_at per job, which gives directly observed lateness rather than
// lateness inferred by replaying a schedule against sleep history.
type hermesProvider struct{}

func (h *hermesProvider) Name() string { return "hermes" }

func (h *hermesProvider) Where() string {
	return shortenHome(h.jobsFile())
}

func (h *hermesProvider) jobsFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".hermes", "cron", "jobs.json")
}

func (h *hermesProvider) Available() bool {
	p := h.jobsFile()
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// hermesFile is the on-disk shape. Only the fields goguma needs are
// declared; Hermes carries a lot more per job (prompts, model settings,
// credentials context) that is deliberately not read.
type hermesFile struct {
	Jobs []hermesJob `json:"jobs"`
}

type hermesJob struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Enabled  bool           `json:"enabled"`
	State    string         `json:"state"`
	Schedule hermesSchedule `json:"schedule"`

	// ScheduleDisplay is Hermes's own rendering and already uses the forms
	// goguma parses ("0 9 * * *", "every 360m").
	ScheduleDisplay string `json:"schedule_display"`

	LastRunAt *time.Time `json:"last_run_at"`
	// FireClaim is Hermes's dispatch claim: stamped when a run is handed out,
	// cleared when it completes. Its presence is the only on-disk signal that
	// a job is in flight right now, and `at` is the only record of when the
	// run began.
	//
	// This is internal coordination state, not a published field. It is read
	// defensively for exactly that reason; see ObserveRun.
	FireClaim  *hermesFireClaim `json:"fire_claim"`
	NextRunAt  *time.Time       `json:"next_run_at"`
	LastStatus string           `json:"last_status"`
	PausedAt   *time.Time       `json:"paused_at"`
}

type hermesFireClaim struct {
	At *time.Time `json:"at"`
	By string     `json:"by"`
}

type hermesSchedule struct {
	Kind    string `json:"kind"`
	Expr    string `json:"expr"`
	Minutes int    `json:"minutes"`
	Display string `json:"display"`
}

func (h *hermesProvider) Discover(ctx context.Context) ([]Entry, error) {
	path := h.jobsFile()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", shortenHome(path), err)
	}
	var f hermesFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", shortenHome(path), err)
	}

	out := make([]Entry, 0, len(f.Jobs))
	for _, j := range f.Jobs {
		if !j.Enabled || j.PausedAt != nil {
			continue
		}
		e := Entry{
			Name:     j.Name,
			Source:   SourceHermes,
			Schedule: hermesScheduleExpr(j),
			Command:  "hermes cron run " + j.Name,
			File:     shortenHome(path),
			Label:    j.ID,

			// A cron-kind schedule names a specific wall-clock time, which
			// implies the time is the point: a 9am briefing delivered at 1pm
			// has lost most of its value. An interval schedule only asks to
			// happen every so often, so running late costs far less.
			TimeSensitive: j.Schedule.Kind == "cron",
		}
		if j.LastRunAt != nil {
			e.LastRun = *j.LastRunAt
		}
		if j.NextRunAt != nil {
			e.NextRun = *j.NextRunAt
		}
		out = append(out, e)
	}
	return out, nil
}

// hermesScheduleExpr picks the expression goguma can parse.
func hermesScheduleExpr(j hermesJob) string {
	switch {
	case j.Schedule.Expr != "":
		return j.Schedule.Expr
	case j.Schedule.Kind == "interval" && j.Schedule.Minutes > 0:
		return fmt.Sprintf("every %dm", j.Schedule.Minutes)
	case j.Schedule.Display != "":
		return j.Schedule.Display
	default:
		return j.ScheduleDisplay
	}
}

// shortenHome renders a path with ~ so output stays readable and does not
// leak a full home directory into logs the user may share.
func shortenHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || p == "" {
		return p
	}
	if rel, err := filepath.Rel(home, p); err == nil && len(rel) > 0 && rel[0] != '.' {
		return filepath.Join("~", rel)
	}
	return p
}

// ObserveRun reads Hermes's own record of a job's last run.
//
// Hermes runs cron jobs *inside* its gateway process; its source is explicit
// that "the cron ticker only runs inside the gateway; there is no standalone
// cron daemon", so no process ever appears or exits for a job and process
// watching cannot see one. What it does do, on every completion, is write:
//
//	job["last_run_at"] = now
//	job["last_status"] = "ok" if success else "error"
//	job["fire_claim"]  = None
//
// and stamp `fire_claim = {"at": now, ...}` on dispatch. That is a complete
// per-job signal (start, finish, and outcome) for the price of reading a
// file goguma already parses for schedules.
//
// Everything here is defensive. `jobs.json` carries no schema version, and
// `fire_claim` is coordination state Hermes is free to rename. Anything
// unexpected returns ok=false, which sends the caller back to a fixed window.
func (h *hermesProvider) ObserveRun(_ context.Context, jobID string) (RunRecord, bool) {
	path := h.jobsFile()
	if path == "" {
		return RunRecord{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return RunRecord{}, false
	}
	var file hermesFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return RunRecord{}, false
	}
	// An empty jobs array is a readable file describing nothing, which is not
	// the same as a file we failed to understand, but either way there is no
	// record for this job.
	for _, j := range file.Jobs {
		if !sameJob(j, jobID) {
			continue
		}
		rec := RunRecord{Running: j.FireClaim != nil}
		if j.FireClaim != nil && j.FireClaim.At != nil {
			rec.StartedAt = *j.FireClaim.At
		}
		if j.LastRunAt != nil {
			rec.CompletedAt = *j.LastRunAt
		}
		if j.NextRunAt != nil {
			rec.NextRun = *j.NextRunAt
		}
		switch j.LastStatus {
		case "ok", "error":
			rec.Status = j.LastStatus
		}
		return rec, true
	}
	return RunRecord{}, false
}

// sameJob matches goguma's slug against Hermes's own id or name, because
// adoption slugs the name and Hermes keys on an opaque id.
func sameJob(j hermesJob, wanted string) bool {
	return j.ID == wanted || model.Slug(j.Name) == wanted || j.Name == wanted
}
