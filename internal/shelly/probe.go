package shelly

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Probe fetches device identity via GET /shelly. It needs no auth and is
// cheap, which is what makes subnet-sweep discovery viable. hc may be nil
// (http.DefaultClient); pass a client with a short Timeout when sweeping.
func Probe(ctx context.Context, hc *http.Client, host string) (*DeviceInfo, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+host+"/shelly", nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("shelly: probe %s: %w", host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("shelly: probe %s: unexpected status %s", host, resp.Status)
	}
	var info DeviceInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("shelly: probe %s: decode: %w", host, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body) // drain so the connection returns to the pool
	return &info, nil
}
