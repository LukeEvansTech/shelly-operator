package drift

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
)

func ptr[T any](v T) *T { return &v }

func rawConfig(t *testing.T, m map[string]any) map[string]json.RawMessage {
	t.Helper()
	out := map[string]json.RawMessage{}
	for k, v := range m {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		out[k] = b
	}
	return out
}

func TestRenderSections(t *testing.T) {
	cfg := shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: ptr(true)},
		Name:   &shellyv1alpha1.NameSection{Managed: true},
		MQTT:   &shellyv1alpha1.MQTTSection{Enable: ptr(true), Server: "mqtt:1883"},
		Cloud:  &shellyv1alpha1.CloudSection{Enable: ptr(false)},
		Switch: &shellyv1alpha1.SwitchSection{InitialState: ptr("restore_last"), AutoOn: ptr(false), AutoOnDelay: ptr(int32(5)), AutoOff: ptr(true), AutoOffDelay: ptr(int32(300)), PowerLimit: ptr(int32(2300))},
	}
	actual := rawConfig(t, map[string]any{
		"sys":      map[string]any{},
		"switch:0": map[string]any{},
		"switch:1": map[string]any{},
		"wifi":     map[string]any{},
	})

	got := Render(cfg, "rack-pdu", actual)
	want := map[string]map[string]any{
		"sys":      {"device": map[string]any{"eco_mode": true, "name": "rack-pdu"}},
		"mqtt":     {"enable": true, "server": "mqtt:1883"},
		"cloud":    {"enable": false},
		"switch:0": {"initial_state": "restore_last", "auto_on": false, "auto_on_delay": float64(5), "auto_off": true, "auto_off_delay": float64(300), "power_limit": float64(2300)},
		"switch:1": {"initial_state": "restore_last", "auto_on": false, "auto_on_delay": float64(5), "auto_off": true, "auto_off_delay": float64(300), "power_limit": float64(2300)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Render() =\n%#v\nwant\n%#v", got, want)
	}
}

func TestRenderOmittedSectionsUnmanaged(t *testing.T) {
	got := Render(shellyv1alpha1.ProfileConfig{}, "", nil)
	if len(got) != 0 {
		t.Errorf("empty config must render nothing, got %v", got)
	}
}

func TestRenderNameUnmanagedWithoutName(t *testing.T) {
	cfg := shellyv1alpha1.ProfileConfig{Name: &shellyv1alpha1.NameSection{Managed: true}}
	got := Render(cfg, "", nil) // managed but no name resolvable -> nothing to declare
	if len(got) != 0 {
		t.Errorf("managed name with empty desired name must render nothing, got %v", got)
	}
}

func TestRenderWifi(t *testing.T) {
	cfg := shellyv1alpha1.ProfileConfig{Wifi: &shellyv1alpha1.WifiSection{
		Sta: &shellyv1alpha1.WifiNetwork{Enable: ptr(true), SSID: "iot-new",
			PassSecretRef: &shellyv1alpha1.SecretKeyRef{Name: "wifi", Key: "new"}},
		Sta1: &shellyv1alpha1.WifiNetwork{Enable: ptr(true), SSID: "iot-old"},
	}}
	got := Render(cfg, "", nil)
	want := map[string]any{
		"sta":  map[string]any{"enable": true, "ssid": "iot-new"},
		"sta1": map[string]any{"enable": true, "ssid": "iot-old"},
	}
	if !reflect.DeepEqual(got["wifi"], want) {
		t.Fatalf("wifi = %#v, want %#v", got["wifi"], want)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "pass") {
		t.Fatalf("rendered output leaks a password field: %s", b)
	}
}

func TestRenderWifiEmptyAndNil(t *testing.T) {
	if got := Render(shellyv1alpha1.ProfileConfig{Wifi: &shellyv1alpha1.WifiSection{}}, "", nil); got["wifi"] != nil {
		t.Fatalf("empty wifi section must render nothing, got %#v", got["wifi"])
	}
	cfg := shellyv1alpha1.ProfileConfig{Wifi: &shellyv1alpha1.WifiSection{
		Sta: &shellyv1alpha1.WifiNetwork{PassSecretRef: &shellyv1alpha1.SecretKeyRef{Name: "wifi", Key: "k"}},
	}}
	if got := Render(cfg, "", nil); got["wifi"] != nil {
		t.Fatalf("pass-only network must render nothing, got %#v", got["wifi"])
	}
}
