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
)

// Metric descriptions shared across all Collect calls (created once).
var (
	descOnline = prometheus.NewDesc(
		"shelly_device_online",
		"1 if the device is online (answers discovery sweeps), 0 otherwise",
		[]string{"mac", "name"}, nil,
	)

	descInSync = prometheus.NewDesc(
		"shelly_device_in_sync",
		"1 if the device InSync condition is True, 0 otherwise",
		[]string{"mac", "name"}, nil,
	)

	descUpdateAvailable = prometheus.NewDesc(
		"shelly_device_update_available",
		"1 if a firmware update is available for the device, 0 otherwise",
		[]string{"mac", "name"}, nil,
	)
)

// DeviceCollector is a prometheus.Collector that emits per-device fleet-health
// gauges by listing ShellyDevice CRs on every scrape. Using a collector (rather
// than per-reconcile gauge.Set calls) avoids stale series when devices are
// deleted and eliminates per-reconcile metric churn.
type DeviceCollector struct {
	reader    client.Reader
	namespace string
}

// NewDeviceCollector returns a DeviceCollector that lists devices from reader
// restricted to namespace.
func NewDeviceCollector(reader client.Reader, namespace string) *DeviceCollector {
	return &DeviceCollector{reader: reader, namespace: namespace}
}

// Describe implements prometheus.Collector. It sends the descriptors for all
// three gauge families to the channel.
func (c *DeviceCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descOnline
	ch <- descInSync
	ch <- descUpdateAvailable
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

	for i := range list.Items {
		dev := &list.Items[i]
		mac, name := labelValues(dev)

		ch <- prometheus.MustNewConstMetric(descOnline, prometheus.GaugeValue,
			boolToFloat(dev.Status.Online), mac, name)

		ch <- prometheus.MustNewConstMetric(descInSync, prometheus.GaugeValue,
			inSyncValue(dev.Status.Conditions), mac, name)

		ch <- prometheus.MustNewConstMetric(descUpdateAvailable, prometheus.GaugeValue,
			boolToFloat(dev.Status.AvailableFirmware != ""), mac, name)
	}
}

// labelValues returns the mac and name label values for a device.
// mac: status.MAC if set, otherwise the object name (lowercased MAC).
// name: status.DeviceName if set, otherwise the object name.
func labelValues(dev *shellyv1alpha1.ShellyDevice) (mac, name string) {
	mac = dev.Status.MAC
	if mac == "" {
		mac = dev.Name
	}
	name = dev.Status.DeviceName
	if name == "" {
		name = dev.Name
	}
	return mac, name
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
