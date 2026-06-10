package controller

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
	"github.com/LukeEvansTech/shelly-operator/internal/drift"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly"
)

// +kubebuilder:rbac:groups=shelly.thirdimpact.io,resources=shellydevices,verbs=get;list;watch
// +kubebuilder:rbac:groups=shelly.thirdimpact.io,resources=shellydevices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=shelly.thirdimpact.io,resources=shellyprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// ShellyDeviceReconciler matches each ShellyDevice to a ShellyProfile and
// reports configuration drift on the InSync condition. Observe-only:
// it never writes to devices (enforcement ships in a later release).
type ShellyDeviceReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	// Reader reads the name-map ConfigMap. Wired to mgr.GetAPIReader() so
	// the manager's cache doesn't start informers for every ConfigMap in
	// the cluster just for one object.
	Reader client.Reader

	// HTTP overrides the device RPC client (nil = shelly default, 10s).
	HTTP *http.Client

	// NameMapName is the ConfigMap (in the device's namespace) mapping
	// lowercased MAC -> desired device name. "" disables the name map.
	NameMapName string

	// Interval is the steady-state requeue (jittered, +/-10%); default 5m.
	Interval time.Duration
}

// Reconcile checks one device for drift against its matched profile.
func (r *ShellyDeviceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var dev shellyv1alpha1.ShellyDevice
	if err := r.Get(ctx, req.NamespacedName, &dev); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if dev.Spec.Paused {
		return r.finish(ctx, &dev, metav1.ConditionUnknown, shellyv1alpha1.ReasonPaused,
			"reconciliation paused via spec.paused", nil, "")
	}
	if !dev.Status.Online {
		return r.finish(ctx, &dev, metav1.ConditionUnknown, shellyv1alpha1.ReasonOffline,
			"device offline; skipping drift check", nil, "")
	}

	var profiles shellyv1alpha1.ShellyProfileList
	if err := r.List(ctx, &profiles, client.InNamespace(dev.Namespace)); err != nil {
		return ctrl.Result{}, err
	}
	profile, warns := drift.MatchProfile(&dev, profiles.Items)
	if profile == nil {
		reason, msg := shellyv1alpha1.ReasonNoProfile, "no ShellyProfile matches this device"
		if dev.Spec.ProfileRef != "" {
			reason = shellyv1alpha1.ReasonProfileNotFound
			msg = fmt.Sprintf("spec.profileRef %q not found", dev.Spec.ProfileRef)
		}
		if len(warns) > 0 {
			msg += " (" + strings.Join(warns, "; ") + ")"
		}
		return r.finish(ctx, &dev, metav1.ConditionUnknown, reason, msg, nil, "")
	}

	desiredName := dev.Spec.DisplayName
	if desiredName == "" {
		desiredName = r.lookupName(ctx, dev.Namespace, dev.Name)
	}

	c := shelly.NewClient(dev.Status.Address, shelly.WithHTTPClient(r.HTTP))
	actual, err := c.GetConfig(ctx)
	if err != nil {
		return r.finish(ctx, &dev, metav1.ConditionUnknown, shellyv1alpha1.ReasonConfigFetchFailed,
			fmt.Sprintf("fetching device config: %v", err), nil, profile.Name)
	}

	desired := drift.Render(profile.Spec.Config, desiredName, actual)
	findings, err := drift.Diff(desired, actual)
	if err != nil {
		return ctrl.Result{}, err
	}
	if a := profile.Spec.Config.Auth; a != nil && a.Enable != nil && *a.Enable != dev.Status.AuthEnabled {
		findings = append(findings, drift.Finding{Section: "auth", Path: "enable", Want: *a.Enable, Have: dev.Status.AuthEnabled})
	}

	if profile.Spec.Mode == shellyv1alpha1.ModeEnforce && len(findings) > 0 && r.Recorder != nil {
		r.Recorder.Event(&dev, corev1.EventTypeWarning, "EnforcementPending",
			"profile mode is enforce, but enforcement is not implemented yet; observing only")
	}

	if len(findings) == 0 {
		return r.finish(ctx, &dev, metav1.ConditionTrue, shellyv1alpha1.ReasonInSync,
			fmt.Sprintf("configuration matches profile %s", profile.Name), nil, profile.Name)
	}
	return r.finish(ctx, &dev, metav1.ConditionFalse, shellyv1alpha1.ReasonDrifted,
		drift.Summarize(findings), findings, profile.Name)
}

// finish records the reconcile outcome on status, emits an Event when the
// condition transitions, and schedules the next check.
func (r *ShellyDeviceReconciler) finish(ctx context.Context, dev *shellyv1alpha1.ShellyDevice,
	status metav1.ConditionStatus, reason, message string, findings []drift.Finding, matchedProfile string) (ctrl.Result, error) {

	prev := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)

	dev.Status.MatchedProfile = matchedProfile
	dev.Status.DriftedSections = drift.Sections(findings)
	meta.SetStatusCondition(&dev.Status.Conditions, metav1.Condition{
		Type:               shellyv1alpha1.ConditionInSync,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: dev.Generation,
	})
	if err := r.Status().Update(ctx, dev); err != nil {
		return ctrl.Result{}, err
	}
	if r.Recorder != nil && (prev == nil || prev.Status != status || prev.Reason != reason) {
		etype := corev1.EventTypeNormal
		if status == metav1.ConditionFalse {
			etype = corev1.EventTypeWarning
		}
		r.Recorder.Event(dev, etype, reason, message)
	}
	return ctrl.Result{RequeueAfter: r.jitter()}, nil
}

// lookupName resolves a device's desired name from the name-map ConfigMap
// ("" when unset/absent). Uses the uncached Reader.
func (r *ShellyDeviceReconciler) lookupName(ctx context.Context, namespace, deviceName string) string {
	if r.NameMapName == "" || r.Reader == nil {
		return ""
	}
	var cm corev1.ConfigMap
	if err := r.Reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: r.NameMapName}, &cm); err != nil {
		return ""
	}
	return cm.Data[deviceName]
}

// jitter spreads requeues +/-10% so 46 devices don't thunder in lockstep.
func (r *ShellyDeviceReconciler) jitter() time.Duration {
	d := r.Interval
	if d <= 0 {
		d = 5 * time.Minute
	}
	return time.Duration(float64(d) * (0.9 + 0.2*rand.Float64()))
}

// SetupWithManager wires the controller: reconciles on device changes and
// re-enqueues every device in the namespace when any profile changes.
// Name-map ConfigMap changes are NOT watched (that would cache every
// ConfigMap in the cluster); they propagate within one requeue interval.
func (r *ShellyDeviceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	mapAll := func(ctx context.Context, obj client.Object) []ctrl.Request {
		var devs shellyv1alpha1.ShellyDeviceList
		if err := r.List(ctx, &devs, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}
		reqs := make([]ctrl.Request, 0, len(devs.Items))
		for i := range devs.Items {
			reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&devs.Items[i])})
		}
		return reqs
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&shellyv1alpha1.ShellyDevice{}).
		Watches(&shellyv1alpha1.ShellyProfile{}, handler.EnqueueRequestsFromMapFunc(mapAll)).
		Named("shellydevice").
		Complete(r)
}
