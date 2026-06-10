package shelly_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
