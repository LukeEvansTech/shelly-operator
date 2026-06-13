package drift

import (
	"sort"
	"strings"
)

// ApplyOrder returns drifted sections sorted safest-first for enforcement:
// sys, then switch components and *_ui (plug LED/button -- harmless to
// connectivity), then mqtt, then cloud, ble, and ws (outbound WebSocket --
// connectivity-harmless), then firmware; auth second-to-last (it changes
// how the controller must talk to the device) and wifi always dead last
// (it can move the device to another network, dropping connectivity at its
// current address -- nothing can be applied after it). Unknown sections
// sort after ws but before auth. The input is not mutated.
func ApplyOrder(sections []string) []string {
	rank := func(s string) int {
		switch {
		case s == "sys":
			return 0
		case strings.HasPrefix(s, "switch:"):
			return 1
		case strings.HasSuffix(s, "_ui"):
			// Plug UI (LED/button) -- harmless to connectivity.
			return 1
		case s == "mqtt":
			return 2
		case s == "cloud":
			return 3
		case s == "ble":
			return 3
		case s == "ws":
			return 3
		case s == "firmware":
			// Pseudo-section (schedule-job writes); harmless to
			// connectivity, applied with the ordinary sections.
			return 4
		case s == "auth":
			return 100
		case s == "wifi":
			return 200
		default:
			return 50
		}
	}
	out := append([]string(nil), sections...)
	sort.SliceStable(out, func(a, b int) bool {
		if rank(out[a]) != rank(out[b]) {
			return rank(out[a]) < rank(out[b])
		}
		return out[a] < out[b]
	})
	return out
}
