package shelly_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LukeEvansTech/shelly-operator/internal/shelly"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly/shellytest"
)

func TestCallGetDeviceInfo(t *testing.T) {
	d := &shellytest.Device{ID: "dev1", MAC: "AABBCCDDEEFF", Model: "SNPL-00112UK", Gen: 2}
	srv := shellytest.New(d)
	defer srv.Close()

	c := shelly.NewClient(hostOf(srv.URL))
	var got shelly.DeviceInfo
	if err := c.Call(context.Background(), "Shelly.GetDeviceInfo", nil, &got); err != nil {
		t.Fatal(err)
	}
	if got.MAC != "AABBCCDDEEFF" {
		t.Errorf("MAC = %q", got.MAC)
	}
}

func TestCallRPCError(t *testing.T) {
	srv := shellytest.New(&shellytest.Device{ID: "dev1", Gen: 2})
	defer srv.Close()

	c := shelly.NewClient(hostOf(srv.URL))
	err := c.Call(context.Background(), "No.Such", nil, nil)
	var rpcErr *shelly.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected *RPCError, got %v", err)
	}
	if rpcErr.Code != 404 {
		t.Errorf("Code = %d, want 404", rpcErr.Code)
	}
}

func TestCallNoCredentialsAgainstProtectedDevice(t *testing.T) {
	d := &shellytest.Device{ID: "dev1", Gen: 2, Password: "secret"}
	srv := shellytest.New(d)
	defer srv.Close()

	err := shelly.NewClient(hostOf(srv.URL)).Call(context.Background(), "Shelly.GetDeviceInfo", nil, nil)
	var authErr *shelly.AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected *AuthError, got %v", err)
	}
	if d.Challenges() != 1 {
		t.Errorf("Challenges() = %d, want 1", d.Challenges())
	}
	if len(d.RecordedCalls()) != 0 {
		t.Errorf("rejected call must not be recorded")
	}
}

func TestCallNonRPCServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := shelly.NewClient(hostOf(srv.URL)).Call(context.Background(), "Shelly.GetDeviceInfo", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("expected status-500 error, got %v", err)
	}
}

// TestRebootCallsRPC verifies Reboot issues Shelly.Reboot. Kept explicit
// because this is the one client call that acts on hardware rather than
// config: a wrong method name here would fail silently and leave devices
// pending a restart forever.
func TestRebootCallsRPC(t *testing.T) {
	fake := &shellytest.Device{ID: "rb1", MAC: "AABBCCDDEEFF", Gen: 2, RestartRequired: true}
	srv := shellytest.New(fake)
	defer srv.Close()

	c := shelly.NewClient(hostOf(srv.URL))
	if err := c.Reboot(context.Background()); err != nil {
		t.Fatalf("Reboot: %v", err)
	}
	found := false
	for _, call := range fake.RecordedCalls() {
		if call.Method == "Shelly.Reboot" {
			found = true
		}
	}
	if !found {
		t.Error("expected Shelly.Reboot to be called")
	}
	if fake.RestartRequired {
		t.Error("device should have cleared restart_required after the reboot")
	}
}

// tooManyThen answers 429 for the first n requests, then a valid RPC result.
func tooManyThen(n int32) (*httptest.Server, *atomic.Int32) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) <= n {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"id":1,"result":{"mac":"AABBCCDDEEFF"}}`))
	}))
	return srv, &hits
}

// A 429 that clears (as firmware 2.0.0's nonce-table 429s did within ~5s)
// must not fail the call.
func TestCallRetriesTooManyRequests(t *testing.T) {
	srv, hits := tooManyThen(2)
	defer srv.Close()
	c := shelly.NewClient(hostOf(srv.URL), shelly.WithRetryDelay(time.Millisecond))
	var got shelly.DeviceInfo
	if err := c.Call(context.Background(), "Shelly.GetDeviceInfo", nil, &got); err != nil {
		t.Fatalf("Call after two 429s: %v", err)
	}
	if hits.Load() != 3 {
		t.Errorf("requests = %d, want 3", hits.Load())
	}
}

// A device that keeps answering 429 is given up on after the retries, and
// the error still reads as a refusal.
func TestCallGivesUpOnPersistentTooManyRequests(t *testing.T) {
	srv, hits := tooManyThen(100)
	defer srv.Close()
	c := shelly.NewClient(hostOf(srv.URL), shelly.WithRetryDelay(time.Millisecond))
	err := c.Call(context.Background(), "Shelly.GetDeviceInfo", nil, nil)
	if err == nil || !shelly.IsRefusal(err) {
		t.Fatalf("err = %v, want a refusal after persistent 429s", err)
	}
	if hits.Load() != 3 {
		t.Errorf("requests = %d, want 3 (1 + 2 retries)", hits.Load())
	}
}
