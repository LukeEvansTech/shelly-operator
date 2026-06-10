package discovery

import (
	"strings"
	"testing"
)

func TestExpandCIDRs(t *testing.T) {
	cases := []struct {
		name  string
		cidrs []string
		want  int
		first string
		last  string
	}{
		{"slash29", []string{"10.32.8.0/29"}, 6, "10.32.8.1", "10.32.8.6"},
		{"slash31 keeps both", []string{"10.32.8.0/31"}, 2, "10.32.8.0", "10.32.8.1"},
		{"slash32 single", []string{"10.32.8.38/32"}, 1, "10.32.8.38", "10.32.8.38"},
		{"two cidrs", []string{"10.32.8.0/30", "10.32.9.0/30"}, 4, "10.32.8.1", "10.32.9.2"},
		{"unmasked input normalized", []string{"10.0.0.5/30"}, 2, "10.0.0.5", "10.0.0.6"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hosts, err := ExpandCIDRs(c.cidrs)
			if err != nil {
				t.Fatal(err)
			}
			if len(hosts) != c.want {
				t.Fatalf("len = %d, want %d (%v)", len(hosts), c.want, hosts)
			}
			if hosts[0] != c.first || hosts[len(hosts)-1] != c.last {
				t.Errorf("range = %s..%s, want %s..%s", hosts[0], hosts[len(hosts)-1], c.first, c.last)
			}
		})
	}
}

func TestExpandCIDRsErrors(t *testing.T) {
	if _, err := ExpandCIDRs([]string{"not-a-cidr"}); err == nil {
		t.Error("expected error for invalid CIDR")
	}
	if _, err := ExpandCIDRs([]string{"2001:db8::/120"}); err == nil {
		t.Error("expected error for IPv6 CIDR")
	}
	if _, err := ExpandCIDRs([]string{"10.0.0.0/16"}); err == nil || !strings.Contains(err.Error(), "4096") {
		t.Errorf("expected too-many-hosts error, got %v", err)
	}
	if _, err := ExpandCIDRs([]string{"::ffff:10.0.0.0/120"}); err == nil {
		t.Error("expected error for IPv4-mapped IPv6 CIDR")
	}
}
