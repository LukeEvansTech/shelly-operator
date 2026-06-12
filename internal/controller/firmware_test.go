package controller

import (
	"testing"

	"github.com/LukeEvansTech/shelly-operator/internal/shelly"
)

// jobs below mirror real fleet shapes (verified on hardware 2026-06-12).
var (
	appCreatedStableJob = shelly.ScheduleJob{ID: 1, Enable: true,
		Timespec: "0 0 0 * * SUN,MON,TUE,WED,THU,FRI,SAT",
		Calls:    []shelly.ScheduleCall{{Method: "Shelly.Update", Params: map[string]any{"stage": "stable"}}}}
	oddTimespecStableJob = shelly.ScheduleJob{ID: 2, Enable: true,
		Timespec: "0 30 4 * * MON",
		Calls:    []shelly.ScheduleCall{{Method: "Shelly.Update", Params: map[string]any{"stage": "stable"}}}}
	stageAbsentJob = shelly.ScheduleJob{ID: 3, Enable: true, Timespec: "@daily",
		Calls: []shelly.ScheduleCall{{Method: "Shelly.Update"}}}
	betaJob = shelly.ScheduleJob{ID: 4, Enable: true, Timespec: "@daily",
		Calls: []shelly.ScheduleCall{{Method: "Shelly.Update", Params: map[string]any{"stage": "beta"}}}}
	disabledStableJob = shelly.ScheduleJob{ID: 5, Enable: false,
		Timespec: "0 0 0 * * SUN,MON,TUE,WED,THU,FRI,SAT",
		Calls:    []shelly.ScheduleCall{{Method: "Shelly.Update", Params: map[string]any{"stage": "stable"}}}}
	unrelatedJob = shelly.ScheduleJob{ID: 6, Enable: true, Timespec: "@sunset",
		Calls: []shelly.ScheduleCall{{Method: "Switch.Set", Params: map[string]any{"id": 0, "on": true}}}}
)

func TestFirmwareFindings(t *testing.T) {
	cases := []struct {
		name string
		want bool
		jobs []shelly.ScheduleJob
		n    int // expected finding count
	}{
		{"want on, app job present", true, []shelly.ScheduleJob{appCreatedStableJob}, 0},
		{"want on, different timespec still compliant", true, []shelly.ScheduleJob{oddTimespecStableJob}, 0},
		{"want on, absent stage defaults to stable", true, []shelly.ScheduleJob{stageAbsentJob}, 0},
		{"want on, no jobs", true, nil, 1},
		{"want on, only unrelated jobs", true, []shelly.ScheduleJob{unrelatedJob}, 1},
		{"want on, beta job is drift plus missing stable", true, []shelly.ScheduleJob{betaJob}, 2},
		{"want on, beta alongside stable", true, []shelly.ScheduleJob{appCreatedStableJob, betaJob}, 1},
		{"want on, disabled stable job", true, []shelly.ScheduleJob{disabledStableJob}, 2},
		{"want off, no jobs", false, nil, 0},
		{"want off, unrelated job untouched", false, []shelly.ScheduleJob{unrelatedJob}, 0},
		{"want off, stable job is drift", false, []shelly.ScheduleJob{appCreatedStableJob}, 1},
		{"want off, beta job is drift", false, []shelly.ScheduleJob{betaJob}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := firmwareFindings(tc.want, tc.jobs)
			if len(fs) != tc.n {
				t.Fatalf("findings = %+v, want %d", fs, tc.n)
			}
			for _, f := range fs {
				if f.Section != sectionFirmware {
					t.Errorf("finding section = %q", f.Section)
				}
			}
		})
	}
}

func TestFirmwareOffendingJobs(t *testing.T) {
	// want=true: beta and disabled jobs offend; stable enabled and
	// unrelated jobs survive.
	offend, hasCompliant := firmwareOffenders(true,
		[]shelly.ScheduleJob{appCreatedStableJob, betaJob, disabledStableJob, unrelatedJob})
	if !hasCompliant {
		t.Error("expected a compliant job")
	}
	if len(offend) != 2 || offend[0] != betaJob.ID || offend[1] != disabledStableJob.ID {
		t.Fatalf("offenders = %v", offend)
	}
	// want=false: every update job offends; unrelated survives.
	offend, hasCompliant = firmwareOffenders(false,
		[]shelly.ScheduleJob{appCreatedStableJob, betaJob, unrelatedJob})
	if hasCompliant {
		t.Error("no job can be compliant when autoUpdate is false")
	}
	if len(offend) != 2 {
		t.Fatalf("offenders = %v", offend)
	}
}
