package drift

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
)

func profile(name string, priority int32, sel *metav1.LabelSelector) shellyv1alpha1.ShellyProfile {
	return shellyv1alpha1.ShellyProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       shellyv1alpha1.ShellyProfileSpec{Selector: sel, Priority: priority},
	}
}

func device(labels map[string]string, profileRef string) *shellyv1alpha1.ShellyDevice {
	return &shellyv1alpha1.ShellyDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "aabbccddee01", Labels: labels},
		Spec:       shellyv1alpha1.ShellyDeviceSpec{ProfileRef: profileRef},
	}
}

func matchPlugs() *metav1.LabelSelector {
	return &metav1.LabelSelector{MatchLabels: map[string]string{shellyv1alpha1.LabelApp: "PlusPlugUK"}}
}

func TestMatchProfileSelector(t *testing.T) {
	dev := device(map[string]string{shellyv1alpha1.LabelApp: "PlusPlugUK"}, "")
	profiles := []shellyv1alpha1.ShellyProfile{
		profile("other", 50, &metav1.LabelSelector{MatchLabels: map[string]string{shellyv1alpha1.LabelApp: "Plus1PMMini"}}),
		profile("plugs", 10, matchPlugs()),
	}
	got, warns := MatchProfile(dev, profiles)
	if got == nil || got.Name != "plugs" || len(warns) != 0 {
		t.Errorf("got %v warns %v, want plugs", got, warns)
	}
}

func TestMatchProfilePriorityAndNameTiebreak(t *testing.T) {
	dev := device(map[string]string{shellyv1alpha1.LabelApp: "PlusPlugUK"}, "")
	profiles := []shellyv1alpha1.ShellyProfile{
		profile("b-low", 1, matchPlugs()),
		profile("z-high", 20, matchPlugs()),
		profile("a-high", 20, matchPlugs()),
	}
	got, _ := MatchProfile(dev, profiles)
	if got == nil || got.Name != "a-high" {
		t.Errorf("got %v, want a-high (priority 20, name tiebreak)", got)
	}
}

func TestMatchProfileRefPinWins(t *testing.T) {
	dev := device(map[string]string{shellyv1alpha1.LabelApp: "PlusPlugUK"}, "pinned")
	profiles := []shellyv1alpha1.ShellyProfile{
		profile("plugs", 100, matchPlugs()),
		profile("pinned", 0, nil), // nil selector: pin-only profile
	}
	got, _ := MatchProfile(dev, profiles)
	if got == nil || got.Name != "pinned" {
		t.Errorf("got %v, want pinned", got)
	}
}

func TestMatchProfileRefMissing(t *testing.T) {
	dev := device(nil, "ghost")
	got, warns := MatchProfile(dev, []shellyv1alpha1.ShellyProfile{profile("plugs", 0, matchPlugs())})
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "ghost") {
		t.Errorf("warns = %v", warns)
	}
}

func TestMatchProfileNilSelectorNeverMatches(t *testing.T) {
	dev := device(map[string]string{shellyv1alpha1.LabelApp: "PlusPlugUK"}, "")
	got, _ := MatchProfile(dev, []shellyv1alpha1.ShellyProfile{profile("pin-only", 100, nil)})
	if got != nil {
		t.Errorf("nil selector must not match, got %v", got)
	}
}

func TestMatchProfileEmptySelectorMatchesAll(t *testing.T) {
	dev := device(map[string]string{shellyv1alpha1.LabelApp: "PlusPlugUK"}, "")
	got, _ := MatchProfile(dev, []shellyv1alpha1.ShellyProfile{profile("everything", 0, &metav1.LabelSelector{})})
	if got == nil || got.Name != "everything" {
		t.Errorf("empty selector must match all, got %v", got)
	}
}

func TestMatchProfileInvalidSelectorSkippedWithWarning(t *testing.T) {
	bad := &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
		{Key: "k", Operator: "Bogus"},
	}}
	dev := device(map[string]string{shellyv1alpha1.LabelApp: "PlusPlugUK"}, "")
	got, warns := MatchProfile(dev, []shellyv1alpha1.ShellyProfile{
		profile("bad", 100, bad),
		profile("plugs", 1, matchPlugs()),
	})
	if got == nil || got.Name != "plugs" {
		t.Errorf("got %v, want plugs (bad selector skipped)", got)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "bad") {
		t.Errorf("warns = %v", warns)
	}
}
