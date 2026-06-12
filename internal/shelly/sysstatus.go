package shelly

import "context"

// FirmwareUpdate is one available_updates entry from Sys.GetStatus.
type FirmwareUpdate struct {
	Version string `json:"version"`
}

// AvailableUpdates lists the firmware versions the device reports as
// available. Devices self-check periodically regardless of whether an
// auto-update schedule job exists; an absent stage means the device is
// current for that stage.
type AvailableUpdates struct {
	Stable *FirmwareUpdate `json:"stable"`
	Beta   *FirmwareUpdate `json:"beta"`
}

// SysStatus is the subset of Sys.GetStatus the operator consumes.
type SysStatus struct {
	AvailableUpdates AvailableUpdates `json:"available_updates"`
}

// GetSysStatus fetches the device's system status. Goes through POST
// /rpc, so it requires credentials on auth-enabled devices.
func (c *Client) GetSysStatus(ctx context.Context) (*SysStatus, error) {
	var st SysStatus
	if err := c.Call(ctx, "Sys.GetStatus", nil, &st); err != nil {
		return nil, err
	}
	return &st, nil
}
