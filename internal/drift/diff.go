package drift

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Finding is one desired leaf value that doesn't match the device.
type Finding struct {
	Section string // component, e.g. "sys", "switch:0"
	Path    string // dotted path within the section, e.g. "device.eco_mode"
	Want    any
	Have    any // nil when the key/section is absent on the device
}

// maxSummarized bounds how many findings Summarize spells out.
const maxSummarized = 5

// Diff compares desired (from Render) against the device's actual config.
// Only desired leaves are compared -- extra device config is never drift.
func Diff(desired map[string]map[string]any, actual map[string]json.RawMessage) ([]Finding, error) {
	var findings []Finding
	for _, section := range sortedKeys(desired) {
		raw, ok := actual[section]
		if !ok {
			findings = append(findings, Finding{Section: section, Path: "", Want: "section present", Have: nil})
			continue
		}
		var have map[string]any
		if err := json.Unmarshal(raw, &have); err != nil {
			return nil, fmt.Errorf("drift: decode section %s: %w", section, err)
		}
		findings = append(findings, diffMap(section, "", desired[section], have)...)
	}
	return findings, nil
}

func diffMap(section, prefix string, want, have map[string]any) []Finding {
	var out []Finding
	for _, k := range sortedKeys(want) {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		w := want[k]
		h, ok := have[k]
		if wm, isMap := w.(map[string]any); isMap {
			hm, _ := h.(map[string]any)
			if hm == nil {
				hm = map[string]any{}
			}
			out = append(out, diffMap(section, path, wm, hm)...)
			continue
		}
		if !ok || !leafEqual(w, h) {
			out = append(out, Finding{Section: section, Path: path, Want: w, Have: h})
		}
	}
	return out
}

// leafEqual compares JSON-normalized leaves; all numbers compare as float64.
func leafEqual(w, h any) bool {
	if wf, ok := toFloat(w); ok {
		hf, ok2 := toFloat(h)
		return ok2 && wf == hf
	}
	return w == h
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// Sections returns the sorted unique sections present in findings.
func Sections(findings []Finding) []string {
	seen := map[string]bool{}
	for _, f := range findings {
		seen[f.Section] = true
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Summarize renders findings as a short human-readable list for condition
// messages and Events, capped at maxSummarized entries.
func Summarize(findings []Finding) string {
	var parts []string
	for i, f := range findings {
		if i == maxSummarized {
			parts = append(parts, fmt.Sprintf("(+%d more)", len(findings)-maxSummarized))
			break
		}
		loc := f.Section
		if f.Path != "" {
			loc += "." + f.Path
		}
		parts = append(parts, fmt.Sprintf("%s: want %v, have %v", loc, f.Want, f.Have))
	}
	return strings.Join(parts, "; ")
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
