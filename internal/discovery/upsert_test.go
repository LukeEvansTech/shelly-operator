package discovery

import (
	"context"
	"net/http"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly/shellytest"
)

// testFirmwareVersion is the pending firmware version used across update tests.
const testFirmwareVersion = "1.7.5"

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
	if dev.Status.LastSeen == nil || dev.Status.LastSeen.Unix() != now.Unix() {
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

func TestApplyDeviceRefreshesLabelsPreservesUserLabels(t *testing.T) {
	ctx := context.Background()
	ns := newNamespace(t)
	now := time.Now()

	f := Found{Host: "10.32.8.40", Info: &shelly.DeviceInfo{
		ID: "dev-aabbccddee10", MAC: "AABBCCDDEE10",
		Model: "SNPL-00112UK", App: "PlusPlugUK", Gen: 2,
	}}
	if err := applyDevice(ctx, k8sClient, ns, now, f); err != nil {
		t.Fatal(err)
	}

	var dev shellyv1alpha1.ShellyDevice
	key := types.NamespacedName{Namespace: ns, Name: "aabbccddee10"}
	if err := k8sClient.Get(ctx, key, &dev); err != nil {
		t.Fatal(err)
	}
	dev.Labels["user/zone"] = "rack-1"
	if err := k8sClient.Update(ctx, &dev); err != nil {
		t.Fatal(err)
	}

	// Firmware update changes the app name; labels must refresh.
	f2 := f
	f2.Info = &shelly.DeviceInfo{
		ID: f.Info.ID, MAC: f.Info.MAC, Model: f.Info.Model,
		App: "PlusPlugUKv2", Gen: 3,
	}
	if err := applyDevice(ctx, k8sClient, ns, now, f2); err != nil {
		t.Fatal(err)
	}
	if err := k8sClient.Get(ctx, key, &dev); err != nil {
		t.Fatal(err)
	}
	if dev.Labels[shellyv1alpha1.LabelApp] != "PlusPlugUKv2" || dev.Labels[shellyv1alpha1.LabelGen] != "3" {
		t.Errorf("labels not refreshed: %v", dev.Labels)
	}
	if dev.Labels["user/zone"] != "rack-1" {
		t.Errorf("user label clobbered: %v", dev.Labels)
	}
}

func TestApplyDeviceAvailableFirmware(t *testing.T) {
	ns := newNamespace(t)
	ctx := context.Background()
	v175 := testFirmwareVersion
	empty := ""

	// First sweep: update pending.
	f := Found{Host: "10.0.0.5", Info: &shelly.DeviceInfo{MAC: "AABBCCDDEF10", Model: "SNPL-00112UK", App: "PlusPlugUK", Gen: 2, Firmware: "1.4.4"},
		AvailableFirmware: &v175}
	if err := applyDevice(ctx, k8sClient, ns, time.Now(), f); err != nil {
		t.Fatal(err)
	}
	var dev shellyv1alpha1.ShellyDevice
	key := types.NamespacedName{Namespace: ns, Name: "aabbccddef10"}
	if err := k8sClient.Get(ctx, key, &dev); err != nil {
		t.Fatal(err)
	}
	if dev.Status.AvailableFirmware != testFirmwareVersion {
		t.Fatalf("availableFirmware = %q", dev.Status.AvailableFirmware)
	}

	// Second sweep: Sys.GetStatus failed (nil) -> previous value kept.
	f.AvailableFirmware = nil
	if err := applyDevice(ctx, k8sClient, ns, time.Now(), f); err != nil {
		t.Fatal(err)
	}
	if err := k8sClient.Get(ctx, key, &dev); err != nil {
		t.Fatal(err)
	}
	if dev.Status.AvailableFirmware != testFirmwareVersion {
		t.Fatalf("availableFirmware after unknown = %q", dev.Status.AvailableFirmware)
	}

	// Third sweep: device is current -> cleared.
	f.AvailableFirmware = &empty
	if err := applyDevice(ctx, k8sClient, ns, time.Now(), f); err != nil {
		t.Fatal(err)
	}
	if err := k8sClient.Get(ctx, key, &dev); err != nil {
		t.Fatal(err)
	}
	if dev.Status.AvailableFirmware != "" {
		t.Fatalf("availableFirmware after current = %q", dev.Status.AvailableFirmware)
	}
}

func TestProbeAllReadsAvailableFirmware(t *testing.T) {
	d := &shellytest.Device{ID: "dev1", MAC: "AABBCCDDEF11", Model: "SNPL-00112UK", App: "PlusPlugUK", Gen: 2,
		AvailableUpdates: map[string]any{"stable": map[string]any{"version": testFirmwareVersion}}}
	srv := shellytest.New(d)
	defer srv.Close()
	hc := &http.Client{Timeout: 3 * time.Second}
	found := probeAll(context.Background(), hc, []string{hostOf(srv.URL)}, 4)
	if len(found) != 1 {
		t.Fatalf("found = %+v", found)
	}
	if found[0].AvailableFirmware == nil || *found[0].AvailableFirmware != testFirmwareVersion {
		t.Fatalf("availableFirmware = %v", found[0].AvailableFirmware)
	}
}
