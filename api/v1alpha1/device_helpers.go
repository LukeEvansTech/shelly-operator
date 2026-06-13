package v1alpha1

import (
	"strconv"
	"strings"
)

// Label keys set by discovery; ShellyProfile selectors match against these.
const (
	LabelModel = "shelly.thirdimpact.io/model"
	LabelApp   = "shelly.thirdimpact.io/app"
	LabelGen   = "shelly.thirdimpact.io/gen"
)

// Label/annotation keys stamped from the device registry ConfigMap.
const (
	LabelRoom      = "shelly.thirdimpact.io/room"
	LabelAppliance = "shelly.thirdimpact.io/appliance"
	AnnotationNote = "shelly.thirdimpact.io/note"
)

// DeviceObjectName derives the ShellyDevice object name from a device MAC:
// lowercase hex with separators stripped, e.g. "3C:8A:1F:EC:8E:3C" ->
// "3c8a1fec8e3c".
// The caller must ensure mac is non-empty (an empty MAC would yield an
// invalid object name); discovery skips devices that report no MAC.
func DeviceObjectName(mac string) string {
	mac = strings.NewReplacer(":", "", "-", "").Replace(mac)
	return strings.ToLower(mac)
}

// sanitizeLabelValue makes a firmware-supplied string safe as a Kubernetes
// label value: characters outside [A-Za-z0-9._-] become '_', the result is
// trimmed to start/end alphanumeric, and capped at 63 characters. Shelly's
// current model/app naming never triggers this, but firmware strings are
// not a contract worth crashing a sweep over.
func sanitizeLabelValue(s string) string {
	isAlnum := func(r rune) bool {
		return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
	}
	var b strings.Builder
	for _, r := range s {
		if isAlnum(r) || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	v := b.String()
	if len(v) > 63 {
		v = v[:63]
	}
	return strings.TrimFunc(v, func(r rune) bool { return !isAlnum(r) })
}

// DeviceLabels returns the selector labels for a device's identity.
func DeviceLabels(model, app string, gen int32) map[string]string {
	return map[string]string{
		LabelModel: sanitizeLabelValue(model),
		LabelApp:   sanitizeLabelValue(app),
		LabelGen:   strconv.Itoa(int(gen)),
	}
}
