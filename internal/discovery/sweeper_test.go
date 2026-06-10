package discovery

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly/shellytest"
)

func TestSweeperRunOnce(t *testing.T) {
	ctx := context.Background()
	ns := newNamespace(t)

	d := &shellytest.Device{
		ID: "shellyplusplug-aabbccddee03", MAC: "AABBCCDDEE03",
		Model: "SNPL-00112UK", App: "PlusPlugUK", Gen: 2, Firmware: "fw-1",
	}
	srv := shellytest.New(d)

	s := &Sweeper{
		Client:       k8sClient,
		Namespace:    ns,
		ExtraHosts:   []string{hostOf(srv.URL), "127.0.0.1:1"},
		Interval:     time.Minute,
		ProbeTimeout: 500 * time.Millisecond,
		OfflineAfter: 50 * time.Millisecond,
	}
	if err := s.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}

	var dev shellyv1alpha1.ShellyDevice
	key := types.NamespacedName{Namespace: ns, Name: "aabbccddee03"}
	if err := k8sClient.Get(ctx, key, &dev); err != nil {
		t.Fatal(err)
	}
	if !dev.Status.Online || dev.Status.Model != "SNPL-00112UK" {
		t.Errorf("after first sweep: %+v", dev.Status)
	}

	// Device disappears; after OfflineAfter elapses the next sweep marks it offline.
	srv.Close()
	time.Sleep(60 * time.Millisecond)
	if err := s.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := k8sClient.Get(ctx, key, &dev); err != nil {
		t.Fatal(err)
	}
	if dev.Status.Online {
		t.Error("device should be offline after disappearing")
	}
}

func TestSweeperNeedsLeaderElection(t *testing.T) {
	if !(&Sweeper{}).NeedLeaderElection() {
		t.Error("sweeper must only run on the elected leader")
	}
}
