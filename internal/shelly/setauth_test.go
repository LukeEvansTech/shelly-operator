package shelly_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LukeEvansTech/shelly-operator/internal/shelly"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly/shellytest"
)

func TestSetAuthLifecycle(t *testing.T) {
	ctx := context.Background()
	d := &shellytest.Device{ID: "dev1", MAC: "AABBCCDDEEFF", Gen: 2}
	srv := shellytest.New(d)
	defer srv.Close()
	host := hostOf(srv.URL)

	if err := shelly.NewClient(host).SetAuth(ctx, "dev1", "pw1"); err != nil {
		t.Fatal(err)
	}
	if !d.AuthEnabled() {
		t.Fatal("device auth should be enabled")
	}

	// Unauthenticated client is now rejected; authenticated client works.
	err := shelly.NewClient(host).Call(ctx, "Shelly.GetDeviceInfo", nil, nil)
	var authErr *shelly.AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected AuthError, got %v", err)
	}
	authed := shelly.NewClient(host, shelly.WithPassword("pw1"))
	if err := authed.Call(ctx, "Shelly.GetDeviceInfo", nil, nil); err != nil {
		t.Fatal(err)
	}

	// Authenticated disable.
	if err := authed.SetAuth(ctx, "dev1", ""); err != nil {
		t.Fatal(err)
	}
	if d.AuthEnabled() {
		t.Error("device auth should be disabled")
	}
}
