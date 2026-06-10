package shelly_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LukeEvansTech/shelly-operator/internal/shelly"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly/shellytest"
)

func TestCallWithDigestAuth(t *testing.T) {
	d := &shellytest.Device{ID: "dev1", MAC: "AABBCCDDEEFF", Gen: 2, Password: "hunter2"}
	srv := shellytest.New(d)
	defer srv.Close()

	c := shelly.NewClient(hostOf(srv.URL), shelly.WithPassword("hunter2"))
	for i := 0; i < 2; i++ { // second call must reuse the cached challenge
		var got shelly.DeviceInfo
		if err := c.Call(context.Background(), "Shelly.GetDeviceInfo", nil, &got); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if d.Challenges() != 1 {
		t.Errorf("Challenges() = %d, want 1 (challenge should be cached)", d.Challenges())
	}
	if len(d.RecordedCalls()) != 2 {
		t.Errorf("authorized calls = %d, want 2", len(d.RecordedCalls()))
	}
}

func TestCallWrongPassword(t *testing.T) {
	srv := shellytest.New(&shellytest.Device{ID: "dev1", Gen: 2, Password: "right"})
	defer srv.Close()

	c := shelly.NewClient(hostOf(srv.URL), shelly.WithPassword("wrong"))
	err := c.Call(context.Background(), "Shelly.GetDeviceInfo", nil, nil)
	var authErr *shelly.AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected *AuthError, got %v", err)
	}
}
