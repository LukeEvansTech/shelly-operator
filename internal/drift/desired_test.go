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

func TestRenderSwitchProtectionLimits(t *testing.T) {
	vl := int32(260)
	cl := int32(16)
	ar := true
	cfg := shellyv1alpha1.ProfileConfig{
		Switch: &shellyv1alpha1.SwitchSection{
			VoltageLimit:             &vl,
			CurrentLimit:             &cl,
			AutorecoverVoltageErrors: &ar,
		},
	}
	actual := rawConfig(t, map[string]any{
		"switch:0": map[string]any{},
		"switch:1": map[string]any{},
	})
	got := Render(cfg, "", actual)

	wantPerSwitch := map[string]any{
		"voltage_limit":              float64(260),
		"current_limit":              float64(16),
		"autorecover_voltage_errors": true,
	}
	for _, comp := range []string{"switch:0", "switch:1"} {
		if !reflect.DeepEqual(got[comp], wantPerSwitch) {
			t.Errorf("Render()[%q] = %#v, want %#v", comp, got[comp], wantPerSwitch)
		}
	}
}

func TestRenderSwitchProtectionLimitsNilUnmanaged(t *testing.T) {
	cfg := shellyv1alpha1.ProfileConfig{
		Switch: &shellyv1alpha1.SwitchSection{},
	}
	actual := rawConfig(t, map[string]any{"switch:0": map[string]any{}})
	got := Render(cfg, "", actual)
	if len(got) != 0 {
		t.Errorf("empty switch section must render nothing, got %v", got)
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

func TestRenderBLEEnable(t *testing.T) {
	cfg := shellyv1alpha1.ProfileConfig{
		BLE: &shellyv1alpha1.BLESection{Enable: ptr(true)},
	}
	got := Render(cfg, "", nil)
	want := map[string]any{"enable": true}
	if !reflect.DeepEqual(got["ble"], want) {
		t.Errorf("ble = %#v, want %#v", got["ble"], want)
	}
}

func TestRenderBLENilUnmanaged(t *testing.T) {
	cfg := shellyv1alpha1.ProfileConfig{BLE: &shellyv1alpha1.BLESection{}}
	got := Render(cfg, "", nil)
	if got["ble"] != nil {
		t.Errorf("nil BLE.Enable must render nothing, got %#v", got["ble"])
	}
}

func TestRenderSysTimezone(t *testing.T) {
	cfg := shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{Timezone: ptr("Europe/London")},
	}
	got := Render(cfg, "", nil)
	wantSys := map[string]any{"location": map[string]any{"tz": "Europe/London"}}
	if !reflect.DeepEqual(got["sys"], wantSys) {
		t.Errorf("sys = %#v, want %#v", got["sys"], wantSys)
	}
}

func TestRenderSysBothEcoModeAndTimezone(t *testing.T) {
	// Setting both must not drop either sub-object.
	cfg := shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{
			EcoMode:  ptr(true),
			Timezone: ptr("America/New_York"),
		},
	}
	got := Render(cfg, "", nil)
	sys := got["sys"]
	device, okD := sys["device"].(map[string]any)
	location, okL := sys["location"].(map[string]any)
	if !okD || device["eco_mode"] != true {
		t.Errorf("sys.device = %#v, want eco_mode=true", sys["device"])
	}
	if !okL || location["tz"] != "America/New_York" {
		t.Errorf("sys.location = %#v, want tz=America/New_York", sys["location"])
	}
}

func TestRenderSysTimezoneDoesNotClobberEcoMode(t *testing.T) {
	// When only timezone is declared, the device sub-map must be absent.
	cfg := shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{Timezone: ptr("UTC")},
	}
	got := Render(cfg, "", nil)
	sys := got["sys"]
	if _, hasDevice := sys["device"]; hasDevice {
		t.Errorf("timezone-only render must not include device sub-map, got %#v", sys)
	}
}

func TestRenderSysEcoModeDoesNotClobberTimezone(t *testing.T) {
	// When only eco_mode is declared, the location sub-map must be absent.
	cfg := shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: ptr(false)},
	}
	got := Render(cfg, "", nil)
	sys := got["sys"]
	if _, hasLocation := sys["location"]; hasLocation {
		t.Errorf("eco_mode-only render must not include location sub-map, got %#v", sys)
	}
}

// ---- UI section tests -------------------------------------------------------

func plugActual(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	return rawConfig(t, map[string]any{
		"sys":      map[string]any{},
		"switch:0": map[string]any{},
		"pluguk_ui": map[string]any{
			"leds": map[string]any{
				"mode": "power",
				"night_mode": map[string]any{
					"enable":         false,
					"brightness":     100,
					"active_between": []any{"22:00", "07:00"},
				},
			},
			"controls": map[string]any{
				"switch:0": map[string]any{"in_mode": "momentary"},
			},
		},
	})
}

func relayActual(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	return rawConfig(t, map[string]any{
		"sys":      map[string]any{},
		"switch:0": map[string]any{},
		"switch:1": map[string]any{},
		// no *_ui key -- relay device
	})
}

func TestRenderUILEDMode(t *testing.T) {
	cfg := shellyv1alpha1.ProfileConfig{
		UI: &shellyv1alpha1.UISection{LEDMode: ptr("switch")},
	}
	got := Render(cfg, "", plugActual(t))
	ui, ok := got["pluguk_ui"]
	if !ok {
		t.Fatalf("expected pluguk_ui in output, got keys %v", keys(got))
	}
	leds, ok := ui["leds"].(map[string]any)
	if !ok {
		t.Fatalf("leds not a map: %#v", ui["leds"])
	}
	if leds["mode"] != "switch" {
		t.Errorf("leds.mode = %v, want switch", leds["mode"])
	}
	// night_mode and controls must be absent (not declared).
	if _, has := leds["night_mode"]; has {
		t.Errorf("night_mode must not be rendered when not declared")
	}
	if _, has := ui["controls"]; has {
		t.Errorf("controls must not be rendered when buttonInMode not declared")
	}
}

func TestRenderUIFullSection(t *testing.T) {
	cfg := shellyv1alpha1.ProfileConfig{
		UI: &shellyv1alpha1.UISection{
			LEDMode: ptr("off"),
			NightMode: &shellyv1alpha1.NightMode{
				Enable:        ptr(true),
				Brightness:    ptr(int32(30)),
				ActiveBetween: []string{"23:00", "06:30"},
			},
			ButtonInMode: ptr("detached"),
		},
	}
	got := Render(cfg, "", plugActual(t))
	ui := got["pluguk_ui"]
	if ui == nil {
		t.Fatalf("expected pluguk_ui, got nil")
	}

	leds, _ := ui["leds"].(map[string]any)
	if leds["mode"] != "off" {
		t.Errorf("leds.mode = %v, want off", leds["mode"])
	}
	nm, _ := leds["night_mode"].(map[string]any)
	if nm["enable"] != true {
		t.Errorf("night_mode.enable = %v, want true", nm["enable"])
	}
	if nm["brightness"] != float64(30) {
		t.Errorf("night_mode.brightness = %v, want 30.0", nm["brightness"])
	}
	ab, _ := nm["active_between"].([]any)
	if len(ab) != 2 || ab[0] != "23:00" || ab[1] != "06:30" {
		t.Errorf("night_mode.active_between = %v, want [23:00 06:30]", nm["active_between"])
	}

	controls, ok := ui["controls"].(map[string]any)
	if !ok {
		t.Fatalf("controls not a map: %#v", ui["controls"])
	}
	sw0, _ := controls["switch:0"].(map[string]any)
	if sw0["in_mode"] != "detached" {
		t.Errorf("controls.switch:0.in_mode = %v, want detached", sw0["in_mode"])
	}
}

func TestRenderUIActiveBetweenIsAnySlice(t *testing.T) {
	// active_between must be []any (not []string) so DeepEqual matches
	// the device's JSON-decoded value (which is also []interface{}).
	cfg := shellyv1alpha1.ProfileConfig{
		UI: &shellyv1alpha1.UISection{
			NightMode: &shellyv1alpha1.NightMode{
				ActiveBetween: []string{"22:00", "07:00"},
			},
		},
	}
	got := Render(cfg, "", plugActual(t))
	leds := got["pluguk_ui"]["leds"].(map[string]any)
	nm := leds["night_mode"].(map[string]any)
	ab := nm["active_between"]
	if _, isAny := ab.([]any); !isAny {
		t.Errorf("active_between must be []any, got %T: %v", ab, ab)
	}
}

func TestRenderUINoFalseDriftActiveBetween(t *testing.T) {
	// Simulate: device returns {"active_between":["22:00","07:00"]} decoded
	// from JSON as []interface{}. The rendered []any must match via DeepEqual.
	cfg := shellyv1alpha1.ProfileConfig{
		UI: &shellyv1alpha1.UISection{
			NightMode: &shellyv1alpha1.NightMode{
				ActiveBetween: []string{"22:00", "07:00"},
			},
		},
	}
	got := Render(cfg, "", plugActual(t))
	leds := got["pluguk_ui"]["leds"].(map[string]any)
	nm := leds["night_mode"].(map[string]any)
	rendered := nm["active_between"]

	// Decode the same value from JSON (simulates what the device returns).
	raw, _ := json.Marshal([]string{"22:00", "07:00"})
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	// JSON unmarshal of an array produces []interface{}.
	if !reflect.DeepEqual(rendered, decoded) {
		t.Errorf("rendered %T(%v) != JSON-decoded %T(%v); would produce false drift", rendered, rendered, decoded, decoded)
	}
}

func TestRenderUIOnlyNightMode(t *testing.T) {
	cfg := shellyv1alpha1.ProfileConfig{
		UI: &shellyv1alpha1.UISection{
			NightMode: &shellyv1alpha1.NightMode{Enable: ptr(false)},
		},
	}
	got := Render(cfg, "", plugActual(t))
	ui := got["pluguk_ui"]
	if ui == nil {
		t.Fatalf("expected pluguk_ui")
	}
	leds, _ := ui["leds"].(map[string]any)
	if _, hasMode := leds["mode"]; hasMode {
		t.Errorf("mode must not be emitted when LEDMode is nil")
	}
	nm, _ := leds["night_mode"].(map[string]any)
	if nm["enable"] != false {
		t.Errorf("night_mode.enable = %v, want false", nm["enable"])
	}
	if _, hasControls := ui["controls"]; hasControls {
		t.Errorf("controls must not be emitted when buttonInMode is nil")
	}
}

func TestRenderUIRelayDeviceNoOp(t *testing.T) {
	// Relay device has no *_ui key -- UI section must produce nothing.
	cfg := shellyv1alpha1.ProfileConfig{
		UI: &shellyv1alpha1.UISection{
			LEDMode: ptr("power"),
		},
	}
	got := Render(cfg, "", relayActual(t))
	for k := range got {
		if strings.HasSuffix(k, "_ui") {
			t.Errorf("relay device must not produce a *_ui entry, got key %q", k)
		}
	}
}

func TestRenderUINilUnmanaged(t *testing.T) {
	cfg := shellyv1alpha1.ProfileConfig{UI: &shellyv1alpha1.UISection{}}
	got := Render(cfg, "", plugActual(t))
	for k := range got {
		if strings.HasSuffix(k, "_ui") {
			t.Errorf("empty UI section must render nothing, got key %q", k)
		}
	}
}

func keys(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
