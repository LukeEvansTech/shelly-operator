package exporterfeed

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
)

// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch

// dataKey is the file name shelly_exporter expects to mount.
const dataKey = "config.yaml"

// Reconciler maintains the exporter's device-list ConfigMap from the
// current set of online ShellyDevices. Any relevant device event
// re-renders; the write is skipped when nothing changed.
type Reconciler struct {
	client.Client
	// Reader performs the ConfigMap read uncached so the manager's cache
	// never starts a cluster-wide ConfigMap informer (writes stay on the
	// cached client; the repo's name-map/secret reads follow the same
	// convention).
	Reader        client.Reader
	Namespace     string // device namespace (the ConfigMap lives here too)
	ConfigMapName string
	Options       Options
}

func (r *Reconciler) reader() client.Reader {
	if r.Reader != nil {
		return r.Reader
	}
	return r.Client
}

// Reconcile re-renders the ConfigMap. The request key is ignored: the
// trigger is "some device changed", the unit of work is the whole fleet.
func (r *Reconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	var devs shellyv1alpha1.ShellyDeviceList
	if err := r.List(ctx, &devs, client.InNamespace(r.Namespace)); err != nil {
		return ctrl.Result{}, fmt.Errorf("exporterfeed: list devices: %w", err)
	}
	rendered, err := RenderConfig(devs.Items, r.Options)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("exporterfeed: render: %w", err)
	}

	var cm corev1.ConfigMap
	key := types.NamespacedName{Namespace: r.Namespace, Name: r.ConfigMapName}
	err = r.reader().Get(ctx, key, &cm)
	switch {
	case apierrors.IsNotFound(err):
		cm = corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: r.Namespace,
				Name:      r.ConfigMapName,
				Labels:    map[string]string{"app.kubernetes.io/managed-by": "shelly-operator"},
			},
			Data: map[string]string{dataKey: rendered},
		}
		if err := r.Create(ctx, &cm); err != nil {
			return ctrl.Result{}, fmt.Errorf("exporterfeed: create %s: %w", r.ConfigMapName, err)
		}
		return ctrl.Result{}, nil
	case err != nil:
		return ctrl.Result{}, fmt.Errorf("exporterfeed: get %s: %w", r.ConfigMapName, err)
	}

	if cm.Data[dataKey] == rendered {
		return ctrl.Result{}, nil
	}
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data[dataKey] = rendered
	if err := r.Update(ctx, &cm); err != nil {
		return ctrl.Result{}, fmt.Errorf("exporterfeed: update %s: %w", r.ConfigMapName, err)
	}
	return ctrl.Result{}, nil
}

// SetupWithManager triggers on device lifecycle and reachability changes
// only (lastSeen refreshes from the sweeper don't re-render). The
// reconcile ignores the request key, so per-device keys just provide
// workqueue coalescing.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	relevant := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldDev, ok1 := e.ObjectOld.(*shellyv1alpha1.ShellyDevice)
			newDev, ok2 := e.ObjectNew.(*shellyv1alpha1.ShellyDevice)
			if !ok1 || !ok2 {
				return false
			}
			return oldDev.Status.Online != newDev.Status.Online ||
				oldDev.Status.Address != newDev.Status.Address
		},
	}
	return ctrl.NewControllerManagedBy(mgr).
		Named("exporterfeed").
		For(&shellyv1alpha1.ShellyDevice{}, builder.WithPredicates(relevant)).
		Complete(r)
}
