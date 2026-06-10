package shelly_test

import (
	"context"
	"errors"
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
