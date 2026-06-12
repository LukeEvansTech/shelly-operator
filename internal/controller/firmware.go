package controller

import (
	"context"
	"fmt"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
	"github.com/LukeEvansTech/shelly-operator/internal/drift"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly"
)

// sectionFirmware is the pseudo-section for the firmware auto-update
// schedule job. Like auth it is not part of Shelly.GetConfig; drift is
// evaluated against Schedule.List and enforced via Schedule.Create and
// Schedule.Delete.
const sectionFirmware = "firmware"

// autoUpdateTimespec matches the job the Shelly app creates when
// "automatic updates" is toggled on, so operator-created jobs are
// indistinguishable from app-created ones. Compliance checking is
// timespec-agnostic; this is only what new jobs are created with.
const autoUpdateTimespec = "0 0 0 * * SUN,MON,TUE,WED,THU,FRI,SAT"

// firmwareEnableOf returns the profile's desired auto-update state
// (nil = section unmanaged).
func firmwareEnableOf(p *shellyv1alpha1.ShellyProfile) *bool {
	if p.Spec.Config.Firmware == nil {
		return nil
	}
	return p.Spec.Config.Firmware.AutoUpdate
}

// isUpdateJob reports whether the job invokes Shelly.Update at all.
// Jobs that don't are invisible to this section: never reported as
// drift, never deleted.
func isUpdateJob(j shelly.ScheduleJob) bool {
	for _, c := range j.Calls {
		if c.Method == "Shelly.Update" {
			return true
		}
	}
	return false
}

// hasNonStableUpdateCall reports whether any Shelly.Update call in the
// job targets a stage other than stable. An absent stage defaults to
// stable on the device.
func hasNonStableUpdateCall(j shelly.ScheduleJob) bool {
	for _, c := range j.Calls {
		if c.Method != "Shelly.Update" {
			continue
		}
		if stage, ok := c.Params["stage"]; ok {
			if s, _ := stage.(string); s != "stable" {
				return true
			}
		}
	}
	return false
}

// firmwareOffenders classifies update jobs against the desired state.
// It returns the ids of jobs enforcement must delete (in input order)
// and whether at least one compliant job exists. A job is compliant
// when autoUpdate is wanted, the job is enabled, and all its update
// calls target stable; with autoUpdate unwanted every update job
// offends. The classification is exactly the one firmwareFindings
// reports, so observe and enforce agree.
func firmwareOffenders(want bool, jobs []shelly.ScheduleJob) (offenders []int, hasCompliant bool) {
	for _, j := range jobs {
		if !isUpdateJob(j) {
			continue
		}
		if want && j.Enable && !hasNonStableUpdateCall(j) {
			hasCompliant = true
			continue
		}
		offenders = append(offenders, j.ID)
	}
	return offenders, hasCompliant
}

// firmwareFindings evaluates the firmware pseudo-section: one finding
// per offending job, plus one when auto-update is wanted but no
// compliant job exists.
func firmwareFindings(want bool, jobs []shelly.ScheduleJob) []drift.Finding {
	var fs []drift.Finding
	for _, j := range jobs {
		if !isUpdateJob(j) {
			continue
		}
		switch {
		case hasNonStableUpdateCall(j):
			fs = append(fs, drift.Finding{Section: sectionFirmware, Path: fmt.Sprintf("job:%d", j.ID),
				Want: "no non-stable Shelly.Update job", Have: "non-stable stage"})
		case !want:
			fs = append(fs, drift.Finding{Section: sectionFirmware, Path: fmt.Sprintf("job:%d", j.ID),
				Want: "no Shelly.Update job", Have: "present"})
		case !j.Enable:
			fs = append(fs, drift.Finding{Section: sectionFirmware, Path: fmt.Sprintf("job:%d", j.ID),
				Want: "enabled", Have: "disabled"})
		}
	}
	if _, hasCompliant := firmwareOffenders(want, jobs); want && !hasCompliant {
		fs = append(fs, drift.Finding{Section: sectionFirmware, Path: "autoUpdate", Want: true, Have: false})
	}
	return fs
}

// appendFirmwareFindings fetches the device's schedule jobs and appends
// the firmware pseudo-section findings. It performs no RPC when the
// profile does not manage the section.
func appendFirmwareFindings(ctx context.Context, c *shelly.Client, profile *shellyv1alpha1.ShellyProfile, fs []drift.Finding) ([]drift.Finding, error) {
	want := firmwareEnableOf(profile)
	if want == nil {
		return fs, nil
	}
	jobs, err := c.ListSchedules(ctx)
	if err != nil {
		return fs, err
	}
	return append(fs, firmwareFindings(*want, jobs)...), nil
}

// applyFirmware converges the device's update jobs on the desired
// auto-update state: offending jobs are deleted, and when auto-update
// is wanted and no compliant job survives, the standard daily job is
// created. Existing compliant jobs (any timespec, including the
// app-created shelly_service one) are never touched.
func applyFirmware(ctx context.Context, c *shelly.Client, want bool) error {
	jobs, err := c.ListSchedules(ctx)
	if err != nil {
		return fmt.Errorf("listing schedule jobs: %w", err)
	}
	offenders, hasCompliant := firmwareOffenders(want, jobs)
	for _, id := range offenders {
		if err := c.DeleteSchedule(ctx, id); err != nil {
			return fmt.Errorf("deleting schedule job %d: %w", id, err)
		}
	}
	if want && !hasCompliant {
		if _, err := c.CreateSchedule(ctx, shelly.ScheduleJob{
			Enable:   true,
			Timespec: autoUpdateTimespec,
			Calls:    []shelly.ScheduleCall{{Method: "Shelly.Update", Params: map[string]any{"stage": "stable"}}},
		}); err != nil {
			return fmt.Errorf("creating auto-update job: %w", err)
		}
	}
	return nil
}
