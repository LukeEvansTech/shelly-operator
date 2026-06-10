package discovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
)

// markStale flips Online=false on devices whose LastSeen is at or before
// cutoff. Callers pass cutoff = sweepStart - OfflineAfter, so devices
// refreshed during this sweep (LastSeen = sweepStart) are never affected.
// LastSeen itself is preserved so operators can see when a device
// disappeared. A resourceVersion conflict means the device changed after
// we listed (most likely refreshed by this very sweep), so flipping it
// offline would be wrong — conflicts are skipped, not errors; the next
// sweep re-evaluates.
func markStale(ctx context.Context, c client.Client, namespace string, cutoff time.Time) error {
	var list shellyv1alpha1.ShellyDeviceList
	if err := c.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("discovery: list devices: %w", err)
	}
	var errs []error
	for i := range list.Items {
		dev := &list.Items[i]
		if !dev.Status.Online || dev.Status.LastSeen == nil || dev.Status.LastSeen.Time.After(cutoff) {
			continue
		}
		dev.Status.Online = false
		if err := c.Status().Update(ctx, dev); err != nil {
			if apierrors.IsConflict(err) {
				continue
			}
			errs = append(errs, fmt.Errorf("discovery: mark %s offline: %w", dev.Name, err))
		}
	}
	return errors.Join(errs...)
}
