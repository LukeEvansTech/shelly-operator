package exporterfeed

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
)

func feedDev(name, addr string, online bool) shellyv1alpha1.ShellyDevice {
	return shellyv1alpha1.ShellyDevice{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     shellyv1alpha1.ShellyDeviceStatus{Address: addr, Online: online},
	}
}

func TestRenderConfig(t *testing.T) {
	devs := []shellyv1alpha1.ShellyDevice{
		feedDev("b", "10.32.8.20", true),
		feedDev("a", "10.32.8.10", true),
		feedDev("c", "10.32.8.30", false), // offline: excluded
		feedDev("d", "", true),            // no address: excluded
	}
	got, err := RenderConfig(devs, Options{ListenAddress: ":8080", DeviceUpdateInterval: 30})
	if err != nil {
		t.Fatal(err)
	}
	want := `debug: false
deviceUpdateInterval: 30
devices:
- host: 10.32.8.10
- host: 10.32.8.20
listenAddress: :8080
`
	if got != want {
		t.Errorf("RenderConfig =\n%s\nwant\n%s", got, want)
	}
}

// The poll interval is the single biggest driver of RPC load on the fleet (the
// exporter issues ~6 RPCs per device per cycle), so it is operator-configurable
// rather than fixed. Guard that a non-default value actually reaches the config
// and is not quietly pinned to the old hardcoded 30.
func TestRenderConfigHonoursDeviceUpdateInterval(t *testing.T) {
	devs := []shellyv1alpha1.ShellyDevice{feedDev("a", "10.32.8.10", true)}
	got, err := RenderConfig(devs, Options{ListenAddress: ":8080", DeviceUpdateInterval: 60})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "deviceUpdateInterval: 60") {
		t.Errorf("configured interval must be rendered, got:\n%s", got)
	}
	if strings.Contains(got, "deviceUpdateInterval: 30") {
		t.Errorf("interval must not fall back to the old hardcoded 30:\n%s", got)
	}
}

func TestRenderConfigEmptyFleet(t *testing.T) {
	got, err := RenderConfig(nil, Options{ListenAddress: ":8080", DeviceUpdateInterval: 30})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "devices: []") {
		t.Errorf("empty fleet must render an empty devices list:\n%s", got)
	}
}

func TestRenderConfigDedupesAddresses(t *testing.T) {
	devs := []shellyv1alpha1.ShellyDevice{
		feedDev("a", "10.32.8.10", true),
		feedDev("b", "10.32.8.10", true),
	}
	got, err := RenderConfig(devs, Options{ListenAddress: ":8080", DeviceUpdateInterval: 30})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, "10.32.8.10") != 1 {
		t.Errorf("duplicate addresses must collapse:\n%s", got)
	}
}
