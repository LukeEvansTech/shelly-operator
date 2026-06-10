// Package shellytest provides a fake Shelly Gen2+ device backed by
// httptest.Server, for testing the RPC client and controllers without
// hardware. It emulates GET /shelly, POST /rpc dispatch, SHA-256 HTTP
// digest auth, and per-component config state with merge-on-SetConfig.
package shellytest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
)

// Device is the mutable state behind a fake Shelly device. Configure it,
// pass it to New, and inspect it after exercising the client.
type Device struct {
	ID       string
	MAC      string
	Model    string
	App      string
	Gen      int
	Firmware string
	Name     string
	Password string // "" = auth disabled

	mu             sync.Mutex
	Config         map[string]map[string]any // component ("sys", "switch:0") -> config
	Calls          []Call                    // recorded RPC calls, in order
	ChallengesSent int                       // number of 401 challenges issued
}

// Call records one RPC invocation that passed auth.
type Call struct {
	Method string
	Params json.RawMessage
}

// New starts the fake device. The caller must Close the returned server.
func New(d *Device) *httptest.Server {
	if d.Config == nil {
		d.Config = map[string]map[string]any{}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /shelly", d.handleProbe)
	mux.HandleFunc("POST /rpc", d.handleRPC)
	return httptest.NewServer(mux)
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
		d.ChallengesSent++
		w.Header().Set("WWW-Authenticate",
			fmt.Sprintf(`Digest qop="auth", realm=%q, nonce="testnonce1", algorithm=SHA-256`, d.ID))
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
	d.Calls = append(d.Calls, Call{Method: req.Method, Params: req.Params})

	switch {
	case req.Method == "Shelly.GetDeviceInfo":
		writeJSON(w, rpcResult(req.ID, d.deviceInfo()))
	case req.Method == "Shelly.GetConfig":
		writeJSON(w, rpcResult(req.ID, d.Config))
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
		d.Config[comp] = merge(d.Config[comp], p.Config)
		writeJSON(w, rpcResult(req.ID, map[string]any{"restart_required": false}))
	default:
		writeJSON(w, rpcError(req.ID, 404, "No handler for "+req.Method))
	}
}

// merge deep-merges src into a copy of dst (maps merged recursively,
// scalars/slices replaced), mirroring Shelly partial SetConfig semantics.
func merge(dst, src map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range dst {
		out[k] = v
	}
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := out[k].(map[string]any); ok {
				out[k] = merge(dm, sm)
				continue
			}
		}
		out[k] = v
	}
	return out
}

func (d *Device) authorized(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Digest ") {
		return false
	}
	f := parseDigestFields(h)
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
