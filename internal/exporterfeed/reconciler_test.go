package exporterfeed

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
)

func createFeedDevice(t *testing.T, ns, mac, addr string, online bool) {
	t.Helper()
	ctx := context.Background()
	dev := &shellyv1alpha1.ShellyDevice{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: shellyv1alpha1.DeviceObjectName(mac)},
	}
	if err := k8sClient.Create(ctx, dev); err != nil {
		t.Fatal(err)
	}
	dev.Status = shellyv1alpha1.ShellyDeviceStatus{Address: addr, MAC: mac, Online: online}
	if err := k8sClient.Status().Update(ctx, dev); err != nil {
		t.Fatal(err)
	}
}

func feedReconcile(t *testing.T, r *Reconciler, ns string) *corev1.ConfigMap {
	t.Helper()
	ctx := context.Background()
	if _, err := r.Reconcile(ctx, ctrl.Request{}); err != nil {
		t.Fatal(err)
	}
	var cm corev1.ConfigMap
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "shelly-exporter-config"}, &cm); err != nil {
		t.Fatal(err)
	}
	return &cm
}

func TestFeedCreatesAndUpdatesConfigMap(t *testing.T) {
	ns := newNamespace(t)
	createFeedDevice(t, ns, "AABBCCDDEE50", "10.32.8.50", true)
	createFeedDevice(t, ns, "AABBCCDDEE51", "10.32.8.51", false) // offline

	r := &Reconciler{Client: k8sClient, Namespace: ns, ConfigMapName: "shelly-exporter-config",
		Options: Options{ListenAddress: ":8080", DeviceUpdateInterval: 30}}
	cm := feedReconcile(t, r, ns)
	if !strings.Contains(cm.Data["config.yaml"], "10.32.8.50") || strings.Contains(cm.Data["config.yaml"], "10.32.8.51") {
		t.Errorf("config.yaml = %q", cm.Data["config.yaml"])
	}
	if cm.Labels["app.kubernetes.io/managed-by"] != "shelly-operator" {
		t.Errorf("labels = %v", cm.Labels)
	}
	firstVersion := cm.ResourceVersion

	// No change -> no update (resourceVersion stable).
	cm = feedReconcile(t, r, ns)
	if cm.ResourceVersion != firstVersion {
		t.Error("idempotent reconcile must not rewrite the ConfigMap")
	}

	// Device comes online -> update.
	ctx := context.Background()
	var dev shellyv1alpha1.ShellyDevice
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "aabbccddee51"}, &dev); err != nil {
		t.Fatal(err)
	}
	dev.Status.Online = true
	if err := k8sClient.Status().Update(ctx, &dev); err != nil {
		t.Fatal(err)
	}
	cm = feedReconcile(t, r, ns)
	if !strings.Contains(cm.Data["config.yaml"], "10.32.8.51") {
		t.Errorf("config.yaml after online flip = %q", cm.Data["config.yaml"])
	}
}

func TestFeedRestoresTamperedConfigMap(t *testing.T) {
	ns := newNamespace(t)
	createFeedDevice(t, ns, "AABBCCDDEE52", "10.32.8.52", true)
	r := &Reconciler{Client: k8sClient, Namespace: ns, ConfigMapName: "shelly-exporter-config",
		Options: Options{ListenAddress: ":8080", DeviceUpdateInterval: 30}}
	cm := feedReconcile(t, r, ns)

	cm.Data["config.yaml"] = "tampered"
	if err := k8sClient.Update(context.Background(), cm); err != nil {
		t.Fatal(err)
	}
	cm = feedReconcile(t, r, ns)
	if cm.Data["config.yaml"] == "tampered" {
		t.Error("reconcile must restore the rendered config")
	}
}
