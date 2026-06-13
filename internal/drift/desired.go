package drift

import (
	"encoding/json"
	"maps"
	"strings"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
)

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

	if cfg.MQTT != nil {
		m := map[string]any{}
		if cfg.MQTT.Enable != nil {
			m["enable"] = *cfg.MQTT.Enable
		}
		// Server renders independently of Enable so the broker address can
		// be pre-configured while MQTT remains disabled.
		if cfg.MQTT.Server != "" {
			m["server"] = cfg.MQTT.Server
		}
		if len(m) > 0 {
			out["mqtt"] = m
		}
	}

	if cfg.Cloud != nil && cfg.Cloud.Enable != nil {
		out["cloud"] = map[string]any{"enable": *cfg.Cloud.Enable}
	}

	if cfg.BLE != nil && cfg.BLE.Enable != nil {
		out["ble"] = map[string]any{"enable": *cfg.BLE.Enable}
	}

	if cfg.Wifi != nil {
		w := map[string]any{}
		if n := renderWifiNetwork(cfg.Wifi.Sta); n != nil {
			w["sta"] = n
		}
		if n := renderWifiNetwork(cfg.Wifi.Sta1); n != nil {
			w["sta1"] = n
		}
		if len(w) > 0 {
			out["wifi"] = w
		}
	}

	if cfg.Switch != nil {
		sw := map[string]any{}
		if cfg.Switch.InitialState != nil {
			sw["initial_state"] = *cfg.Switch.InitialState
		}
		if cfg.Switch.AutoOn != nil {
			sw["auto_on"] = *cfg.Switch.AutoOn
		}
		if cfg.Switch.AutoOnDelay != nil {
			sw["auto_on_delay"] = float64(*cfg.Switch.AutoOnDelay)
		}
		if cfg.Switch.AutoOff != nil {
			sw["auto_off"] = *cfg.Switch.AutoOff
		}
		if cfg.Switch.AutoOffDelay != nil {
			sw["auto_off_delay"] = float64(*cfg.Switch.AutoOffDelay)
		}
		if cfg.Switch.PowerLimit != nil {
			sw["power_limit"] = float64(*cfg.Switch.PowerLimit)
		}
		if cfg.Switch.VoltageLimit != nil {
			sw["voltage_limit"] = float64(*cfg.Switch.VoltageLimit)
		}
		if cfg.Switch.CurrentLimit != nil {
			sw["current_limit"] = float64(*cfg.Switch.CurrentLimit)
		}
		if cfg.Switch.AutorecoverVoltageErrors != nil {
			sw["autorecover_voltage_errors"] = *cfg.Switch.AutorecoverVoltageErrors
		}
		if len(sw) > 0 {
			for comp := range actual {
				if strings.HasPrefix(comp, "switch:") {
					cp := make(map[string]any, len(sw))
					maps.Copy(cp, sw)
					out[comp] = cp
				}
			}
		}
	}

	return out
}

// renderSys builds the desired "sys" component map from the system and name
// sections. Fields are nested under their correct sub-object (device for
// eco_mode and name, location for tz) so that a partial sys map is safe:
// Shelly's SetConfig deep-merges, and Diff compares only declared leaves.
func renderSys(sys *shellyv1alpha1.SystemSection, name *shellyv1alpha1.NameSection, desiredName string) map[string]any {
	out := map[string]any{}
	device := map[string]any{}
	if sys != nil && sys.EcoMode != nil {
		device["eco_mode"] = *sys.EcoMode
	}
	if name != nil && name.Managed && desiredName != "" {
		device["name"] = desiredName
	}
	if len(device) > 0 {
		out["device"] = device
	}
	if sys != nil && sys.Timezone != nil {
		out["location"] = map[string]any{"tz": *sys.Timezone}
	}
	return out
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
