package controller

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
	"github.com/LukeEvansTech/shelly-operator/internal/drift"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly"
)

// +kubebuilder:rbac:groups=shelly.thirdimpact.io,resources=shellydevices,verbs=get;list;watch
// +kubebuilder:rbac:groups=shelly.thirdimpact.io,resources=shellydevices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=shelly.thirdimpact.io,resources=shellyprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get

// ShellyDeviceReconciler matches each ShellyDevice to a ShellyProfile,
// reports drift on the InSync condition, and (for enforce-mode profiles)
// corrects it by writing drifted sections to the device, safest-first
// with auth last.
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
			"reconciliation paused via spec.paused", nil, dev.Status.MatchedProfile)
	}
	if !dev.Status.Online {
		return r.finish(ctx, &dev, metav1.ConditionUnknown, shellyv1alpha1.ReasonOffline,
			"device offline; skipping drift check", nil, dev.Status.MatchedProfile)
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

	password, credErr := r.lookupPassword(ctx, dev.Namespace, profile.Spec.Config.Auth)
	if credErr != nil {
		return r.finish(ctx, &dev, metav1.ConditionUnknown, shellyv1alpha1.ReasonCredentialsError,
			credErr.Error(), nil, profile.Name)
	}

	desiredName := dev.Spec.DisplayName
	if desiredName == "" {
		var nameErr error
		desiredName, nameErr = r.lookupName(ctx, dev.Namespace, dev.Name)
		if nameErr != nil {
			return r.finish(ctx, &dev, metav1.ConditionUnknown, shellyv1alpha1.ReasonConfigFetchFailed,
				nameErr.Error(), nil, profile.Name)
		}
	}
	if n := profile.Spec.Config.Name; n != nil && n.Managed && desiredName == "" {
		warns = append(warns, "name managed but unresolvable (no displayName or name-map entry)")
	}

	c := r.deviceClient(dev.Status.Address, password)
	actual, err := c.GetConfig(ctx)
	if err != nil {
		reason := shellyv1alpha1.ReasonConfigFetchFailed
		msg := fmt.Sprintf("fetching device config: %v", err)
		var authErr *shelly.AuthError
		if errors.As(err, &authErr) {
			reason = shellyv1alpha1.ReasonAuthRequired
			if password == "" {
				msg = "device requires auth and no password is configured (set spec.config.auth.passwordSecretRef)"
			} else {
				// SetAuth itself requires working auth, so a wrong password
				// cannot self-correct.
				msg = "configured password rejected by device; restore the previous password in the Secret or factory-reset the device"
			}
		}
		return r.finish(ctx, &dev, metav1.ConditionUnknown, reason, msg, nil, profile.Name)
	}

	desired := drift.Render(profile.Spec.Config, desiredName, actual)
	findings, err := drift.Diff(desired, actual)
	if err != nil {
		return r.finish(ctx, &dev, metav1.ConditionUnknown, shellyv1alpha1.ReasonConfigFetchFailed,
			fmt.Sprintf("parsing device config: %v", err), nil, profile.Name)
	}
	findings = appendAuthFinding(profile, findings, dev.Status.AuthEnabled)

	if profile.Spec.Mode == shellyv1alpha1.ModeEnforce && len(findings) > 0 && !dev.Spec.Paused { // defense-in-depth; paused returns earlier
		var enforceResult ctrl.Result
		var done bool
		findings, enforceResult, done = r.runEnforce(ctx, c, &dev, profile, desired, desiredName, password, findings, warns)
		if done {
			return enforceResult, nil
		}
	}

	if len(findings) == 0 {
		return r.finish(ctx, &dev, metav1.ConditionTrue, shellyv1alpha1.ReasonInSync,
			withWarnings(fmt.Sprintf("configuration matches profile %s", profile.Name), warns), nil, profile.Name)
	}
	return r.finish(ctx, &dev, metav1.ConditionFalse, shellyv1alpha1.ReasonDrifted,
		withWarnings(drift.Summarize(findings), warns), findings, profile.Name)
}

// recheckError marks a failure that happened AFTER all device writes
// succeeded (post-apply verification read). It must not be reported as
// ApplyFailed -- the device converged; only the verification failed.
type recheckError struct{ err error }

func (e *recheckError) Error() string { return e.err.Error() }
func (e *recheckError) Unwrap() error { return e.err }

// appendAuthFinding appends an auth pseudo-finding when the profile's desired
// auth-enable state differs from the device's current state. It is used both
// before and after enforcement so the logic is never duplicated.
func appendAuthFinding(profile *shellyv1alpha1.ShellyProfile, fs []drift.Finding, authNow bool) []drift.Finding {
	if a := profile.Spec.Config.Auth; a != nil && a.Enable != nil && *a.Enable != authNow {
		return append(fs, drift.Finding{Section: sectionAuth, Path: "enable", Want: *a.Enable, Have: authNow})
	}
	return fs
}

// runEnforce is the enforcement gate inside Reconcile. It handles damping,
// delegates to enforceAndRecheck, and detects non-convergence. It returns the
// (possibly updated) findings plus a terminal ctrl.Result and done=true when
// Reconcile should return immediately; done=false means enforcement completed
// normally and the caller should continue with the (possibly updated) findings.
func (r *ShellyDeviceReconciler) runEnforce(
	ctx context.Context, c *shelly.Client, dev *shellyv1alpha1.ShellyDevice,
	profile *shellyv1alpha1.ShellyProfile, desired map[string]map[string]any,
	desiredName, password string, findings []drift.Finding, warns []string,
) ([]drift.Finding, ctrl.Result, bool) {
	// Damping: if the previous cycle already wrote these exact sections
	// and they came back unchanged, don't rewrite device flash every
	// cycle. Any change in the diff (external fix, profile edit) re-arms
	// enforcement.
	if prev := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync); prev != nil &&
		prev.Reason == shellyv1alpha1.ReasonNotConverging &&
		slices.Equal(drift.Sections(findings), dev.Status.DriftedSections) {
		res, _ := r.finish(ctx, dev, metav1.ConditionFalse, shellyv1alpha1.ReasonNotConverging,
			withWarnings(drift.Summarize(findings), warns), findings, profile.Name)
		return findings, res, true
	}

	var applyErr error
	var applied []string
	findings, applied, applyErr = r.enforceAndRecheck(ctx, c, dev, profile, desired, desiredName, password, findings, dev.Status.AuthEnabled)
	if applyErr != nil {
		var rerr *recheckError
		if errors.As(applyErr, &rerr) {
			res, _ := r.finish(ctx, dev, metav1.ConditionUnknown, shellyv1alpha1.ReasonConfigFetchFailed,
				withWarnings(applyErr.Error(), warns), nil, profile.Name)
			return findings, res, true
		}
		res, _ := r.finish(ctx, dev, metav1.ConditionFalse, shellyv1alpha1.ReasonApplyFailed,
			withWarnings(fmt.Sprintf("enforcing drifted config: %v", applyErr), warns), findings, profile.Name)
		return findings, res, true
	}
	// Detect non-convergence: any post-recheck finding whose section was
	// just written means the device reverted or clamped our write.
	for _, f := range findings {
		if slices.Contains(applied, f.Section) {
			if r.Recorder != nil {
				r.Recorder.Event(dev, corev1.EventTypeWarning, "EnforcementNotConverging",
					"device still reports drift in sections that were just written")
			}
			res, _ := r.finish(ctx, dev, metav1.ConditionFalse, shellyv1alpha1.ReasonNotConverging,
				withWarnings(drift.Summarize(findings), warns), findings, profile.Name)
			return findings, res, true
		}
	}
	return findings, ctrl.Result{}, false
}

// enforceAndRecheck applies all drifted sections to the device and then
// re-fetches config to verify the apply succeeded. It returns the updated
// findings, the sections that were written, and any apply/recheck error.
func (r *ShellyDeviceReconciler) enforceAndRecheck(
	ctx context.Context, c *shelly.Client, dev *shellyv1alpha1.ShellyDevice,
	profile *shellyv1alpha1.ShellyProfile, desired map[string]map[string]any,
	desiredName, password string, findings []drift.Finding, authNow bool,
) ([]drift.Finding, []string, error) {
	res, applyErr := r.applyFindings(ctx, c, dev, desired, findings, authEnableOf(profile), password)
	if len(res.applied) > 0 && r.Recorder != nil {
		r.Recorder.Event(dev, corev1.EventTypeNormal, "DriftCorrected",
			fmt.Sprintf("applied sections: %s", strings.Join(res.applied, ", ")))
	}
	if res.restartRequired && r.Recorder != nil {
		r.Recorder.Event(dev, corev1.EventTypeNormal, "RestartRequired",
			"a configuration change requires a device restart to take effect")
	}
	if applyErr != nil {
		return findings, res.applied, applyErr
	}
	// Track auth state locally: status.authEnabled is stale until next sweep.
	for _, s := range res.applied {
		if s == sectionAuth {
			if e := authEnableOf(profile); e != nil {
				authNow = *e
			}
		}
	}
	c = r.deviceClient(dev.Status.Address, password)
	actual, err := c.GetConfig(ctx)
	if err != nil {
		return nil, res.applied, &recheckError{err: fmt.Errorf("re-checking after enforcement: %w", err)}
	}
	desired = drift.Render(profile.Spec.Config, desiredName, actual)
	findings, err = drift.Diff(desired, actual)
	if err != nil {
		return nil, res.applied, &recheckError{err: fmt.Errorf("parsing device config after enforcement: %w", err)}
	}
	findings = appendAuthFinding(profile, findings, authNow)
	return findings, res.applied, nil
}

// finish records the reconcile outcome on status, emits an Event when the
// condition transitions, and schedules the next check.
func (r *ShellyDeviceReconciler) finish(ctx context.Context, dev *shellyv1alpha1.ShellyDevice,
	status metav1.ConditionStatus, reason, message string, findings []drift.Finding, matchedProfile string) (ctrl.Result, error) {

	prev := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	base := dev.DeepCopy()

	dev.Status.MatchedProfile = matchedProfile
	dev.Status.DriftedSections = drift.Sections(findings)
	meta.SetStatusCondition(&dev.Status.Conditions, metav1.Condition{
		Type:               shellyv1alpha1.ConditionInSync,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: dev.Generation,
	})
	if err := r.Status().Patch(ctx, dev, client.MergeFrom(base)); err != nil {
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

// lookupName resolves a device's desired name from the name-map ConfigMap.
// A missing ConfigMap means "no name managed" (""); any other read error
// is returned so a transient API failure can't masquerade as in-sync.
func (r *ShellyDeviceReconciler) lookupName(ctx context.Context, namespace, deviceName string) (string, error) {
	if r.NameMapName == "" || r.Reader == nil {
		return "", nil
	}
	var cm corev1.ConfigMap
	if err := r.Reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: r.NameMapName}, &cm); err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading name map %s/%s: %w", namespace, r.NameMapName, err)
	}
	return cm.Data[deviceName], nil
}

// lookupPassword resolves the device admin password from the profile's
// auth passwordSecretRef ("" when no ref configured). Read failures are
// errors -- a configured ref that cannot be read must not silently
// degrade to "no password".
func (r *ShellyDeviceReconciler) lookupPassword(ctx context.Context, namespace string, auth *shellyv1alpha1.AuthSection) (string, error) {
	if auth == nil || auth.PasswordSecretRef == nil || r.Reader == nil {
		return "", nil
	}
	ref := auth.PasswordSecretRef
	var secret corev1.Secret
	if err := r.Reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, &secret); err != nil {
		return "", fmt.Errorf("reading password secret %s/%s: %w", namespace, ref.Name, err)
	}
	pw, ok := secret.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf("password secret %s/%s has no key %q", namespace, ref.Name, ref.Key)
	}
	return string(pw), nil
}

// rpcHTTPClient bounds all device HTTP, including probes. r.HTTP may be
// nil in production wiring; falling back to http.DefaultClient would mean
// an unbounded read against flaky IoT hardware can wedge a worker.
var defaultRPCHTTP = &http.Client{Timeout: 10 * time.Second}

func (r *ShellyDeviceReconciler) rpcHTTPClient() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return defaultRPCHTTP
}

// deviceClient builds an RPC client for the device, authenticated when a
// password is available.
func (r *ShellyDeviceReconciler) deviceClient(addr, password string) *shelly.Client {
	opts := []shelly.Option{shelly.WithHTTPClient(r.HTTP)}
	if password != "" {
		opts = append(opts, shelly.WithPassword(password))
	}
	return shelly.NewClient(addr, opts...)
}

// withWarnings appends non-fatal warnings to a condition message.
func withWarnings(msg string, warns []string) string {
	if len(warns) == 0 {
		return msg
	}
	return msg + " (warnings: " + strings.Join(warns, "; ") + ")"
}

// authEnableOf returns the profile's desired auth state (nil = unmanaged).
func authEnableOf(p *shellyv1alpha1.ShellyProfile) *bool {
	if p.Spec.Config.Auth == nil {
		return nil
	}
	return p.Spec.Config.Auth.Enable
}

// jitter spreads requeues +/-10% so 46 devices don't thunder in lockstep.
func (r *ShellyDeviceReconciler) jitter() time.Duration {
	d := r.Interval
	if d <= 0 {
		d = 5 * time.Minute
	}
	return time.Duration(float64(d) * (0.9 + 0.2*rand.Float64()))
}

// SetupWithManager wires the controller: reconciles on device changes (only
// when spec/labels or consumed status fields change, so discovery lastSeen
// sweeps do not re-trigger reconciles) and re-enqueues every device in the
// namespace when any profile changes. Name-map ConfigMap changes are NOT
// watched (that would cache every ConfigMap in the cluster); they propagate
// within one requeue interval.
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
	devChanged := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldDev, ok1 := e.ObjectOld.(*shellyv1alpha1.ShellyDevice)
			newDev, ok2 := e.ObjectNew.(*shellyv1alpha1.ShellyDevice)
			if !ok1 || !ok2 {
				return true
			}
			if oldDev.Generation != newDev.Generation || !maps.Equal(oldDev.Labels, newDev.Labels) {
				return true
			}
			// Only the status fields this controller consumes; lastSeen
			// refreshes from the sweeper must not re-trigger reconciles.
			return oldDev.Status.Online != newDev.Status.Online ||
				oldDev.Status.Address != newDev.Status.Address ||
				oldDev.Status.AuthEnabled != newDev.Status.AuthEnabled
		},
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&shellyv1alpha1.ShellyDevice{}, builder.WithPredicates(devChanged)).
		Watches(&shellyv1alpha1.ShellyProfile{}, handler.EnqueueRequestsFromMapFunc(mapAll)).
		WithOptions(ctrlcontroller.Options{MaxConcurrentReconciles: 4}).
		Named("shellydevice").
		Complete(r)
}
