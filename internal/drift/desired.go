package drift

import (
	"encoding/json"
	"maps"
	"regexp"
	"strconv"
	"strings"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
)

// uiKeyRe matches the component key for plug UI config (e.g. "pluguk_ui").
var uiKeyRe = regexp.MustCompile(`^[a-z0-9]+_ui$`)

// Render maps a profile's declared config onto the device's component
// namespace ("sys", "mqtt", "switch:0", ...). Only declared fields are
// emitted, and leaf values are JSON-normalized (bool/string/float64) so
// Diff can compare them with plain equality. desiredName is the resolved
// device name ("" = name unmanaged even if the section asks for it).
// actual is consulted only to discover which switch components exist.
// Auth is deliberately not rendered: auth state is not part of
// Shelly.GetConfig; the controller diffs it against status.authEnabled.
// A nil pointer field within a section is likewise unmanaged and produces
// no output. Wifi network passwords are never rendered; see
// renderWifiNetwork.
func Render(cfg shellyv1alpha1.ProfileConfig, desiredName string, actual map[string]json.RawMessage) map[string]map[string]any {
	out := map[string]map[string]any{}

	if sys := renderSys(cfg.System, cfg.Name, desiredName); len(sys) > 0 {
		out["sys"] = sys
	}

	if m := renderMQTT(cfg.MQTT); len(m) > 0 {
		out["mqtt"] = m
	}

	if cfg.Cloud != nil && cfg.Cloud.Enable != nil {
		out["cloud"] = map[string]any{"enable": *cfg.Cloud.Enable}
	}

	if cfg.BLE != nil && cfg.BLE.Enable != nil {
		out["ble"] = map[string]any{"enable": *cfg.BLE.Enable}
	}

	if cfg.WS != nil && cfg.WS.Enable != nil {
		out["ws"] = map[string]any{"enable": *cfg.WS.Enable}
	}

	if cfg.Wifi != nil {
		w := map[string]any{}
		if n := renderWifiNetwork(cfg.Wifi.Sta); n != nil {
			w["sta"] = n
		}
		if n := renderWifiNetwork(cfg.Wifi.Sta1); n != nil {
			w["sta1"] = n
		}
		if ap := renderWifiAP(cfg.Wifi.AP); ap != nil {
			w["ap"] = ap
		}
		if roam := renderWifiRoam(cfg.Wifi.Roam); roam != nil {
			w["roam"] = roam
		}
		if len(w) > 0 {
			out["wifi"] = w
		}
	}

	renderSwitchComponents(cfg.Switch, actual, out)

	if cfg.UI != nil {
		if uiKey := discoverUIKey(actual); uiKey != "" {
			if uiMap := renderUI(cfg.UI, actual[uiKey]); len(uiMap) > 0 {
				out[uiKey] = uiMap
			}
		}
	}

	return out
}

// discoverUIKey finds the first component key in actual that matches the
// ^[a-z0-9]+_ui$ pattern (e.g. "pluguk_ui"). Returns "" when none exists,
// making this section a no-op for relay devices.
func discoverUIKey(actual map[string]json.RawMessage) string {
	for k := range actual {
		if uiKeyRe.MatchString(k) {
			return k
		}
	}
	return ""
}

// renderUI builds the desired map for a plug's *_ui component. Only
// declared fields are emitted. actualRaw is the device's current *_ui
// JSON (used to enumerate existing switch controls for buttonInMode).
func renderUI(ui *shellyv1alpha1.UISection, actualRaw json.RawMessage) map[string]any {
	out := map[string]any{}

	leds := renderUILeds(ui)
	if len(leds) > 0 {
		out["leds"] = leds
	}

	if ui.ButtonInMode != nil {
		controls := renderUIControls(*ui.ButtonInMode, actualRaw)
		if len(controls) > 0 {
			out["controls"] = controls
		}
	}

	return out
}

// renderUILeds builds the desired leds sub-map. Only declared leaves are
// emitted so a partial update is safe with Shelly's deep-merge SetConfig.
func renderUILeds(ui *shellyv1alpha1.UISection) map[string]any {
	leds := map[string]any{}

	if ui.LEDMode != nil {
		leds["mode"] = *ui.LEDMode
	}

	if ui.NightMode != nil {
		nm := renderNightMode(ui.NightMode)
		if len(nm) > 0 {
			leds["night_mode"] = nm
		}
	}

	return leds
}

// renderNightMode builds the desired night_mode sub-map. brightness is
// emitted as float64 (JSON-normalized) so Diff's leafEqual compares it
// correctly against the device's decoded number. active_between is emitted
// as []any (not []string) so reflect.DeepEqual matches the device's
// JSON-decoded []interface{} and produces no false drift.
func renderNightMode(nm *shellyv1alpha1.NightMode) map[string]any {
	out := map[string]any{}
	if nm.Enable != nil {
		out["enable"] = *nm.Enable
	}
	if nm.Brightness != nil {
		out["brightness"] = float64(*nm.Brightness)
	}
	if len(nm.ActiveBetween) > 0 {
		ab := make([]any, len(nm.ActiveBetween))
		for i, s := range nm.ActiveBetween {
			ab[i] = s
		}
		out["active_between"] = ab
	}
	return out
}

// renderUIControls builds the desired controls sub-map. It enumerates the
// switch:N keys that the device already exposes under its *_ui component's
// "controls" object. If the controls object is absent or unparseable, it
// defaults to switch:0, which is correct for all single-switch plug models.
func renderUIControls(inMode string, actualRaw json.RawMessage) map[string]any {
	switchKeys := uiControlSwitchKeys(actualRaw)
	if len(switchKeys) == 0 {
		switchKeys = []string{"switch:0"}
	}
	controls := make(map[string]any, len(switchKeys))
	for _, k := range switchKeys {
		controls[k] = map[string]any{"in_mode": inMode}
	}
	return controls
}

// uiControlSwitchKeys extracts the switch:N key names from the "controls"
// object in the device's *_ui JSON. Returns nil when the data is absent or
// not in the expected shape; the caller falls back to ["switch:0"].
func uiControlSwitchKeys(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	controls, _ := v["controls"].(map[string]any)
	if len(controls) == 0 {
		return nil
	}
	var out []string
	for k := range controls {
		if strings.HasPrefix(k, "switch:") {
			out = append(out, k)
		}
	}
	return out
}

// renderSys builds the desired "sys" component map from the system and name
// sections. Fields are nested under their correct sub-object (device for
// eco_mode, discoverable, and name; location for tz/lat/lon; sntp for
// server) so that a partial sys map is safe: Shelly's SetConfig
// deep-merges, and Diff compares only declared leaves.
func renderSys(sys *shellyv1alpha1.SystemSection, name *shellyv1alpha1.NameSection, desiredName string) map[string]any {
	out := map[string]any{}

	device := map[string]any{}
	if sys != nil && sys.EcoMode != nil {
		device["eco_mode"] = *sys.EcoMode
	}
	if sys != nil && sys.Discoverable != nil {
		device["discoverable"] = *sys.Discoverable
	}
	if name != nil && name.Managed && desiredName != "" {
		device["name"] = desiredName
	}
	if len(device) > 0 {
		out["device"] = device
	}

	if sys != nil {
		location := renderSysLocation(sys)
		if len(location) > 0 {
			out["location"] = location
		}
		if sys.SNTPServer != nil {
			out["sntp"] = map[string]any{"server": *sys.SNTPServer}
		}
		if sys.DebugLevel != nil {
			out["debug"] = map[string]any{"level": float64(*sys.DebugLevel)}
		}
	}

	return out
}

// renderSysLocation builds the sys.location sub-map from Timezone,
// Latitude, and Longitude. Latitude and Longitude are validated by the
// CRD pattern so ParseFloat should always succeed; on the rare chance it
// doesn't (e.g. a hand-edited object bypassing validation) the leaf is
// silently skipped rather than failing Render.
func renderSysLocation(sys *shellyv1alpha1.SystemSection) map[string]any {
	loc := map[string]any{}
	if sys.Timezone != nil {
		loc["tz"] = *sys.Timezone
	}
	if sys.Latitude != nil {
		if f, err := strconv.ParseFloat(*sys.Latitude, 64); err == nil {
			loc["lat"] = f
		}
	}
	if sys.Longitude != nil {
		if f, err := strconv.ParseFloat(*sys.Longitude, 64); err == nil {
			loc["lon"] = f
		}
	}
	return loc
}

// renderMQTT builds the desired "mqtt" component map. Server renders
// independently of Enable so the broker address can be pre-configured
// while MQTT remains disabled.
func renderMQTT(mqtt *shellyv1alpha1.MQTTSection) map[string]any {
	if mqtt == nil {
		return nil
	}
	m := map[string]any{}
	if mqtt.Enable != nil {
		m["enable"] = *mqtt.Enable
	}
	if mqtt.Server != "" {
		m["server"] = mqtt.Server
	}
	return m
}

// renderSwitchComponents builds a per-switch-component desired map from
// the switch section and writes each matched "switch:N" component into
// out. Extracted to keep Render's cyclomatic complexity within the
// linter's limit.
func renderSwitchComponents(sw *shellyv1alpha1.SwitchSection, actual map[string]json.RawMessage, out map[string]map[string]any) {
	if sw == nil {
		return
	}
	m := map[string]any{}
	if sw.InitialState != nil {
		m["initial_state"] = *sw.InitialState
	}
	if sw.AutoOn != nil {
		m["auto_on"] = *sw.AutoOn
	}
	if sw.AutoOnDelay != nil {
		m["auto_on_delay"] = float64(*sw.AutoOnDelay)
	}
	if sw.AutoOff != nil {
		m["auto_off"] = *sw.AutoOff
	}
	if sw.AutoOffDelay != nil {
		m["auto_off_delay"] = float64(*sw.AutoOffDelay)
	}
	if sw.PowerLimit != nil {
		m["power_limit"] = float64(*sw.PowerLimit)
	}
	if sw.VoltageLimit != nil {
		m["voltage_limit"] = float64(*sw.VoltageLimit)
	}
	if sw.CurrentLimit != nil {
		m["current_limit"] = float64(*sw.CurrentLimit)
	}
	if sw.AutorecoverVoltageErrors != nil {
		m["autorecover_voltage_errors"] = *sw.AutorecoverVoltageErrors
	}
	if len(m) == 0 {
		return
	}
	for comp := range actual {
		if strings.HasPrefix(comp, "switch:") {
			cp := make(map[string]any, len(m))
			maps.Copy(cp, m)
			out[comp] = cp
		}
	}
}

// renderWifiNetwork emits the diffable leaves of one WiFi network. The
// password is deliberately absent: devices never report stored passwords,
// so they cannot be diffed (and rendered output is shown on the
// dashboard); enforcement injects them at apply time instead.
func renderWifiNetwork(n *shellyv1alpha1.WifiNetwork) map[string]any {
	if n == nil {
		return nil
	}
	m := map[string]any{}
	if n.Enable != nil {
		m["enable"] = *n.Enable
	}
	if n.SSID != "" {
		m["ssid"] = n.SSID
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// renderWifiAP emits the desired wifi.ap sub-map. range_extender is a
// nested object on the device with its own enable key.
func renderWifiAP(ap *shellyv1alpha1.WifiAP) map[string]any {
	if ap == nil {
		return nil
	}
	m := map[string]any{}
	if ap.Enable != nil {
		m["enable"] = *ap.Enable
	}
	if ap.RangeExtender != nil {
		m["range_extender"] = map[string]any{"enable": *ap.RangeExtender}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// renderWifiRoam emits the desired wifi.roam sub-map. Numeric values are
// emitted as float64 so Diff's leafEqual compares them correctly against
// the device's JSON-decoded numbers.
func renderWifiRoam(roam *shellyv1alpha1.WifiRoam) map[string]any {
	if roam == nil {
		return nil
	}
	m := map[string]any{}
	if roam.RSSIThreshold != nil {
		m["rssi_thr"] = float64(*roam.RSSIThreshold)
	}
	if roam.Interval != nil {
		m["interval"] = float64(*roam.Interval)
	}
	if len(m) == 0 {
		return nil
	}
	return m
}
