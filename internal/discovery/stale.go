package discovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
)

// markStale flips Online=false on devices whose LastSeen is older than
// cutoff. LastSeen itself is preserved so operators can see when a device
// disappeared.
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
			errs = append(errs, fmt.Errorf("discovery: mark %s offline: %w", dev.Name, err))
		}
	}
	return errors.Join(errs...)
}
