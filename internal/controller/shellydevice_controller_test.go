package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly/shellytest"
)

func hostOf(url string) string { return strings.TrimPrefix(url, "http://") }

func newReconciler() (*ShellyDeviceReconciler, *record.FakeRecorder) {
	rec := record.NewFakeRecorder(20)
	return &ShellyDeviceReconciler{
		Client:      k8sClient,
		Reader:      k8sClient,
		Recorder:    rec,
		NameMapName: "shelly-names",
		Interval:    time.Minute,
	}, rec
}

// createDevice creates a ShellyDevice with discovery-like labels/status.
func createDevice(t *testing.T, ns, mac, addr string, online, paused bool, profileRef string) *shellyv1alpha1.ShellyDevice {
	t.Helper()
	ctx := context.Background()
	dev := &shellyv1alpha1.ShellyDevice{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      shellyv1alpha1.DeviceObjectName(mac),
			Labels:    shellyv1alpha1.DeviceLabels("SNPL-00112UK", "PlusPlugUK", 2),
		},
		Spec: shellyv1alpha1.ShellyDeviceSpec{Paused: paused, ProfileRef: profileRef},
	}
	if err := k8sClient.Create(ctx, dev); err != nil {
		t.Fatal(err)
	}
	dev.Status = shellyv1alpha1.ShellyDeviceStatus{
		Address: addr, MAC: mac, Model: "SNPL-00112UK", App: "PlusPlugUK", Gen: 2,
		Online: online, LastSeen: &metav1.Time{Time: time.Now()},
	}
	if err := k8sClient.Status().Update(ctx, dev); err != nil {
		t.Fatal(err)
	}
	return dev
}

func createProfile(t *testing.T, ns, name string, cfg shellyv1alpha1.ProfileConfig) {
	t.Helper()
	p := &shellyv1alpha1.ShellyProfile{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
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

func reconcile(t *testing.T, r *ShellyDeviceReconciler, ns, name string) *shellyv1alpha1.ShellyDevice {
	t.Helper()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: ns, Name: name}
	res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter <= 0 {
		t.Errorf("expected jittered requeue, got %+v", res)
	}
	var dev shellyv1alpha1.ShellyDevice
	if err := k8sClient.Get(ctx, key, &dev); err != nil {
		t.Fatal(err)
	}
	return &dev
}

func boolPtr(b bool) *bool { return &b }

func TestReconcileDriftDetected(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev1", MAC: "AABBCCDDEE20", Gen: 2, InitialConfig: map[string]map[string]any{
		"sys":   {"device": map[string]any{"eco_mode": false, "name": "old"}},
		"cloud": {"enable": false},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE20", hostOf(srv.URL), true, false, "")
	createProfile(t, ns, "plugs", shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: boolPtr(true)}, // drifted
		Cloud:  &shellyv1alpha1.CloudSection{Enable: boolPtr(false)},  // in sync
	})

	r, rec := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee20")

	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != shellyv1alpha1.ReasonDrifted {
		t.Fatalf("condition = %+v", cond)
	}
	if dev.Status.MatchedProfile != "plugs" {
		t.Errorf("matchedProfile = %q", dev.Status.MatchedProfile)
	}
	if len(dev.Status.DriftedSections) != 1 || dev.Status.DriftedSections[0] != "sys" {
		t.Errorf("driftedSections = %v", dev.Status.DriftedSections)
	}
	select {
	case e := <-rec.Events:
		if !strings.Contains(e, shellyv1alpha1.ReasonDrifted) {
			t.Errorf("event = %q", e)
		}
	default:
		t.Error("expected a drift event")
	}
}

func TestReconcileInSync(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev2", MAC: "AABBCCDDEE21", Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"eco_mode": true}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE21", hostOf(srv.URL), true, false, "")
	createProfile(t, ns, "plugs", shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: boolPtr(true)},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee21")
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != shellyv1alpha1.ReasonInSync {
		t.Fatalf("condition = %+v", cond)
	}
	if len(dev.Status.DriftedSections) != 0 {
		t.Errorf("driftedSections = %v", dev.Status.DriftedSections)
	}
}

func TestReconcileNoProfile(t *testing.T) {
	ns := newNamespace(t)
	createDevice(t, ns, "AABBCCDDEE22", "127.0.0.1:1", true, false, "")
	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee22")
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionUnknown || cond.Reason != shellyv1alpha1.ReasonNoProfile {
		t.Fatalf("condition = %+v", cond)
	}
}

func TestReconcilePausedSkipsRPC(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev4", MAC: "AABBCCDDEE23", Gen: 2}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE23", hostOf(srv.URL), true, true, "")
	createProfile(t, ns, "plugs", shellyv1alpha1.ProfileConfig{})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee23")
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionUnknown || cond.Reason != shellyv1alpha1.ReasonPaused {
		t.Fatalf("condition = %+v", cond)
	}
	if len(fake.RecordedCalls()) != 0 {
		t.Errorf("paused device must not be probed: %v", fake.RecordedCalls())
	}
}

func TestReconcileNameMapDrift(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev5", MAC: "AABBCCDDEE24", Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"name": "PDU-01"}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE24", hostOf(srv.URL), true, false, "")
	createProfile(t, ns, "plugs", shellyv1alpha1.ProfileConfig{
		Name: &shellyv1alpha1.NameSection{Managed: true},
	})
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "shelly-names"},
		Data:       map[string]string{"aabbccddee24": "rack-pdu"},
	}
	if err := k8sClient.Create(context.Background(), cm); err != nil {
		t.Fatal(err)
	}

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee24")
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionFalse {
		t.Fatalf("condition = %+v", cond)
	}
	if !strings.Contains(cond.Message, "rack-pdu") {
		t.Errorf("message should mention desired name: %q", cond.Message)
	}
}

func TestReconcileAuthDrift(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev6", MAC: "AABBCCDDEE25", Gen: 2}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE25", hostOf(srv.URL), true, false, "") // status.authEnabled=false
	createProfile(t, ns, "plugs", shellyv1alpha1.ProfileConfig{
		Auth: &shellyv1alpha1.AuthSection{Enable: boolPtr(true)},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee25")
	if len(dev.Status.DriftedSections) != 1 || dev.Status.DriftedSections[0] != "auth" {
		t.Errorf("driftedSections = %v", dev.Status.DriftedSections)
	}
}
