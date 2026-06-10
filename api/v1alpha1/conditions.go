package v1alpha1

// ConditionInSync reports whether a device's configuration matches its
// matched ShellyProfile.
const ConditionInSync = "InSync"

// Reasons for the InSync condition.
const (
	ReasonInSync            = "InSync"
	ReasonDrifted           = "Drifted"
	ReasonNoProfile         = "NoProfile"
	ReasonProfileNotFound   = "ProfileNotFound"
	ReasonOffline           = "Offline"
	ReasonPaused            = "Paused"
	ReasonConfigFetchFailed = "ConfigFetchFailed"
)
