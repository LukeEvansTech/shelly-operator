// Package shellytest provides a fake Shelly Gen2+ device backed by
// httptest.Server, for testing the RPC client and controllers without
// hardware. It emulates GET /shelly, POST /rpc dispatch, SHA-256 HTTP
// digest auth, per-component config state with merge-on-SetConfig,
// schedule jobs (Schedule.List/Create/Delete), and Sys.GetStatus with
// available_updates.
//
// Usage: populate the exported identity fields (ID, MAC, Model, App, Gen,
// Firmware, Name, Password), InitialConfig, InitialSchedules, and
// AvailableUpdates before calling New; do not change them while the
// server is running. Inspect runtime state via the accessor methods
// RecordedCalls, ConfigSnapshot, ScheduleSnapshot, and Challenges.
package shellytest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
)

// testNonce is the fixed nonce the fake device issues in every 401 challenge
// and validates in every Authorization header.
const testNonce = "testnonce1"

// Device is the mutable state behind a fake Shelly device. Populate the
// identity fields and InitialConfig, InitialSchedules, and AvailableUpdates,
// then pass it to New. Do not modify any field after New returns. Inspect
// runtime state via RecordedCalls, ConfigSnapshot, ScheduleSnapshot,
// Challenges, and AuthEnabled. RestartOnSetConfig and SetConfigError are
// optional knobs, set before New.
type Device struct {
	ID       string
	MAC      string
	Model    string
	App      string
	Gen      int
	Firmware string
	Name     string
	Password string // "" = auth disabled at start; seeds ha1 in New

	// RestartOnSetConfig makes every *.SetConfig response report
	// restart_required=true.
	RestartOnSetConfig bool
	// SetConfigError, when non-empty, fails every *.SetConfig call with
	// this message (simulates a device rejecting config).
	SetConfigError string
	// IgnoreSetConfig, when true, accepts *.SetConfig calls (success
	// response) without changing config (simulates a device that clamps
	// or reverts written values).
	IgnoreSetConfig bool

	// InitialConfig seeds the per-component config the device starts with.
	// Keys are component names ("sys", "switch:0", ...); values are config maps.
	// New copies this into the internal config store.
	InitialConfig map[string]map[string]any

	// GetConfigErrorAfter, when > 0, fails Shelly.GetConfig calls after
	// the first N successful ones (simulates a device that answers the
	// initial read but dies before verification).
	GetConfigErrorAfter int

	// InitialSchedules seeds the device's schedule jobs; ids are assigned
	// 1..N in order. Each entry uses the Schedule.Create wire shape
	// (enable, timespec, calls). New copies it into the internal store.
	InitialSchedules []map[string]any

	// AvailableUpdates seeds Sys.GetStatus available_updates, e.g.
	// {"stable": map[string]any{"version": "1.7.5"}}. nil means no
	// updates available (renders as an empty object).
	AvailableUpdates map[string]any

	mu             sync.Mutex
	ha1            string
	config         map[string]map[string]any // component ("sys", "switch:0") -> config
	calls          []Call                    // recorded RPC calls, in order
	challengesSent int                       // number of 401 challenges issued
	getConfigCalls int                       // number of successful Shelly.GetConfig calls served
	schedules      []map[string]any          // schedule jobs, each with an "id" key
	scheduleRev    int                       // bumped on every schedule mutation
	nextSchedID    int                       // next id to assign
}

// Call records one RPC invocation that passed auth.
type Call struct {
	Method string
	Params json.RawMessage
}

// New starts the fake device. The caller must Close the returned server.
func New(d *Device) *httptest.Server {
	d.config = make(map[string]map[string]any, len(d.InitialConfig))
	for comp, cfg := range d.InitialConfig {
		d.config[comp] = merge(nil, cfg)
	}
	d.nextSchedID = 1
	for _, j := range d.InitialSchedules {
		job := merge(nil, j)
		job["id"] = d.nextSchedID
		d.nextSchedID++
		d.schedules = append(d.schedules, job)
	}
	if d.Password != "" {
		d.ha1 = sha256hex("admin:" + d.ID + ":" + d.Password)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /shelly", d.handleProbe)
	mux.HandleFunc("POST /rpc", d.handleRPC)
	return httptest.NewServer(mux)
}

// RecordedCalls returns a copy of the RPC calls that passed auth, in order.
func (d *Device) RecordedCalls() []Call {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Call(nil), d.calls...)
}

// ConfigSnapshot returns a deep copy of the device's current config.
func (d *Device) ConfigSnapshot() map[string]map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]map[string]any, len(d.config))
	for comp, cfg := range d.config {
		out[comp] = merge(nil, cfg)
	}
	return out
}

// ScheduleSnapshot returns a deep copy of the device's schedule jobs.
func (d *Device) ScheduleSnapshot() []map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]map[string]any, 0, len(d.schedules))
	for _, j := range d.schedules {
		out = append(out, merge(nil, j))
	}
	return out
}

// Challenges returns how many 401 digest challenges the device has issued.
func (d *Device) Challenges() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.challengesSent
}

// AuthEnabled reports whether the device currently requires digest auth.
func (d *Device) AuthEnabled() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ha1 != ""
}

// deviceInfo builds the /shelly identity map. callers must hold d.mu.
func (d *Device) deviceInfo() map[string]any {
	var name any
	if d.Name != "" {
		name = d.Name
	}
	return map[string]any{
		"id": d.ID, "mac": d.MAC, "model": d.Model, "gen": d.Gen,
		"fw_id": d.Firmware, "app": d.App, "auth_en": d.ha1 != "", "name": name,
	}
}

func (d *Device) handleProbe(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	info := d.deviceInfo()
	d.mu.Unlock()
	writeJSON(w, info) // probe never requires auth, like real devices
}

func (d *Device) handleRPC(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.ha1 != "" && !d.authorized(r) {
		d.challengesSent++
		w.Header().Set("WWW-Authenticate",
			fmt.Sprintf(`Digest qop="auth", realm=%q, nonce=%q, algorithm=SHA-256`, d.ID, testNonce))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	var req struct {
		ID     int64           `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	d.calls = append(d.calls, Call{Method: req.Method, Params: req.Params})

	switch {
	case req.Method == "Shelly.GetDeviceInfo":
		writeJSON(w, rpcResult(req.ID, d.deviceInfo()))
	case req.Method == "Shelly.GetConfig":
		d.handleGetConfig(w, req.ID)
	case req.Method == "Shelly.SetAuth":
		d.handleSetAuth(w, req.ID, req.Params)
	case req.Method == "Sys.GetStatus":
		d.handleSysGetStatus(w, req.ID)
	case req.Method == "Schedule.List":
		d.handleScheduleList(w, req.ID)
	case req.Method == "Schedule.Create":
		d.handleScheduleCreate(w, req.ID, req.Params)
	case req.Method == "Schedule.Update":
		d.handleScheduleUpdate(w, req.ID, req.Params)
	case req.Method == "Schedule.Delete":
		d.handleScheduleDelete(w, req.ID, req.Params)
	case strings.HasSuffix(req.Method, ".SetConfig"):
		d.handleSetConfig(w, req.ID, req.Method, req.Params)
	default:
		writeJSON(w, rpcError(req.ID, 404, "No handler for "+req.Method))
	}
}

func (d *Device) handleGetConfig(w http.ResponseWriter, id int64) {
	if d.GetConfigErrorAfter > 0 && d.getConfigCalls >= d.GetConfigErrorAfter {
		writeJSON(w, rpcError(id, -108, "config read failed"))
		return
	}
	d.getConfigCalls++
	writeJSON(w, rpcResult(id, d.config))
}

func (d *Device) handleSetAuth(w http.ResponseWriter, id int64, params json.RawMessage) {
	var p struct {
		User  string  `json:"user"`
		Realm string  `json:"realm"`
		HA1   *string `json:"ha1"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.User != "admin" || p.Realm != d.ID {
		writeJSON(w, rpcError(id, -103, "invalid SetAuth params"))
		return
	}
	if p.HA1 != nil && *p.HA1 == "" {
		writeJSON(w, rpcError(id, -103, "invalid SetAuth params"))
		return
	}
	if p.HA1 == nil {
		d.ha1 = ""
	} else {
		d.ha1 = *p.HA1
	}
	writeJSON(w, rpcResult(id, nil))
}

func (d *Device) handleSysGetStatus(w http.ResponseWriter, id int64) {
	avail := d.AvailableUpdates
	if avail == nil {
		avail = map[string]any{}
	}
	writeJSON(w, rpcResult(id, map[string]any{"available_updates": avail}))
}

func (d *Device) handleScheduleList(w http.ResponseWriter, id int64) {
	jobs := d.schedules
	if jobs == nil {
		jobs = []map[string]any{}
	}
	writeJSON(w, rpcResult(id, map[string]any{"jobs": jobs, "rev": d.scheduleRev}))
}

func (d *Device) handleScheduleCreate(w http.ResponseWriter, id int64, params json.RawMessage) {
	var p map[string]any
	if err := json.Unmarshal(params, &p); err != nil || p["timespec"] == nil || p["calls"] == nil {
		writeJSON(w, rpcError(id, -103, "invalid Schedule.Create params"))
		return
	}
	job := merge(nil, p)
	job["id"] = d.nextSchedID
	d.nextSchedID++
	d.schedules = append(d.schedules, job)
	d.scheduleRev++
	writeJSON(w, rpcResult(id, map[string]any{"id": job["id"], "rev": d.scheduleRev}))
}

func (d *Device) handleScheduleUpdate(w http.ResponseWriter, id int64, params json.RawMessage) {
	var p struct {
		ID       *int    `json:"id"`
		Enable   *bool   `json:"enable"`
		Timespec *string `json:"timespec"`
		Calls    any     `json:"calls"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.ID == nil {
		writeJSON(w, rpcError(id, -103, "invalid Schedule.Update params"))
		return
	}
	idx := -1
	for i, j := range d.schedules {
		if idOf(j["id"]) == *p.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeJSON(w, rpcError(id, -103, fmt.Sprintf("schedule job %d not found", *p.ID)))
		return
	}
	var raw map[string]any
	if err := json.Unmarshal(params, &raw); err != nil {
		writeJSON(w, rpcError(id, -103, "invalid Schedule.Update params"))
		return
	}
	d.schedules[idx] = merge(d.schedules[idx], raw)
	d.scheduleRev++
	writeJSON(w, rpcResult(id, map[string]any{"rev": d.scheduleRev}))
}

func (d *Device) handleScheduleDelete(w http.ResponseWriter, id int64, params json.RawMessage) {
	var p struct {
		ID *int `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.ID == nil {
		writeJSON(w, rpcError(id, -103, "invalid Schedule.Delete params"))
		return
	}
	idx := -1
	for i, j := range d.schedules {
		if idOf(j["id"]) == *p.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeJSON(w, rpcError(id, -103, fmt.Sprintf("schedule job %d not found", *p.ID)))
		return
	}
	d.schedules = append(d.schedules[:idx], d.schedules[idx+1:]...)
	d.scheduleRev++
	writeJSON(w, rpcResult(id, map[string]any{"rev": d.scheduleRev}))
}

func (d *Device) handleSetConfig(w http.ResponseWriter, id int64, method string, params json.RawMessage) {
	if d.SetConfigError != "" {
		writeJSON(w, rpcError(id, -114, d.SetConfigError))
		return
	}
	if d.IgnoreSetConfig {
		writeJSON(w, rpcResult(id, map[string]any{"restart_required": d.RestartOnSetConfig}))
		return
	}
	comp := strings.ToLower(strings.TrimSuffix(method, ".SetConfig"))
	var p struct {
		ID     *int           `json:"id"`
		Config map[string]any `json:"config"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Config == nil {
		writeJSON(w, rpcError(id, -103, "invalid params"))
		return
	}
	if p.ID != nil { // keyed component, e.g. Switch.SetConfig {"id":0,...} -> "switch:0"
		comp = fmt.Sprintf("%s:%d", comp, *p.ID)
	}
	d.config[comp] = merge(d.config[comp], p.Config)
	writeJSON(w, rpcResult(id, map[string]any{"restart_required": d.RestartOnSetConfig}))
}

// idOf normalises a job "id" value that may be int (set by the fake) or
// float64 (after a JSON round-trip) to int for comparison.
func idOf(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	default:
		return -1
	}
}

// merge deep-merges src into a copy of dst (maps merged recursively,
// scalars/slices replaced), mirroring Shelly partial SetConfig semantics.
func merge(dst, src map[string]any) map[string]any {
	out := map[string]any{}
	maps.Copy(out, dst)
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			dm, _ := out[k].(map[string]any) // nil dst -> pure recursive copy
			out[k] = merge(dm, sm)
			continue
		}
		out[k] = v
	}
	return out
}

// authorized returns true when the request carries a valid SHA-256 digest
// credential. It validates that the echoed realm, nonce, and uri match what
// the server issued before computing the expected response hash.
func (d *Device) authorized(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Digest ") {
		return false
	}
	f := parseDigestFields(h)
	if f["username"] != "admin" || f["realm"] != d.ID || f["nonce"] != testNonce || f["uri"] != r.RequestURI {
		return false
	}
	ha1 := d.ha1
	ha2 := sha256hex(r.Method + ":" + f["uri"])
	want := sha256hex(strings.Join([]string{ha1, f["nonce"], f["nc"], f["cnonce"], "auth", ha2}, ":"))
	return f["response"] != "" && f["response"] == want
}

var digestFieldRe = regexp.MustCompile(`(\w+)=(?:"([^"]*)"|([^",\s]+))`)

func parseDigestFields(h string) map[string]string {
	out := map[string]string{}
	for _, m := range digestFieldRe.FindAllStringSubmatch(h, -1) {
		v := m[2]
		if v == "" {
			v = m[3]
		}
		out[m[1]] = v
	}
	return out
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func rpcResult(id int64, result any) map[string]any {
	return map[string]any{"id": id, "result": result}
}

func rpcError(id int64, code int, msg string) map[string]any {
	return map[string]any{"id": id, "error": map[string]any{"code": code, "message": msg}}
}
