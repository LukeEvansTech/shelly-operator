package v1alpha1

import (
	"reflect"
	"strings"
	"testing"
)

func TestDeviceObjectName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"3C8A1FEC8E3C", "3c8a1fec8e3c"},
		{"3c:8a:1f:ec:8e:3c", "3c8a1fec8e3c"},
		{"3C-8A-1F-EC-8E-3C", "3c8a1fec8e3c"},
	}
	for _, c := range cases {
		if got := DeviceObjectName(c.in); got != c.want {
			t.Errorf("DeviceObjectName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDeviceLabels(t *testing.T) {
	got := DeviceLabels("SNPL-00112UK", "PlusPlugUK", 2)
	want := map[string]string{
		"shelly.thirdimpact.io/model": "SNPL-00112UK",
		"shelly.thirdimpact.io/app":   "PlusPlugUK",
		"shelly.thirdimpact.io/gen":   "2",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DeviceLabels() = %v, want %v", got, want)
	}
}

func TestSanitizeLabelValue(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SNPL-00112UK", "SNPL-00112UK"},                          // clean value untouched
		{"Plus Plug UK", "Plus_Plug_UK"},                          // space replaced
		{"-weird.", "weird"},                                      // trimmed to alphanumeric edges
		{strings.Repeat("a", 70), strings.Repeat("a", 63)},       // capped at 63
	}
	for _, c := range cases {
		if got := sanitizeLabelValue(c.in); got != c.want {
			t.Errorf("sanitizeLabelValue(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDeviceLabelsSanitizes(t *testing.T) {
	got := DeviceLabels("Plus Plug", "App/Name", 2)
	if got[LabelModel] != "Plus_Plug" || got[LabelApp] != "App_Name" {
		t.Errorf("DeviceLabels() = %v", got)
	}
}
