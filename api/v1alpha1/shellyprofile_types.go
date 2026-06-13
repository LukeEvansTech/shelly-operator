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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
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
	BLE *BLESection `json:"ble,omitempty"`
	// +optional
	WS *WSSection `json:"ws,omitempty"`
	// +optional
	Auth *AuthSection `json:"auth,omitempty"`
	// +optional
	Switch *SwitchSection `json:"switch,omitempty"`
	// +optional
	Wifi *WifiSection `json:"wifi,omitempty"`
	// +optional
	Firmware *FirmwareSection `json:"firmware,omitempty"`
	// Schedules manages timed schedule jobs on the device (Schedule RPCs). When
	// this section is present, the operator takes FULL OWNERSHIP of all
	// non-firmware schedule jobs on the device: any job not declared in this
	// section will be deleted. A profile without this section leaves all
	// schedules untouched.
	// +optional
	Schedules *ScheduleSection `json:"schedules,omitempty"`
	// UI manages the plug LED ring and physical button on plug models that
	// expose a *_ui component (e.g. PlusPlugUK). Relay-only devices that
	// have no *_ui component in their config are unaffected; this section
	// is a no-op for them.
	// +optional
	UI *UISection `json:"ui,omitempty"`
}

// UISection manages the plug LED ring and physical button configuration.
// It applies only to devices whose Shelly.GetConfig response includes a
// component matching the pattern ^[a-z0-9]+_ui$ (e.g. pluguk_ui).
// Relay devices with no such component are unaffected.
type UISection struct {
	// LEDMode controls what the LED ring displays.
	// power = brightness tracks power consumption,
	// switch = on/off state,
	// off = always off.
	// +kubebuilder:validation:Enum=power;switch;off
	// +optional
	LEDMode *string `json:"ledMode,omitempty"`
	// NightMode configures the LED night-mode dimming schedule.
	// +optional
	NightMode *NightMode `json:"nightMode,omitempty"`
	// ButtonInMode controls the physical button behaviour.
	// momentary = press-and-release toggles,
	// follow = output tracks button state,
	// flip = each press toggles,
	// detached = button does not affect the relay.
	// +kubebuilder:validation:Enum=momentary;follow;flip;detached
	// +optional
	ButtonInMode *string `json:"buttonInMode,omitempty"`
}

// NightMode configures LED night-mode dimming for plug models.
type NightMode struct {
	// Enable turns night-mode dimming on or off.
	// +optional
	Enable *bool `json:"enable,omitempty"`
	// Brightness is the LED brightness percentage during night mode (0-100).
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	Brightness *int32 `json:"brightness,omitempty"`
	// ActiveBetween is a [start, end] pair of "HH:MM" 24-hour local times
	// that bound the night-mode window.
	// +kubebuilder:validation:MaxItems=2
	// +optional
	ActiveBetween []string `json:"activeBetween,omitempty"`
}

// SystemSection maps to the device's sys configuration.
type SystemSection struct {
	// EcoMode toggles the device's power-saving mode (sys.device.eco_mode).
	// +optional
	EcoMode *bool `json:"ecoMode,omitempty"`
	// Discoverable controls whether the device is visible via mDNS/BLE
	// advertisement (sys.device.discoverable).
	// +optional
	Discoverable *bool `json:"discoverable,omitempty"`
	// Timezone sets the device's local timezone (sys.location.tz), e.g.
	// "Europe/London". See the Shelly API docs for the accepted tz strings.
	// +optional
	Timezone *string `json:"timezone,omitempty"`
	// Latitude sets the device's geographic latitude (sys.location.lat).
	// Must be a decimal number, e.g. "51.5074". Used for sunrise/sunset
	// schedules on the device.
	// +kubebuilder:validation:Pattern=`^-?\d+(\.\d+)?$`
	// +optional
	Latitude *string `json:"latitude,omitempty"`
	// Longitude sets the device's geographic longitude (sys.location.lon).
	// Must be a decimal number, e.g. "-0.1278".
	// +kubebuilder:validation:Pattern=`^-?\d+(\.\d+)?$`
	// +optional
	Longitude *string `json:"longitude,omitempty"`
	// SNTPServer sets the NTP server the device uses for time sync
	// (sys.sntp.server), e.g. "time.cloudflare.com".
	// +optional
	SNTPServer *string `json:"sntpServer,omitempty"`
	// DebugLevel sets the device's debug verbosity (sys.debug.level).
	// Shelly devices accept levels 0-4; 0 disables debug output. Only
	// non-negative values are accepted.
	// +kubebuilder:validation:Minimum=0
	// +optional
	DebugLevel *int32 `json:"debugLevel,omitempty"`
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

// BLESection maps to the device's ble (Bluetooth Low Energy) configuration.
type BLESection struct {
	// +optional
	Enable *bool `json:"enable,omitempty"`
}

// WSSection maps to the device's outbound WebSocket (ws) configuration.
type WSSection struct {
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

// WifiSection maps to the device's wifi configuration. The sta (primary)
// and sta1 (fallback) client networks are managed, as are the onboard AP
// and roaming settings. Devices never report stored WiFi passwords, so
// password drift is undetectable -- passwords are injected at apply time
// whenever a network's section is written for another reason (ssid or
// enable drift). AP and roam fields carry no passwords and are diffed
// directly.
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
	// AP configures the device's onboard access point (wifi.ap).
	// +optional
	AP *WifiAP `json:"ap,omitempty"`
	// Roam configures the client roaming parameters (wifi.roam).
	// +optional
	Roam *WifiRoam `json:"roam,omitempty"`
}

// WifiAP configures the device's onboard access point (wifi.ap.*).
type WifiAP struct {
	// Enable turns the onboard AP on or off (wifi.ap.enable).
	// +optional
	Enable *bool `json:"enable,omitempty"`
	// RangeExtender enables the AP range-extender mode
	// (wifi.ap.range_extender.enable).
	// +optional
	RangeExtender *bool `json:"rangeExtender,omitempty"`
}

// WifiRoam configures the client roaming parameters (wifi.roam.*).
type WifiRoam struct {
	// RSSIThreshold is the RSSI level (dBm) below which the device will
	// attempt to roam to a stronger AP (wifi.roam.rssi_thr).
	// +optional
	RSSIThreshold *int32 `json:"rssiThreshold,omitempty"`
	// Interval is the roaming scan interval in seconds
	// (wifi.roam.interval).
	// +kubebuilder:validation:Minimum=0
	// +optional
	Interval *int32 `json:"interval,omitempty"`
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

// ScheduleSection declares timed schedule jobs the operator must maintain on
// the device. When this section is present the operator takes FULL OWNERSHIP
// of the device's non-firmware schedule jobs: any job not declared here is
// treated as drift and will be deleted. The firmware auto-update job (any job
// invoking Shelly.Update) is always excluded from this ownership domain and
// is never touched by this section.
//
// WARNING: declaring a ScheduleSection on a profile opts that device in to
// schedule ownership. Pre-existing custom schedules created by the Shelly app
// or any other tool that are not listed here WILL be deleted at the next
// enforce cycle. This behaviour is intentional; the operator is the sole
// source of truth for custom schedules.
type ScheduleSection struct {
	// Jobs is the list of schedule jobs the operator must maintain.
	// +optional
	Jobs []ScheduleJobSpec `json:"jobs,omitempty"`
}

// ScheduleJobSpec declares one schedule job.
type ScheduleJobSpec struct {
	// Enable controls whether the job is active. Defaults to true when omitted.
	// +optional
	Enable *bool `json:"enable,omitempty"`
	// Timespec is a Shelly cron-like spec, e.g. "0 0 22 * * *" (seconds
	// minutes hours dom month dow). See the Shelly Schedule.Create documentation
	// for the accepted format.
	Timespec string `json:"timespec"`
	// Calls is the list of RPC invocations the job performs when it fires.
	// +kubebuilder:validation:MinItems=1
	Calls []ScheduleCallSpec `json:"calls"`
}

// ScheduleCallSpec is one RPC call a schedule job performs.
// +kubebuilder:validation:XValidation:rule="self.method != 'Shelly.Update'",message="Shelly.Update is managed by the firmware section (config.firmware.autoUpdate), not the schedule section"
type ScheduleCallSpec struct {
	// Method is the RPC method name, e.g. "Switch.Set". Shelly.Update is
	// rejected here -- firmware auto-update is managed by config.firmware.
	Method string `json:"method"`
	// Params is arbitrary JSON forwarded verbatim as the RPC method's params.
	// Use this to pass device-specific arguments, e.g. {"id":0,"on":true}.
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Params *apiextensionsv1.JSON `json:"params,omitempty"`
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
