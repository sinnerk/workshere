package scan

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Workflow is the part of a GitHub Actions file workshere understands. The
// parser is defensive on purpose: an unfamiliar shape becomes a warning, never
// a crash and never a false accusation.
type Workflow struct {
	Path     string
	Jobs     []Job
	Triggers []Trigger
}

// Trigger is one `on:` entry that filters by path.
type Trigger struct {
	Event       string
	Paths       []string
	PathsIgnore []string
}

// Job is one job in a workflow.
type Job struct {
	ID        string
	RunsOn    []string
	Env       map[string]string
	Steps     []Step
	Matrix    map[string][]string
	Container string
}

// Step is one step in a job.
type Step struct {
	Name string
	Uses string
	Run  string
	With map[string]string
	Env  map[string]string
}

// ParseWorkflow turns YAML bytes into the model above.
func ParseWorkflow(relPath string, data []byte) (Workflow, error) {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Workflow{}, err
	}
	if raw == nil {
		return Workflow{}, fmt.Errorf("empty workflow")
	}

	w := Workflow{Path: relPath}

	// yaml.v3 resolves the bare key `on` to the boolean true, so accept both.
	w.Triggers = parseTriggers(first(raw, "on", "true"))

	jobs := asMap(first(raw, "jobs"))
	ids := make([]string, 0, len(jobs))
	for id := range jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		jm := asMap(jobs[id])
		if jm == nil {
			continue
		}
		job := Job{
			ID:     id,
			Env:    asStringMap(jm["env"]),
			RunsOn: asStringSlice(jm["runs-on"]),
		}
		job.Container = asString(first(jm, "container"))
		if strategy := asMap(jm["strategy"]); strategy != nil {
			job.Matrix = parseMatrix(strategy["matrix"])
		}
		for _, stepRaw := range asSlice(jm["steps"]) {
			sm := asMap(stepRaw)
			if sm == nil {
				continue
			}
			job.Steps = append(job.Steps, Step{
				Name: asString(sm["name"]),
				Uses: asString(sm["uses"]),
				Run:  asString(sm["run"]),
				With: asStringMap(sm["with"]),
				Env:  asStringMap(sm["env"]),
			})
		}
		w.Jobs = append(w.Jobs, job)
	}

	return w, nil
}

func first(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}

func parseTriggers(v any) []Trigger {
	var out []Trigger

	add := func(event string, body any) {
		t := Trigger{Event: event}
		if m := asMap(body); m != nil {
			t.Paths = asStringSlice(m["paths"])
			t.PathsIgnore = asStringSlice(m["paths-ignore"])
		}
		out = append(out, t)
	}

	switch on := v.(type) {
	case string:
		add(on, nil)
	case []any:
		for _, e := range on {
			if s := asString(e); s != "" {
				add(s, nil)
			}
		}
	case map[string]any:
		events := make([]string, 0, len(on))
		for e := range on {
			events = append(events, e)
		}
		sort.Strings(events)
		for _, e := range events {
			add(e, on[e])
		}
	}

	return out
}

func parseMatrix(v any) map[string][]string {
	m := asMap(v)
	if m == nil {
		return nil
	}
	out := map[string][]string{}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "include" || k == "exclude" {
			continue
		}
		if vals := asStringSlice(m[k]); len(vals) > 0 {
			out[k] = vals
		}
	}
	return out
}

// ExpandSpec resolves "${{ matrix.foo }}" against the job matrix. A literal
// value comes back unchanged.
func ExpandSpec(spec string, matrix map[string][]string) []string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil
	}
	if !strings.Contains(spec, "matrix.") {
		return []string{spec}
	}

	ref := spec
	if i := strings.Index(ref, "matrix."); i >= 0 {
		ref = ref[i+len("matrix."):]
	}
	ref = strings.TrimRight(strings.TrimSpace(ref), "}")
	ref = strings.TrimSpace(ref)

	if vals, ok := matrix[ref]; ok && len(vals) > 0 {
		return vals
	}
	return nil
}

// --- tiny YAML accessors -------------------------------------------------

func asMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func asSlice(v any) []any {
	if v == nil {
		return nil
	}
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

func asString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return ""
	}
}

func asStringSlice(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		if t == "" {
			return nil
		}
		return []string{strings.TrimSpace(t)}
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s := asString(e); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func asStringMap(v any) map[string]string {
	m := asMap(v)
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, val := range m {
		out[k] = asString(val)
	}
	return out
}
