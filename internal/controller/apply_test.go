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
