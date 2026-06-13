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
	// ModeEnforce reports drift and corrects it by writing the drifted
	// sections to the device, safest-first with auth second-to-last and
	// wifi dead last.
	ModeEnforce = "enforce"
)

// ShellyProfileSpec declares desired configuration for a set of devices.
type ShellyProfileSpec struct {
	// Selector matches ShellyDevices by their discovery labels
	// (shelly.thirdimpact.io/model, /app, /gen). A nil selector matches no
	// devices -- such a profile applies only via ShellyDevice
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
	// corrected by writing the drifted sections to the device (enforce).
	// Enforcement applies safest-first with auth second-to-last and wifi
	// dead last; failures surface on the InSync condition with reason
	// ApplyFailed.
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
// unmanaged. Passwords are read from Secrets via auth.passwordSecretRef.
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
	// +optional
	Wifi *WifiSection `json:"wifi,omitempty"`
	// +optional
	Firmware *FirmwareSection `json:"firmware,omitempty"`
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
// +kubebuilder:validation:XValidation:rule="!has(self.enable) || self.enable == false || (has(self.server) && size(self.server) > 0)",message="server is required when enable is true"
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

// AuthSection declares whether device auth must be enabled, and where the
// admin password lives.
type AuthSection struct {
	// +optional
	Enable *bool `json:"enable,omitempty"`

	// PasswordSecretRef names a Secret (same namespace) and key holding
	// the device admin password. Required to enforce enable=true, and
	// used to authenticate to devices that already have auth enabled.
	// +optional
	PasswordSecretRef *SecretKeyRef `json:"passwordSecretRef,omitempty"`
}

// SecretKeyRef points at a key within a Secret in the same namespace.
type SecretKeyRef struct {
	// Name of the Secret.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Key within the Secret's data holding the value.
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
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
	// VoltageLimit in volts; the switch turns off above it.
	// +kubebuilder:validation:Minimum=0
	// +optional
	VoltageLimit *int32 `json:"voltageLimit,omitempty"`
	// CurrentLimit in amps; the switch turns off above it.
	// +kubebuilder:validation:Minimum=0
	// +optional
	CurrentLimit *int32 `json:"currentLimit,omitempty"`
	// AutorecoverVoltageErrors controls whether the switch automatically
	// re-enables itself after a voltage-limit trip.
	// +optional
	AutorecoverVoltageErrors *bool `json:"autorecoverVoltageErrors,omitempty"`
}

// WifiSection maps to the device's wifi configuration. Only the sta
// (primary) and sta1 (fallback) client networks are managed; AP and
// roaming settings are untouched. Devices never report stored WiFi
// passwords, so password drift is undetectable -- passwords are injected
// at apply time whenever a network's section is written for another
// reason (ssid or enable drift).
// +kubebuilder:validation:XValidation:rule="!has(self.sta) || !has(self.sta1) || !has(self.sta.ssid) || !has(self.sta1.ssid) || self.sta.ssid != self.sta1.ssid",message="sta and sta1 must not declare the same ssid"
type WifiSection struct {
	// Sta is the primary client network.
	// +optional
	Sta *WifiNetwork `json:"sta,omitempty"`
	// Sta1 is the fallback client network, used by the device when sta is
	// unreachable. During a migration, point sta1 at the old network so a
	// bad sta rollout cannot strand devices.
	// +optional
	Sta1 *WifiNetwork `json:"sta1,omitempty"`
}

// WifiNetwork declares one WiFi client network (wifi.sta / wifi.sta1).
// +kubebuilder:validation:XValidation:rule="!has(self.enable) || self.enable == false || (has(self.ssid) && size(self.ssid) > 0)",message="ssid is required when enable is true"
type WifiNetwork struct {
	// +optional
	Enable *bool `json:"enable,omitempty"`
	// SSID of the network. Required when enable is true.
	// +optional
	SSID string `json:"ssid,omitempty"`
	// PassSecretRef names a Secret (same namespace) and key holding the
	// network password. Omit for open networks or to keep whatever
	// password is already stored on the device.
	// +optional
	PassSecretRef *SecretKeyRef `json:"passSecretRef,omitempty"`
}

// FirmwareSection manages the device's firmware auto-update schedule
// job -- the Gen2 mechanism behind "automatic updates". It is a
// pseudo-section backed by Schedule RPCs (like auth, it is not part of
// Shelly.GetConfig). Only stable-channel updates are ever scheduled.
type FirmwareSection struct {
	// AutoUpdate declares whether the device keeps itself on the latest
	// stable firmware via a daily self-check (00:00 device-local time,
	// matching the job the Shelly app creates). Any enabled schedule job
	// calling Shelly.Update with stage "stable" satisfies it regardless
	// of timespec; jobs targeting other stages are drift. nil leaves the
	// device's setting unmanaged.
	// +optional
	AutoUpdate *bool `json:"autoUpdate,omitempty"`
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
