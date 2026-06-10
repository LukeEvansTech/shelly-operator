// Package shelly is a minimal client for the Shelly Gen2+ JSON-RPC API
// (https://shelly-api-docs.shelly.cloud/gen2/). Stdlib only.
package shelly

// DeviceInfo is the response of the unauthenticated GET /shelly probe.
type DeviceInfo struct {
	ID          string `json:"id"`
	MAC         string `json:"mac"`
	Model       string `json:"model"`
	Gen         int    `json:"gen"`
	Firmware    string `json:"fw_id"`
	App         string `json:"app"`
	AuthEnabled bool   `json:"auth_en"`
	Name        string `json:"name"` // empty if unset (device returns null)
}
