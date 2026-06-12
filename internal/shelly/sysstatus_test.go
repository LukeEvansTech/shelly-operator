package shelly_test

import (
	"context"
	"testing"

	"github.com/LukeEvansTech/shelly-operator/internal/shelly"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly/shellytest"
)

func TestGetSysStatusAvailableUpdates(t *testing.T) {
	d := &shellytest.Device{ID: "dev1", MAC: "AABBCCDDEEFF", Gen: 2,
		AvailableUpdates: map[string]any{
			"stable": map[string]any{"version": "1.7.5"},
			"beta":   map[string]any{"version": "2.0.0-beta1"},
		}}
	srv := shellytest.New(d)
	defer srv.Close()
	c := shelly.NewClient(hostOf(srv.URL))

	st, err := c.GetSysStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.AvailableUpdates.Stable == nil || st.AvailableUpdates.Stable.Version != "1.7.5" {
		t.Fatalf("stable = %+v", st.AvailableUpdates.Stable)
	}
	if st.AvailableUpdates.Beta == nil || st.AvailableUpdates.Beta.Version != "2.0.0-beta1" {
		t.Fatalf("beta = %+v", st.AvailableUpdates.Beta)
	}
}

func TestGetSysStatusNoUpdates(t *testing.T) {
	d := &shellytest.Device{ID: "dev1", MAC: "AABBCCDDEEFF", Gen: 2}
	srv := shellytest.New(d)
	defer srv.Close()
	c := shelly.NewClient(hostOf(srv.URL))

	st, err := c.GetSysStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.AvailableUpdates.Stable != nil || st.AvailableUpdates.Beta != nil {
		t.Fatalf("expected no updates, got %+v", st.AvailableUpdates)
	}
}
