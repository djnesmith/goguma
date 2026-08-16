package daemon

import (
	"testing"

	"github.com/junnam586/goguma/internal/model"
)

// "none scheduled" reads identically whether there are no jobs, they are all
// switched off, or a schedule cannot be parsed. Those need three different
// things done about them, so the surface has to say which it is.
func TestNoWakeSaysWhy(t *testing.T) {
	cases := []struct {
		name string
		jobs []model.Job
		want string
	}{
		{"nothing registered", nil, "no jobs are registered yet"},
		{
			"all switched off",
			[]model.Job{
				{ID: "a", Name: "a", Schedule: "0 9 * * *", Enabled: false, Detection: model.DetectNone},
				{ID: "b", Name: "b", Schedule: "0 9 * * *", Enabled: false, Detection: model.DetectNone},
			},
			"every job is switched off",
		},
		{
			"nothing readable",
			[]model.Job{{ID: "a", Name: "a", Schedule: "not a schedule", Enabled: true, Detection: model.DetectNone}},
			"no job has a schedule goguma can read",
		},
		{
			// The ordinary transient case: a job is enabled and readable, so
			// there is nothing wrong and nothing to explain.
			"a healthy job",
			[]model.Job{{ID: "a", Name: "a", Schedule: "0 9 * * *", Enabled: true, Detection: model.DetectNone}},
			"",
		},
		{
			// One good job among broken ones is not a fault worth reporting.
			"one good among broken",
			[]model.Job{
				{ID: "a", Name: "a", Schedule: "nonsense", Enabled: true, Detection: model.DetectNone},
				{ID: "b", Name: "b", Schedule: "0 9 * * *", Enabled: true, Detection: model.DetectNone},
			},
			"",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := testDaemon(t)
			for i := range c.jobs {
				job := c.jobs[i]
				if err := d.store.Add(&job); err != nil {
					t.Fatal(err)
				}
			}
			if got := d.explainNoWakeLocked(); got != c.want {
				t.Errorf("explainNoWakeLocked() = %q, want %q", got, c.want)
			}
		})
	}
}
