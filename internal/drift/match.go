// Package drift contains the pure logic of profile-driven configuration:
// matching devices to profiles, rendering desired config, and diffing it
// against live device config. No Kubernetes client code lives here.
package drift

import (
	"fmt"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
)

// MatchProfile resolves which profile governs dev. An explicit
// spec.profileRef always wins (a missing ref returns nil plus a warning).
// Otherwise selector-matching profiles compete: highest priority wins,
// then the lexicographically smallest name. A nil selector never matches
// (pin-only profile); an empty non-nil selector matches everything.
// Invalid selectors are skipped with a warning so one bad profile can't
// break matching for the rest.
func MatchProfile(dev *shellyv1alpha1.ShellyDevice, profiles []shellyv1alpha1.ShellyProfile) (*shellyv1alpha1.ShellyProfile, []string) {
	if ref := dev.Spec.ProfileRef; ref != "" {
		for i := range profiles {
			if profiles[i].Name == ref {
				return &profiles[i], nil
			}
		}
		return nil, []string{fmt.Sprintf("profileRef %q not found", ref)}
	}

	var warnings []string
	var candidates []*shellyv1alpha1.ShellyProfile
	set := labels.Set(dev.Labels)
	for i := range profiles {
		p := &profiles[i]
		if p.Spec.Selector == nil {
			continue
		}
		sel, err := metav1.LabelSelectorAsSelector(p.Spec.Selector)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("profile %s: invalid selector: %v", p.Name, err))
			continue
		}
		if sel.Matches(set) {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		return nil, warnings
	}
	sort.Slice(candidates, func(a, b int) bool {
		if candidates[a].Spec.Priority != candidates[b].Spec.Priority {
			return candidates[a].Spec.Priority > candidates[b].Spec.Priority
		}
		return candidates[a].Name < candidates[b].Name
	})
	return candidates[0], warnings
}
