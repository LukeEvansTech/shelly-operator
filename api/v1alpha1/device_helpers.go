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

// DeviceObjectName derives the ShellyDevice object name from a device MAC:
// lowercase hex with separators stripped, e.g. "3C:8A:1F:EC:8E:3C" ->
// "3c8a1fec8e3c".
func DeviceObjectName(mac string) string {
	mac = strings.NewReplacer(":", "", "-", "").Replace(mac)
	return strings.ToLower(mac)
}

// DeviceLabels returns the selector labels for a device's identity.
func DeviceLabels(model, app string, gen int) map[string]string {
	return map[string]string{
		LabelModel: model,
		LabelApp:   app,
		LabelGen:   strconv.Itoa(gen),
	}
}
