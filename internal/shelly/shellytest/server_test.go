package shellytest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func TestProbeEndpoint(t *testing.T) {
	d := &Device{ID: "shellyplusplug-3c8a1fec8e3c", MAC: "3C8A1FEC8E3C", Model: "SNPL-00112UK", App: "PlusPlugUK", Gen: 2, Firmware: "20241011-114449/1.4.4-g6d2a586"}
	srv := New(d)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/shelly")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["mac"] != "3C8A1FEC8E3C" || got["auth_en"] != false || got["name"] != nil {
		t.Errorf("unexpected probe response: %v", got)
	}
}

func TestRPCSetConfigMergesAndRecords(t *testing.T) {
	d := &Device{ID: "dev1", Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"name": "old", "eco_mode": false}},
	}}
	srv := New(d)
	defer srv.Close()

	body := `{"id":1,"method":"Sys.SetConfig","params":{"config":{"device":{"name":"PDU-01"}}}}`
	resp, err := http.Post(srv.URL+"/rpc", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %s", resp.Status)
	}
	cfg := d.ConfigSnapshot()
	dev := cfg["sys"]["device"].(map[string]any)
	if dev["name"] != "PDU-01" {
		t.Errorf("name not applied: %v", dev)
	}
	if dev["eco_mode"] != false {
		t.Errorf("merge clobbered sibling key: %v", dev)
	}
	calls := d.RecordedCalls()
	if len(calls) != 1 || calls[0].Method != "Sys.SetConfig" {
		t.Errorf("calls not recorded: %+v", calls)
	}
}

func TestRPCUnknownMethodReturnsError(t *testing.T) {
	srv := New(&Device{ID: "dev1", Gen: 2})
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/rpc", "application/json", bytes.NewBufferString(`{"id":1,"method":"No.Such"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var rr struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		t.Fatal(err)
	}
	if rr.Error == nil || rr.Error.Code != 404 {
		t.Errorf("expected error 404, got %+v", rr.Error)
	}
}

func TestRPCSwitchSetConfigKeyedComponent(t *testing.T) {
	d := &Device{ID: "dev1", Gen: 2}
	srv := New(d)
	defer srv.Close()

	body := `{"id":1,"method":"Switch.SetConfig","params":{"id":0,"config":{"auto_off":true}}}`
	resp, err := http.Post(srv.URL+"/rpc", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	cfg := d.ConfigSnapshot()
	if cfg["switch:0"] == nil || cfg["switch:0"]["auto_off"] != true {
		t.Errorf("switch:0 config = %v", cfg["switch:0"])
	}
}

func TestRPCWrongPasswordStays401(t *testing.T) {
	d := &Device{ID: "dev1", Gen: 2, Password: "right"}
	srv := New(d)
	defer srv.Close()

	// No Authorization header at all -> challenge
	resp, err := http.Post(srv.URL+"/rpc", "application/json", bytes.NewBufferString(`{"id":1,"method":"Shelly.GetDeviceInfo"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %s, want 401", resp.Status)
	}
	if d.Challenges() != 1 {
		t.Errorf("Challenges() = %d, want 1", d.Challenges())
	}
	if len(d.RecordedCalls()) != 0 {
		t.Errorf("unauthenticated call must not be recorded")
	}
}
