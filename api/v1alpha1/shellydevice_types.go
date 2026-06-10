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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ShellyDeviceSpec holds human-set overrides. The discovery sweeper never
// writes spec; it is reserved for users (via the fleet repo) to influence
// how this device is managed.
type ShellyDeviceSpec struct {
	// Paused suspends config reconciliation for this device. Discovery
	// still updates status.
	// +optional
	Paused bool `json:"paused,omitempty"`

	// DisplayName overrides the fleet name map for this device.
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// ProfileRef pins this device to a ShellyProfile by object name (same
	// namespace), bypassing selector matching. (Consumed by the device
	// controller in a later plan.)
	// +kubebuilder:validation:MaxLength=253
	// +optional
	ProfileRef string `json:"profileRef,omitempty"`
}

// ShellyDeviceStatus is owned by the operator: discovery records identity
// and reachability here.
type ShellyDeviceStatus struct {
	// Address is the host[:port] the device last answered a probe at.
	// +optional
	Address string `json:"address,omitempty"`

	// MAC as reported by the device (uppercase hex, no separators).
	// +optional
	MAC string `json:"mac,omitempty"`

	// Model is the device model identifier as reported by the device,
	// e.g. "SNPL-00112UK".
	// +optional
	Model string `json:"model,omitempty"`

	// App is the device application name, e.g. "PlusPlugUK".
	// +optional
	App string `json:"app,omitempty"`

	// Gen is the Shelly RPC API generation (2, 3, or 4).
	// +optional
	Gen int32 `json:"gen,omitempty"`

	// Firmware identifier, e.g. "20241011-114449/1.4.4-g6d2a586".
	// +optional
	Firmware string `json:"firmware,omitempty"`

	// AuthEnabled reports whether the device requires digest auth.
	// +optional
	AuthEnabled bool `json:"authEnabled,omitempty"`

	// DeviceName is the name currently configured on the device itself.
	// +optional
	DeviceName string `json:"deviceName,omitempty"`

	// Online is true while the device answers discovery sweeps.
	// +optional
	Online bool `json:"online,omitempty"`

	// LastSeen is when the device last answered a probe.
	// +optional
	LastSeen *metav1.Time `json:"lastSeen,omitempty"`

	// MatchedProfile is the ShellyProfile currently governing this device.
	// +optional
	MatchedProfile string `json:"matchedProfile,omitempty"`

	// DriftedSections lists config sections that differ from the matched
	// profile (empty when in sync).
	// +optional
	DriftedSections []string `json:"driftedSections,omitempty"`

	// Conditions describe the device's reconciliation state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Address",type=string,JSONPath=`.status.address`
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.status.model`
// +kubebuilder:printcolumn:name="Device Name",type=string,JSONPath=`.status.deviceName`
// +kubebuilder:printcolumn:name="Online",type=boolean,JSONPath=`.status.online`
// +kubebuilder:printcolumn:name="Last Seen",type=date,JSONPath=`.status.lastSeen`
// +kubebuilder:printcolumn:name="In Sync",type=string,JSONPath=`.status.conditions[?(@.type=="InSync")].status`

// ShellyDevice represents one discovered Shelly Gen2+ device. Objects are
// created and maintained by the discovery sweeper (named by lowercased
// MAC); humans only edit spec.
type ShellyDevice struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ShellyDeviceSpec   `json:"spec,omitempty"`
	Status ShellyDeviceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ShellyDeviceList contains a list of ShellyDevice.
type ShellyDeviceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ShellyDevice `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ShellyDevice{}, &ShellyDeviceList{})
}
