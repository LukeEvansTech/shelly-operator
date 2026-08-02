package controller

import (
	"context"
	"encoding/json"
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

// rpc method name constants used in multiple tests (satisfies goconst).
const (
	rpcSysSetConfig  = "Sys.SetConfig"
	rpcWifiSetConfig = "Wifi.SetConfig"
	sntpCloudflare   = "time.cloudflare.com"
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
		System: &shellyv1alpha1.SystemSection{EcoMode: new(true)}, // drifted
		Cloud:  &shellyv1alpha1.CloudSection{Enable: new(false)},  // in sync
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
		System: &shellyv1alpha1.SystemSection{EcoMode: new(true)},
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
		Auth: &shellyv1alpha1.AuthSection{Enable: new(true)},
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
		System: &shellyv1alpha1.SystemSection{EcoMode: new(true)},
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
		System: &shellyv1alpha1.SystemSection{EcoMode: new(true)},
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
		System: &shellyv1alpha1.SystemSection{EcoMode: new(true)},
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
		System: &shellyv1alpha1.SystemSection{EcoMode: new(true)},
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
		System: &shellyv1alpha1.SystemSection{EcoMode: new(true)},
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
		System: &shellyv1alpha1.SystemSection{EcoMode: new(true)},
		Auth: &shellyv1alpha1.AuthSection{
			Enable:            new(true),
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
		if call.Method == rpcSysSetConfig && sysIdx == -1 {
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
			Enable:            new(true),
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
		System: &shellyv1alpha1.SystemSection{EcoMode: new(true)},
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
		System: &shellyv1alpha1.SystemSection{EcoMode: new(true)},
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
		System: &shellyv1alpha1.SystemSection{EcoMode: new(true)},
		Auth: &shellyv1alpha1.AuthSection{
			Enable:            new(true),
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
		System: &shellyv1alpha1.SystemSection{EcoMode: new(true)},
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
			Enable:            new(false),
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
			Enable:            new(true),
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
		System: &shellyv1alpha1.SystemSection{EcoMode: new(true)},
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

//go:fix inline
func ptrInt32(v int32) *int32 { return new(v) }

func TestEnforceDampingRearmsOnValueChange(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev32", MAC: "AABBCCDDEE3C", Gen: 2, IgnoreSetConfig: true, InitialConfig: map[string]map[string]any{
		"switch:0": {"auto_off_delay": float64(0)},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE3C", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Switch: &shellyv1alpha1.SwitchSection{AutoOffDelay: ptrInt32(300)},
	})

	r, _ := newReconciler()
	_ = reconcile(t, r, ns, "aabbccddee3c") // first cycle: writes, NotConverging
	countWrites := func() int {
		n := 0
		for _, call := range fake.RecordedCalls() {
			if strings.Contains(call.Method, "SetConfig") {
				n++
			}
		}
		return n
	}
	w1 := countWrites()
	_ = reconcile(t, r, ns, "aabbccddee3c") // damped
	if countWrites() != w1 {
		t.Fatal("second cycle should be damped")
	}

	// Operator changes the desired value -> damper must re-arm.
	var p shellyv1alpha1.ShellyProfile
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "plugs"}, &p); err != nil {
		t.Fatal(err)
	}
	p.Spec.Config.Switch.AutoOffDelay = ptrInt32(600)
	if err := k8sClient.Update(context.Background(), &p); err != nil {
		t.Fatal(err)
	}
	_ = reconcile(t, r, ns, "aabbccddee3c")
	if countWrites() == w1 {
		t.Error("value change must re-arm enforcement (new write expected)")
	}
}

func TestEnforceSwitchConfig(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev33", MAC: "AABBCCDDEE3D", Gen: 2, InitialConfig: map[string]map[string]any{
		"switch:0": {"auto_off": false, "auto_off_delay": float64(0)},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE3D", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Switch: &shellyv1alpha1.SwitchSection{AutoOff: new(true), AutoOffDelay: ptrInt32(300)},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee3d")
	cfg := fake.ConfigSnapshot()["switch:0"]
	if cfg["auto_off"] != true || cfg["auto_off_delay"] != float64(300) {
		t.Errorf("switch:0 not enforced: %v", cfg)
	}
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition = %+v, want True", cond)
	}
	sawSwitchSet := false
	for _, call := range fake.RecordedCalls() {
		if call.Method == "Switch.SetConfig" {
			sawSwitchSet = true
		}
	}
	if !sawSwitchSet {
		t.Error("expected Switch.SetConfig call")
	}
}

func TestEnforceSwitchProtectionLimits(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev34", MAC: "AABBCCDDEE3E", Gen: 2, InitialConfig: map[string]map[string]any{
		"switch:0": {"voltage_limit": float64(0), "current_limit": float64(0), "autorecover_voltage_errors": false},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE3E", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Switch: &shellyv1alpha1.SwitchSection{
			VoltageLimit:             ptrInt32(260),
			CurrentLimit:             ptrInt32(16),
			AutorecoverVoltageErrors: new(true),
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee3e")
	cfg := fake.ConfigSnapshot()["switch:0"]
	if cfg["voltage_limit"] != float64(260) || cfg["current_limit"] != float64(16) || cfg["autorecover_voltage_errors"] != true {
		t.Errorf("switch:0 protection limits not enforced: %v", cfg)
	}
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition = %+v, want True", cond)
	}
}

func createWifiSecret(t *testing.T, ns string) {
	t.Helper()
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "wifi-creds"},
		StringData: map[string]string{"new": "hunter2"},
	}
	if err := k8sClient.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}

func TestEnforceWifiAppliedLastWithPasswordInjected(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev40", MAC: "AABBCCDDEE40", Gen: 2, InitialConfig: map[string]map[string]any{
		"wifi":  {"sta": map[string]any{"ssid": "iot-old", "enable": true}},
		"cloud": {"enable": true},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE40", hostOf(srv.URL), true, false, "")
	createWifiSecret(t, ns)
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Cloud: &shellyv1alpha1.CloudSection{Enable: new(false)},
		Wifi: &shellyv1alpha1.WifiSection{
			Sta: &shellyv1alpha1.WifiNetwork{
				Enable:        new(true),
				SSID:          "iot-new",
				PassSecretRef: &shellyv1alpha1.SecretKeyRef{Name: "wifi-creds", Key: "new"},
			},
			Sta1: &shellyv1alpha1.WifiNetwork{Enable: new(true), SSID: "iot-old"},
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee40")
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition after wifi enforce = %+v, want True", cond)
	}

	cloudIdx, wifiIdx := -1, -1
	var wifiParams json.RawMessage
	for i, call := range fake.RecordedCalls() {
		if call.Method == "Cloud.SetConfig" && cloudIdx == -1 {
			cloudIdx = i
		}
		if call.Method == rpcWifiSetConfig {
			wifiIdx = i
			wifiParams = call.Params
		}
	}
	if cloudIdx == -1 || wifiIdx == -1 || cloudIdx > wifiIdx {
		t.Fatalf("wifi must apply dead last: cloud@%d wifi@%d", cloudIdx, wifiIdx)
	}
	var p struct {
		Config map[string]map[string]any `json:"config"`
	}
	if err := json.Unmarshal(wifiParams, &p); err != nil {
		t.Fatal(err)
	}
	sta := p.Config["sta"]
	if sta["ssid"] != "iot-new" || sta["pass"] != "hunter2" {
		t.Errorf("sta payload = %v, want ssid iot-new with pass hunter2", sta)
	}
	sta1, ok := p.Config["sta1"]
	if !ok {
		t.Fatal("sta1 must be present in the wifi payload")
	}
	if _, hasPass := sta1["pass"]; hasPass {
		t.Errorf("sta1 has no passSecretRef, payload must not carry a pass key: %v", sta1)
	}
}

func TestEnforceWifiDeviceVanishesAfterApply(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev41", MAC: "AABBCCDDEE41", Gen: 2, GetConfigErrorAfter: 1, InitialConfig: map[string]map[string]any{
		"wifi": {"sta": map[string]any{"ssid": "iot-old", "enable": true}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE41", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Wifi: &shellyv1alpha1.WifiSection{
			Sta:  &shellyv1alpha1.WifiNetwork{Enable: new(true), SSID: "iot-new"},
			Sta1: &shellyv1alpha1.WifiNetwork{Enable: new(true), SSID: "iot-old"},
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee41")
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionUnknown || cond.Reason != shellyv1alpha1.ReasonWifiApplied {
		t.Fatalf("condition = %+v, want Unknown/WifiApplied", cond)
	}
	if !strings.Contains(cond.Message, "may have moved networks") {
		t.Errorf("message should explain the migration outcome: %q", cond.Message)
	}
}

func TestEnforceWifiMissingSecretIsCredentialsError(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev42", MAC: "AABBCCDDEE42", Gen: 2, InitialConfig: map[string]map[string]any{
		"wifi": {"sta": map[string]any{"ssid": "iot-old", "enable": true}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE42", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Wifi: &shellyv1alpha1.WifiSection{
			Sta: &shellyv1alpha1.WifiNetwork{
				Enable:        new(true),
				SSID:          "iot-new",
				PassSecretRef: &shellyv1alpha1.SecretKeyRef{Name: "nope", Key: "new"},
			},
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee42")
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionUnknown || cond.Reason != shellyv1alpha1.ReasonCredentialsError {
		t.Fatalf("condition = %+v, want Unknown/CredentialsError", cond)
	}
	if !strings.Contains(cond.Message, "wifi sta password") {
		t.Errorf("message should name the unreadable credential: %q", cond.Message)
	}
	for _, call := range fake.RecordedCalls() {
		if strings.Contains(call.Method, "Set") {
			t.Fatalf("unresolvable wifi secret must block all writes, saw %s", call.Method)
		}
	}
}

func TestWifiStaWithoutSta1Warns(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev43", MAC: "AABBCCDDEE43", Gen: 2, InitialConfig: map[string]map[string]any{
		"wifi": {"sta": map[string]any{"ssid": "iot-old"}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE43", hostOf(srv.URL), true, false, "")
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{ // observe; matches the device -> no drift
		Wifi: &shellyv1alpha1.WifiSection{
			Sta: &shellyv1alpha1.WifiNetwork{SSID: "iot-old"},
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee43")
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition = %+v, want True (no drift)", cond)
	}
	if !strings.Contains(cond.Message, "wifi.sta is managed without a wifi.sta1 fallback") {
		t.Errorf("expected sta1 fallback warning in message: %q", cond.Message)
	}
}

func TestObserveNeverWritesWifi(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev44", MAC: "AABBCCDDEE44", Gen: 2, InitialConfig: map[string]map[string]any{
		"wifi": {"sta": map[string]any{"ssid": "iot-old", "enable": true}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE44", hostOf(srv.URL), true, false, "")
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{ // observe; wifi drifted
		Wifi: &shellyv1alpha1.WifiSection{
			Sta:  &shellyv1alpha1.WifiNetwork{Enable: new(true), SSID: "iot-new"},
			Sta1: &shellyv1alpha1.WifiNetwork{Enable: new(true), SSID: "iot-old"},
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee44")
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != shellyv1alpha1.ReasonDrifted {
		t.Fatalf("condition = %+v, want False/Drifted", cond)
	}
	found := false
	for _, s := range dev.Status.DriftedSections {
		if s == "wifi" {
			found = true
		}
	}
	if !found {
		t.Errorf("driftedSections = %v, want wifi listed", dev.Status.DriftedSections)
	}
	for _, call := range fake.RecordedCalls() {
		if call.Method == rpcWifiSetConfig {
			t.Fatal("observe mode must never write wifi")
		}
	}
}

func TestWifiStaDeclaredDisabledWarns(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev45", MAC: "AABBCCDDEE45", Gen: 2, InitialConfig: map[string]map[string]any{
		"wifi": {"sta": map[string]any{"ssid": "iot-old", "enable": false}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE45", hostOf(srv.URL), true, false, "")
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{ // observe; matches the device -> no drift
		Wifi: &shellyv1alpha1.WifiSection{
			Sta: &shellyv1alpha1.WifiNetwork{Enable: new(false), SSID: "iot-old"},
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee45")
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition = %+v, want True (no drift)", cond)
	}
	if !strings.Contains(cond.Message, "wifi.sta is declared disabled") {
		t.Errorf("expected sta-disabled warning in message: %q", cond.Message)
	}
}

func TestProfileRejectsSharedStaSta1SSID(t *testing.T) {
	ns := newNamespace(t)
	ctx := context.Background()
	dup := &shellyv1alpha1.ShellyProfile{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "dup-ssid"},
		Spec: shellyv1alpha1.ShellyProfileSpec{
			Mode: shellyv1alpha1.ModeObserve,
			Config: shellyv1alpha1.ProfileConfig{
				Wifi: &shellyv1alpha1.WifiSection{
					Sta:  &shellyv1alpha1.WifiNetwork{SSID: "same-net"},
					Sta1: &shellyv1alpha1.WifiNetwork{SSID: "same-net"},
				},
			},
		},
	}
	err := k8sClient.Create(ctx, dup)
	if err == nil {
		t.Fatal("expected create to be rejected when sta and sta1 share an ssid")
	}
	if !strings.Contains(err.Error(), "must not declare the same ssid") {
		t.Errorf("error = %v, want message about shared ssid", err)
	}

	ok := &shellyv1alpha1.ShellyProfile{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "distinct-ssid"},
		Spec: shellyv1alpha1.ShellyProfileSpec{
			Mode: shellyv1alpha1.ModeObserve,
			Config: shellyv1alpha1.ProfileConfig{
				Wifi: &shellyv1alpha1.WifiSection{
					Sta:  &shellyv1alpha1.WifiNetwork{SSID: "iot-new"},
					Sta1: &shellyv1alpha1.WifiNetwork{SSID: "iot-old"},
				},
			},
		},
	}
	if err := k8sClient.Create(ctx, ok); err != nil {
		t.Fatalf("profile with distinct ssids should be accepted: %v", err)
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

func TestFirmwareCompliantAppJobNoWrites(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "devfw1", MAC: "AABBCCDDEF01", Gen: 2,
		InitialConfig: map[string]map[string]any{"sys": {"device": map[string]any{}}},
		InitialSchedules: []map[string]any{
			{"enable": true, "timespec": "0 0 0 * * SUN,MON,TUE,WED,THU,FRI,SAT",
				"calls": []any{map[string]any{"method": "Shelly.Update", "params": map[string]any{"stage": "stable"}, "origin": "shelly_service"}}},
		}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEF01", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Firmware: &shellyv1alpha1.FirmwareSection{AutoUpdate: new(true)},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddef01")

	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition = %+v", cond)
	}
	// The pre-existing app job must be untouched: no Create/Delete calls.
	for _, call := range fake.RecordedCalls() {
		if call.Method == "Schedule.Create" || call.Method == "Schedule.Delete" {
			t.Fatalf("unexpected schedule write: %s", call.Method)
		}
	}
	if jobs := fake.ScheduleSnapshot(); len(jobs) != 1 {
		t.Fatalf("jobs = %+v", jobs)
	}
}

func TestFirmwareEnforceCreatesJob(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "devfw2", MAC: "AABBCCDDEF02", Gen: 2,
		InitialConfig: map[string]map[string]any{"sys": {"device": map[string]any{}}}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEF02", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Firmware: &shellyv1alpha1.FirmwareSection{AutoUpdate: new(true)},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddef02")

	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != shellyv1alpha1.ReasonInSync {
		t.Fatalf("condition = %+v", cond)
	}
	jobs := fake.ScheduleSnapshot()
	if len(jobs) != 1 {
		t.Fatalf("jobs = %+v", jobs)
	}
	if jobs[0]["timespec"] != "0 0 0 * * SUN,MON,TUE,WED,THU,FRI,SAT" || jobs[0]["enable"] != true {
		t.Fatalf("created job = %+v", jobs[0])
	}
}

func TestFirmwareEnforceDeletesBetaJob(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "devfw3", MAC: "AABBCCDDEF03", Gen: 2,
		InitialConfig: map[string]map[string]any{"sys": {"device": map[string]any{}}},
		InitialSchedules: []map[string]any{
			{"enable": true, "timespec": "@daily",
				"calls": []any{map[string]any{"method": "Shelly.Update", "params": map[string]any{"stage": "beta"}}}},
			{"enable": true, "timespec": "@sunset",
				"calls": []any{map[string]any{"method": "Switch.Set", "params": map[string]any{"id": 0, "on": true}}}},
		}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEF03", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Firmware: &shellyv1alpha1.FirmwareSection{AutoUpdate: new(true)},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddef03")

	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition = %+v", cond)
	}
	// Beta job deleted, unrelated Switch.Set job untouched, stable job created.
	jobs := fake.ScheduleSnapshot()
	if len(jobs) != 2 {
		t.Fatalf("jobs = %+v", jobs)
	}
	methods := map[string]bool{}
	for _, j := range jobs {
		calls := j["calls"].([]any)
		m := calls[0].(map[string]any)["method"].(string)
		methods[m] = true
		if m == "Shelly.Update" {
			if calls[0].(map[string]any)["params"].(map[string]any)["stage"] != "stable" {
				t.Fatalf("surviving update job = %+v", j)
			}
		}
	}
	if !methods["Switch.Set"] || !methods["Shelly.Update"] {
		t.Fatalf("jobs = %+v", jobs)
	}
}

func TestFirmwareEnforceDisableDeletesJobs(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "devfw4", MAC: "AABBCCDDEF04", Gen: 2,
		InitialConfig: map[string]map[string]any{"sys": {"device": map[string]any{}}},
		InitialSchedules: []map[string]any{
			{"enable": true, "timespec": "0 0 0 * * SUN,MON,TUE,WED,THU,FRI,SAT",
				"calls": []any{map[string]any{"method": "Shelly.Update", "params": map[string]any{"stage": "stable"}}}},
		}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEF04", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Firmware: &shellyv1alpha1.FirmwareSection{AutoUpdate: new(false)},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddef04")

	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition = %+v", cond)
	}
	if jobs := fake.ScheduleSnapshot(); len(jobs) != 0 {
		t.Fatalf("jobs = %+v", jobs)
	}
}

func TestFirmwareObserveModeReportsDriftWithoutWrites(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "devfw5", MAC: "AABBCCDDEF05", Gen: 2,
		InitialConfig: map[string]map[string]any{"sys": {"device": map[string]any{}}}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEF05", hostOf(srv.URL), true, false, "")
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{ // observe mode
		Firmware: &shellyv1alpha1.FirmwareSection{AutoUpdate: new(true)},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddef05")

	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != shellyv1alpha1.ReasonDrifted {
		t.Fatalf("condition = %+v", cond)
	}
	if len(dev.Status.DriftedSections) != 1 || dev.Status.DriftedSections[0] != "firmware" {
		t.Fatalf("driftedSections = %v", dev.Status.DriftedSections)
	}
	if jobs := fake.ScheduleSnapshot(); len(jobs) != 0 {
		t.Fatalf("observe mode must not write; jobs = %+v", jobs)
	}
}

func TestFirmwareUnmanagedSkipsScheduleRPC(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "devfw6", MAC: "AABBCCDDEF06", Gen: 2,
		InitialConfig: map[string]map[string]any{"cloud": {"enable": false}}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEF06", hostOf(srv.URL), true, false, "")
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Cloud: &shellyv1alpha1.CloudSection{Enable: new(false)},
	})

	r, _ := newReconciler()
	reconcile(t, r, ns, "aabbccddef06")

	for _, call := range fake.RecordedCalls() {
		if call.Method == "Schedule.List" {
			t.Fatal("Schedule.List called for a profile that does not manage firmware")
		}
	}
}

func TestEnforceBLEEnable(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "devble1", MAC: "AABBCCDDF001", Gen: 2, InitialConfig: map[string]map[string]any{
		"ble": {"enable": false},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDF001", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		BLE: &shellyv1alpha1.BLESection{Enable: new(true)},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddf001")

	if got := fake.ConfigSnapshot()["ble"]["enable"]; got != true {
		t.Errorf("device ble.enable = %v, want true (enforced)", got)
	}
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition after BLE enforce = %+v, want True", cond)
	}
	sawBLESet := false
	for _, call := range fake.RecordedCalls() {
		if call.Method == "BLE.SetConfig" {
			sawBLESet = true
		}
	}
	if !sawBLESet {
		t.Error("expected BLE.SetConfig call")
	}
}

func TestEnforceSysTimezone(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "devtz1", MAC: "AABBCCDDF002", Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {
			"device":   map[string]any{"eco_mode": false},
			"location": map[string]any{"tz": "UTC"},
		},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDF002", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{Timezone: new("Europe/London")},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddf002")

	snap := fake.ConfigSnapshot()["sys"]
	loc, ok := snap["location"].(map[string]any)
	if !ok || loc["tz"] != "Europe/London" {
		t.Errorf("sys.location = %#v, want tz=Europe/London", snap["location"])
	}
	// eco_mode untouched (not declared in profile).
	dev2Device, ok2 := snap["device"].(map[string]any)
	if !ok2 || dev2Device["eco_mode"] != false {
		t.Errorf("sys.device should be untouched, got %#v", snap["device"])
	}
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition after timezone enforce = %+v, want True", cond)
	}
	sawSysSet := false
	for _, call := range fake.RecordedCalls() {
		if call.Method == rpcSysSetConfig {
			sawSysSet = true
		}
	}
	if !sawSysSet {
		t.Error("expected Sys.SetConfig call for timezone")
	}
}

// ---- UI section envtests ---------------------------------------------------

// plugUIInitialConfig returns an InitialConfig for a PlusPlugUK device with
// a pluguk_ui component in its starting state.
func plugUIInitialConfig(ledMode, inMode string) map[string]map[string]any {
	return map[string]map[string]any{
		"sys":      {"device": map[string]any{"eco_mode": false}},
		"switch:0": {"auto_off": false},
		"pluguk_ui": {
			"leds": map[string]any{
				"mode": ledMode,
				"night_mode": map[string]any{
					"enable":         false,
					"brightness":     float64(100),
					"active_between": []any{"22:00", "07:00"},
				},
			},
			"controls": map[string]any{
				"switch:0": map[string]any{"in_mode": inMode},
			},
		},
	}
}

func TestEnforceUILEDModeAndButton(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{
		ID: "devui1", MAC: "AABBCCDDEE50", Gen: 2,
		InitialConfig: plugUIInitialConfig("power", "momentary"),
	}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE50", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		UI: &shellyv1alpha1.UISection{
			LEDMode:      new("off"),
			ButtonInMode: new("detached"),
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee50")

	snap := fake.ConfigSnapshot()["pluguk_ui"]
	leds, ok := snap["leds"].(map[string]any)
	if !ok || leds["mode"] != "off" {
		t.Errorf("pluguk_ui.leds.mode = %v, want off", leds["mode"])
	}
	controls, ok2 := snap["controls"].(map[string]any)
	if !ok2 {
		t.Fatalf("controls not a map: %#v", snap["controls"])
	}
	sw0, _ := controls["switch:0"].(map[string]any)
	if sw0["in_mode"] != "detached" {
		t.Errorf("controls.switch:0.in_mode = %v, want detached", sw0["in_mode"])
	}
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition after UI enforce = %+v, want True", cond)
	}
	// Verify the RPC was PLUGUK_UI.SetConfig (uppercase fallback path).
	sawUISet := false
	for _, call := range fake.RecordedCalls() {
		if strings.EqualFold(call.Method, "pluguk_ui.SetConfig") {
			sawUISet = true
		}
	}
	if !sawUISet {
		calls := fake.RecordedCalls()
		t.Errorf("expected PLUGUK_UI.SetConfig call, saw %v", calls)
	}
}

func TestEnforceUINightMode(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{
		ID: "devui2", MAC: "AABBCCDDEE51", Gen: 2,
		InitialConfig: plugUIInitialConfig("power", "momentary"),
	}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE51", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		UI: &shellyv1alpha1.UISection{
			NightMode: &shellyv1alpha1.NightMode{
				Enable:        new(true),
				Brightness:    ptrInt32(50),
				ActiveBetween: []string{"23:00", "06:00"},
			},
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee51")

	snap := fake.ConfigSnapshot()["pluguk_ui"]
	leds := snap["leds"].(map[string]any)
	nm := leds["night_mode"].(map[string]any)
	if nm["enable"] != true {
		t.Errorf("night_mode.enable = %v, want true", nm["enable"])
	}
	if nm["brightness"] != float64(50) {
		t.Errorf("night_mode.brightness = %v, want 50", nm["brightness"])
	}
	ab, ok := nm["active_between"].([]any)
	if !ok || len(ab) != 2 || ab[0] != "23:00" || ab[1] != "06:00" {
		t.Errorf("active_between = %v, want [23:00 06:00]", nm["active_between"])
	}
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition after night mode enforce = %+v, want True", cond)
	}
}

func TestEnforceUIAlreadyInSync(t *testing.T) {
	ns := newNamespace(t)
	// Device already matches the desired LED mode -- no writes expected.
	fake := &shellytest.Device{
		ID: "devui3", MAC: "AABBCCDDEE52", Gen: 2,
		InitialConfig: plugUIInitialConfig("off", "momentary"),
	}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE52", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		UI: &shellyv1alpha1.UISection{LEDMode: new("off")},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee52")
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("in-sync device should not drift, condition = %+v", cond)
	}
	for _, call := range fake.RecordedCalls() {
		if strings.Contains(strings.ToUpper(call.Method), "PLUGUK_UI") {
			t.Errorf("in-sync device must not receive UI writes, saw %s", call.Method)
		}
	}
}

func TestEnforceUIRelayDeviceNoOp(t *testing.T) {
	ns := newNamespace(t)
	// Relay device: no *_ui key in config. UI section must be a no-op.
	fake := &shellytest.Device{
		ID: "devui4", MAC: "AABBCCDDEE53", Gen: 2,
		InitialConfig: map[string]map[string]any{
			"sys":      {"device": map[string]any{"eco_mode": false}},
			"switch:0": {},
			"switch:1": {},
			// no *_ui component
		},
	}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE53", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		UI: &shellyv1alpha1.UISection{LEDMode: new("power")},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee53")
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("relay with UI profile must be InSync (no-op), condition = %+v", cond)
	}
	for _, call := range fake.RecordedCalls() {
		if strings.HasSuffix(strings.ToLower(call.Method), "_ui.setconfig") {
			t.Errorf("relay device must not receive *_ui writes, saw %s", call.Method)
		}
	}
}

func TestEnforceSysTimezoneAndEcoModeTogether(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "devtz2", MAC: "AABBCCDDF003", Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {
			"device":   map[string]any{"eco_mode": false},
			"location": map[string]any{"tz": "UTC"},
		},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDF003", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{
			EcoMode:  new(true),
			Timezone: new("America/New_York"),
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddf003")

	snap := fake.ConfigSnapshot()["sys"]
	device, okD := snap["device"].(map[string]any)
	location, okL := snap["location"].(map[string]any)
	if !okD || device["eco_mode"] != true {
		t.Errorf("sys.device.eco_mode = %v, want true", snap["device"])
	}
	if !okL || location["tz"] != "America/New_York" {
		t.Errorf("sys.location.tz = %v, want America/New_York", snap["location"])
	}
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition = %+v, want True", cond)
	}
}

// ---- Feature A: SNTPServer, Discoverable, Latitude, Longitude envtests ------

func TestEnforceSysSNTPServer(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "devsys1", MAC: "AABBCCDDE001", Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {
			"device": map[string]any{"eco_mode": false},
			"sntp":   map[string]any{"server": "pool.ntp.org"},
		},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDE001", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{SNTPServer: new(sntpCloudflare)},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccdde001")

	snap := fake.ConfigSnapshot()["sys"]
	sntp, ok := snap["sntp"].(map[string]any)
	if !ok || sntp["server"] != sntpCloudflare {
		t.Errorf("sys.sntp.server = %v, want %s", snap["sntp"], sntpCloudflare)
	}
	// eco_mode must be untouched (not in profile).
	device, okD := snap["device"].(map[string]any)
	if !okD || device["eco_mode"] != false {
		t.Errorf("sys.device.eco_mode should be untouched, got %#v", snap["device"])
	}
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition after sntp enforce = %+v, want True", cond)
	}
	sawSysSet := false
	for _, call := range fake.RecordedCalls() {
		if call.Method == rpcSysSetConfig {
			sawSysSet = true
		}
	}
	if !sawSysSet {
		t.Error("expected Sys.SetConfig call")
	}
}

func TestEnforceSysDiscoverable(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "devsys2", MAC: "AABBCCDDE002", Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {
			"device": map[string]any{"eco_mode": false, "discoverable": true},
		},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDE002", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{Discoverable: new(false)},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccdde002")

	snap := fake.ConfigSnapshot()["sys"]
	device, ok := snap["device"].(map[string]any)
	if !ok || device["discoverable"] != false {
		t.Errorf("sys.device.discoverable = %v, want false (enforced)", snap["device"])
	}
	// eco_mode must be untouched.
	if device["eco_mode"] != false {
		t.Errorf("sys.device.eco_mode should be untouched, got %v", device["eco_mode"])
	}
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition after discoverable enforce = %+v, want True", cond)
	}
}

func TestEnforceSysLatLon(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "devsys3", MAC: "AABBCCDDE003", Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {
			"location": map[string]any{"tz": "UTC", "lat": float64(0), "lon": float64(0)},
		},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDE003", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{
			Latitude:  new("51.5074"),
			Longitude: new("-0.1278"),
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccdde003")

	snap := fake.ConfigSnapshot()["sys"]
	location, ok := snap["location"].(map[string]any)
	if !ok {
		t.Fatalf("sys.location not a map: %#v", snap["location"])
	}
	if location["lat"] != 51.5074 {
		t.Errorf("sys.location.lat = %v, want 51.5074", location["lat"])
	}
	if location["lon"] != -0.1278 {
		t.Errorf("sys.location.lon = %v, want -0.1278", location["lon"])
	}
	// tz must be untouched.
	if location["tz"] != "UTC" {
		t.Errorf("sys.location.tz should be untouched, got %v", location["tz"])
	}
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition after lat/lon enforce = %+v, want True", cond)
	}
}

func TestEnforceSysAllNewLeaves(t *testing.T) {
	// All new sys leaves together: sntp, discoverable, lat, lon alongside
	// existing eco_mode and timezone -- all must be applied; nothing clobbered.
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "devsys4", MAC: "AABBCCDDE004", Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {
			"device":   map[string]any{"eco_mode": false, "discoverable": true},
			"location": map[string]any{"tz": "UTC", "lat": float64(0), "lon": float64(0)},
			"sntp":     map[string]any{"server": "pool.ntp.org"},
		},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDE004", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{
			EcoMode:      new(true),
			Discoverable: new(false),
			Timezone:     new("Europe/London"),
			Latitude:     new("51.5074"),
			Longitude:    new("-0.1278"),
			SNTPServer:   new(sntpCloudflare),
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccdde004")

	snap := fake.ConfigSnapshot()["sys"]
	device, _ := snap["device"].(map[string]any)
	location, _ := snap["location"].(map[string]any)
	sntp, _ := snap["sntp"].(map[string]any)

	if device["eco_mode"] != true {
		t.Errorf("eco_mode = %v, want true", device["eco_mode"])
	}
	if device["discoverable"] != false {
		t.Errorf("discoverable = %v, want false", device["discoverable"])
	}
	if location["tz"] != "Europe/London" {
		t.Errorf("tz = %v, want Europe/London", location["tz"])
	}
	if location["lat"] != 51.5074 {
		t.Errorf("lat = %v, want 51.5074", location["lat"])
	}
	if location["lon"] != -0.1278 {
		t.Errorf("lon = %v, want -0.1278", location["lon"])
	}
	if sntp["server"] != sntpCloudflare {
		t.Errorf("sntp.server = %v, want %s", sntp["server"], sntpCloudflare)
	}

	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition after full sys enforce = %+v, want True", cond)
	}
}

// ---- Feature B: WifiAP and WifiRoam envtests ---------------------------------

func TestEnforceWifiAP(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "devwifi1", MAC: "AABBCCDDE010", Gen: 2, InitialConfig: map[string]map[string]any{
		"wifi": {
			"ap": map[string]any{"enable": true, "range_extender": map[string]any{"enable": false}},
		},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDE010", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Wifi: &shellyv1alpha1.WifiSection{
			AP: &shellyv1alpha1.WifiAP{Enable: new(false), RangeExtender: new(false)},
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccdde010")

	wifiSnap := fake.ConfigSnapshot()["wifi"]
	ap, ok := wifiSnap["ap"].(map[string]any)
	if !ok {
		t.Fatalf("wifi.ap not a map: %#v", wifiSnap["ap"])
	}
	if ap["enable"] != false {
		t.Errorf("wifi.ap.enable = %v, want false (enforced)", ap["enable"])
	}
	re, ok2 := ap["range_extender"].(map[string]any)
	if !ok2 || re["enable"] != false {
		t.Errorf("wifi.ap.range_extender.enable = %#v, want false", ap["range_extender"])
	}
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition after wifi AP enforce = %+v, want True", cond)
	}
	sawWifiSet := false
	for _, call := range fake.RecordedCalls() {
		if call.Method == rpcWifiSetConfig {
			sawWifiSet = true
		}
	}
	if !sawWifiSet {
		t.Error("expected Wifi.SetConfig call")
	}
}

func TestEnforceWifiRoam(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "devwifi2", MAC: "AABBCCDDE011", Gen: 2, InitialConfig: map[string]map[string]any{
		"wifi": {
			"roam": map[string]any{"rssi_thr": float64(-70), "interval": float64(0)},
		},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDE011", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Wifi: &shellyv1alpha1.WifiSection{
			Roam: &shellyv1alpha1.WifiRoam{
				RSSIThreshold: ptrInt32(-80),
				Interval:      ptrInt32(60),
			},
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccdde011")

	wifiSnap := fake.ConfigSnapshot()["wifi"]
	roam, ok := wifiSnap["roam"].(map[string]any)
	if !ok {
		t.Fatalf("wifi.roam not a map: %#v", wifiSnap["roam"])
	}
	if roam["rssi_thr"] != float64(-80) {
		t.Errorf("wifi.roam.rssi_thr = %v, want -80", roam["rssi_thr"])
	}
	if roam["interval"] != float64(60) {
		t.Errorf("wifi.roam.interval = %v, want 60", roam["interval"])
	}
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition after wifi roam enforce = %+v, want True", cond)
	}
}

func TestEnforceWifiAPAndRoamInSync(t *testing.T) {
	// When AP and Roam already match the profile, no writes should occur.
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "devwifi3", MAC: "AABBCCDDE012", Gen: 2, InitialConfig: map[string]map[string]any{
		"wifi": {
			"ap":   map[string]any{"enable": false},
			"roam": map[string]any{"rssi_thr": float64(-80), "interval": float64(60)},
		},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDE012", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Wifi: &shellyv1alpha1.WifiSection{
			AP:   &shellyv1alpha1.WifiAP{Enable: new(false)},
			Roam: &shellyv1alpha1.WifiRoam{RSSIThreshold: ptrInt32(-80), Interval: ptrInt32(60)},
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccdde012")

	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("in-sync wifi AP/Roam should report True, got %+v", cond)
	}
	for _, call := range fake.RecordedCalls() {
		if call.Method == rpcWifiSetConfig {
			t.Errorf("in-sync device must not receive Wifi.SetConfig, saw %s", call.Method)
		}
	}
}

func TestEnforceWifiAPRoamDoesNotInterfereWithStaAppliedLogic(t *testing.T) {
	// AP/Roam drift alone must NOT trigger the WifiApplied recheck path --
	// only sta SSID/enable changes do. Enforce AP drift and verify we get
	// InSync=True (not WifiApplied/Unknown).
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "devwifi4", MAC: "AABBCCDDE013", Gen: 2, InitialConfig: map[string]map[string]any{
		"wifi": {
			"sta": map[string]any{"ssid": "iot", "enable": true},
			"ap":  map[string]any{"enable": true},
		},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDE013", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Wifi: &shellyv1alpha1.WifiSection{
			Sta: &shellyv1alpha1.WifiNetwork{Enable: new(true), SSID: "iot"},
			AP:  &shellyv1alpha1.WifiAP{Enable: new(false)},
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccdde013")

	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("AP-only wifi drift should converge to InSync=True, got %+v", cond)
	}
	// AP must have been updated.
	wifiSnap := fake.ConfigSnapshot()["wifi"]
	ap, _ := wifiSnap["ap"].(map[string]any)
	if ap["enable"] != false {
		t.Errorf("wifi.ap.enable = %v, want false (enforced)", ap["enable"])
	}
}

// ---- Feature: WS (outbound WebSocket) section envtests -----------------------

func TestEnforceWSEnable(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "devws1", MAC: "AABBCCDDE020", Gen: 2, InitialConfig: map[string]map[string]any{
		"ws": {"enable": true},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDE020", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		WS: &shellyv1alpha1.WSSection{Enable: new(false)},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccdde020")

	if got := fake.ConfigSnapshot()["ws"]["enable"]; got != false {
		t.Errorf("device ws.enable = %v, want false (enforced)", got)
	}
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition after WS enforce = %+v, want True", cond)
	}
	sawWSSet := false
	for _, call := range fake.RecordedCalls() {
		if call.Method == "WS.SetConfig" {
			sawWSSet = true
		}
	}
	if !sawWSSet {
		t.Error("expected WS.SetConfig call")
	}
}

func TestEnforceWSAlreadyInSync(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "devws2", MAC: "AABBCCDDE021", Gen: 2, InitialConfig: map[string]map[string]any{
		"ws": {"enable": false},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDE021", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		WS: &shellyv1alpha1.WSSection{Enable: new(false)},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccdde021")

	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("in-sync ws should report True, got %+v", cond)
	}
	for _, call := range fake.RecordedCalls() {
		if call.Method == "WS.SetConfig" {
			t.Errorf("in-sync ws must not receive WS.SetConfig, saw %s", call.Method)
		}
	}
}

// ---- Feature: sys.debug.level envtests ---------------------------------------

func TestEnforceSysDebugLevel(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "devdbg1", MAC: "AABBCCDDE030", Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {
			"device": map[string]any{"eco_mode": false},
			"debug":  map[string]any{"level": float64(3)},
		},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDE030", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{DebugLevel: ptrInt32(0)},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccdde030")

	snap := fake.ConfigSnapshot()["sys"]
	debug, ok := snap["debug"].(map[string]any)
	if !ok || debug["level"] != float64(0) {
		t.Errorf("sys.debug.level = %v, want 0 (enforced)", snap["debug"])
	}
	// eco_mode (device sub-map) must be untouched.
	device, okD := snap["device"].(map[string]any)
	if !okD || device["eco_mode"] != false {
		t.Errorf("sys.device.eco_mode should be untouched, got %#v", snap["device"])
	}
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition after debug.level enforce = %+v, want True", cond)
	}
	sawSysSet := false
	for _, call := range fake.RecordedCalls() {
		if call.Method == rpcSysSetConfig {
			sawSysSet = true
		}
	}
	if !sawSysSet {
		t.Error("expected Sys.SetConfig call for debug.level")
	}
}

func TestEnforceSysDebugLevelDoesNotClobberSntp(t *testing.T) {
	// DebugLevel and SNTPServer both drifted: Sys.SetConfig must carry both
	// sub-maps; the device's deep-merge preserves the other sub-maps.
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "devdbg2", MAC: "AABBCCDDE031", Gen: 2, InitialConfig: map[string]map[string]any{
		"sys": {
			"debug": map[string]any{"level": float64(3)},
			"sntp":  map[string]any{"server": "pool.ntp.org"},
		},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDE031", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{
			DebugLevel: ptrInt32(0),
			SNTPServer: new(sntpCloudflare),
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccdde031")

	snap := fake.ConfigSnapshot()["sys"]
	debug, okG := snap["debug"].(map[string]any)
	sntp, okS := snap["sntp"].(map[string]any)
	if !okG || debug["level"] != float64(0) {
		t.Errorf("sys.debug.level = %v, want 0", snap["debug"])
	}
	if !okS || sntp["server"] != sntpCloudflare {
		t.Errorf("sys.sntp.server = %v, want %s", snap["sntp"], sntpCloudflare)
	}
	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition after debug+sntp enforce = %+v, want True", cond)
	}
}

// TestReconcileReusesDigestNonceAcrossReconciles pins the fix for the
// firmware 2.0.0 HTTP 429 storm. 2.0.0 keeps a 32-entry circular nonce
// buffer; when it is exhausted the device opens a 2s throttle window and
// answers new-nonce requests with 429 instead of 401. Building a fresh
// shelly.Client per reconcile threw away the cached digest challenge and
// forced a new nonce every cycle, so the operator sawed through the buffer
// and reported spurious ConfigFetchFailed/Unknown (surfacing as bogus
// "config drift" alerts). One nonce is good for ~30k requests or 1h, so the
// client -- and its digest state -- must survive across reconciles.
func TestReconcileReusesDigestNonceAcrossReconciles(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev40", MAC: "AABBCCDDEE40", Gen: 2, Password: "hunter2",
		InitialConfig: map[string]map[string]any{
			"sys": {"device": map[string]any{"eco_mode": true}},
		}}
	srv := shellytest.New(fake)
	defer srv.Close()
	dev := createDevice(t, ns, "AABBCCDDEE40", hostOf(srv.URL), true, false, "")
	dev.Status.AuthEnabled = true
	if err := k8sClient.Status().Update(context.Background(), dev); err != nil {
		t.Fatal(err)
	}
	createPasswordSecret(t, ns, "hunter2")
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: new(true)},
		Auth: &shellyv1alpha1.AuthSection{
			Enable:            new(true),
			PasswordSecretRef: &shellyv1alpha1.SecretKeyRef{Name: "device-admin", Key: "password"},
		},
	})

	r, _ := newReconciler()
	for i := 1; i <= 3; i++ {
		got := reconcile(t, r, ns, "aabbccddee40")
		cond := meta.FindStatusCondition(got.Status.Conditions, shellyv1alpha1.ConditionInSync)
		if cond == nil || cond.Status != metav1.ConditionTrue {
			t.Fatalf("reconcile %d: condition = %+v, want InSync=True", i, cond)
		}
	}

	if got := fake.Challenges(); got != 1 {
		t.Errorf("device issued %d digest challenges across 3 reconciles, want 1 "+
			"(the cached nonce must be reused; a fresh challenge per reconcile "+
			"exhausts firmware 2.0.0's nonce buffer and triggers HTTP 429)", got)
	}
}

// TestReconcileStampsAvailableFirmwareOnAuthedDevice pins pending-firmware
// visibility on an auth-enabled device. Sys.GetStatus goes through POST /rpc
// and so needs credentials; the discovery sweeper has none, so its
// unauthenticated read could only ever 401 -- minting a nonce it could not
// use and leaving status.availableFirmware empty forever (which pinned
// shelly_device_update_available at 0 and made ShellyDevicePendingFirmware
// undeployable in practice). The reconciler holds the resolved password and
// a cached client, so it refreshes the field on the nonce it already has.
func TestReconcileStampsAvailableFirmwareOnAuthedDevice(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev41", MAC: "AABBCCDDEE41", Gen: 2, Password: "hunter2",
		InitialConfig: map[string]map[string]any{
			"sys": {"device": map[string]any{"eco_mode": true}},
		},
		AvailableUpdates: map[string]any{"stable": map[string]any{"version": "2.0.0"}},
	}
	srv := shellytest.New(fake)
	defer srv.Close()
	dev := createDevice(t, ns, "AABBCCDDEE41", hostOf(srv.URL), true, false, "")
	dev.Status.AuthEnabled = true
	if err := k8sClient.Status().Update(context.Background(), dev); err != nil {
		t.Fatal(err)
	}
	createPasswordSecret(t, ns, "hunter2")
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: new(true)},
		Auth: &shellyv1alpha1.AuthSection{
			Enable:            new(true),
			PasswordSecretRef: &shellyv1alpha1.SecretKeyRef{Name: "device-admin", Key: "password"},
		},
	})

	r, _ := newReconciler()
	got := reconcile(t, r, ns, "aabbccddee41")

	if got.Status.AvailableFirmware != "2.0.0" {
		t.Errorf("availableFirmware = %q, want %q (the reconciler must read Sys.GetStatus "+
			"with the resolved password; the sweeper cannot)", got.Status.AvailableFirmware, "2.0.0")
	}
}

// createRebootProfile is createEnforceProfile with the opt-in reboot flag set.
func createRebootProfile(t *testing.T, ns string, mode string, reboot bool, cfg shellyv1alpha1.ProfileConfig) {
	t.Helper()
	p := &shellyv1alpha1.ShellyProfile{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "plugs"},
		Spec: shellyv1alpha1.ShellyProfileSpec{
			Selector:           &metav1.LabelSelector{MatchLabels: map[string]string{shellyv1alpha1.LabelApp: "PlusPlugUK"}},
			Mode:               mode,
			RebootWhenRequired: reboot,
			Config:             cfg,
		},
	}
	if err := k8sClient.Create(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

func rebootCalled(fake *shellytest.Device) bool {
	for _, c := range fake.RecordedCalls() {
		if c.Method == "Shelly.Reboot" {
			return true
		}
	}
	return false
}

// A device already carrying the standing restart_required flag must surface it
// on status, so the state outlives the transient RestartRequired Event. This is
// the whole point: without it the only signal expires within the hour.
func TestStampsRestartRequiredOnStatus(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev90", MAC: "AABBCCDDEE90", Gen: 2, RestartRequired: true, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"eco_mode": true}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE90", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: new(true)},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee90")
	if !dev.Status.RestartRequired {
		t.Error("status.restartRequired should mirror the device's standing flag")
	}
	// Nothing opted in, so the device must be left alone.
	if rebootCalled(fake) {
		t.Error("rebooted a device on a profile that never asked for it")
	}
}

// The flag must clear itself once the device stops reporting it, otherwise a
// stale true would alert forever after someone reboots by hand.
func TestRestartRequiredClearsWhenDeviceStopsReporting(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev91", MAC: "AABBCCDDEE91", Gen: 2, RestartRequired: false, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"eco_mode": true}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	d := createDevice(t, ns, "AABBCCDDEE91", hostOf(srv.URL), true, false, "")
	d.Status.RestartRequired = true
	if err := k8sClient.Status().Update(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: new(true)},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee91")
	if dev.Status.RestartRequired {
		t.Error("status.restartRequired should clear once the device stops reporting it")
	}
}

// Opted in + enforce + device asking: reboot, and the device's own flag goes false.
func TestRebootWhenRequiredOptedIn(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev92", MAC: "AABBCCDDEE92", Gen: 2, RestartRequired: true, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"eco_mode": true}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE92", hostOf(srv.URL), true, false, "")
	createRebootProfile(t, ns, shellyv1alpha1.ModeEnforce, true, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: new(true)},
	})

	r, rec := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee92")
	if !rebootCalled(fake) {
		t.Fatal("an opted-in profile should reboot a device that is asking for it")
	}
	if dev.Status.RestartRequired {
		t.Error("status.restartRequired should be cleared after a successful reboot")
	}
	found := false
	for len(rec.Events) > 0 {
		if e := <-rec.Events; strings.Contains(e, "Rebooted") {
			found = true
		}
	}
	if !found {
		t.Error("expected a Rebooted event")
	}
}

// The safety case that matters most: observe mode must never reboot hardware,
// even with the opt-in set. A profile that does not write config has no
// business power-cycling someone's load.
func TestNoRebootInObserveMode(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev93", MAC: "AABBCCDDEE93", Gen: 2, RestartRequired: true, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"eco_mode": false}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE93", hostOf(srv.URL), true, false, "")
	createRebootProfile(t, ns, shellyv1alpha1.ModeObserve, true, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: new(true)},
	})

	r, _ := newReconciler()
	_ = reconcile(t, r, ns, "aabbccddee93")
	if rebootCalled(fake) {
		t.Error("observe mode must never reboot a device")
	}
}

// A device NOT asking for a restart must not be rebooted just because the
// profile opted in -- the trigger is the device's flag, not the setting.
func TestNoRebootWhenDeviceIsNotAsking(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{ID: "dev94", MAC: "AABBCCDDEE94", Gen: 2, RestartRequired: false, InitialConfig: map[string]map[string]any{
		"sys": {"device": map[string]any{"eco_mode": true}},
	}}
	srv := shellytest.New(fake)
	defer srv.Close()
	createDevice(t, ns, "AABBCCDDEE94", hostOf(srv.URL), true, false, "")
	createRebootProfile(t, ns, shellyv1alpha1.ModeEnforce, true, shellyv1alpha1.ProfileConfig{
		System: &shellyv1alpha1.SystemSection{EcoMode: new(true)},
	})

	r, _ := newReconciler()
	_ = reconcile(t, r, ns, "aabbccddee94")
	if rebootCalled(fake) {
		t.Error("must not reboot a device that is not reporting restart_required")
	}
}
