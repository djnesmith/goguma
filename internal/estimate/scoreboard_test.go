package estimate

import (
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/model"
)

// The only honest claim goguma can make about its own worth is a count of runs
// it woke the machine for, because every one of those was going to be skipped.
// Printed beside the misses, since a scoreboard showing only wins is an advert.
func TestSummarizeCountsWakesAndMisses(t *testing.T) {
	job := &model.Job{ID: "j", Name: "j", Detection: model.DetectMark}
	now := time.Now()
	runs := []model.Run{
		{JobID: "j", Outcome: model.OutcomeOK, Started: now, Ended: now.Add(time.Second),
			Duration: model.Duration(time.Second), WokeMachine: true},
		{JobID: "j", Outcome: model.OutcomeOK, Started: now, Ended: now.Add(time.Second),
			Duration: model.Duration(time.Second), WokeMachine: true},
		{JobID: "j", Outcome: model.OutcomeOK, Started: now, Ended: now.Add(time.Second),
			Duration: model.Duration(time.Second)},
		{JobID: "j", Outcome: model.OutcomeSlept, WindowOpened: now},
	}
	st := Summarize(job, runs, config.Default())

	if st.Woken != 2 {
		t.Errorf("Woken = %d, want 2", st.Woken)
	}
	if st.Slept != 1 {
		t.Errorf("Slept = %d, want 1", st.Slept)
	}
	// A missed run is not evidence about how long the job takes.
	if model.OutcomeSlept.TrainsEstimator() {
		t.Error("a run that never happened is training the duration estimate")
	}
}
