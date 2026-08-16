package scan

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// InferManifest works out how to read an app's schedule file by looking at it.
//
// Writing a manifest by hand means opening someone's JSON, finding the array
// of jobs inside whatever wrapper object it sits in, and identifying which
// field is the name and which is the cron expression. That is exactly the kind
// of thing a person gets wrong once and then gives up on, and it is entirely
// mechanical: a cron expression looks like a cron expression.
//
// The result is a starting point, not an answer. It is written to a file the
// user can read and correct, and every guess it makes is one they can check
// against the sample values printed beside it.
func InferManifest(path, name string) (Manifest, []string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, nil, err
	}
	var doc any
	if err := json.Unmarshal(b, &doc); err != nil {
		return Manifest{}, nil, fmt.Errorf("%s is not JSON goguma can read: %w", path, err)
	}

	jobsPath, jobs := findJobArray(doc, "")
	if len(jobs) == 0 {
		return Manifest{}, nil, fmt.Errorf("%s has no list of objects in it that looks like jobs", path)
	}

	m := Manifest{
		Name:   name,
		Files:  []string{path},
		Jobs:   jobsPath,
		Fields: ManifestFields{},
	}

	// Two different searches, because two different things are being looked for.
	//
	// Identity fields (name, id, enabled) must be present in most jobs, or a
	// field one entry happens to carry becomes the mapping for all of them.
	//
	// Schedule fields are the opposite. An app that supports cron *and*
	// intervals *and* one-shots stores each in its own field, so no single one
	// appears in a majority: hermes has 3 cron jobs, 1 interval and 3 one-offs,
	// which put `schedule.expr` under the threshold and made the first version
	// of this find no schedule at all. They are matched over every path, by the
	// shape of their values.
	common := commonPaths(jobs)
	all := allPaths(jobs)
	var notes []string
	search := func(paths []string) func(*string, string, func(string, []any) bool) {
		return func(target *string, label string, match func(p string, values []any) bool) {
			for _, p := range paths {
				if match(p, valuesAt(jobs, p)) {
					*target = p
					notes = append(notes, fmt.Sprintf("%-14s %-28s e.g. %s", label, p, sample(jobs, p)))
					return
				}
			}
		}
	}
	pick := search(common)
	pickAny := search(all)

	pickAny(&m.Fields.Cron, "cron", func(_ string, vs []any) bool { return mostlyCron(vs) })
	pickAny(&m.Fields.Every, "every_minutes", func(p string, vs []any) bool {
		return looksLikeInterval(p) && mostlyNumbers(vs)
	})
	pickAny(&m.Fields.At, "at", func(p string, vs []any) bool {
		return looksLikeRunAt(p) && mostlyTimestamps(vs)
	})
	pick(&m.Fields.Name, "name", func(p string, vs []any) bool {
		return looksLikeName(p) && mostlyStrings(vs)
	})
	pick(&m.Fields.ID, "id", func(p string, vs []any) bool {
		return looksLikeID(p) && mostlyStrings(vs)
	})
	pick(&m.Fields.Enabled, "enabled", func(p string, vs []any) bool {
		return looksLikeEnabled(p) && mostlyBools(vs)
	})
	pick(&m.Fields.Disabled, "disabled", func(p string, _ []any) bool { return looksLikePaused(p) })
	pick(&m.Fields.LastRun, "last_run", func(p string, vs []any) bool {
		return looksLikeLastRun(p) && mostlyTimestamps(vs)
	})
	pick(&m.Fields.NextRun, "next_run", func(p string, vs []any) bool {
		return looksLikeNextRun(p) && mostlyTimestamps(vs)
	})
	pick(&m.Fields.Status, "status", func(p string, vs []any) bool {
		return looksLikeStatus(p) && mostlyStrings(vs)
	})

	if m.Fields.Name == "" {
		// Any string field, rather than refusing outright: the user can see
		// what was chosen and change it.
		pick(&m.Fields.Name, "name (guess)", func(_ string, vs []any) bool { return mostlyStrings(vs) })
	}
	m.Command = name + " run {name}"
	return m, notes, nil
}

// findJobArray looks for the array of job objects, which is usually one key
// down ("jobs", "tasks", "schedules") but is sometimes the document itself.
func findJobArray(node any, path string) (string, []map[string]any) {
	switch v := node.(type) {
	case []any:
		var objs []map[string]any
		for _, item := range v {
			if o, ok := item.(map[string]any); ok {
				objs = append(objs, o)
			}
		}
		if len(objs) > 0 {
			return path, objs
		}
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		// A likely-named key first, so a document with several arrays picks
		// the one that means jobs.
		sort.SliceStable(keys, func(i, j int) bool {
			return jobKeyRank(keys[i]) < jobKeyRank(keys[j])
		})
		for _, k := range keys {
			child := join(path, k)
			if p, objs := findJobArray(v[k], child); len(objs) > 0 {
				return p, objs
			}
		}
		// An object keyed by id, whose values are all job-shaped.
		var objs []map[string]any
		for _, k := range keys {
			if o, ok := v[k].(map[string]any); ok {
				objs = append(objs, o)
			}
		}
		if len(objs) == len(v) && len(objs) > 1 {
			return path, objs
		}
	}
	return "", nil
}

func jobKeyRank(k string) int {
	switch strings.ToLower(k) {
	case "jobs":
		return 0
	case "tasks":
		return 1
	case "schedules", "scheduled", "crons", "timers":
		return 2
	}
	return 9
}

func join(base, key string) string {
	if base == "" {
		return key
	}
	return base + "." + key
}

// commonPaths returns every leaf path present in more than half the jobs,
// sorted shallowest first so `cron` beats `schedule.cron` when both exist.
func commonPaths(jobs []map[string]any) []string {
	counts := map[string]int{}
	for _, j := range jobs {
		for _, p := range leafPaths(j, "") {
			counts[p]++
		}
	}
	var out []string
	for p, n := range counts {
		if n*2 > len(jobs) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := strings.Count(out[i], "."), strings.Count(out[j], ".")
		if di != dj {
			return di < dj
		}
		return out[i] < out[j]
	})
	return out
}

// allPaths is every leaf path seen in any job, shallowest first.
func allPaths(jobs []map[string]any) []string {
	seen := map[string]bool{}
	for _, j := range jobs {
		for _, p := range leafPaths(j, "") {
			seen[p] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := strings.Count(out[i], "."), strings.Count(out[j], ".")
		if di != dj {
			return di < dj
		}
		return out[i] < out[j]
	})
	return out
}

func leafPaths(node map[string]any, base string) []string {
	var out []string
	for k, v := range node {
		p := join(base, k)
		if child, ok := v.(map[string]any); ok {
			out = append(out, leafPaths(child, p)...)
			continue
		}
		out = append(out, p)
	}
	return out
}

func valuesAt(jobs []map[string]any, path string) []any {
	var out []any
	for _, j := range jobs {
		if v := lookup(j, path); v != nil {
			out = append(out, v)
		}
	}
	return out
}

func sample(jobs []map[string]any, path string) string {
	for _, v := range valuesAt(jobs, path) {
		s := fmt.Sprintf("%v", v)
		if len(s) > 40 {
			s = s[:37] + "…"
		}
		return s
	}
	return ""
}

// ---- shape tests ----

func majority(vs []any, ok func(any) bool) bool {
	if len(vs) == 0 {
		return false
	}
	n := 0
	for _, v := range vs {
		if ok(v) {
			n++
		}
	}
	return n*2 > len(vs)
}

func mostlyStrings(vs []any) bool {
	return majority(vs, func(v any) bool { s, ok := v.(string); return ok && s != "" })
}

func mostlyNumbers(vs []any) bool {
	return majority(vs, func(v any) bool { n, ok := v.(float64); return ok && n > 0 })
}

func mostlyBools(vs []any) bool {
	return majority(vs, func(v any) bool { _, ok := v.(bool); return ok })
}

func mostlyTimestamps(vs []any) bool {
	return majority(vs, func(v any) bool {
		s, ok := v.(string)
		return ok && !timeAt(map[string]any{"x": s}, "x").IsZero()
	})
}

// mostlyCron recognises a cron expression by its shape, which is the one
// field that can be identified from its value rather than its name.
func mostlyCron(vs []any) bool {
	return majority(vs, func(v any) bool {
		s, ok := v.(string)
		if !ok {
			return false
		}
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "@") {
			return len(s) > 1
		}
		fields := strings.Fields(s)
		if len(fields) != 5 {
			return false
		}
		for _, f := range fields {
			if strings.TrimLeft(f, "0123456789*/,-") != "" {
				return false
			}
		}
		return true
	})
}

// leafOf is the last segment of a dotted path. Every name test below works on
// it rather than on the whole path, because a substring match against the path
// is how "created_at" became the one-shot schedule field: the path contains
// "at", and so does almost everything else.
func leafOf(p string) string {
	if i := strings.LastIndex(p, "."); i >= 0 {
		p = p[i+1:]
	}
	return strings.ToLower(p)
}

// leafIs matches the leaf exactly against any of the given names.
func leafIs(p string, names ...string) bool {
	leaf := leafOf(p)
	for _, n := range names {
		if leaf == n {
			return true
		}
	}
	return false
}

// leafHas matches a word inside the leaf, for the fields whose names vary too
// much to enumerate ("scheduleMinutes", "interval_minutes", "everyMinutes").
func leafHas(p string, words ...string) bool {
	leaf := leafOf(p)
	for _, w := range words {
		if strings.Contains(leaf, w) {
			return true
		}
	}
	return false
}

func looksLikeName(p string) bool {
	return leafIs(p, "name", "title", "label", "summary", "description")
}

func looksLikeID(p string) bool {
	return leafIs(p, "id", "uuid", "guid", "key", "slug", "ref", "identifier", "job_id", "jobid")
}

func looksLikeEnabled(p string) bool {
	return leafIs(p, "enabled", "active", "on", "is_enabled", "isenabled")
}

func looksLikePaused(p string) bool {
	return leafHas(p, "paused", "disabled", "archived", "deleted", "suspended")
}

func looksLikeInterval(p string) bool {
	return leafHas(p, "minute", "interval", "every", "period", "frequency")
}

// looksLikeRunAt is deliberately strict, and explicitly rejects the
// bookkeeping timestamps every record carries. `created_at` is a timestamp on
// a job and is not when the job runs.
func looksLikeRunAt(p string) bool {
	if leafHas(p, "created", "updated", "modified", "deleted", "last", "completed", "started") {
		return false
	}
	return leafIs(p, "at", "run_at", "runat", "when", "due", "due_at", "scheduled_at", "fire_at", "start_at")
}

func looksLikeLastRun(p string) bool {
	return leafHas(p, "last_run", "lastrun", "last_fired", "lastfired", "last_executed", "previous_run")
}

func looksLikeNextRun(p string) bool {
	return leafHas(p, "next_run", "nextrun", "next_fire", "nextfire", "scheduled_for")
}

func looksLikeStatus(p string) bool {
	return leafHas(p, "status", "result", "outcome")
}
