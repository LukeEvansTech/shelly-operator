// Package shellytest provides a fake Shelly Gen2+ device backed by
// httptest.Server, for testing the RPC client and controllers without
// hardware. It emulates GET /shelly, POST /rpc dispatch, SHA-256 HTTP
// digest auth, and per-component config state with merge-on-SetConfig.
//
// Usage: populate the exported identity fields (ID, MAC, Model, App, Gen,
// Firmware, Name, Password) and InitialConfig before calling New; do not
// change them while the server is running. Inspect runtime state via the
// accessor methods RecordedCalls, ConfigSnapshot, and Challenges.
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
// identity fields and InitialConfig, then pass it to New. Do not modify any
// field after New returns. Inspect runtime state via RecordedCalls,
// ConfigSnapshot, and Challenges.
type Device struct {
	ID       string
	MAC      string
	Model    string
	App      string
	Gen      int
	Firmware string
	Name     string
	Password string // "" = auth disabled

	// InitialConfig seeds the per-component config the device starts with.
	// Keys are component names ("sys", "switch:0", …); values are config maps.
	// New copies this into the internal config store.
	InitialConfig map[string]map[string]any

	mu             sync.Mutex
	config         map[string]map[string]any // component ("sys", "switch:0") -> config
	calls          []Call                    // recorded RPC calls, in order
	challengesSent int                       // number of 401 challenges issued
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

// Challenges returns how many 401 digest challenges the device has issued.
func (d *Device) Challenges() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.challengesSent
}

func (d *Device) deviceInfo() map[string]any {
	var name any
	if d.Name != "" {
		name = d.Name
	}
	return map[string]any{
		"id": d.ID, "mac": d.MAC, "model": d.Model, "gen": d.Gen,
		"fw_id": d.Firmware, "app": d.App, "auth_en": d.Password != "", "name": name,
	}
}

func (d *Device) handleProbe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, d.deviceInfo()) // probe never requires auth, like real devices
}

func (d *Device) handleRPC(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.Password != "" && !d.authorized(r) {
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
		writeJSON(w, rpcResult(req.ID, d.config))
	case strings.HasSuffix(req.Method, ".SetConfig"):
		comp := strings.ToLower(strings.TrimSuffix(req.Method, ".SetConfig"))
		var p struct {
			ID     *int           `json:"id"`
			Config map[string]any `json:"config"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Config == nil {
			writeJSON(w, rpcError(req.ID, -103, "invalid params"))
			return
		}
		if p.ID != nil { // keyed component, e.g. Switch.SetConfig {"id":0,...} -> "switch:0"
			comp = fmt.Sprintf("%s:%d", comp, *p.ID)
		}
		d.config[comp] = merge(d.config[comp], p.Config)
		writeJSON(w, rpcResult(req.ID, map[string]any{"restart_required": false}))
	default:
		writeJSON(w, rpcError(req.ID, 404, "No handler for "+req.Method))
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
	ha1 := sha256hex("admin:" + f["realm"] + ":" + d.Password)
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
