package shelly

import "context"

// Reboot asks the device to restart, which is how a pending
// restart_required flag is cleared.
//
// The relay output is NOT cut by this: a Gen2 plug keeps its load energised
// across the restart and re-applies initial_state on the way back up. That
// still matters for two cases the caller must think about first -- a device
// whose initial_state is "off" comes back OFF, and anything mid-task (a
// recording, a firmware flash, an out-of-band session) loses it. Rebooting is
// therefore opt-in per profile, never automatic fleet-wide.
//
// The RPC is fire-and-forget by nature: the device acknowledges and then drops
// the connection to restart, so a transport error after the call was accepted
// is normal and not worth retrying blindly.
func (c *Client) Reboot(ctx context.Context) error {
	return c.Call(ctx, "Shelly.Reboot", nil, nil)
}
