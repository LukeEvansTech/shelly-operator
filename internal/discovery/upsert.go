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
// updates status. Spec is never touched — it belongs to users.
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

	dev.Status = shellyv1alpha1.ShellyDeviceStatus{
		Address:     f.Host,
		MAC:         f.Info.MAC,
		Model:       f.Info.Model,
		App:         f.Info.App,
		Gen:         f.Info.Gen,
		Firmware:    f.Info.Firmware,
		AuthEnabled: f.Info.AuthEnabled,
		DeviceName:  f.Info.Name,
		Online:      true,
		LastSeen:    &metav1.Time{Time: now},
	}
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
