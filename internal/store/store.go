// Package store persists jobs, run history, and the event log.
//
// All writes are atomic (write-temp-then-rename) because the daemon can be
// killed at any moment — logout, shutdown, a launchd bootout — and a
// half-written jobs.json that fails to parse would silently disable every
// registered job.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/junnam/wakeguard/internal/model"
	"github.com/junnam/wakeguard/internal/paths"
)

// Store owns the on-disk state. Safe for concurrent use: the daemon's loop
// and its IPC handlers both touch it.
type Store struct {
	layout paths.Layout
	mu     sync.RWMutex
	jobs   map[string]*model.Job
	loaded bool

	// loadErr records that jobs.json could not be parsed.
	//
	// When set, the store refuses to write. Starting with an empty job list
	// and then persisting would overwrite the user's unreadable-but-recoverable
	// file with an empty one, destroying the very data they need to repair. So
	// the daemon runs, reports the problem loudly, and changes nothing on disk
	// until the file is fixed.
	loadErr error
}

func New(layout paths.Layout) *Store {
	return &Store{layout: layout, jobs: map[string]*model.Job{}}
}

// Layout exposes the resolved paths, so callers do not each re-resolve them.
func (s *Store) Layout() paths.Layout { return s.layout }

// jobsFile is the on-disk envelope. Versioned so a future format change can
// migrate rather than silently misread.
type jobsFile struct {
	Version int          `json:"version"`
	Jobs    []*model.Job `json:"jobs"`
}

const jobsFileVersion = 1

// Load reads jobs.json into memory. A missing file is not an error — a fresh
// install legitimately has no jobs.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.layout.JobsFile())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s.jobs, s.loaded = map[string]*model.Job{}, true
			return nil
		}
		return err
	}
	var f jobsFile
	if err := json.Unmarshal(b, &f); err != nil {
		// Not fatal. A daemon that refuses to start is indistinguishable from
		// one that was never installed: `status` reports only "not running",
		// launchd restarts it into the same failure, and every job silently
		// stops waking. Starting up and saying exactly what is wrong is far
		// more recoverable.
		s.jobs, s.loaded = map[string]*model.Job{}, true
		s.loadErr = fmt.Errorf("parsing %s: %w", s.layout.JobsFile(), err)
		return nil
	}
	if f.Version > jobsFileVersion {
		return fmt.Errorf("%s was written by a newer WakeGuard (format v%d, this build understands v%d)",
			s.layout.JobsFile(), f.Version, jobsFileVersion)
	}

	jobs := make(map[string]*model.Job, len(f.Jobs))
	for _, j := range f.Jobs {
		if j == nil {
			continue
		}
		if j.ID == "" {
			j.ID = model.Slug(j.Name)
		}
		// A malformed entry is skipped rather than failing the whole load:
		// one bad hand-edit should not take every other job offline. The
		// daemon surfaces these as warnings.
		if err := j.Validate(); err != nil {
			continue
		}
		jobs[j.ID] = j
	}
	s.jobs, s.loaded = jobs, true
	return nil
}

// InvalidJobs re-reads the file and reports entries that failed validation,
// so `status` and `doctor` can tell the user exactly which job is broken and
// why instead of it just being absent.
func (s *Store) InvalidJobs() []error {
	b, err := os.ReadFile(s.layout.JobsFile())
	if err != nil {
		return nil
	}
	var f jobsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return []error{fmt.Errorf("jobs.json is not valid JSON: %w", err)}
	}
	var out []error
	for _, j := range f.Jobs {
		if j == nil {
			continue
		}
		if j.ID == "" {
			j.ID = model.Slug(j.Name)
		}
		if err := j.Validate(); err != nil {
			out = append(out, err)
		}
	}
	return out
}

// Jobs returns all jobs, sorted by name for stable output.
func (s *Store) Jobs() []*model.Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*model.Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		cp := *j
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Groups returns the distinct group names in use, sorted.
//
// Ungrouped jobs are deliberately not represented: the empty group is the
// absence of a name, not a name, and inventing one here would force every
// caller to special-case it back out. Callers render ungrouped jobs however
// suits them.
func (s *Store) Groups() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]bool, len(s.jobs))
	var out []string
	for _, j := range s.jobs {
		if j.Group == "" || seen[j.Group] {
			continue
		}
		seen[j.Group] = true
		out = append(out, j.Group)
	}
	sort.Strings(out)
	return out
}

// Job looks a job up by id, or by name if no id matches — the CLI takes
// whichever the user typed.
func (s *Store) Job(ref string) (*model.Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if j, ok := s.jobs[ref]; ok {
		cp := *j
		return &cp, true
	}
	if j, ok := s.jobs[model.Slug(ref)]; ok {
		cp := *j
		return &cp, true
	}
	for _, j := range s.jobs {
		if j.Name == ref {
			cp := *j
			return &cp, true
		}
	}
	return nil, false
}

// Add registers a new job, rejecting duplicate ids.
func (s *Store) Add(j *model.Job) error {
	if j.ID == "" {
		j.ID = model.Slug(j.Name)
	}
	if j.ID == "" {
		return fmt.Errorf("job name %q produces an empty id; use a name with letters or digits", j.Name)
	}
	if j.CreatedAt.IsZero() {
		j.CreatedAt = time.Now()
	}
	if err := j.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.jobs[j.ID]; exists {
		return fmt.Errorf("a job named %q already exists (remove it first, or pick another name)", j.Name)
	}
	s.jobs[j.ID] = j
	return s.persistLocked()
}

// Put inserts or replaces a job. Used by GUI edits and by import
// reconciliation, where overwriting is the intent.
func (s *Store) Put(j *model.Job) error {
	if j.ID == "" {
		j.ID = model.Slug(j.Name)
	}
	if err := j.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.jobs[j.ID]; ok && j.CreatedAt.IsZero() {
		j.CreatedAt = existing.CreatedAt
	}
	if j.CreatedAt.IsZero() {
		j.CreatedAt = time.Now()
	}
	s.jobs[j.ID] = j
	return s.persistLocked()
}

// Remove deletes a job. History is deliberately retained: re-adding a job
// with the same name should not start from a cold-start ceiling when the
// machine already knows how long it takes.
func (s *Store) Remove(ref string) (*model.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := ref
	if _, ok := s.jobs[id]; !ok {
		id = model.Slug(ref)
	}
	j, ok := s.jobs[id]
	if !ok {
		return nil, fmt.Errorf("no job named %q", ref)
	}
	delete(s.jobs, id)
	if err := s.persistLocked(); err != nil {
		return nil, err
	}
	return j, nil
}

// SetEnabled toggles a job without removing it.
func (s *Store) SetEnabled(ref string, enabled bool) (*model.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := ref
	if _, ok := s.jobs[id]; !ok {
		id = model.Slug(ref)
	}
	j, ok := s.jobs[id]
	if !ok {
		return nil, fmt.Errorf("no job named %q", ref)
	}
	j.Enabled = enabled
	if err := s.persistLocked(); err != nil {
		return nil, err
	}
	cp := *j
	return &cp, nil
}

// LoadError reports that the job file could not be read, so the daemon can
// surface it and refuse to overwrite what it could not parse.
func (s *Store) LoadError() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadErr
}

func (s *Store) persistLocked() error {
	if s.loadErr != nil {
		return fmt.Errorf(
			"refusing to write jobs.json because it could not be read: %w\n"+
				"fix or remove the file, then restart the daemon", s.loadErr)
	}
	list := make([]*model.Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		list = append(list, j)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })

	b, err := json.MarshalIndent(jobsFile{Version: jobsFileVersion, Jobs: list}, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.layout.JobsFile(), append(b, '\n'), 0o600)
}

// writeAtomic writes via a temp file in the same directory then renames, so
// a reader never observes a partial file and a crash leaves the previous
// version intact.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	// fsync before rename: on a crash-and-reboot the rename can otherwise be
	// visible while the contents are not.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
