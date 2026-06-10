package discovery

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
)

// applyDevice records a probe result as a ShellyDevice: creates the object
// on first sight, refreshes selector labels if identity changed, and
// updates the discovery-owned status fields. Spec is never touched — it
// belongs to users. Status fields owned by other controllers (e.g. future
// conditions) are deliberately left alone, which is why fields are set
// individually rather than replacing the whole struct.
// f.Info must be non-nil with a non-empty MAC; the prober guarantees this.
func applyDevice(ctx context.Context, c client.Client, namespace string, now time.Time, f Found) error {
	name := shellyv1alpha1.DeviceObjectName(f.Info.MAC)
	labels := shellyv1alpha1.DeviceLabels(f.Info.Model, f.Info.App, f.Info.Gen)

	var dev shellyv1alpha1.ShellyDevice
	err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &dev)
	switch {
	case apierrors.IsNotFound(err):
		dev = shellyv1alpha1.ShellyDevice{ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace, Name: name, Labels: labels,
		}}
		if err := c.Create(ctx, &dev); err != nil {
			if apierrors.IsAlreadyExists(err) {
				// Stale cache: the object exists but our informer hasn't
				// caught up. Skip this sweep; the next one will update it.
				return nil
			}
			return fmt.Errorf("discovery: create %s: %w", name, err)
		}
	case err != nil:
		return fmt.Errorf("discovery: get %s: %w", name, err)
	default:
		if !hasLabels(dev.Labels, labels) {
			if dev.Labels == nil {
				dev.Labels = map[string]string{}
			}
			for k, v := range labels {
				dev.Labels[k] = v
			}
			if err := c.Update(ctx, &dev); err != nil {
				return fmt.Errorf("discovery: update labels %s: %w", name, err)
			}
		}
	}

	dev.Status.Address = f.Host
	dev.Status.MAC = f.Info.MAC
	dev.Status.Model = f.Info.Model
	dev.Status.App = f.Info.App
	dev.Status.Gen = f.Info.Gen
	dev.Status.Firmware = f.Info.Firmware
	dev.Status.AuthEnabled = f.Info.AuthEnabled
	dev.Status.DeviceName = f.Info.Name
	dev.Status.Online = true
	dev.Status.LastSeen = &metav1.Time{Time: now}
	if err := c.Status().Update(ctx, &dev); err != nil {
		return fmt.Errorf("discovery: update status %s: %w", name, err)
	}
	return nil
}

// hasLabels reports whether have contains every key/value in want.
func hasLabels(have, want map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}
