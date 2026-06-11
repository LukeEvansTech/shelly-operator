package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

func createProfile(t *testing.T, ns string, cfg shellyv1alpha1.ProfileConfig) {
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
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{
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
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{
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
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{})

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
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{
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
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Auth: &shellyv1alpha1.AuthSection{Enable: boolPtr(true)},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee25")
	if len(dev.Status.DriftedSections) != 1 || dev.Status.DriftedSections[0] != "auth" {
		t.Errorf("driftedSections = %v", dev.Status.DriftedSections)
	}
}

func TestReconcileConfigFetchFailedKeepsProfile(t *testing.T) {
	ns := newNamespace(t)
	createDevice(t, ns, "AABBCCDDEE26", "127.0.0.1:1", true, false, "")
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: boolPtr(true)},
	})
	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee26")
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionUnknown || cond.Reason != shellyv1alpha1.ReasonConfigFetchFailed {
		t.Fatalf("condition = %+v", cond)
	}
	if dev.Status.MatchedProfile != "plugs" {
		t.Errorf("matchedProfile must survive fetch failure, got %q", dev.Status.MatchedProfile)
	}
}

func TestReconcileAuthRequired(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev7", MAC: "AABBCCDDEE27", Gen: 2, Password: "secret"}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE27", hostOf(srv.URL), true, false, "")
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: boolPtr(true)},
	})
	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee27")
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Reason != shellyv1alpha1.ReasonAuthRequired {
		t.Fatalf("condition = %+v, want AuthRequired", cond)
	}
}

func TestReconcileProfileRefPin(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev8", MAC: "AABBCCDDEE28", Gen: 2}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE28", hostOf(srv.URL), true, false, "pinned")
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{}) // selector match, would win without pin
	pin := &shellyv1alpha1.ShellyProfile{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "pinned"},
		Spec:       shellyv1alpha1.ShellyProfileSpec{Mode: shellyv1alpha1.ModeObserve},
	}
	if err := k8sClient.Create(context.Background(), pin); err != nil {
		t.Fatal(err)
	}
	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee28")
	if dev.Status.MatchedProfile != "pinned" {
		t.Errorf("matchedProfile = %q, want pinned", dev.Status.MatchedProfile)
	}
}

func TestReconcileDisplayNamePrecedence(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev9", MAC: "AABBCCDDEE29", Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"name": "old"}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	dev := createDevice(t, ns, "AABBCCDDEE29", hostOf(srv.URL), true, false, "")
	dev.Spec.DisplayName = "front-desk"
	if err := k8sClient.Update(context.Background(), dev); err != nil {
		t.Fatal(err)
	}
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Name: &shellyv1alpha1.NameSection{Managed: true},
	})
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "shelly-names"},
		Data:       map[string]string{"aabbccddee29": "rack-pdu"},
	}
	if err := k8sClient.Create(context.Background(), cm); err != nil {
		t.Fatal(err)
	}
	r, _ := newReconciler()
	got := reconcile(t, r, ns, "aabbccddee29")
	cond := meta.FindStatusCondition(got.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || !strings.Contains(cond.Message, "front-desk") {
		t.Errorf("displayName must beat name map; condition = %+v", cond)
	}
}

func TestReconcileFixpointNoStatusChurn(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev10", MAC: "AABBCCDDEE2A", Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"eco_mode": true}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE2A", hostOf(srv.URL), true, false, "")
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: boolPtr(true)},
	})
	r, _ := newReconciler()
	first := reconcile(t, r, ns, "aabbccddee2a")
	second := reconcile(t, r, ns, "aabbccddee2a")
	if first.ResourceVersion != second.ResourceVersion {
		t.Errorf("steady-state reconcile must not churn status: rv %s -> %s", first.ResourceVersion, second.ResourceVersion)
	}
}

func TestReconcileOfflineSkipsRPC(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev11", MAC: "AABBCCDDEE2B", Gen: 2}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE2B", hostOf(srv.URL), false, false, "")
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee2b")
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionUnknown || cond.Reason != shellyv1alpha1.ReasonOffline {
		t.Fatalf("condition = %+v", cond)
	}
	if len(fake.RecordedCalls()) != 0 {
		t.Errorf("offline device must not be probed: %v", fake.RecordedCalls())
	}
}

// errReader fails every Get with a non-NotFound error.
type errReader struct{}

func (errReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return fmt.Errorf("boom")
}

func (errReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return fmt.Errorf("boom")
}

func TestReconcileNameMapReadError(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev12", MAC: "AABBCCDDEE2C", Gen: 2}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE2C", hostOf(srv.URL), true, false, "")
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Name: &shellyv1alpha1.NameSection{Managed: true},
	})
	r, _ := newReconciler()
	r.Reader = errReader{}
	dev := reconcile(t, r, ns, "aabbccddee2c")
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionUnknown || !strings.Contains(cond.Message, "name map") {
		t.Fatalf("name-map read failure must not masquerade as in-sync; condition = %+v", cond)
	}
}

func createEnforceProfile(t *testing.T, ns string, cfg shellyv1alpha1.ProfileConfig) {
	t.Helper()
	p := &shellyv1alpha1.ShellyProfile{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "plugs"},
		Spec: shellyv1alpha1.ShellyProfileSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{shellyv1alpha1.LabelApp: "PlusPlugUK"}},
			Mode:     shellyv1alpha1.ModeEnforce,
			Config:   cfg,
		},
	}
	if err := k8sClient.Create(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

func createPasswordSecret(t *testing.T, ns, password string) {
	t.Helper()
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "device-admin"},
		StringData: map[string]string{"password": password},
	}
	if err := k8sClient.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}

func TestEnforceCorrectsDrift(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev20", MAC: "AABBCCDDEE30", Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"eco_mode": false}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE30", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: boolPtr(true)},
	})

	r, rec := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee30")

	if got := fake.ConfigSnapshot()["sys"]["device"].(map[string]any)["eco_mode"]; got != true {
		t.Errorf("device eco_mode = %v, want true (enforced)", got)
	}
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition after enforce = %+v, want True", cond)
	}
	foundCorrected := false
	for len(rec.Events) > 0 {
		if e := <-rec.Events; strings.Contains(e, "DriftCorrected") {
			foundCorrected = true
		}
	}
	if !foundCorrected {
		t.Error("expected a DriftCorrected event")
	}
}

func TestEnforceObserveModeNeverWrites(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev21", MAC: "AABBCCDDEE31", Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"eco_mode": false}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE31", hostOf(srv.URL), true, false, "")
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{ // observe mode
		System: &shellyv1alpha1.SystemSection{EcoMode: boolPtr(true)},
	})

	r, _ := newReconciler()
	_ = reconcile(t, r, ns, "aabbccddee31")
	for _, call := range fake.RecordedCalls() {
		if strings.Contains(call.Method, "Set") {
			t.Fatalf("observe mode must never write, saw %s", call.Method)
		}
	}
}

func TestEnforceAuthRolloutOrdersAuthLast(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev22", MAC: "AABBCCDDEE32", Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"eco_mode": false}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE32", hostOf(srv.URL), true, false, "")
	createPasswordSecret(t, ns, "hunter2")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: boolPtr(true)},
		Auth: &shellyv1alpha1.AuthSection{
			Enable:            boolPtr(true),
			PasswordSecretRef: &shellyv1alpha1.SecretKeyRef{Name: "device-admin", Key: "password"},
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee32")

	if !fake.AuthEnabled() {
		t.Fatal("device auth should be enabled")
	}
	calls := fake.RecordedCalls()
	sysIdx, authIdx := -1, -1
	for i, call := range calls {
		if call.Method == "Sys.SetConfig" && sysIdx == -1 {
			sysIdx = i
		}
		if call.Method == "Shelly.SetAuth" {
			authIdx = i
		}
	}
	if sysIdx == -1 || authIdx == -1 || sysIdx > authIdx {
		t.Errorf("auth must apply last: sys@%d auth@%d calls=%v", sysIdx, authIdx, calls)
	}
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition after auth rollout = %+v, want True", cond)
	}
}

func TestEnforceMissingPasswordSecret(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev23", MAC: "AABBCCDDEE33", Gen: 2}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE33", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Auth: &shellyv1alpha1.AuthSection{
			Enable:            boolPtr(true),
			PasswordSecretRef: &shellyv1alpha1.SecretKeyRef{Name: "nope", Key: "password"},
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee33")
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Reason != shellyv1alpha1.ReasonCredentialsError {
		t.Fatalf("condition = %+v, want CredentialsError", cond)
	}
	if fake.AuthEnabled() {
		t.Error("auth must not be enabled without a readable secret")
	}
}

func TestEnforceApplyFailureSurfaces(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev24", MAC: "AABBCCDDEE34", Gen: 2, SetConfigError: "rejected by device", InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"eco_mode": false}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE34", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: boolPtr(true)},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee34")
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != shellyv1alpha1.ReasonApplyFailed {
		t.Fatalf("condition = %+v, want False/ApplyFailed", cond)
	}
	if !strings.Contains(cond.Message, "rejected by device") {
		t.Errorf("message should carry the device error: %q", cond.Message)
	}
}

func TestEnforceRestartRequiredEvent(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev25", MAC: "AABBCCDDEE35", Gen: 2, RestartOnSetConfig: true, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"eco_mode": false}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE35", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: boolPtr(true)},
	})

	r, rec := newReconciler()
	_ = reconcile(t, r, ns, "aabbccddee35")
	foundRestart := false
	for len(rec.Events) > 0 {
		if e := <-rec.Events; strings.Contains(e, "RestartRequired") {
			foundRestart = true
		}
	}
	if !foundRestart {
		t.Error("expected a RestartRequired event")
	}
}

func TestAuthEnabledDeviceUsesProfilePassword(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev26", MAC: "AABBCCDDEE36", Gen: 2, Password: "hunter2", InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"eco_mode": true}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	dev := createDevice(t, ns, "AABBCCDDEE36", hostOf(srv.URL), true, false, "")
	dev.Status.AuthEnabled = true
	if err := k8sClient.Status().Update(context.Background(), dev); err != nil {
		t.Fatal(err)
	}
	createPasswordSecret(t, ns, "hunter2")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: boolPtr(true)},
		Auth: &shellyv1alpha1.AuthSection{
			Enable:            boolPtr(true),
			PasswordSecretRef: &shellyv1alpha1.SecretKeyRef{Name: "device-admin", Key: "password"},
		},
	})

	r, _ := newReconciler()
	got := reconcile(t, r, ns, "aabbccddee36")
	cond := meta.FindStatusCondition(got.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("authed device should be readable and in sync, condition = %+v", cond)
	}
}

func TestEnforceRecheckFailureIsNotApplyFailed(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev28", MAC: "AABBCCDDEE38", Gen: 2, GetConfigErrorAfter: 1, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"eco_mode": false}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE38", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: boolPtr(true)},
	})

	r, rec := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee38")

	// The write itself succeeded...
	if got := fake.ConfigSnapshot()["sys"]["device"].(map[string]any)["eco_mode"]; got != true {
		t.Errorf("device eco_mode = %v, want true (apply succeeded)", got)
	}
	// ...so the verification failure must NOT be reported as ApplyFailed.
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionUnknown || cond.Reason != shellyv1alpha1.ReasonConfigFetchFailed {
		t.Fatalf("condition = %+v, want Unknown/ConfigFetchFailed", cond)
	}
	foundCorrected := false
	for len(rec.Events) > 0 {
		if e := <-rec.Events; strings.Contains(e, "DriftCorrected") {
			foundCorrected = true
		}
	}
	if !foundCorrected {
		t.Error("DriftCorrected event must still fire for the successful writes")
	}
}

func TestEnforceAuthDisable(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev29", MAC: "AABBCCDDEE39", Gen: 2, Password: "hunter2"}
	srv := shellytest.New(fake)
	defer srv.Close()
	dev := createDevice(t, ns, "AABBCCDDEE39", hostOf(srv.URL), true, false, "")
	dev.Status.AuthEnabled = true
	if err := k8sClient.Status().Update(context.Background(), dev); err != nil {
		t.Fatal(err)
	}
	createPasswordSecret(t, ns, "hunter2")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Auth: &shellyv1alpha1.AuthSection{
			Enable:            boolPtr(false),
			PasswordSecretRef: &shellyv1alpha1.SecretKeyRef{Name: "device-admin", Key: "password"},
		},
	})

	r, _ := newReconciler()
	got := reconcile(t, r, ns, "aabbccddee39")
	if fake.AuthEnabled() {
		t.Fatal("device auth should be disabled")
	}
	cond := meta.FindStatusCondition(got.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition = %+v, want True after auth disable", cond)
	}
}

func TestWrongPasswordCannotSelfCorrect(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev30", MAC: "AABBCCDDEE3A", Gen: 2, Password: "right"}
	srv := shellytest.New(fake)
	defer srv.Close()
	dev := createDevice(t, ns, "AABBCCDDEE3A", hostOf(srv.URL), true, false, "")
	dev.Status.AuthEnabled = true
	if err := k8sClient.Status().Update(context.Background(), dev); err != nil {
		t.Fatal(err)
	}
	createPasswordSecret(t, ns, "wrong")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Auth: &shellyv1alpha1.AuthSection{
			Enable:            boolPtr(true),
			PasswordSecretRef: &shellyv1alpha1.SecretKeyRef{Name: "device-admin", Key: "password"},
		},
	})

	r, _ := newReconciler()
	got := reconcile(t, r, ns, "aabbccddee3a")
	cond := meta.FindStatusCondition(got.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Reason != shellyv1alpha1.ReasonAuthRequired {
		t.Fatalf("condition = %+v, want AuthRequired", cond)
	}
	if !strings.Contains(cond.Message, "rejected") {
		t.Errorf("message should say the password was rejected: %q", cond.Message)
	}
	for _, call := range fake.RecordedCalls() {
		if strings.Contains(call.Method, "Set") {
			t.Fatalf("locked-out device must not be written, saw %s", call.Method)
		}
	}
}

func TestEnforceNonConvergenceDamping(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev31", MAC: "AABBCCDDEE3B", Gen: 2, IgnoreSetConfig: true, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"eco_mode": false}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE3B", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: boolPtr(true)},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee3b")
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Reason != shellyv1alpha1.ReasonNotConverging {
		t.Fatalf("first cycle condition = %+v, want NotConverging", cond)
	}
	writesAfterFirst := 0
	for _, call := range fake.RecordedCalls() {
		if strings.Contains(call.Method, "SetConfig") {
			writesAfterFirst++
		}
	}

	// Second cycle: same stuck sections -> damped, no new writes.
	_ = reconcile(t, r, ns, "aabbccddee3b")
	writesAfterSecond := 0
	for _, call := range fake.RecordedCalls() {
		if strings.Contains(call.Method, "SetConfig") {
			writesAfterSecond++
		}
	}
	if writesAfterSecond != writesAfterFirst {
		t.Errorf("damped cycle must not rewrite: writes %d -> %d", writesAfterFirst, writesAfterSecond)
	}
}

func TestNameManagedButUnresolvableWarns(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev27", MAC: "AABBCCDDEE37", Gen: 2}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE37", hostOf(srv.URL), true, false, "")
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{ // observe; no name map CM, no displayName
		Name: &shellyv1alpha1.NameSection{Managed: true},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee37")
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || !strings.Contains(cond.Message, "unresolvable") {
		t.Errorf("expected unresolvable-name warning in message, got %+v", cond)
	}
}
