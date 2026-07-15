package discovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LukeEvansTech/shelly-operator/internal/shelly/shellytest"
)

func hostOf(url string) string { return strings.TrimPrefix(url, "http://") }

func TestProbeAll(t *testing.T) {
	d1 := &shellytest.Device{ID: "dev1", MAC: "AABBCCDDEE01", Model: "SNPL-00112UK", App: "PlusPlugUK", Gen: 2}
	d2 := &shellytest.Device{ID: "dev2", MAC: "AABBCCDDEE02", Model: "SNSW-001P8EU", App: "Plus1PMMini", Gen: 2}
	gen1 := &shellytest.Device{ID: "shelly1-aabbccddee99", MAC: "AABBCCDDEE99", Model: "SHSW-1", App: "switch", Gen: 1}
	srv1, srv2 := shellytest.New(d1), shellytest.New(d2)
	defer srv1.Close()
	defer srv2.Close()
	srvGen1 := shellytest.New(gen1)
	defer srvGen1.Close()
	// A live HTTP server that is not a Shelly device (returns 404).
	notShelly := httptest.NewServer(http.HandlerFunc(http.NotFound))
	defer notShelly.Close()

	hc := &http.Client{Timeout: 5 * time.Second}
	targets := []string{hostOf(srv1.URL), "127.0.0.1:1", hostOf(notShelly.URL), hostOf(srv2.URL), hostOf(srvGen1.URL)}
	found := probeAll(context.Background(), hc, targets, 4)

	if len(found) != 2 {
		t.Fatalf("found %d devices, want 2: %+v", len(found), found)
	}
	macs := map[string]string{}
	for _, f := range found {
		macs[f.Info.MAC] = f.Host
	}
	if macs["AABBCCDDEE01"] != hostOf(srv1.URL) || macs["AABBCCDDEE02"] != hostOf(srv2.URL) {
		t.Errorf("unexpected hosts: %v", macs)
	}
}

func TestProbeAllCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	hc := &http.Client{Timeout: 5 * time.Second}
	if found := probeAll(ctx, hc, []string{"127.0.0.1:1", "127.0.0.1:2"}, 2); len(found) != 0 {
		t.Errorf("expected no results after cancel, got %+v", found)
	}
}

// TestProbeAllDoesNotChallengeAuthedDevice pins that a sweep costs an
// auth-enabled device no nonce. The sweeper holds no credentials, so any
// call it makes through POST /rpc can only 401 -- and on firmware 2.0.0 a
// 401 mints a nonce into a 32-entry circular buffer that the sweeper then
// throws away. Every 5m sweep across the fleet burned a slot for nothing
// and, once the buffer saturated, the device answered 429 to everyone
// (including the reconciler). Probing must stay on the unauthenticated
// Shelly.GetDeviceInfo endpoint; availableFirmware is the reconciler's job,
// as it is the only component holding the password.
func TestProbeAllDoesNotChallengeAuthedDevice(t *testing.T) {
	d := &shellytest.Device{ID: "dev3", MAC: "AABBCCDDEE03", Model: "SNPL-00112UK",
		App: "PlusPlugUK", Gen: 2, Password: "hunter2"}
	srv := shellytest.New(d)
	defer srv.Close()

	hc := &http.Client{Timeout: 5 * time.Second}
	found := probeAll(context.Background(), hc, []string{hostOf(srv.URL)}, 1)

	if len(found) != 1 {
		t.Fatalf("found %d devices, want 1 (probe must work unauthenticated)", len(found))
	}
	if got := d.Challenges(); got != 0 {
		t.Errorf("sweep issued %d digest challenges, want 0 (each mints a nonce the "+
			"sweeper cannot use and cannot reuse, exhausting firmware 2.0.0's buffer)", got)
	}
}
