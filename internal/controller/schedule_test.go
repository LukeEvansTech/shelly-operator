package controller

import (
	"encoding/json"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly"
)

// action kind constants (satisfies goconst lint rule).
const (
	actionCreate = "create"
	actionDelete = "delete"
	actionUpdate = "update"
)

// ---- helpers ---------------------------------------------------------------

func jsonRaw(v any) *apiextensionsv1.JSON {
	b, _ := json.Marshal(v)
	return &apiextensionsv1.JSON{Raw: b}
}

func newSection(jobs ...shellyv1alpha1.ScheduleJobSpec) *shellyv1alpha1.ScheduleSection {
	return &shellyv1alpha1.ScheduleSection{Jobs: jobs}
}

// switchSetJob creates a device Switch.Set schedule job for tests. Jobs are
// always created enabled; the enable field is not a parameter because every
// call site passes true (unparam).
func switchSetJob(id int, timespec string) shelly.ScheduleJob {
	return shelly.ScheduleJob{
		ID:       id,
		Enable:   true,
		Timespec: timespec,
		Calls:    []shelly.ScheduleCall{{Method: "Switch.Set", Params: map[string]any{"id": float64(0), "on": true}}},
	}
}

// switchSetSpec declares a Switch.Set schedule spec. id is always 0 in unit
// tests so it is not a parameter; on selects the desired output state.
func switchSetSpec(timespec string, enable *bool, on bool) shellyv1alpha1.ScheduleJobSpec {
	return shellyv1alpha1.ScheduleJobSpec{
		Enable:   enable,
		Timespec: timespec,
		Calls: []shellyv1alpha1.ScheduleCallSpec{{
			Method: "Switch.Set",
			Params: jsonRaw(map[string]any{"id": 0, "on": on}),
		}},
	}
}

// firmwareJob creates a fake firmware-type schedule job for testing the
// ownership split.
var firmwareScheduleJob = shelly.ScheduleJob{
	ID:       99,
	Enable:   true,
	Timespec: "0 0 0 * * SUN,MON,TUE,WED,THU,FRI,SAT",
	Calls:    []shelly.ScheduleCall{{Method: "Shelly.Update", Params: map[string]any{"stage": "stable"}}},
}

// ---- unit tests: isUpdateJob vs schedule jobs ------------------------------

func TestIsUpdateJobDistinguishesFirmwareVsSchedule(t *testing.T) {
	if !isUpdateJob(firmwareScheduleJob) {
		t.Error("Shelly.Update job should be identified as update job")
	}
	sw := switchSetJob(1, "@sunset")
	if isUpdateJob(sw) {
		t.Error("Switch.Set job should NOT be identified as update job")
	}
}

// ---- unit tests: content matching ------------------------------------------

func TestJobSpecMatchesDevice_TimespecMismatch(t *testing.T) {
	spec := switchSetSpec("0 0 22 * * *", nil, true)
	job := switchSetJob(1, "0 0 23 * * *")
	if jobSpecMatchesDevice(job, spec) {
		t.Error("different timespec should not match")
	}
}

func TestJobSpecMatchesDevice_MethodMismatch(t *testing.T) {
	spec := shellyv1alpha1.ScheduleJobSpec{
		Timespec: "@sunset",
		Calls:    []shellyv1alpha1.ScheduleCallSpec{{Method: "Switch.Set"}},
	}
	job := shelly.ScheduleJob{
		ID:       1,
		Enable:   true,
		Timespec: "@sunset",
		Calls:    []shelly.ScheduleCall{{Method: "Switch.Toggle"}},
	}
	if jobSpecMatchesDevice(job, spec) {
		t.Error("different method should not match")
	}
}

func TestJobSpecMatchesDevice_ParamsOrderInsensitive(t *testing.T) {
	// Device returns params in different key order to what the spec declares.
	// Matching must be order-insensitive.
	spec := shellyv1alpha1.ScheduleJobSpec{
		Timespec: "@sunset",
		Calls: []shellyv1alpha1.ScheduleCallSpec{{
			Method: "Switch.Set",
			Params: jsonRaw(map[string]any{"on": true, "id": 0}),
		}},
	}
	// Same params, different insertion order in Go map (marshalled differently).
	job := shelly.ScheduleJob{
		ID:       1,
		Enable:   true,
		Timespec: "@sunset",
		Calls: []shelly.ScheduleCall{{
			Method: "Switch.Set",
			Params: map[string]any{"id": float64(0), "on": true},
		}},
	}
	if !jobSpecMatchesDevice(job, spec) {
		t.Error("params with same key-values but different order must match")
	}
}

func TestJobSpecMatchesDevice_EnableIgnoredInContentMatch(t *testing.T) {
	// enable differs but content (timespec+calls) matches -> still a match.
	// (enable mismatch produces an "update" action, not a "create+delete" pair)
	spec := switchSetSpec("@sunset", new(false), true) // want disabled
	job := switchSetJob(1, "@sunset")                  // currently enabled
	if !jobSpecMatchesDevice(job, spec) {
		t.Error("enable difference must not affect content match")
	}
}

func TestJobSpecMatchesDevice_NilParams(t *testing.T) {
	// Spec with no params should match a device job with no params.
	spec := shellyv1alpha1.ScheduleJobSpec{
		Timespec: "@daily",
		Calls:    []shellyv1alpha1.ScheduleCallSpec{{Method: "Shelly.Reboot"}},
	}
	job := shelly.ScheduleJob{
		ID:       1,
		Enable:   true,
		Timespec: "@daily",
		Calls:    []shelly.ScheduleCall{{Method: "Shelly.Reboot"}},
	}
	if !jobSpecMatchesDevice(job, spec) {
		t.Error("nil params should match empty params")
	}
}

// ---- unit tests: scheduleActions (the convergence plan) --------------------

func TestScheduleActionsNilSection(t *testing.T) {
	actions := scheduleActions(nil, []shelly.ScheduleJob{switchSetJob(1, "@sunset")})
	if len(actions) != 0 {
		t.Errorf("nil section must produce no actions, got %+v", actions)
	}
}

func TestScheduleActionsIdempotent(t *testing.T) {
	// Re-applying an unchanged declared set must produce ZERO actions.
	spec := switchSetSpec("0 0 22 * * *", new(true), false)
	job := shelly.ScheduleJob{
		ID:       1,
		Enable:   true,
		Timespec: "0 0 22 * * *",
		Calls:    []shelly.ScheduleCall{{Method: "Switch.Set", Params: map[string]any{"id": float64(0), "on": false}}},
	}
	actions := scheduleActions(newSection(spec), []shelly.ScheduleJob{job})
	if len(actions) != 0 {
		t.Errorf("identical desired/actual must produce no actions, got %+v", actions)
	}
}

func TestScheduleActionsCreatesAbsentJob(t *testing.T) {
	spec := switchSetSpec("0 0 22 * * *", nil, false)
	actions := scheduleActions(newSection(spec), nil)
	if len(actions) != 1 || actions[0].kind != actionCreate {
		t.Fatalf("absent declared job must produce create action, got %+v", actions)
	}
	if actions[0].finding.Section != sectionSchedule {
		t.Errorf("finding.Section = %q, want %q", actions[0].finding.Section, sectionSchedule)
	}
}

func TestScheduleActionsDeletesUndeclaredJob(t *testing.T) {
	// Device has a job not in the profile -> must be deleted.
	job := switchSetJob(1, "@sunset")
	actions := scheduleActions(newSection(), []shelly.ScheduleJob{job})
	if len(actions) != 1 || actions[0].kind != actionDelete || actions[0].id != 1 {
		t.Fatalf("undeclared device job must produce delete action, got %+v", actions)
	}
}

func TestScheduleActionsUpdateEnable(t *testing.T) {
	// Job exists on device (enabled) but profile wants it disabled.
	spec := switchSetSpec("@sunset", new(false), true)
	job := switchSetJob(1, "@sunset") // enabled on device
	actions := scheduleActions(newSection(spec), []shelly.ScheduleJob{job})
	if len(actions) != 1 || actions[0].kind != actionUpdate {
		t.Fatalf("enable mismatch must produce update action, got %+v", actions)
	}
	if actions[0].id != 1 {
		t.Errorf("update must carry the device job id, got %d", actions[0].id)
	}
	if actions[0].job.Enable {
		t.Errorf("update job should have Enable=false, got %+v", actions[0].job)
	}
}

func TestScheduleActionsIgnoresDeclaredUpdateJob(t *testing.T) {
	// A declared schedule job that calls Shelly.Update belongs to the firmware
	// section, so the schedule section must ignore it -- no spurious create
	// action (the mirror of the device-side firmware-job filter). Otherwise it
	// would write a Shelly.Update job that isUpdateJob then excludes, looping
	// as non-converging.
	updateSpec := shellyv1alpha1.ScheduleJobSpec{
		Timespec: "0 0 3 * * *",
		Calls:    []shellyv1alpha1.ScheduleCallSpec{{Method: "Shelly.Update"}},
	}
	if got := scheduleActions(newSection(updateSpec), nil); len(got) != 0 {
		t.Errorf("declared Shelly.Update job must be ignored, got %+v", got)
	}
	// Case-insensitive: a lowercase variant must also be ignored.
	lowerSpec := shellyv1alpha1.ScheduleJobSpec{
		Timespec: "0 0 3 * * *",
		Calls:    []shellyv1alpha1.ScheduleCallSpec{{Method: "shelly.update"}},
	}
	if got := scheduleActions(newSection(lowerSpec), nil); len(got) != 0 {
		t.Errorf("declared shelly.update (any case) must be ignored, got %+v", got)
	}
}

func TestScheduleActionsIgnoresFirmwareJobs(t *testing.T) {
	// A firmware (Shelly.Update) job on the device must survive even when the
	// profile declares an empty schedule section (full ownership of non-update
	// jobs, firmware jobs are invisible to this section).
	actions := scheduleActions(newSection(), []shelly.ScheduleJob{firmwareScheduleJob})
	if len(actions) != 0 {
		t.Errorf("firmware job must be invisible to schedule section, got %+v", actions)
	}
}

func TestScheduleActionsMultipleJobs(t *testing.T) {
	// Device has: firmware job (must be preserved), one matching declared job,
	// one undeclared job (must be deleted).
	spec := switchSetSpec("0 0 22 * * *", new(true), false)
	matchingJob := shelly.ScheduleJob{
		ID:       2,
		Enable:   true,
		Timespec: "0 0 22 * * *",
		Calls:    []shelly.ScheduleCall{{Method: "Switch.Set", Params: map[string]any{"id": float64(0), "on": false}}},
	}
	undeclaredJob := switchSetJob(3, "@sunrise")

	deviceJobs := []shelly.ScheduleJob{firmwareScheduleJob, matchingJob, undeclaredJob}
	actions := scheduleActions(newSection(spec), deviceJobs)

	// Only the undeclared Switch.Set job should be deleted; matchingJob is in
	// sync (enable also matches); firmware job is invisible.
	if len(actions) != 1 || actions[0].kind != actionDelete || actions[0].id != 3 {
		t.Fatalf("expected one delete for undeclared job, got %+v", actions)
	}
}

func TestScheduleActionsFindings(t *testing.T) {
	// Actions must carry findings in the schedule section.
	spec := switchSetSpec("@daily", nil, true)
	actions := scheduleActions(newSection(spec), nil)
	for _, a := range actions {
		if a.finding.Section != sectionSchedule {
			t.Errorf("action finding.Section = %q, want %q", a.finding.Section, sectionSchedule)
		}
	}
}

// ---- unit tests: normaliseParams / callsMatch ------------------------------

func TestNormaliseParamsEmpty(t *testing.T) {
	if normaliseParams(nil) != normaliseParams(map[string]any{}) {
		t.Error("nil and empty params must normalise the same")
	}
}

func TestCallsMatchParamsDifferentNumericTypes(t *testing.T) {
	// Device may return numeric params as float64 after JSON decode; spec uses int.
	// Both should normalise to the same JSON and match.
	spec := shellyv1alpha1.ScheduleCallSpec{
		Method: "Switch.Set",
		Params: jsonRaw(map[string]any{"id": 0, "on": true}),
	}
	deviceCall := shelly.ScheduleCall{
		Method: "Switch.Set",
		Params: map[string]any{"id": float64(0), "on": true},
	}
	if !callsMatch(deviceCall, spec) {
		t.Error("int 0 and float64(0) should match after normalisation")
	}
}
