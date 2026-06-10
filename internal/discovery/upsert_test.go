package discovery

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly"
)

func TestApplyDeviceCreatesAndUpdates(t *testing.T) {
	ctx := context.Background()
	ns := newNamespace(t)
	now := time.Now()

	f := Found{Host: "10.32.8.38", Info: &shelly.DeviceInfo{
		ID: "shellyplusplug-3c8a1fec8e3c", MAC: "3C8A1FEC8E3C",
		Model: "SNPL-00112UK", App: "PlusPlugUK", Gen: 2,
		Firmware: "fw-1", AuthEnabled: false, Name: "",
	}}
	if err := applyDevice(ctx, k8sClient, ns, now, f); err != nil {
		t.Fatal(err)
	}

	var dev shellyv1alpha1.ShellyDevice
	key := types.NamespacedName{Namespace: ns, Name: "3c8a1fec8e3c"}
	if err := k8sClient.Get(ctx, key, &dev); err != nil {
		t.Fatal(err)
	}
	if dev.Labels[shellyv1alpha1.LabelModel] != "SNPL-00112UK" ||
		dev.Labels[shellyv1alpha1.LabelApp] != "PlusPlugUK" ||
		dev.Labels[shellyv1alpha1.LabelGen] != "2" {
		t.Errorf("labels = %v", dev.Labels)
	}
	if dev.Status.Address != "10.32.8.38" || !dev.Status.Online || dev.Status.Firmware != "fw-1" {
		t.Errorf("status = %+v", dev.Status)
	}
	if dev.Status.LastSeen == nil || dev.Status.LastSeen.Time.Unix() != now.Unix() {
		t.Errorf("lastSeen = %v, want %v", dev.Status.LastSeen, now)
	}

	// User sets spec; a later sweep must preserve it while updating status.
	dev.Spec.DisplayName = "office-desk"
	if err := k8sClient.Update(ctx, &dev); err != nil {
		t.Fatal(err)
	}

	f2 := f
	f2.Host = "10.32.8.99"
	f2.Info = &shelly.DeviceInfo{
		ID: f.Info.ID, MAC: f.Info.MAC, Model: f.Info.Model, App: f.Info.App,
		Gen: f.Info.Gen, Firmware: "fw-2", AuthEnabled: true, Name: "PDU-01",
	}
	later := now.Add(time.Minute)
	if err := applyDevice(ctx, k8sClient, ns, later, f2); err != nil {
		t.Fatal(err)
	}
	if err := k8sClient.Get(ctx, key, &dev); err != nil {
		t.Fatal(err)
	}
	if dev.Spec.DisplayName != "office-desk" {
		t.Errorf("spec clobbered: %+v", dev.Spec)
	}
	if dev.Status.Address != "10.32.8.99" || dev.Status.Firmware != "fw-2" ||
		!dev.Status.AuthEnabled || dev.Status.DeviceName != "PDU-01" {
		t.Errorf("status not refreshed: %+v", dev.Status)
	}
}
