package v1alpha1

// ConditionInSync reports whether a device's configuration matches its
// matched ShellyProfile.
const ConditionInSync = "InSync"

// Reasons for the InSync condition.
const (
	ReasonInSync  = "InSync"
	ReasonDrifted = "Drifted"
	// ReasonNoProfile: no ShellyProfile selector matches this device.
	ReasonNoProfile = "NoProfile"
	// ReasonProfileNotFound: spec.profileRef names a profile that does not exist.
	ReasonProfileNotFound   = "ProfileNotFound"
	ReasonOffline           = "Offline"
	ReasonPaused            = "Paused"
	ReasonConfigFetchFailed = "ConfigFetchFailed"
	// ReasonAuthRequired: the device requires digest auth credentials the
	// controller does not have yet (configure
	// spec.config.auth.passwordSecretRef).
	ReasonAuthRequired = "AuthRequired"
	// ReasonApplyFailed: enforcement wrote to the device and a section
	// apply failed; earlier sections may have been applied.
	ReasonApplyFailed = "ApplyFailed"
	// ReasonCredentialsError: the profile references a password Secret
	// that cannot be read (missing Secret/key or API error).
	ReasonCredentialsError = "CredentialsError"
	// ReasonNotConverging: enforcement wrote the drifted sections but the
	// device still reports the same drift; writes are paused for these
	// sections until the diff changes (protects device flash from rewrite
	// loops).
	ReasonNotConverging = "NotConverging"
)
