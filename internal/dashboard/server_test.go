package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
)

func createDashDevice(t *testing.T, ns, mac, addr string, online bool, matchedProfile string, drifted []string) {
	t.Helper()
	ctx := context.Background()
	dev := &shellyv1alpha1.ShellyDevice{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: shellyv1alpha1.DeviceObjectName(mac),
			Labels: shellyv1alpha1.DeviceLabels("SNPL-00112UK", "PlusPlugUK", 2)},
	}
	if err := k8sClient.Create(ctx, dev); err != nil {
		t.Fatal(err)
	}
	dev.Status = shellyv1alpha1.ShellyDeviceStatus{
		Address: addr, MAC: mac, Model: "SNPL-00112UK", App: "PlusPlugUK", Gen: 2,
		Online: online, MatchedProfile: matchedProfile, DriftedSections: drifted,
	}
	status := metav1.ConditionTrue
	reason := shellyv1alpha1.ReasonInSync
	if len(drifted) > 0 {
		status = metav1.ConditionFalse
		reason = shellyv1alpha1.ReasonDrifted
	}
	meta.SetStatusCondition(&dev.Status.Conditions, metav1.Condition{
		Type: shellyv1alpha1.ConditionInSync, Status: status, Reason: reason, Message: "test",
	})
	if err := k8sClient.Status().Update(ctx, dev); err != nil {
		t.Fatal(err)
	}
}

func newServer(ns string) *Server {
	return &Server{Client: k8sClient, Reader: k8sClient, Namespace: ns, NameMapName: "shelly-names", Addr: ":0"}
}

func get(t *testing.T, s *Server, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestFleetView(t *testing.T) {
	ns := newNamespace(t)
	createDashDevice(t, ns, "AABBCCDDEE60", "10.32.8.60", true, "plugs", nil)
	createDashDevice(t, ns, "AABBCCDDEE61", "10.32.8.61", false, "", []string{"sys"})

	code, body := get(t, newServer(ns), "/")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	for _, want := range []string{"aabbccddee60", "10.32.8.60", "plugs", "SNPL-00112UK", "aabbccddee61", "sys"} {
		if !strings.Contains(body, want) {
			t.Errorf("fleet page missing %q", want)
		}
	}
}

func TestServerIsNotLeaderElected(t *testing.T) {
	if (&Server{}).NeedLeaderElection() {
		t.Error("dashboard must run on every replica (read-only)")
	}
}

func TestFleetShowsAvailableUpdate(t *testing.T) {
	ns := newNamespace(t)
	dev := &shellyv1alpha1.ShellyDevice{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "aabbccddef20"}}
	if err := k8sClient.Create(context.Background(), dev); err != nil {
		t.Fatal(err)
	}
	dev.Status = shellyv1alpha1.ShellyDeviceStatus{
		Address: "10.0.0.9", Model: "SNPL-00112UK", Online: true,
		Firmware: "20241011-114446/1.4.4-g6d2a586", AvailableFirmware: "1.7.5",
	}
	if err := k8sClient.Status().Update(context.Background(), dev); err != nil {
		t.Fatal(err)
	}

	_, body := get(t, newServer(ns), "/")
	if !strings.Contains(body, `pill warn">1.7.5`) {
		t.Fatalf("fleet page missing available update pill:\n%s", body)
	}
}
