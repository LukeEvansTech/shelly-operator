package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
	"github.com/LukeEvansTech/shelly-operator/internal/drift"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly"
)

// specIsUpdateJob reports whether a declared schedule job invokes
// Shelly.Update. Such jobs belong to the firmware section, not here, so the
// schedule section ignores them entirely -- the mirror of the device-side
// isUpdateJob filter. Without this guard a declared Shelly.Update call would
// be created on the device (where isUpdateJob then excludes it from the
// schedule domain), producing one spurious write and a permanent
// non-converging state. A CEL rule on ScheduleCallSpec also rejects it at
// admission; this is defense-in-depth for programmatic API callers.
func specIsUpdateJob(spec shellyv1alpha1.ScheduleJobSpec) bool {
	for _, c := range spec.Calls {
		if strings.EqualFold(c.Method, shellyUpdateMethod) {
			return true
		}
	}
	return false
}

// sectionSchedule is the pseudo-section name for declarative schedule
// management. Like firmware it is backed by Schedule RPCs and not part of
// Shelly.GetConfig.
const sectionSchedule = "schedule"

// scheduleAction describes a single unit of convergence work the operator
// must perform on a device schedule job.
type scheduleAction struct {
	kind    string // "create", "delete", "update"
	id      int    // device-assigned id (for delete/update)
	job     shelly.ScheduleJob
	finding drift.Finding
}

// scheduleSectionOf returns the profile's schedule section, or nil when the
// profile does not manage schedules.
func scheduleSectionOf(p *shellyv1alpha1.ShellyProfile) *shellyv1alpha1.ScheduleSection {
	return p.Spec.Config.Schedules
}

// enableOf returns the effective enable value for a ScheduleJobSpec: true
// when the Enable field is nil (default), or the pointed-to value otherwise.
func enableOf(spec shellyv1alpha1.ScheduleJobSpec) bool {
	if spec.Enable == nil {
		return true
	}
	return *spec.Enable
}

// specCalls converts a []ScheduleCallSpec to a []shelly.ScheduleCall for
// device wire format. Params JSON bytes are unmarshalled back into
// map[string]any.
func specCalls(calls []shellyv1alpha1.ScheduleCallSpec) ([]shelly.ScheduleCall, error) {
	out := make([]shelly.ScheduleCall, 0, len(calls))
	for _, c := range calls {
		sc := shelly.ScheduleCall{Method: c.Method}
		if c.Params != nil && len(c.Params.Raw) > 0 {
			if err := json.Unmarshal(c.Params.Raw, &sc.Params); err != nil {
				return nil, fmt.Errorf("schedule: unmarshalling params for %s: %w", c.Method, err)
			}
		}
		out = append(out, sc)
	}
	return out, nil
}

// normaliseParams returns a canonical JSON string for a params map, used
// for content-based comparison. Keys are sorted so two maps with the same
// key-value pairs but different insertion order compare equal.
func normaliseParams(p map[string]any) string {
	if len(p) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// Re-marshal via sorted key iteration for determinism.
	type kv struct {
		k string
		v any
	}
	ordered := make([]kv, 0, len(p))
	for _, k := range keys {
		ordered = append(ordered, kv{k, p[k]})
	}
	m := make(map[string]any, len(ordered))
	for _, pair := range ordered {
		m[pair.k] = pair.v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// callsMatch reports whether a device call matches a declared call spec.
// Matching is by method name and normalised params JSON so param key ordering
// does not affect the comparison.
func callsMatch(deviceCall shelly.ScheduleCall, specCall shellyv1alpha1.ScheduleCallSpec) bool {
	if deviceCall.Method != specCall.Method {
		return false
	}
	var specParams map[string]any
	if specCall.Params != nil && len(specCall.Params.Raw) > 0 {
		_ = json.Unmarshal(specCall.Params.Raw, &specParams)
	}
	return normaliseParams(deviceCall.Params) == normaliseParams(specParams)
}

// jobSpecMatchesDevice reports whether a device job matches the declared spec
// by content: same timespec and same call list (length and per-call match).
// The job's enable field is NOT part of the content match: a matching job
// with a wrong enable value is an update, not a mismatch.
func jobSpecMatchesDevice(job shelly.ScheduleJob, spec shellyv1alpha1.ScheduleJobSpec) bool {
	if job.Timespec != spec.Timespec {
		return false
	}
	if len(job.Calls) != len(spec.Calls) {
		return false
	}
	for i, c := range job.Calls {
		if !callsMatch(c, spec.Calls[i]) {
			return false
		}
	}
	return true
}

// scheduleActions computes the convergence plan for the non-firmware jobs on
// a device against the declared spec. It returns one action per required
// operation (create, delete, update) and one Finding per action. This
// function contains no I/O and is pure -- safe to unit-test directly.
//
// Non-update jobs that exist on the device but are not declared in the spec
// are deleted (the operator owns all non-firmware schedules when the section
// is present). A device job is matched by content (timespec + calls), not by
// id, because ids are device-assigned.
func scheduleActions(section *shellyv1alpha1.ScheduleSection, deviceJobs []shelly.ScheduleJob) []scheduleAction {
	if section == nil {
		return nil
	}

	// Partition device jobs: update jobs belong to the firmware section;
	// non-update jobs are this section's domain.
	var ownedJobs []shelly.ScheduleJob
	for _, j := range deviceJobs {
		if !isUpdateJob(j) {
			ownedJobs = append(ownedJobs, j)
		}
	}

	var actions []scheduleAction

	// Track which declared specs have been matched to a device job.
	matched := make([]bool, len(section.Jobs))
	// Track which device jobs have been matched.
	deviceMatched := make([]bool, len(ownedJobs))

	// First pass: match declared specs to device jobs by content.
	for si, spec := range section.Jobs {
		if specIsUpdateJob(spec) {
			continue // Shelly.Update jobs belong to the firmware section.
		}
		for di, dj := range ownedJobs {
			if deviceMatched[di] {
				continue
			}
			if jobSpecMatchesDevice(dj, spec) {
				matched[si] = true
				deviceMatched[di] = true
				// Check whether enable needs to be updated.
				wantEnable := enableOf(spec)
				if dj.Enable != wantEnable {
					actions = append(actions, scheduleAction{
						kind: "update",
						id:   dj.ID,
						job: shelly.ScheduleJob{
							ID:       dj.ID,
							Enable:   wantEnable,
							Timespec: dj.Timespec,
							Calls:    dj.Calls,
						},
						finding: drift.Finding{
							Section: sectionSchedule,
							Path:    fmt.Sprintf("job:%d:enable", dj.ID),
							Want:    wantEnable,
							Have:    dj.Enable,
						},
					})
				}
				break
			}
		}
	}

	// Declared specs with no device match -> create.
	for si, spec := range section.Jobs {
		if matched[si] {
			continue
		}
		if specIsUpdateJob(spec) {
			continue // never create a Shelly.Update job here; firmware owns it.
		}
		// Build the job struct; we don't have calls yet (error handled in
		// applySchedule); store spec for later.
		actions = append(actions, scheduleAction{
			kind: "create",
			job: shelly.ScheduleJob{
				Enable:   enableOf(spec),
				Timespec: spec.Timespec,
			},
			// specIdx stored implicitly via the finding path.
			finding: drift.Finding{
				Section: sectionSchedule,
				Path:    fmt.Sprintf("spec:%d:timespec=%s", si, spec.Timespec),
				Want:    "present",
				Have:    "absent",
			},
		})
		// Store spec index in the job ID field (negative sentinel) so
		// applySchedule can retrieve the full spec. We use -1-(si) to avoid
		// collision with id=0.
		actions[len(actions)-1].job.ID = -1 - si
	}

	// Device jobs with no declared match -> delete.
	for di, dj := range ownedJobs {
		if deviceMatched[di] {
			continue
		}
		actions = append(actions, scheduleAction{
			kind: "delete",
			id:   dj.ID,
			job:  dj,
			finding: drift.Finding{
				Section: sectionSchedule,
				Path:    fmt.Sprintf("job:%d", dj.ID),
				Want:    "absent",
				Have:    "present",
			},
		})
	}

	return actions
}

// appendScheduleFindings fetches the device's schedule jobs and appends the
// schedule section findings. It performs no RPC when the profile does not
// manage the schedule section (Schedules == nil).
func appendScheduleFindings(ctx context.Context, c *shelly.Client, profile *shellyv1alpha1.ShellyProfile, fs []drift.Finding) ([]drift.Finding, error) {
	sec := scheduleSectionOf(profile)
	if sec == nil {
		return fs, nil
	}
	jobs, err := c.ListSchedules(ctx)
	if err != nil {
		return fs, err
	}
	actions := scheduleActions(sec, jobs)
	for _, a := range actions {
		fs = append(fs, a.finding)
	}
	return fs, nil
}

// applySchedule converges the device's non-firmware schedule jobs to the
// declared state. It re-fetches the job list (to get current ids) and then
// performs creates, updates, and deletes in that order.
func applySchedule(ctx context.Context, c *shelly.Client, profile *shellyv1alpha1.ShellyProfile) error {
	sec := scheduleSectionOf(profile)
	if sec == nil {
		return nil
	}
	jobs, err := c.ListSchedules(ctx)
	if err != nil {
		return fmt.Errorf("listing schedule jobs: %w", err)
	}
	actions := scheduleActions(sec, jobs)

	// Apply in actions order: updates, then creates, then deletes (the order
	// scheduleActions appends them). Creates always precede deletes, so a
	// changed job's replacement exists before the old one is removed -- the
	// device never has a momentarily-missing job.
	for _, a := range actions {
		switch a.kind {
		case "create":
			// Recover the spec index from the sentinel job ID.
			si := -1 - a.job.ID
			if si < 0 || si >= len(sec.Jobs) {
				return fmt.Errorf("schedule: internal: invalid spec index %d", si)
			}
			spec := sec.Jobs[si]
			calls, err := specCalls(spec.Calls)
			if err != nil {
				return err
			}
			job := shelly.ScheduleJob{
				Enable:   enableOf(spec),
				Timespec: spec.Timespec,
				Calls:    calls,
			}
			if _, err := c.CreateSchedule(ctx, job); err != nil {
				return fmt.Errorf("creating schedule job %q: %w", spec.Timespec, err)
			}
		case "update":
			if err := c.UpdateSchedule(ctx, a.job); err != nil {
				return fmt.Errorf("updating schedule job %d: %w", a.id, err)
			}
		case "delete":
			if err := c.DeleteSchedule(ctx, a.id); err != nil {
				return fmt.Errorf("deleting schedule job %d: %w", a.id, err)
			}
		}
	}
	return nil
}
