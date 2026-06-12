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
	// Devices with no available updates must carry a pointer to empty string
	// (Sys.GetStatus succeeded, stable update field absent => current firmware).
	for _, f := range found {
		if f.AvailableFirmware == nil || *f.AvailableFirmware != "" {
			t.Errorf("device with no updates: AvailableFirmware = %v, want pointer to empty string", f.AvailableFirmware)
		}
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
