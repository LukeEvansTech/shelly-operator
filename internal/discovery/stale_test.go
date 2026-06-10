package discovery

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly"
)

func TestMarkStale(t *testing.T) {
	ctx := context.Background()
	ns := newNamespace(t)
	now := time.Now()

	mk := func(mac, host string, seen time.Time) {
		t.Helper()
		f := Found{Host: host, Info: &shelly.DeviceInfo{
			ID: "dev-" + mac, MAC: mac, Model: "SNPL-00112UK", App: "PlusPlugUK", Gen: 2,
		}}
		if err := applyDevice(ctx, k8sClient, ns, seen, f); err != nil {
			t.Fatal(err)
		}
	}
	mk("AABBCCDDEE01", "10.32.8.10", now.Add(-10*time.Minute)) // stale
	mk("AABBCCDDEE02", "10.32.8.11", now)                      // fresh

	if err := markStale(ctx, k8sClient, ns, now.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}

	var dev shellyv1alpha1.ShellyDevice
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "aabbccddee01"}, &dev); err != nil {
		t.Fatal(err)
	}
	if dev.Status.Online {
		t.Error("stale device should be offline")
	}
	if dev.Status.LastSeen == nil {
		t.Error("lastSeen must be preserved on offline devices")
	}
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "aabbccddee02"}, &dev); err != nil {
		t.Fatal(err)
	}
	if !dev.Status.Online {
		t.Error("fresh device should stay online")
	}

	// Second run is a no-op: already-offline devices are skipped.
	if err := markStale(ctx, k8sClient, ns, now.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "aabbccddee01"}, &dev); err != nil {
		t.Fatal(err)
	}
	if dev.Status.Online || dev.Status.LastSeen == nil {
		t.Errorf("idempotent re-run changed state: %+v", dev.Status)
	}
}
