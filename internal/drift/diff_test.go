package drift

import (
	"strings"
	"testing"
)

func TestDiffInSync(t *testing.T) {
	desired := map[string]map[string]any{
		"sys":   {"device": map[string]any{"eco_mode": true}},
		"cloud": {"enable": false},
	}
	actual := rawConfig(t, map[string]any{
		"sys":   map[string]any{"device": map[string]any{"eco_mode": true, "name": "x"}, "location": map[string]any{"tz": "UTC"}},
		"cloud": map[string]any{"enable": false, "server": "foo"},
		"wifi":  map[string]any{"sta": map[string]any{"ssid": "net"}},
	})
	findings, err := Diff(desired, actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Errorf("expected in sync, got %+v", findings)
	}
}

func TestDiffFindsDriftedLeaves(t *testing.T) {
	desired := map[string]map[string]any{
		"sys":      {"device": map[string]any{"eco_mode": true, "name": "rack-pdu"}},
		"mqtt":     {"enable": true},
		"switch:0": {"auto_off_delay": float64(300)},
	}
	actual := rawConfig(t, map[string]any{
		"sys":      map[string]any{"device": map[string]any{"eco_mode": false, "name": "rack-pdu"}},
		"mqtt":     map[string]any{"enable": false},
		"switch:0": map[string]any{"auto_off_delay": 300}, // JSON int vs desired float64: must be equal
	})
	findings, err := Diff(desired, actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %+v, want 2 (eco_mode + mqtt.enable)", findings)
	}
	if Sections(findings)[0] != "mqtt" || Sections(findings)[1] != "sys" {
		t.Errorf("Sections = %v, want [mqtt sys]", Sections(findings))
	}
	s := Summarize(findings)
	if !strings.Contains(s, "eco_mode") || !strings.Contains(s, "mqtt") {
		t.Errorf("Summarize = %q", s)
	}
}

func TestDiffMissingSectionAndKey(t *testing.T) {
	desired := map[string]map[string]any{
		"mqtt":     {"enable": true},
		"switch:0": {"power_limit": float64(2300)},
	}
	actual := rawConfig(t, map[string]any{
		"switch:0": map[string]any{"auto_off": true}, // power_limit key absent
		// mqtt section entirely absent
	})
	findings, err := Diff(desired, actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Errorf("findings = %+v, want 2", findings)
	}
}

func TestSummarizeCapsLength(t *testing.T) {
	var findings []Finding
	for i := 0; i < 20; i++ {
		findings = append(findings, Finding{Section: "sys", Path: "device.eco_mode", Want: true, Have: false})
	}
	s := Summarize(findings)
	if !strings.Contains(s, "more") {
		t.Errorf("Summarize must cap and mention remaining findings: %q", s)
	}
}
