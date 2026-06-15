/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package metrics_test

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
	"github.com/LukeEvansTech/shelly-operator/internal/metrics"
)

const testNamespace = "shelly-devices"

// newScheme builds a scheme with the shelly types registered.
func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = shellyv1alpha1.AddToScheme(s)
	return s
}

// makeDevice creates a ShellyDevice with the given status for use in tests.
func makeDevice(name, mac, deviceName string, online bool, inSyncStatus metav1.ConditionStatus, availFW string) *shellyv1alpha1.ShellyDevice {
	dev := &shellyv1alpha1.ShellyDevice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Status: shellyv1alpha1.ShellyDeviceStatus{
			MAC:               mac,
			DeviceName:        deviceName,
			Online:            online,
			AvailableFirmware: availFW,
		},
	}
	if inSyncStatus != "" {
		dev.Status.Conditions = []metav1.Condition{
			{
				Type:               shellyv1alpha1.ConditionInSync,
				Status:             inSyncStatus,
				Reason:             "Test",
				LastTransitionTime: metav1.Now(),
			},
		}
	}
	return dev
}

// TestDeviceCollector_Online verifies shelly_device_online gauge values.
func TestDeviceCollector_Online(t *testing.T) {
	onlineDev := makeDevice("aabbcc001100", "AABBCC001100", "living-room", true, metav1.ConditionTrue, "")
	offlineDev := makeDevice("aabbcc001101", "AABBCC001101", "kitchen", false, metav1.ConditionFalse, "")

	s := newScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&shellyv1alpha1.ShellyDevice{}).
		WithObjects(onlineDev, offlineDev).
		Build()

	col := metrics.NewDeviceCollector(fakeClient, testNamespace, "")

	reg := prometheus.NewPedanticRegistry()
	if err := reg.Register(col); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Expect exactly 2 samples: one per device.
	count := testutil.CollectAndCount(col, "shelly_device_online")
	if count != 2 {
		t.Errorf("shelly_device_online: want 2 samples, got %d", count)
	}

	// Verify exact values via text comparison.
	expected := `# HELP shelly_device_online 1 if the device is online (answers discovery sweeps), 0 otherwise
# TYPE shelly_device_online gauge
shelly_device_online{appliance="",mac="AABBCC001100",name="living-room",room=""} 1
shelly_device_online{appliance="",mac="AABBCC001101",name="kitchen",room=""} 0
`
	if err := testutil.CollectAndCompare(col, strings.NewReader(expected), "shelly_device_online"); err != nil {
		t.Errorf("shelly_device_online mismatch:\n%v", err)
	}
}

// TestDeviceCollector_InSync verifies shelly_device_in_sync:
// True -> 1, False -> 0, Unknown -> 0, absent -> 0.
func TestDeviceCollector_InSync(t *testing.T) {
	devTrue := makeDevice("aabbcc000001", "AABBCC000001", "dev-true", true, metav1.ConditionTrue, "")
	devFalse := makeDevice("aabbcc000002", "AABBCC000002", "dev-false", true, metav1.ConditionFalse, "")
	devUnknown := makeDevice("aabbcc000003", "AABBCC000003", "dev-unknown", true, metav1.ConditionUnknown, "")
	devAbsent := makeDevice("aabbcc000004", "AABBCC000004", "dev-absent", true, "", "") // no condition

	s := newScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&shellyv1alpha1.ShellyDevice{}).
		WithObjects(devTrue, devFalse, devUnknown, devAbsent).
		Build()

	col := metrics.NewDeviceCollector(fakeClient, testNamespace, "")

	expected := `# HELP shelly_device_in_sync 1 if the device InSync condition is True, 0 otherwise
# TYPE shelly_device_in_sync gauge
shelly_device_in_sync{appliance="",mac="AABBCC000001",name="dev-true",room=""} 1
shelly_device_in_sync{appliance="",mac="AABBCC000002",name="dev-false",room=""} 0
shelly_device_in_sync{appliance="",mac="AABBCC000003",name="dev-unknown",room=""} 0
shelly_device_in_sync{appliance="",mac="AABBCC000004",name="dev-absent",room=""} 0
`
	if err := testutil.CollectAndCompare(col, strings.NewReader(expected), "shelly_device_in_sync"); err != nil {
		t.Errorf("shelly_device_in_sync mismatch:\n%v", err)
	}
}

// TestDeviceCollector_UpdateAvailable verifies shelly_device_update_available:
// non-empty availableFirmware -> 1, empty -> 0.
func TestDeviceCollector_UpdateAvailable(t *testing.T) {
	devUpdate := makeDevice("aabbcc002200", "AABBCC002200", "needs-update", true, metav1.ConditionTrue, "1.5.0")
	devCurrent := makeDevice("aabbcc002201", "AABBCC002201", "up-to-date", true, metav1.ConditionTrue, "")

	s := newScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&shellyv1alpha1.ShellyDevice{}).
		WithObjects(devUpdate, devCurrent).
		Build()

	col := metrics.NewDeviceCollector(fakeClient, testNamespace, "")

	expected := `# HELP shelly_device_update_available 1 if a firmware update is available for the device, 0 otherwise
# TYPE shelly_device_update_available gauge
shelly_device_update_available{appliance="",mac="AABBCC002200",name="needs-update",room=""} 1
shelly_device_update_available{appliance="",mac="AABBCC002201",name="up-to-date",room=""} 0
`
	if err := testutil.CollectAndCompare(col, strings.NewReader(expected), "shelly_device_update_available"); err != nil {
		t.Errorf("shelly_device_update_available mismatch:\n%v", err)
	}
}

// TestDeviceCollector_LabelValues verifies that mac label uses status.MAC and
// name label uses status.DeviceName, falling back to object name when DeviceName is empty.
func TestDeviceCollector_LabelValues(t *testing.T) {
	// Device with DeviceName set.
	devWithName := makeDevice("aabbcc003300", "AABBCC003300", "my-plug", true, metav1.ConditionTrue, "")
	// Device with no DeviceName -- should fall back to object name.
	devNoName := makeDevice("aabbcc003301", "AABBCC003301", "", true, metav1.ConditionTrue, "")

	s := newScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&shellyv1alpha1.ShellyDevice{}).
		WithObjects(devWithName, devNoName).
		Build()

	col := metrics.NewDeviceCollector(fakeClient, testNamespace, "")

	expected := `# HELP shelly_device_online 1 if the device is online (answers discovery sweeps), 0 otherwise
# TYPE shelly_device_online gauge
shelly_device_online{appliance="",mac="AABBCC003300",name="my-plug",room=""} 1
shelly_device_online{appliance="",mac="AABBCC003301",name="aabbcc003301",room=""} 1
`
	if err := testutil.CollectAndCompare(col, strings.NewReader(expected), "shelly_device_online"); err != nil {
		t.Errorf("label values mismatch:\n%v", err)
	}
}

// TestDeviceCollector_RegistryLabels verifies name/room/appliance resolve from
// the device registry ConfigMap: the registry name wins over the on-device name,
// spec.displayName wins over the registry, and a device with no registry entry
// falls back (name -> on-device name -> object name; room/appliance empty).
func TestDeviceCollector_RegistryLabels(t *testing.T) {
	registry := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "shelly-registry", Namespace: testNamespace},
		Data: map[string]string{
			"3c8a1fed2134": `{"name":"Sonos Living Room","room":"Garage","type":"sonos"}`,
			"3c8a1fec6fe4": `{"name":"TV","room":"Master Bedroom","type":"tv"}`,
		},
	}
	// Resolves entirely from the registry (no on-device name).
	fromRegistry := makeDevice("3c8a1fed2134", "3C8A1FED2134", "", true, metav1.ConditionTrue, "")
	// spec.displayName overrides the registry name; room/appliance still from registry.
	withOverride := makeDevice("3c8a1fec6fe4", "3C8A1FEC6FE4", "", true, metav1.ConditionTrue, "")
	withOverride.Spec.DisplayName = "Bedroom Telly"
	// No registry entry -> name falls back to the on-device name; room/appliance empty.
	noEntry := makeDevice("aabbcc999999", "AABBCC999999", "on-device-name", true, metav1.ConditionTrue, "")

	s := newScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&shellyv1alpha1.ShellyDevice{}).
		WithObjects(registry, fromRegistry, withOverride, noEntry).
		Build()

	col := metrics.NewDeviceCollector(fakeClient, testNamespace, "shelly-registry")

	expected := `# HELP shelly_device_online 1 if the device is online (answers discovery sweeps), 0 otherwise
# TYPE shelly_device_online gauge
shelly_device_online{appliance="tv",mac="3C8A1FEC6FE4",name="Bedroom Telly",room="Master Bedroom"} 1
shelly_device_online{appliance="sonos",mac="3C8A1FED2134",name="Sonos Living Room",room="Garage"} 1
shelly_device_online{appliance="",mac="AABBCC999999",name="on-device-name",room=""} 1
`
	if err := testutil.CollectAndCompare(col, strings.NewReader(expected), "shelly_device_online"); err != nil {
		t.Errorf("registry label resolution mismatch:\n%v", err)
	}
}

// TestDeviceCollector_EmptyNamespace verifies zero samples when no devices exist.
func TestDeviceCollector_EmptyNamespace(t *testing.T) {
	s := newScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		Build()

	col := metrics.NewDeviceCollector(fakeClient, testNamespace, "")

	for _, name := range []string{"shelly_device_online", "shelly_device_in_sync", "shelly_device_update_available"} {
		count := testutil.CollectAndCount(col, name)
		if count != 0 {
			t.Errorf("%s: want 0 samples for empty namespace, got %d", name, count)
		}
	}
}

// TestDeviceCollector_AllThreeMetrics verifies that a single scrape emits all
// three metric families for a device in a representative state.
func TestDeviceCollector_AllThreeMetrics(t *testing.T) {
	dev := makeDevice("ddeeff001122", "DDEEFF001122", "office-plug", true, metav1.ConditionTrue, "1.6.0")

	s := newScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&shellyv1alpha1.ShellyDevice{}).
		WithObjects(dev).
		Build()

	col := metrics.NewDeviceCollector(fakeClient, testNamespace, "")

	expected := `# HELP shelly_device_in_sync 1 if the device InSync condition is True, 0 otherwise
# TYPE shelly_device_in_sync gauge
shelly_device_in_sync{appliance="",mac="DDEEFF001122",name="office-plug",room=""} 1
# HELP shelly_device_online 1 if the device is online (answers discovery sweeps), 0 otherwise
# TYPE shelly_device_online gauge
shelly_device_online{appliance="",mac="DDEEFF001122",name="office-plug",room=""} 1
# HELP shelly_device_update_available 1 if a firmware update is available for the device, 0 otherwise
# TYPE shelly_device_update_available gauge
shelly_device_update_available{appliance="",mac="DDEEFF001122",name="office-plug",room=""} 1
`
	if err := testutil.CollectAndCompare(col, strings.NewReader(expected),
		"shelly_device_online", "shelly_device_in_sync", "shelly_device_update_available"); err != nil {
		t.Errorf("all-three-metrics mismatch:\n%v", err)
	}
}

// TestDeviceCollector_MACFallback verifies mac label falls back to object name
// when status.MAC is empty (device not yet probed).
func TestDeviceCollector_MACFallback(t *testing.T) {
	dev := makeDevice("aabbcc004400", "", "pending-device", false, "", "")

	s := newScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&shellyv1alpha1.ShellyDevice{}).
		WithObjects(dev).
		Build()

	col := metrics.NewDeviceCollector(fakeClient, testNamespace, "")

	expected := `# HELP shelly_device_online 1 if the device is online (answers discovery sweeps), 0 otherwise
# TYPE shelly_device_online gauge
shelly_device_online{appliance="",mac="aabbcc004400",name="pending-device",room=""} 0
`
	if err := testutil.CollectAndCompare(col, strings.NewReader(expected), "shelly_device_online"); err != nil {
		t.Errorf("MAC fallback mismatch:\n%v", err)
	}
}
