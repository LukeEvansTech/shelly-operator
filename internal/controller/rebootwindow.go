package controller

import (
	"fmt"
	"time"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
)

// withinRebootWindow reports whether now falls inside the profile's daily
// reboot window.
//
// A nil window means "any time", matching the behaviour before windows
// existed. A window whose End is not after its Start wraps over midnight
// (23:00-01:00), which is the shape most people want for an out-of-hours
// window and the easy thing to get wrong.
//
// Errors are returned rather than swallowed so the caller can decide; the
// caller treats an unparseable window as "do not reboot", because silently
// falling back to "any time" would turn a typo into unattended power cycling
// at an arbitrary hour.
func withinRebootWindow(now time.Time, w *shellyv1alpha1.RebootWindow) (bool, error) {
	if w == nil {
		return true, nil
	}

	loc := time.UTC
	if w.TimeZone != "" {
		l, err := time.LoadLocation(w.TimeZone)
		if err != nil {
			return false, fmt.Errorf("reboot window timeZone %q: %w", w.TimeZone, err)
		}
		loc = l
	}

	start, err := parseHHMM(w.Start)
	if err != nil {
		return false, fmt.Errorf("reboot window start: %w", err)
	}
	end, err := parseHHMM(w.End)
	if err != nil {
		return false, fmt.Errorf("reboot window end: %w", err)
	}

	local := now.In(loc)
	mins := local.Hour()*60 + local.Minute()

	if start == end {
		// A zero-width window is almost certainly a mistake, and treating it
		// as "always" would be the most dangerous possible reading.
		return false, fmt.Errorf("reboot window start and end are both %q, which never opens", w.Start)
	}
	if start < end {
		return mins >= start && mins < end, nil
	}
	// Wraps midnight: inside if after start OR before end.
	return mins >= start || mins < end, nil
}

// parseHHMM converts "HH:MM" to minutes since midnight. The CRD pattern
// already rejects malformed values, so this is defence for objects that
// predate the schema or were written by a client that skipped validation.
func parseHHMM(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("%q is not HH:MM", s)
	}
	return t.Hour()*60 + t.Minute(), nil
}
