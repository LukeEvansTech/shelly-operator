package shelly

import "context"

// Update asks the device to download and install the given firmware stage
// ("stable"). The device answers once it has accepted the request, then
// downloads, flashes and reboots on its own over the next minute or two; the
// result of the install is visible only afterwards, as a changed firmware
// version.
//
// A refusal comes back as an RPC error, the useful one being -114 "No update
// info" when the device's own update check has come back empty. That error is
// the whole reason to call this rather than rely on the on-device schedule
// job: the job hits the same refusal and nothing records it.
func (c *Client) Update(ctx context.Context, stage string) error {
	return c.Call(ctx, "Shelly.Update", map[string]any{"stage": stage}, nil)
}
