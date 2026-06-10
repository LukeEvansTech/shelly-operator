package drift

import (
	"encoding/json"
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
func Render(cfg shellyv1alpha1.ProfileConfig, desiredName string, actual map[string]json.RawMessage) map[string]map[string]any {
	out := map[string]map[string]any{}

	device := map[string]any{}
	if cfg.System != nil && cfg.System.EcoMode != nil {
		device["eco_mode"] = *cfg.System.EcoMode
	}
	if cfg.Name != nil && cfg.Name.Managed && desiredName != "" {
		device["name"] = desiredName
	}
	if len(device) > 0 {
		out["sys"] = map[string]any{"device": device}
	}

	if cfg.MQTT != nil {
		m := map[string]any{}
		if cfg.MQTT.Enable != nil {
			m["enable"] = *cfg.MQTT.Enable
		}
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
		if len(sw) > 0 {
			for comp := range actual {
				if strings.HasPrefix(comp, "switch:") {
					out[comp] = sw
				}
			}
		}
	}

	return out
}
