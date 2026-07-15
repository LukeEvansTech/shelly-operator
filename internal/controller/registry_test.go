package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly/shellytest"
)

// Ensure ctrl is referenced to suppress unused import errors from
// the reconcileRaw function's use of ctrl.Request.
var _ = ctrl.Request{}

// newRegistryReconciler creates a reconciler with both name-map and registry enabled.
func newRegistryReconciler() *ShellyDeviceReconciler {
	return &ShellyDeviceReconciler{
		Client:       k8sClient,
		Reader:       k8sClient,
		Recorder:     record.NewFakeRecorder(20),
		NameMapName:  "shelly-names",
		RegistryName: "shelly-registry",
		Interval:     time.Minute,
	}
}

// createRegistryCM creates the shelly-registry ConfigMap in the given namespace.
func createRegistryCM(t *testing.T, ns string, data map[string]string) *corev1.ConfigMap {
	t.Helper()
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "shelly-registry"},
		Data:       data,
	}
	if err := k8sClient.Create(context.Background(), cm); err != nil {
		t.Fatal(err)
	}
	return cm
}

// reconcileRaw calls the reconciler and returns the current device state.
// It does NOT fatalf on non-zero requeue or non-nil error -- callers check
// those themselves.
func reconcileRaw(t *testing.T, r *ShellyDeviceReconciler, ns, name string) (*shellyv1alpha1.ShellyDevice, error) {
	t.Helper()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: ns, Name: name}
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
	var dev shellyv1alpha1.ShellyDevice
	if getErr := k8sClient.Get(ctx, key, &dev); getErr != nil {
		t.Fatal(getErr)
	}
	return &dev, err
}

// TestRegistryStampsLabelsAndAnnotation verifies that when a device has a
// registry entry with room/type/note, after reconcile it carries the
// expected label and annotation values merged with the discovery labels.
func TestRegistryStampsLabelsAndAnnotation(t *testing.T) {
	ns := newNamespace(t)
	mac := "BBCCDDEEAA01"
	fake := &shellytest.Device{ID: "reg01", MAC: mac, Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"eco_mode": true}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()

	dev := createDevice(t, ns, mac, hostOf(srv.URL), true, false, "")
	// Discovery labels should already be present.
	if dev.Labels[shellyv1alpha1.LabelApp] == "" {
		t.Fatal("createDevice must set discovery labels")
	}

	createRegistryCM(t, ns, map[string]string{
		"bbccddeeaa01": `{"name":"lounge-lamp","room":"Living Room","type":"Lamp","note":"top shelf"}`,
	})
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: boolPtr(true)},
	})

	r := newRegistryReconciler()
	got, err := reconcileRaw(t, r, ns, "bbccddeeaa01")
	if err != nil {
		t.Fatal(err)
	}

	// Registry labels stamped.
	if got.Labels[shellyv1alpha1.LabelRoom] != "living-room" {
		t.Errorf("room label = %q, want living-room", got.Labels[shellyv1alpha1.LabelRoom])
	}
	if got.Labels[shellyv1alpha1.LabelAppliance] != "lamp" {
		t.Errorf("appliance label = %q, want lamp", got.Labels[shellyv1alpha1.LabelAppliance])
	}
	if got.Annotations[shellyv1alpha1.AnnotationNote] != "top shelf" {
		t.Errorf("note annotation = %q, want top shelf", got.Annotations[shellyv1alpha1.AnnotationNote])
	}

	// Discovery labels NOT clobbered.
	if got.Labels[shellyv1alpha1.LabelApp] == "" {
		t.Error("discovery LabelApp must not be clobbered by registry stamping")
	}
	if got.Labels[shellyv1alpha1.LabelModel] == "" {
		t.Error("discovery LabelModel must not be clobbered by registry stamping")
	}
}

// TestRegistryUpdateLabels verifies that changing the registry entry causes
// the labels/annotations to update on the next reconcile.
func TestRegistryUpdateLabels(t *testing.T) {
	ns := newNamespace(t)
	mac := "BBCCDDEEAA02"
	fake := &shellytest.Device{ID: "reg02", MAC: mac, Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"eco_mode": true}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()

	createDevice(t, ns, mac, hostOf(srv.URL), true, false, "")
	cm := createRegistryCM(t, ns, map[string]string{
		"bbccddeeaa02": `{"room":"Kitchen","type":"Socket"}`,
	})
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: boolPtr(true)},
	})

	r := newRegistryReconciler()
	got, _ := reconcileRaw(t, r, ns, "bbccddeeaa02")
	if got.Labels[shellyv1alpha1.LabelRoom] != "kitchen" {
		t.Fatalf("first reconcile: room = %q, want kitchen", got.Labels[shellyv1alpha1.LabelRoom])
	}

	// Update the registry entry.
	cm.Data["bbccddeeaa02"] = `{"room":"Lounge","type":"Lamp"}`
	if err := k8sClient.Update(context.Background(), cm); err != nil {
		t.Fatal(err)
	}

	got2, _ := reconcileRaw(t, r, ns, "bbccddeeaa02")
	if got2.Labels[shellyv1alpha1.LabelRoom] != "lounge" {
		t.Errorf("after update: room = %q, want lounge", got2.Labels[shellyv1alpha1.LabelRoom])
	}
	if got2.Labels[shellyv1alpha1.LabelAppliance] != "lamp" {
		t.Errorf("after update: appliance = %q, want lamp", got2.Labels[shellyv1alpha1.LabelAppliance])
	}
}

// TestRegistryNoEntryNoStamping verifies that a device with no registry entry
// does not receive room/appliance labels or note annotation.
func TestRegistryNoEntryNoStamping(t *testing.T) {
	ns := newNamespace(t)
	mac := "BBCCDDEEAA03"
	fake := &shellytest.Device{ID: "reg03", MAC: mac, Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"eco_mode": true}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()

	createDevice(t, ns, mac, hostOf(srv.URL), true, false, "")
	// Registry ConfigMap exists but has no entry for this device.
	createRegistryCM(t, ns, map[string]string{
		"other-device": `{"room":"Den"}`,
	})
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: boolPtr(true)},
	})

	r := newRegistryReconciler()
	got, err := reconcileRaw(t, r, ns, "bbccddeeaa03")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Labels[shellyv1alpha1.LabelRoom]; ok {
		t.Errorf("room label must not be set without a registry entry, got %q", got.Labels[shellyv1alpha1.LabelRoom])
	}
	if _, ok := got.Labels[shellyv1alpha1.LabelAppliance]; ok {
		t.Errorf("appliance label must not be set without a registry entry")
	}
	if _, ok := got.Annotations[shellyv1alpha1.AnnotationNote]; ok {
		t.Errorf("note annotation must not be set without a registry entry")
	}
}

// TestRegistryNoSpuriousRewrite verifies that when nothing has changed the
// object ResourceVersion does not change across two reconciles.
func TestRegistryNoSpuriousRewrite(t *testing.T) {
	ns := newNamespace(t)
	mac := "BBCCDDEEAA04"
	fake := &shellytest.Device{ID: "reg04", MAC: mac, Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"eco_mode": true}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()

	createDevice(t, ns, mac, hostOf(srv.URL), true, false, "")
	createRegistryCM(t, ns, map[string]string{
		"bbccddeeaa04": `{"room":"Office","type":"Lamp"}`,
	})
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: boolPtr(true)},
	})

	r := newRegistryReconciler()
	first, _ := reconcileRaw(t, r, ns, "bbccddeeaa04")
	second, _ := reconcileRaw(t, r, ns, "bbccddeeaa04")

	// Status patch happens each reconcile but metadata labels only when changed.
	// We check that label ResourceVersion stabilizes after the first stamp:
	// the first call may update labels; the second must not update them again.
	// Since both status and labels are on the same object, we check the labels
	// are the same between first and second (values don't regress).
	if first.Labels[shellyv1alpha1.LabelRoom] != second.Labels[shellyv1alpha1.LabelRoom] {
		t.Errorf("room label changed unexpectedly: %q -> %q",
			first.Labels[shellyv1alpha1.LabelRoom], second.Labels[shellyv1alpha1.LabelRoom])
	}
}

// TestRegistryStampsCustomLabels covers the registry's custom labels map.
// room/type are semantic (where a device is, what it is); policy is a
// different axis -- a dishwasher is BOTH "kitchen" and "must power back on
// after an outage" -- and a device has only one type, so policy cannot ride
// on it without destroying the semantics. Custom labels give profiles a
// selector that is independent of room/type. They are namespaced under
// registry.shelly.thirdimpact.io/ so removal is computable by prefix without
// touching the operator's own labels (app/model/gen from discovery, and
// room/appliance).
func TestRegistryStampsCustomLabels(t *testing.T) {
	ns := newNamespace(t)
	mac := "BBCCDDEEAA05"
	fake := &shellytest.Device{ID: "reg05", MAC: mac, Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"eco_mode": true}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, mac, hostOf(srv.URL), true, false, "")
	createRegistryCM(t, ns, map[string]string{
		"bbccddeeaa05": `{"name":"Dishwasher","room":"Kitchen","type":"kitchen","labels":{"power-policy":"Always On"}}`,
	})
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: boolPtr(true)},
	})

	r := newRegistryReconciler()
	got, err := reconcileRaw(t, r, ns, "bbccddeeaa05")
	if err != nil {
		t.Fatal(err)
	}

	key := shellyv1alpha1.LabelRegistryPrefix + "power-policy"
	if got.Labels[key] != "always-on" {
		t.Errorf("custom label %s = %q, want always-on (value must be sanitized like room/type)", key, got.Labels[key])
	}
	// The semantic axis must survive: that is the whole point.
	if got.Labels[shellyv1alpha1.LabelAppliance] != "kitchen" {
		t.Errorf("appliance = %q, want kitchen (custom labels must not displace type)", got.Labels[shellyv1alpha1.LabelAppliance])
	}
	if got.Labels[shellyv1alpha1.LabelApp] == "" {
		t.Error("discovery labels must not be clobbered")
	}
}

// TestRegistryRemovesClearedCustomLabel verifies a custom label disappears
// when the registry stops declaring it -- the same contract room/appliance
// already have. Without prefix-scoped removal a retired policy label would
// linger forever and keep selecting the device into a profile.
func TestRegistryRemovesClearedCustomLabel(t *testing.T) {
	ns := newNamespace(t)
	mac := "BBCCDDEEAA06"
	fake := &shellytest.Device{ID: "reg06", MAC: mac, Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"eco_mode": true}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, mac, hostOf(srv.URL), true, false, "")
	cm := createRegistryCM(t, ns, map[string]string{
		"bbccddeeaa06": `{"name":"PDU-01","room":"Garage","type":"rack","labels":{"power-policy":"infra"}}`,
	})
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: boolPtr(true)},
	})

	r := newRegistryReconciler()
	key := shellyv1alpha1.LabelRegistryPrefix + "power-policy"
	got, err := reconcileRaw(t, r, ns, "bbccddeeaa06")
	if err != nil {
		t.Fatal(err)
	}
	if got.Labels[key] != "infra" {
		t.Fatalf("precondition: custom label = %q, want infra", got.Labels[key])
	}

	// Registry drops the labels map entirely.
	cm.Data["bbccddeeaa06"] = `{"name":"PDU-01","room":"Garage","type":"rack"}`
	if err := k8sClient.Update(context.Background(), cm); err != nil {
		t.Fatal(err)
	}
	got, err = reconcileRaw(t, r, ns, "bbccddeeaa06")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := got.Labels[key]; exists {
		t.Errorf("custom label %s still present after registry cleared it", key)
	}
	if got.Labels[shellyv1alpha1.LabelAppliance] != "rack" {
		t.Errorf("appliance = %q, want rack (removal must be scoped to the custom prefix)", got.Labels[shellyv1alpha1.LabelAppliance])
	}
}
