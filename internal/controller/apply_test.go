package controller

import (
	"reflect"
	"testing"

	"github.com/LukeEvansTech/shelly-operator/internal/fleet"
)

func TestWifiPayload(t *testing.T) {
	rendered := map[string]any{
		"sta":  map[string]any{"enable": true, "ssid": "iot-new"},
		"sta1": map[string]any{"ssid": "iot-old"},
	}
	got := wifiPayload(rendered, fleet.WifiPasswords{Sta: "hunter2"})

	sta, ok := got["sta"].(map[string]any)
	if !ok || sta["pass"] != "hunter2" || sta["ssid"] != "iot-new" {
		t.Fatalf("sta = %#v", got["sta"])
	}
	if _, ok := got["sta1"].(map[string]any)["pass"]; ok {
		t.Fatalf("sta1 must have no pass: %#v", got["sta1"])
	}
	if _, ok := rendered["sta"].(map[string]any)["pass"]; ok {
		t.Fatal("wifiPayload mutated its input")
	}
}

func TestWifiPayloadNoPasswords(t *testing.T) {
	rendered := map[string]any{"sta": map[string]any{"ssid": "open-net"}}
	got := wifiPayload(rendered, fleet.WifiPasswords{})
	if !reflect.DeepEqual(got, rendered) {
		t.Fatalf("got %#v, want unchanged payload", got)
	}
}

// Nested objects (e.g. ap.range_extender) must be deep-copied so the returned
// payload does not alias the caller's rendered map.
func TestWifiPayloadDeepCopiesNestedMaps(t *testing.T) {
	rendered := map[string]any{
		"ap": map[string]any{"enable": false, "range_extender": map[string]any{"enable": false}},
	}
	got := wifiPayload(rendered, fleet.WifiPasswords{})

	gotRE, ok := got["ap"].(map[string]any)["range_extender"].(map[string]any)
	if !ok {
		t.Fatalf("ap.range_extender missing: %#v", got["ap"])
	}
	// Mutating the returned nested map must not touch the input.
	gotRE["enable"] = true
	srcRE := rendered["ap"].(map[string]any)["range_extender"].(map[string]any)
	if srcRE["enable"] != false {
		t.Fatal("wifiPayload aliased a nested map back into its input")
	}
}
