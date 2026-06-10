package shelly_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LukeEvansTech/shelly-operator/internal/shelly"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly/shellytest"
)

func TestGetConfig(t *testing.T) {
	d := &shellytest.Device{ID: "dev1", Gen: 2, InitialConfig: map[string]map[string]any{
		"sys":  {"device": map[string]any{"name": "PDU-01", "eco_mode": true}},
		"mqtt": {"enable": false},
	}}
	srv := shellytest.New(d)
	defer srv.Close()

	cfg, err := shelly.NewClient(hostOf(srv.URL)).GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var sys struct {
		Device struct {
			Name    string `json:"name"`
			EcoMode bool   `json:"eco_mode"`
		} `json:"device"`
	}
	if err := json.Unmarshal(cfg["sys"], &sys); err != nil {
		t.Fatal(err)
	}
	if sys.Device.Name != "PDU-01" || !sys.Device.EcoMode {
		t.Errorf("sys config = %+v", sys)
	}
}

func TestSetConfig(t *testing.T) {
	d := &shellytest.Device{ID: "dev1", Gen: 2}
	srv := shellytest.New(d)
	defer srv.Close()

	c := shelly.NewClient(hostOf(srv.URL))
	restart, err := c.SetConfig(context.Background(), "sys",
		map[string]any{"device": map[string]any{"name": "office-desk"}})
	if err != nil {
		t.Fatal(err)
	}
	if restart {
		t.Error("restart should be false")
	}
	if calls := d.RecordedCalls(); calls[0].Method != "Sys.SetConfig" {
		t.Errorf("method = %q, want Sys.SetConfig", calls[0].Method)
	}
	if got := d.ConfigSnapshot()["sys"]["device"].(map[string]any)["name"]; got != "office-desk" {
		t.Errorf("name = %v", got)
	}
}

func TestSetConfigUnknownComponent(t *testing.T) {
	srv := shellytest.New(&shellytest.Device{ID: "dev1", Gen: 2})
	defer srv.Close()
	if _, err := shelly.NewClient(hostOf(srv.URL)).SetConfig(context.Background(), "nope", map[string]any{}); err == nil {
		t.Error("expected error for unknown component")
	}
}

func TestSetSwitchConfig(t *testing.T) {
	d := &shellytest.Device{ID: "dev1", Gen: 2}
	srv := shellytest.New(d)
	defer srv.Close()

	c := shelly.NewClient(hostOf(srv.URL))
	if _, err := c.SetSwitchConfig(context.Background(), 0, map[string]any{"auto_off": true}); err != nil {
		t.Fatal(err)
	}
	if calls := d.RecordedCalls(); calls[0].Method != "Switch.SetConfig" {
		t.Errorf("method = %q", calls[0].Method)
	}
	if cfg := d.ConfigSnapshot(); cfg["switch:0"]["auto_off"] != true {
		t.Errorf("switch:0 config = %v", cfg["switch:0"])
	}
}
