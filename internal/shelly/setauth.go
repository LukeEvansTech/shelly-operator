package shelly

import "context"

// SetAuth enables (password non-empty) or disables (password empty) digest
// auth on the device. deviceID is the device's id, which doubles as the
// digest realm. After enabling, subsequent calls need a client built
// WithPassword; this client's cached challenge (if any) keeps working for
// the old credentials only.
func (c *Client) SetAuth(ctx context.Context, deviceID, password string) error {
	params := map[string]any{"user": "admin", "realm": deviceID}
	if password == "" {
		params["ha1"] = nil
	} else {
		params["ha1"] = sha256hex("admin:" + deviceID + ":" + password)
	}
	return c.Call(ctx, "Shelly.SetAuth", params, nil)
}
