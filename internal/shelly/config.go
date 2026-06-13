package shelly

import (
	"context"
	"encoding/json"
	"strings"
)

// componentMethods maps lowercase component names to their RPC namespace.
// Known components are listed here so their canonical casing is preserved
// (e.g. "MQTT", "BLE"). Keyed components (switch) have dedicated helpers
// because their params carry an instance id.
var componentMethods = map[string]string{
	"sys":   "Sys",
	"wifi":  "Wifi",
	"mqtt":  "MQTT",
	"cloud": "Cloud",
	"ble":   "BLE",
	"ws":    "WS",
}

type setConfigResult struct {
	RestartRequired bool `json:"restart_required"`
}

// GetConfig returns the device's full configuration keyed by component
// instance ("sys", "wifi", "switch:0", ...), raw so callers can diff or
// decode the sections they manage.
func (c *Client) GetConfig(ctx context.Context) (map[string]json.RawMessage, error) {
	var cfg map[string]json.RawMessage
	if err := c.Call(ctx, "Shelly.GetConfig", nil, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// SetConfig applies a partial config to a non-keyed component, e.g.
// SetConfig(ctx, "sys", map[string]any{"device": map[string]any{"name": "PDU-01"}}).
// Returns whether the device wants a restart for the change to take effect.
// For components not in componentMethods (e.g. dynamically discovered *_ui
// components), the RPC namespace is derived by uppercasing the component
// name (e.g. "pluguk_ui" -> "PLUGUK_UI"). Shelly method names are
// case-insensitive, so this works for all Gen2+ devices.
func (c *Client) SetConfig(ctx context.Context, component string, config any) (restartRequired bool, err error) {
	ns, ok := componentMethods[component]
	if !ok {
		// Fallback: uppercase the component name as the RPC namespace.
		// Shelly devices accept case-insensitive method names, so
		// PLUGUK_UI.SetConfig works the same as Pluguk_Ui.SetConfig.
		ns = strings.ToUpper(component)
	}
	var res setConfigResult
	err = c.Call(ctx, ns+".SetConfig", map[string]any{"config": config}, &res)
	return res.RestartRequired, err
}

// SetSwitchConfig applies a partial config to one switch instance.
func (c *Client) SetSwitchConfig(ctx context.Context, id int, config any) (restartRequired bool, err error) {
	var res setConfigResult
	err = c.Call(ctx, "Switch.SetConfig", map[string]any{"id": id, "config": config}, &res)
	return res.RestartRequired, err
}
