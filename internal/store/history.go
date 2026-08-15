package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sort"

	"github.com/junnam586/goguma/internal/model"
	"time"
)

// maxHistoryLines bounds a job's retained history. The estimator only ever
// looks at the last ~20 runs, so unbounded growth buys nothing; keeping a few
// hundred leaves room for the trend view without the file becoming a problem
// on a job that runs every 30 minutes for a year.
const maxHistoryLines = 500

// AppendRun records a completed run. JSONL is used rather than a database
// because appends are atomic at the OS level for small writes, the file stays
// greppable by hand, and a corrupted tail loses one run rather than all of
// them.
func (s *Store) AppendRun(r model.Run) error {
	path := s.layout.HistoryFile(r.JobID)
	if err := os.MkdirAll(s.layout.HistoryDir(), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	b = append(b, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return s.trimHistoryLocked(path)
}

// Runs reads a job's history in chronological order. Unparseable lines are
// skipped: a truncated final line from a hard kill must not make the whole
// history unreadable and silently reset the job to a cold-start ceiling.
func (s *Store) Runs(jobID string) ([]model.Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return readRuns(s.layout.HistoryFile(jobID))
}

// HasRunAt reports whether a run is already recorded for this fire time.
//
// Guards against double-recording a slept-through fire. The sleep detector can
// observe overlapping intervals across a suspend/resume cycle, and without
// this a single missed 06:00 could be written twice and read as two misses.
// Matched to the minute, because a fire time is a schedule instant and the
// stored value is the same instant, not an observation of one.
func (s *Store) HasRunAt(jobID string, fire time.Time) bool {
	runs, err := s.Runs(jobID)
	if err != nil {
		return false
	}
	for _, r := range runs {
		if r.WindowOpened.Truncate(time.Minute).Equal(fire.Truncate(time.Minute)) {
			return true
		}
		if !r.Started.IsZero() && r.Started.Truncate(time.Minute).Equal(fire.Truncate(time.Minute)) {
			return true
		}
	}
	return false
}

func readRuns(path string) ([]model.Run, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []model.Run
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 8*1024), 256*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r model.Run
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].WindowOpened.Before(out[j].WindowOpened)
	})
	return out, nil
}

// trimHistoryLocked caps a history file, keeping the most recent runs.
func (s *Store) trimHistoryLocked(path string) error {
	runs, err := readRuns(path)
	if err != nil || len(runs) <= maxHistoryLines {
		return err
	}
	runs = runs[len(runs)-maxHistoryLines:]

	var buf []byte
	for _, r := range runs {
		b, err := json.Marshal(r)
		if err != nil {
			continue
		}
		buf = append(buf, b...)
		buf = append(buf, '\n')
	}
	return writeAtomic(path, buf, 0o600)
}

// DeleteHistory removes a job's recorded runs. Only reachable through an
// explicit `goguma history --reset`, never as a side effect of removing a
// job, so a re-added job keeps its learned ceiling.
func (s *Store) DeleteHistory(jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.layout.HistoryFile(jobID))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
