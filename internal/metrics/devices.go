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

// Package metrics implements Prometheus collectors for fleet-health observability.
package metrics

import (
	"context"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
	"github.com/LukeEvansTech/shelly-operator/internal/fleet"
)

// Metric descriptions shared across all Collect calls (created once).
var (
	descOnline = prometheus.NewDesc(
		"shelly_device_online",
		"1 if the device is online (answers discovery sweeps), 0 otherwise",
		[]string{"mac", "name", "room", "appliance"}, nil,
	)

	descInSync = prometheus.NewDesc(
		"shelly_device_in_sync",
		"1 if the device InSync condition is True, 0 otherwise",
		[]string{"mac", "name", "room", "appliance"}, nil,
	)

	descUpdateAvailable = prometheus.NewDesc(
		"shelly_device_update_available",
		"1 if a firmware update is available for the device, 0 otherwise",
		[]string{"mac", "name", "room", "appliance"}, nil,
	)

	// Deliberately its own series rather than something folded into
	// shelly_device_in_sync: a device pending a restart reports its config as
	// already written, so it is in sync AND not yet doing what was asked.
	// Without this the state is invisible -- the operator's RestartRequired
	// Event expires within the hour, long before anyone reboots the device.
	descRestartRequired = prometheus.NewDesc(
		"shelly_device_restart_required",
		"1 if the device reports a setting that needs a restart to take effect, 0 otherwise",
		[]string{"mac", "name", "room", "appliance"}, nil,
	)
)

// DeviceCollector is a prometheus.Collector that emits per-device fleet-health
// gauges by listing ShellyDevice CRs on every scrape. Using a collector (rather
// than per-reconcile gauge.Set calls) avoids stale series when devices are
// deleted and eliminates per-reconcile metric churn.
type DeviceCollector struct {
	reader       client.Reader
	namespace    string
	registryName string
}

// NewDeviceCollector returns a DeviceCollector that lists devices from reader
// restricted to namespace. registryName is the device-registry ConfigMap used
// to resolve the friendly name/room/appliance labels ("" disables registry
// resolution, leaving name to the on-device name / MAC fallback).
func NewDeviceCollector(reader client.Reader, namespace, registryName string) *DeviceCollector {
	return &DeviceCollector{reader: reader, namespace: namespace, registryName: registryName}
}

// Describe implements prometheus.Collector. It sends the descriptors for all
// four gauge families to the channel.
func (c *DeviceCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descOnline
	ch <- descInSync
	ch <- descUpdateAvailable
	ch <- descRestartRequired
}

// Collect implements prometheus.Collector. It lists all ShellyDevices in the
// configured namespace and emits one sample per device per metric.
func (c *DeviceCollector) Collect(ch chan<- prometheus.Metric) {
	var list shellyv1alpha1.ShellyDeviceList
	if err := c.reader.List(context.Background(), &list, client.InNamespace(c.namespace)); err != nil {
		// Report a collection error so Prometheus knows the scrape was partial.
		ch <- prometheus.NewInvalidMetric(descOnline, fmt.Errorf("listing ShellyDevices: %w", err))
		return
	}

	ctx := context.Background()
	for i := range list.Items {
		dev := &list.Items[i]
		mac, name, room, appliance := c.labelValues(ctx, dev)

		ch <- prometheus.MustNewConstMetric(descOnline, prometheus.GaugeValue,
			boolToFloat(dev.Status.Online), mac, name, room, appliance)

		ch <- prometheus.MustNewConstMetric(descInSync, prometheus.GaugeValue,
			inSyncValue(dev.Status.Conditions), mac, name, room, appliance)

		ch <- prometheus.MustNewConstMetric(descUpdateAvailable, prometheus.GaugeValue,
			boolToFloat(dev.Status.AvailableFirmware != ""), mac, name, room, appliance)

		ch <- prometheus.MustNewConstMetric(descRestartRequired, prometheus.GaugeValue,
			boolToFloat(dev.Status.RestartRequired), mac, name, room, appliance)
	}
}

// labelValues returns the mac, name, room and appliance label values for a
// device.
//
//	mac:       status.MAC if set, otherwise the object name (lowercased MAC).
//	name:      spec.displayName, else the registry name, else the on-device
//	           name (status.DeviceName), else the object name. Resolving from
//	           the registry means offline/never-named devices still get a
//	           friendly name -- which is exactly when alerts need it.
//	room:      the registry entry's room (unsanitized, e.g. "Master Bedroom").
//	appliance: the registry entry's type (e.g. "sonos"). Both "" when unset.
//
// A registry read error degrades gracefully to the empty entry (name falls
// back to the on-device name / MAC) rather than failing the scrape.
func (c *DeviceCollector) labelValues(ctx context.Context, dev *shellyv1alpha1.ShellyDevice) (mac, name, room, appliance string) {
	mac = dev.Status.MAC
	if mac == "" {
		mac = dev.Name
	}

	entry, err := fleet.ResolveRegistry(ctx, c.reader, dev, c.registryName)
	if err != nil {
		entry = fleet.RegistryEntry{}
	}

	name = dev.Spec.DisplayName
	if name == "" {
		name = entry.Name
	}
	if name == "" {
		name = dev.Status.DeviceName
	}
	if name == "" {
		name = dev.Name
	}

	return mac, name, entry.Room, entry.Type
}

// inSyncValue returns 1 if the InSync condition is True, 0 for all other
// states (False, Unknown, or absent).
func inSyncValue(conditions []metav1.Condition) float64 {
	for _, c := range conditions {
		if c.Type == shellyv1alpha1.ConditionInSync {
			if c.Status == metav1.ConditionTrue {
				return 1
			}
			return 0
		}
	}
	return 0 // absent
}

// boolToFloat converts a bool to a Prometheus gauge value.
func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
