package fleet

import "strings"

// SanitizeLabel makes a human-supplied registry string safe as a Kubernetes
// label value: the result is lowercased; any character outside [a-z0-9._-]
// is mapped to "-"; consecutive "-" runs are collapsed to one; the value is
// trimmed to 63 characters; and leading/trailing non-alphanumeric characters
// are stripped. An empty input returns "".
//
// Examples:
//
//	"Living Room" -> "living-room"
//	"AV / Media"  -> "av-media"
func SanitizeLabel(s string) string {
	if s == "" {
		return ""
	}
	s = strings.ToLower(s)

	var b strings.Builder
	b.Grow(len(s))
	prev := rune(0)
	for _, r := range s {
		if isAlnum(r) || r == '.' || r == '_' {
			b.WriteRune(r)
			prev = r
		} else {
			// Map anything else (spaces, slashes, etc.) to '-', but collapse runs.
			if prev != '-' {
				b.WriteRune('-')
				prev = '-'
			}
		}
	}

	v := b.String()
	if len(v) > 63 {
		v = v[:63]
	}
	// Trim leading/trailing non-alphanumeric.
	v = strings.TrimFunc(v, func(r rune) bool { return !isAlnum(r) })
	return v
}

func isAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}
