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

const (
	// ModeObserve reports drift without correcting it.
	ModeObserve = "observe"
	// ModeEnforce reports and corrects drift. Until enforcement ships it
	// behaves like observe plus an EnforcementPending event.
	ModeEnforce = "enforce"
)

// ShellyProfileSpec declares desired configuration for a set of devices.
type ShellyProfileSpec struct {
	// Selector matches ShellyDevices by their discovery labels
	// (shelly.thirdimpact.io/model, /app, /gen). A nil selector matches no
	// devices — such a profile applies only via ShellyDevice
	// spec.profileRef. An empty non-nil selector matches every device in
	// the namespace.
	// +optional
	Selector *metav1.LabelSelector `json:"selector,omitempty"`

	// Priority breaks ties when several profiles match a device: highest
	// wins, then lexicographically smallest profile name. Omitted means
	// priority 0.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Priority int32 `json:"priority,omitempty"`

	// Mode controls whether drift is only reported (observe) or also
	// corrected on the device (enforce). Enforcement ships in a later
	// release; until then enforce behaves like observe and the controller
	// records an EnforcementPending event.
	// +kubebuilder:validation:Enum=observe;enforce
	// +kubebuilder:default=observe
	// +optional
	Mode string `json:"mode,omitempty"`

	// Config declares the desired device configuration. Omitted sections
	// are unmanaged.
	// +optional
	Config ProfileConfig `json:"config,omitempty"`
}

// ProfileConfig declares desired device configuration. Every section is
// optional; omitted sections and omitted fields within a section are
// unmanaged. Secret-backed fields (passwords) ship with enforcement.
type ProfileConfig struct {
	// +optional
	System *SystemSection `json:"system,omitempty"`
	// +optional
	Name *NameSection `json:"name,omitempty"`
	// +optional
	MQTT *MQTTSection `json:"mqtt,omitempty"`
	// +optional
	Cloud *CloudSection `json:"cloud,omitempty"`
	// +optional
	Auth *AuthSection `json:"auth,omitempty"`
	// +optional
	Switch *SwitchSection `json:"switch,omitempty"`
}

// SystemSection maps to the device's sys configuration.
type SystemSection struct {
	// EcoMode toggles the device's power-saving mode (sys.device.eco_mode).
	// +optional
	EcoMode *bool `json:"ecoMode,omitempty"`
}

// NameSection enables device-name management.
type NameSection struct {
	// Managed enables name reconciliation for matching devices. The
	// desired name is resolved from ShellyDevice spec.displayName,
	// falling back to the fleet name-map ConfigMap (configurable via the
	// operator's --name-map flag, default "shelly-names", keyed by
	// lowercased MAC). false leaves the device name untouched.
	Managed bool `json:"managed"`
}

// MQTTSection maps to the device's mqtt configuration.
// +kubebuilder:validation:XValidation:rule="!has(self.enable) || self.enable == false || (has(self.server) && self.server != ”)",message="server is required when enable is true"
type MQTTSection struct {
	// +optional
	Enable *bool `json:"enable,omitempty"`
	// Server is the MQTT broker address (host:port). Required when enable
	// is true.
	// +optional
	Server string `json:"server,omitempty"`
}

// CloudSection maps to the device's cloud configuration.
type CloudSection struct {
	// +optional
	Enable *bool `json:"enable,omitempty"`
}

// AuthSection declares whether device auth must be enabled. Drift is
// observed against the device's reported auth_en; password rollout ships
// with enforcement.
type AuthSection struct {
	// +optional
	Enable *bool `json:"enable,omitempty"`
}

// SwitchSection applies to every switch component the device exposes
// (switch:0, switch:1, ...). Per-component overrides are not supported in
// v1alpha1; all switch components on a device receive the same declared
// values.
type SwitchSection struct {
	// InitialState is the output state after power-on.
	// +kubebuilder:validation:Enum=on;off;restore_last;match_input
	// +optional
	InitialState *string `json:"initialState,omitempty"`
	// +optional
	AutoOn *bool `json:"autoOn,omitempty"`
	// AutoOnDelay in seconds.
	// +kubebuilder:validation:Minimum=0
	// +optional
	AutoOnDelay *int32 `json:"autoOnDelay,omitempty"`
	// +optional
	AutoOff *bool `json:"autoOff,omitempty"`
	// AutoOffDelay in seconds.
	// +kubebuilder:validation:Minimum=0
	// +optional
	AutoOffDelay *int32 `json:"autoOffDelay,omitempty"`
	// PowerLimit in watts; the switch turns off above it.
	// +kubebuilder:validation:Minimum=0
	// +optional
	PowerLimit *int32 `json:"powerLimit,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.mode`
// +kubebuilder:printcolumn:name="Priority",type=integer,JSONPath=`.spec.priority`

// ShellyProfile declares desired configuration for the ShellyDevices its
// selector matches. Profiles are the unit of GitOps config for the fleet.
type ShellyProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ShellyProfileSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// ShellyProfileList contains a list of ShellyProfile.
type ShellyProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ShellyProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ShellyProfile{}, &ShellyProfileList{})
}
