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
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	"github.com/LukeEvansTech/shelly-operator/internal/fleet"
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
// with auth second-to-last and wifi dead last.
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

	// RegistryName is the ConfigMap (in the device's namespace) holding
	// per-device inventory metadata (name, room, type, note) keyed by
	// lowercased MAC. "" disables the registry.
	RegistryName string

	// Interval is the steady-state requeue (jittered, +/-10%); default 5m.
	Interval time.Duration

	// clientMu guards clients.
	clientMu sync.Mutex
	// clients caches one RPC client per device address so its digest nonce
	// survives across reconciles. See deviceClient.
	clients map[string]*deviceClientEntry
}

// deviceClientEntry is a cached RPC client plus the password it was built
// with, so a rotated password rebuilds it.
type deviceClientEntry struct {
	password string
	client   *shelly.Client
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

	password, credErr := fleet.ResolvePassword(ctx, r.Reader, dev.Namespace, profile.Spec.Config.Auth)
	if credErr != nil {
		return r.finish(ctx, &dev, metav1.ConditionUnknown, shellyv1alpha1.ReasonCredentialsError,
			credErr.Error(), nil, profile.Name)
	}

	wifiPw, wifiErr := fleet.ResolveWifiPasswords(ctx, r.Reader, dev.Namespace, profile.Spec.Config.Wifi)
	if wifiErr != nil {
		return r.finish(ctx, &dev, metav1.ConditionUnknown, shellyv1alpha1.ReasonCredentialsError,
			wifiErr.Error(), nil, profile.Name)
	}

	desiredName, nameErr := fleet.ResolveName(ctx, r.Reader, &dev, r.NameMapName, r.RegistryName)
	if nameErr != nil {
		return r.finish(ctx, &dev, metav1.ConditionUnknown, shellyv1alpha1.ReasonConfigFetchFailed,
			nameErr.Error(), nil, profile.Name)
	}
	warns = appendProfileWarnings(profile, desiredName, warns)

	// Stamp registry metadata (room/appliance labels, note annotation) before
	// the drift check so the object is always current regardless of profile state.
	if err := r.stampRegistry(ctx, &dev); err != nil {
		return r.finish(ctx, &dev, metav1.ConditionUnknown, shellyv1alpha1.ReasonConfigFetchFailed,
			err.Error(), nil, profile.Name)
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
	findings, err = appendFirmwareFindings(ctx, c, profile, findings)
	if err != nil {
		return r.finish(ctx, &dev, metav1.ConditionUnknown, shellyv1alpha1.ReasonConfigFetchFailed,
			fmt.Sprintf("fetching schedule jobs: %v", err), nil, profile.Name)
	}
	findings, err = appendScheduleFindings(ctx, c, profile, findings)
	if err != nil {
		return r.finish(ctx, &dev, metav1.ConditionUnknown, shellyv1alpha1.ReasonConfigFetchFailed,
			fmt.Sprintf("fetching schedule jobs for schedule section: %v", err), nil, profile.Name)
	}

	if profile.Spec.Mode == shellyv1alpha1.ModeEnforce && len(findings) > 0 && !dev.Spec.Paused { // defense-in-depth; paused returns earlier
		var enforceResult ctrl.Result
		var enforceErr error
		var done bool
		findings, enforceResult, enforceErr, done = r.runEnforce(ctx, c, &dev, profile, desired, desiredName, password, wifiPw, findings, warns)
		if done {
			return enforceResult, enforceErr
		}
	}

	if len(findings) == 0 {
		return r.finish(ctx, &dev, metav1.ConditionTrue, shellyv1alpha1.ReasonInSync,
			withWarnings(fmt.Sprintf("configuration matches profile %s", profile.Name), warns), nil, profile.Name)
	}
	return r.finish(ctx, &dev, metav1.ConditionFalse, shellyv1alpha1.ReasonDrifted,
		withWarnings(drift.Summarize(findings), warns), findings, profile.Name)
}

// appendProfileWarnings adds operator-facing warnings about risky but
// valid profile declarations. Warnings never block reconciliation; they
// ride along in the InSync condition message.
func appendProfileWarnings(profile *shellyv1alpha1.ShellyProfile, desiredName string, warns []string) []string {
	if n := profile.Spec.Config.Name; n != nil && n.Managed && desiredName == "" {
		warns = append(warns, "name managed but unresolvable (no displayName or name-map entry)")
	}
	if w := profile.Spec.Config.Wifi; w != nil && w.Sta != nil && w.Sta.SSID != "" && w.Sta1 == nil {
		warns = append(warns, "wifi.sta is managed without a wifi.sta1 fallback; a wrong network can strand devices")
	}
	if w := profile.Spec.Config.Wifi; w != nil && w.Sta != nil && w.Sta.Enable != nil && !*w.Sta.Enable {
		warns = append(warns, "wifi.sta is declared disabled; a device without another enabled network will be stranded")
	}
	return warns
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
// (possibly updated) findings, a terminal ctrl.Result, any status-patch error,
// and done=true when Reconcile should return immediately; done=false means
// enforcement completed normally and the caller should continue with the
// (possibly updated) findings.
func (r *ShellyDeviceReconciler) runEnforce(
	ctx context.Context, c *shelly.Client, dev *shellyv1alpha1.ShellyDevice,
	profile *shellyv1alpha1.ShellyProfile, desired map[string]map[string]any,
	desiredName, password string, wifiPw fleet.WifiPasswords, findings []drift.Finding, warns []string,
) ([]drift.Finding, ctrl.Result, error, bool) {
	// Damping: if the previous cycle already wrote these exact sections and
	// values and they came back unchanged, don't rewrite device flash every
	// cycle. The comparison is against the full drift summary stored in the
	// condition message -- any changed want/have value re-arms enforcement
	// even when the drifted section set is identical. Note: Summarize lists
	// at most 5 findings, so changes only beyond the 5th finding may not
	// re-arm (acceptable).
	if prev := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync); prev != nil &&
		prev.Reason == shellyv1alpha1.ReasonNotConverging &&
		prev.Message == withWarnings(drift.Summarize(findings), warns) {
		res, err := r.finish(ctx, dev, metav1.ConditionFalse, shellyv1alpha1.ReasonNotConverging,
			withWarnings(drift.Summarize(findings), warns), findings, profile.Name)
		return findings, res, err, true
	}

	var applyErr error
	var applied []string
	var authNow bool
	findings, applied, authNow, applyErr = r.enforceAndRecheck(ctx, c, dev, profile, desired, desiredName, password, wifiPw, findings, dev.Status.AuthEnabled)
	if applyErr != nil {
		var rerr *recheckError
		if errors.As(applyErr, &rerr) {
			// A recheck failure right after a wifi write is the expected
			// migration outcome (the device moved networks), not an error.
			if slices.Contains(applied, sectionWifi) {
				res, err := r.finish(ctx, dev, metav1.ConditionUnknown, shellyv1alpha1.ReasonWifiApplied,
					withWarnings(fmt.Sprintf("wifi configuration applied; device no longer reachable at %s -- it may have moved networks; discovery will update the address when it reappears (ensure --discovery-cidrs covers the new subnet)", dev.Status.Address), warns), nil, profile.Name)
				return findings, res, err, true
			}
			res, err := r.finish(ctx, dev, metav1.ConditionUnknown, shellyv1alpha1.ReasonConfigFetchFailed,
				withWarnings(applyErr.Error(), warns), nil, profile.Name)
			return findings, res, err, true
		}
		res, err := r.finish(ctx, dev, metav1.ConditionFalse, shellyv1alpha1.ReasonApplyFailed,
			withWarnings(fmt.Sprintf("enforcing drifted config: %v", applyErr), warns), findings, profile.Name)
		return findings, res, err, true
	}
	// Persist the fresh auth state so pre-sweep reconciles don't re-issue
	// SetAuth; the discovery sweeper will confirm it on its next pass.
	dev.Status.AuthEnabled = authNow
	// Detect non-convergence: any post-recheck finding whose section was
	// just written means the device reverted or clamped our write.
	for _, f := range findings {
		if slices.Contains(applied, f.Section) {
			if r.Recorder != nil {
				r.Recorder.Event(dev, corev1.EventTypeWarning, "EnforcementNotConverging",
					"device still reports drift in sections that were just written")
			}
			res, err := r.finish(ctx, dev, metav1.ConditionFalse, shellyv1alpha1.ReasonNotConverging,
				withWarnings(drift.Summarize(findings), warns), findings, profile.Name)
			return findings, res, err, true
		}
	}
	return findings, ctrl.Result{}, nil, false
}

// enforceAndRecheck applies all drifted sections to the device and then
// re-fetches config to verify the apply succeeded. It returns the updated
// findings, the sections that were written, the fresh auth-enabled state,
// and any apply/recheck error.
func (r *ShellyDeviceReconciler) enforceAndRecheck(
	ctx context.Context, c *shelly.Client, dev *shellyv1alpha1.ShellyDevice,
	profile *shellyv1alpha1.ShellyProfile, desired map[string]map[string]any,
	desiredName, password string, wifiPw fleet.WifiPasswords, findings []drift.Finding, authNow bool,
) ([]drift.Finding, []string, bool, error) {
	res, applyErr := r.applyFindings(ctx, c, dev, desired, findings, authEnableOf(profile), firmwareEnableOf(profile), password, wifiPw, profile)
	if len(res.applied) > 0 && r.Recorder != nil {
		r.Recorder.Event(dev, corev1.EventTypeNormal, "DriftCorrected",
			fmt.Sprintf("applied sections: %s", strings.Join(res.applied, ", ")))
	}
	if res.restartRequired && r.Recorder != nil {
		r.Recorder.Event(dev, corev1.EventTypeNormal, "RestartRequired",
			"a configuration change requires a device restart to take effect")
	}
	if applyErr != nil {
		return findings, res.applied, authNow, applyErr
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
		return nil, res.applied, authNow, &recheckError{err: fmt.Errorf("re-checking after enforcement: %w", err)}
	}
	desired = drift.Render(profile.Spec.Config, desiredName, actual)
	findings, err = drift.Diff(desired, actual)
	if err != nil {
		return nil, res.applied, authNow, &recheckError{err: fmt.Errorf("parsing device config after enforcement: %w", err)}
	}
	findings = appendAuthFinding(profile, findings, authNow)
	findings, err = appendFirmwareFindings(ctx, c, profile, findings)
	if err != nil {
		return nil, res.applied, authNow, &recheckError{err: fmt.Errorf("re-checking schedule jobs after enforcement: %w", err)}
	}
	findings, err = appendScheduleFindings(ctx, c, profile, findings)
	if err != nil {
		return nil, res.applied, authNow, &recheckError{err: fmt.Errorf("re-checking schedule section jobs after enforcement: %w", err)}
	}
	return findings, res.applied, authNow, nil
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
// deviceClient returns the cached RPC client for addr, building one on first
// use. The client is cached rather than rebuilt per reconcile so its digest
// challenge -- and therefore its nonce -- is reused: firmware 2.0.0 keeps
// only a 32-entry circular nonce buffer and answers new-nonce requests with
// HTTP 429 while that buffer is exhausted, whereas one nonce serves ~30k
// requests or 1h (nc is incremented per request, see internal/shelly/digest).
// A rotated password rebuilds the entry. A stale entry left behind by an
// address change is harmless: the device answering at that address issues a
// 401 and the client re-challenges.
func (r *ShellyDeviceReconciler) deviceClient(addr, password string) *shelly.Client {
	r.clientMu.Lock()
	defer r.clientMu.Unlock()
	if e, ok := r.clients[addr]; ok && e.password == password {
		return e.client
	}
	opts := []shelly.Option{shelly.WithHTTPClient(r.HTTP)}
	if password != "" {
		opts = append(opts, shelly.WithPassword(password))
	}
	e := &deviceClientEntry{password: password, client: shelly.NewClient(addr, opts...)}
	if r.clients == nil {
		r.clients = map[string]*deviceClientEntry{}
	}
	r.clients[addr] = e
	return e.client
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

// stampRegistry reads the registry ConfigMap entry for dev and ensures the
// device object carries the matching room/appliance labels and note
// annotation. It merges carefully -- never clobbering discovery's model/app
// labels or any other pre-existing labels. It only calls Update when
// something actually changed, and tolerates conflict errors gracefully
// (the next reconcile will retry).
func (r *ShellyDeviceReconciler) stampRegistry(ctx context.Context, dev *shellyv1alpha1.ShellyDevice) error {
	entry, err := fleet.ResolveRegistry(ctx, r.Reader, dev, r.RegistryName)
	if err != nil {
		return fmt.Errorf("reading registry: %w", err)
	}

	want := map[string]string{}
	if entry.Room != "" {
		want[shellyv1alpha1.LabelRoom] = fleet.SanitizeLabel(entry.Room)
	}
	if entry.Type != "" {
		want[shellyv1alpha1.LabelAppliance] = fleet.SanitizeLabel(entry.Type)
	}

	wantAnnotations := map[string]string{}
	if entry.Note != "" {
		wantAnnotations[shellyv1alpha1.AnnotationNote] = entry.Note
	}

	// Determine whether any change is required.
	changed := false
	for k, v := range want {
		if dev.Labels[k] != v {
			changed = true
			break
		}
	}
	// Check for labels that should be removed (registry cleared them).
	if !changed {
		for _, k := range []string{shellyv1alpha1.LabelRoom, shellyv1alpha1.LabelAppliance} {
			if _, inWant := want[k]; !inWant {
				if _, exists := dev.Labels[k]; exists {
					changed = true
					break
				}
			}
		}
	}
	if !changed {
		for k, v := range wantAnnotations {
			if dev.Annotations[k] != v {
				changed = true
				break
			}
		}
	}
	if !changed {
		// Check for annotation that should be removed.
		if _, inWant := wantAnnotations[shellyv1alpha1.AnnotationNote]; !inWant {
			if _, exists := dev.Annotations[shellyv1alpha1.AnnotationNote]; exists {
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}

	base := dev.DeepCopy()

	// Apply desired labels (merge, never clobber existing ones not in our set).
	if dev.Labels == nil {
		dev.Labels = map[string]string{}
	}
	maps.Copy(dev.Labels, want)
	// Remove labels the registry no longer provides.
	for _, k := range []string{shellyv1alpha1.LabelRoom, shellyv1alpha1.LabelAppliance} {
		if _, inWant := want[k]; !inWant {
			delete(dev.Labels, k)
		}
	}

	// Apply desired annotations.
	if dev.Annotations == nil {
		dev.Annotations = map[string]string{}
	}
	maps.Copy(dev.Annotations, wantAnnotations)
	if _, inWant := wantAnnotations[shellyv1alpha1.AnnotationNote]; !inWant {
		delete(dev.Annotations, shellyv1alpha1.AnnotationNote)
	}

	if err := r.Patch(ctx, dev, client.MergeFrom(base)); err != nil {
		if apierrors.IsConflict(err) {
			// Stale resourceVersion: another writer changed the object. Our
			// update will land on the next reconcile.
			return nil
		}
		return fmt.Errorf("stamping registry labels: %w", err)
	}
	return nil
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
