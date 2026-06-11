package dashboard

import (
	"context"
	"net/http"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly/shellytest"
)

func hostOf(url string) string { return strings.TrimPrefix(url, "http://") }

func boolPtr(b bool) *bool { return &b }

func createDashProfile(t *testing.T, ns string, cfg shellyv1alpha1.ProfileConfig) {
	t.Helper()
	p := &shellyv1alpha1.ShellyProfile{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "plugs"},
		Spec: shellyv1alpha1.ShellyProfileSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{shellyv1alpha1.LabelApp: "PlusPlugUK"}},
			Mode:     shellyv1alpha1.ModeObserve,
			Config:   cfg,
		},
	}
	if err := k8sClient.Create(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceDetailShowsDiffAndRedactsSecrets(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev60", MAC: "AABBCCDDEE62", Gen: 2, InitialConfig: map[string]map[string]any{
		"sys":  {"device": map[string]any{"eco_mode": false}},
		"wifi": {"sta": map[string]any{"ssid": "homenet", "pass": "supersecret123"}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDashDevice(t, ns, "AABBCCDDEE62", hostOf(srv.URL), true, "plugs", []string{"sys"})
	createDashProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: boolPtr(true)},
	})

	code, body := get(t, newServer(ns), "/device/aabbccddee62")
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, body)
	}
	for _, want := range []string{"eco_mode", "plugs", "homenet"} {
		if !strings.Contains(body, want) {
			t.Errorf("detail page missing %q", want)
		}
	}
	if strings.Contains(body, "supersecret123") {
		t.Fatal("secret value leaked into the dashboard")
	}
	if !strings.Contains(body, redacted) {
		t.Error("expected redaction marker")
	}
}

func TestDeviceDetailOfflineSkipsRPC(t *testing.T) {
	ns := newNamespace(t)
	createDashDevice(t, ns, "AABBCCDDEE63", "127.0.0.1:1", false, "", nil)
	code, body := get(t, newServer(ns), "/device/aabbccddee63")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if !strings.Contains(body, "offline") {
		t.Errorf("offline device page should say offline: %s", body)
	}
}

func TestDeviceDetailNotFound(t *testing.T) {
	ns := newNamespace(t)
	code, _ := get(t, newServer(ns), "/device/nope")
	if code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}

func TestProfilesView(t *testing.T) {
	ns := newNamespace(t)
	createDashProfile(t, ns, shellyv1alpha1.ProfileConfig{})
	createDashDevice(t, ns, "AABBCCDDEE64", "10.32.8.64", true, "plugs", nil)

	code, body := get(t, newServer(ns), "/profiles")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	for _, want := range []string{"plugs", "observe", "1 device"} {
		if !strings.Contains(body, want) {
			t.Errorf("profiles page missing %q", want)
		}
	}
}

const redacted = "[redacted]"

func TestRedactSecrets(t *testing.T) {
	in := map[string]any{
		"sta":  map[string]any{"ssid": "net", "pass": "x", "password": "y"},
		"safe": "value",
	}
	out := redactSecrets(in)
	sta := out["sta"].(map[string]any)
	if sta["pass"] != redacted || sta["password"] != redacted {
		t.Errorf("pass keys must be redacted: %v", sta)
	}
	if sta["ssid"] != "net" || out["safe"] != "value" {
		t.Errorf("non-secret values must survive: %v", out)
	}
	if in["sta"].(map[string]any)["pass"] != "x" {
		t.Error("input must not be mutated")
	}
}
