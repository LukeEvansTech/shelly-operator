package controller

import (
	"testing"
	"time"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
)

func at(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestWithinRebootWindow(t *testing.T) {
	win := func(start, end, tz string) *shellyv1alpha1.RebootWindow {
		return &shellyv1alpha1.RebootWindow{Start: start, End: end, TimeZone: tz}
	}

	cases := []struct {
		name    string
		now     string
		window  *shellyv1alpha1.RebootWindow
		want    bool
		wantErr bool
	}{
		{name: "nil window means any time", now: "2026-08-02T03:17:00Z", window: nil, want: true},

		{name: "inside a simple window", now: "2026-08-02T09:10:00Z", window: win("09:00", "09:30", ""), want: true},
		{name: "before it opens", now: "2026-08-02T08:59:00Z", window: win("09:00", "09:30", ""), want: false},
		{name: "start is inclusive", now: "2026-08-02T09:00:00Z", window: win("09:00", "09:30", ""), want: true},
		{name: "end is exclusive", now: "2026-08-02T09:30:00Z", window: win("09:00", "09:30", ""), want: false},

		// The case that matters for this fleet: the operator container has no
		// TZ, so a bare 09:00 window is UTC. During BST that is 10:00 local.
		{
			name: "09:00 Europe/London during BST is 08:00 UTC",
			now:  "2026-08-02T08:15:00Z", window: win("09:00", "09:30", "Europe/London"), want: true,
		},
		{
			name: "09:00 UTC is NOT 09:00 London during BST",
			now:  "2026-08-02T09:15:00Z", window: win("09:00", "09:30", "Europe/London"), want: false,
		},
		{
			name: "same window in winter is 09:00 UTC",
			now:  "2026-01-15T09:15:00Z", window: win("09:00", "09:30", "Europe/London"), want: true,
		},

		{name: "wraps midnight, late side", now: "2026-08-02T23:30:00Z", window: win("23:00", "01:00", ""), want: true},
		{name: "wraps midnight, early side", now: "2026-08-02T00:30:00Z", window: win("23:00", "01:00", ""), want: true},
		{name: "wraps midnight, outside", now: "2026-08-02T12:00:00Z", window: win("23:00", "01:00", ""), want: false},

		// Fail closed: a broken window must never mean "reboot whenever".
		{name: "unknown timezone errors", now: "2026-08-02T09:15:00Z", window: win("09:00", "09:30", "Mars/Olympus"), wantErr: true},
		{name: "malformed start errors", now: "2026-08-02T09:15:00Z", window: win("9am", "09:30", ""), wantErr: true},
		{name: "zero-width window errors", now: "2026-08-02T09:00:00Z", window: win("09:00", "09:00", ""), wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := withinRebootWindow(at(t, tc.now), tc.window)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want an error, got nil")
				}
				if got {
					t.Error("an errored window must report false, never true")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("withinRebootWindow = %v, want %v", got, tc.want)
			}
		})
	}
}
