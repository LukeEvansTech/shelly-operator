package shelly

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// HTTP digest auth, SHA-256, per the Shelly Gen2+ docs: username is always
// "admin", realm is the device id, qop is "auth".

type digestState struct {
	realm string
	nonce string
	nc    uint32
}

var digestFieldRe = regexp.MustCompile(`(\w+)=(?:"([^"]*)"|([^",\s]+))`)

// setChallenge parses a WWW-Authenticate header and caches its parameters.
func (c *Client) setChallenge(header string) error {
	f := map[string]string{}
	for _, m := range digestFieldRe.FindAllStringSubmatch(header, -1) {
		v := m[2]
		if v == "" {
			v = m[3]
		}
		f[m[1]] = v
	}
	if f["realm"] == "" || f["nonce"] == "" {
		return fmt.Errorf("shelly: %s: unsupported auth challenge %q", c.host, header)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.auth = &digestState{realm: f["realm"], nonce: f["nonce"]}
	return nil
}

// authHeader computes an Authorization header from the cached challenge,
// or returns "" when no challenge has been seen / no password configured.
func (c *Client) authHeader() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.auth == nil || c.password == "" {
		return ""
	}
	c.auth.nc++
	nc := fmt.Sprintf("%08x", c.auth.nc)
	cnonce := randomHex(8)
	ha1 := sha256hex("admin:" + c.auth.realm + ":" + c.password)
	ha2 := sha256hex("POST:/rpc")
	response := sha256hex(strings.Join([]string{ha1, c.auth.nonce, nc, cnonce, "auth", ha2}, ":"))
	return fmt.Sprintf(
		`Digest username="admin", realm=%q, nonce=%q, uri="/rpc", response=%q, qop=auth, nc=%s, cnonce=%q, algorithm=SHA-256`,
		c.auth.realm, c.auth.nonce, response, nc, cnonce)
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
