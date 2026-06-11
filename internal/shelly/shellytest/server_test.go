package shellytest

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	defer func() { _ = resp.Body.Close() }()
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
	defer func() { _ = resp.Body.Close() }()
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
	defer func() { _ = resp.Body.Close() }()
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
	defer func() { _ = resp.Body.Close() }()
	cfg := d.ConfigSnapshot()
	if cfg["switch:0"] == nil || cfg["switch:0"]["auto_off"] != true {
		t.Errorf("switch:0 config = %v", cfg["switch:0"])
	}
}

func TestRPCRejectsMissingAndWrongAuth(t *testing.T) {
	d := &Device{ID: "dev1", Gen: 2, Password: "right"}
	srv := New(d)
	defer srv.Close()

	// 1) No Authorization header -> challenge.
	resp, err := http.Post(srv.URL+"/rpc", "application/json", bytes.NewBufferString(`{"id":1,"method":"Shelly.GetDeviceInfo"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing auth: status = %s, want 401", resp.Status)
	}

	// 2) Well-formed digest computed from the WRONG password -> still 401.
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/rpc", bytes.NewBufferString(`{"id":1,"method":"Shelly.GetDeviceInfo"}`))
	if err != nil {
		t.Fatal(err)
	}
	ha1 := sha256hex("admin:dev1:wrong")
	ha2 := sha256hex("POST:/rpc")
	response := sha256hex(ha1 + ":" + testNonce + ":00000001:abcdef:auth:" + ha2)
	req.Header.Set("Authorization", fmt.Sprintf(
		`Digest username="admin", realm="dev1", nonce=%q, uri="/rpc", response=%q, qop=auth, nc=00000001, cnonce="abcdef", algorithm=SHA-256`,
		testNonce, response))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: status = %s, want 401", resp2.Status)
	}

	if d.Challenges() != 2 {
		t.Errorf("Challenges() = %d, want 2", d.Challenges())
	}
	if len(d.RecordedCalls()) != 0 {
		t.Errorf("rejected calls must not be recorded")
	}
}

func TestConfigSnapshotIsDeepCopy(t *testing.T) {
	d := &Device{ID: "dev1", Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"name": "PDU-01"}},
	}}
	srv := New(d)
	defer srv.Close()

	snap := d.ConfigSnapshot()
	snap["sys"]["device"].(map[string]any)["name"] = "mutated"
	if got := d.ConfigSnapshot()["sys"]["device"].(map[string]any)["name"]; got != "PDU-01" {
		t.Errorf("snapshot mutation leaked into device state: name = %v", got)
	}
}

func TestSetAuthEnableAndReject(t *testing.T) {
	d := &Device{ID: "dev1", Gen: 2}
	srv := New(d)
	defer srv.Close()

	if d.AuthEnabled() {
		t.Fatal("auth must start disabled")
	}
	ha1 := sha256hex("admin:dev1:newpw")
	body := fmt.Sprintf(`{"id":1,"method":"Shelly.SetAuth","params":{"user":"admin","realm":"dev1","ha1":%q}}`, ha1)
	resp, err := http.Post(srv.URL+"/rpc", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SetAuth status = %s", resp.Status)
	}
	if !d.AuthEnabled() {
		t.Fatal("auth should be enabled after SetAuth")
	}
	// Unauthenticated calls are now rejected.
	resp2, err := http.Post(srv.URL+"/rpc", "application/json", bytes.NewBufferString(`{"id":1,"method":"Shelly.GetDeviceInfo"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("post-SetAuth unauthenticated status = %s, want 401", resp2.Status)
	}
	// Probe still answers without auth and reports auth_en true.
	resp3, err := http.Get(srv.URL + "/shelly")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp3.Body.Close() }()
	var info map[string]any
	if err := json.NewDecoder(resp3.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	if info["auth_en"] != true {
		t.Errorf("auth_en = %v, want true", info["auth_en"])
	}
}

func TestSetConfigRestartAndErrorKnobs(t *testing.T) {
	d := &Device{ID: "dev1", Gen: 2, RestartOnSetConfig: true}
	srv := New(d)
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/rpc", "application/json",
		bytes.NewBufferString(`{"id":1,"method":"Sys.SetConfig","params":{"config":{"device":{"eco_mode":true}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var rr struct {
		Result struct {
			RestartRequired bool `json:"restart_required"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		t.Fatal(err)
	}
	if !rr.Result.RestartRequired {
		t.Error("RestartOnSetConfig knob must surface restart_required=true")
	}

	d2 := &Device{ID: "dev2", Gen: 2, SetConfigError: "boom"}
	srv2 := New(d2)
	defer srv2.Close()
	resp2, err := http.Post(srv2.URL+"/rpc", "application/json",
		bytes.NewBufferString(`{"id":1,"method":"Sys.SetConfig","params":{"config":{"device":{}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp2.Body.Close() }()
	var er struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&er); err != nil {
		t.Fatal(err)
	}
	if er.Error == nil || er.Error.Message != "boom" {
		t.Errorf("SetConfigError knob must fail SetConfig, got %+v", er.Error)
	}
}
