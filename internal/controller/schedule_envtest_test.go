package controller

import (
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	shellyv1alpha1 "github.com/LukeEvansTech/shelly-operator/api/v1alpha1"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly/shellytest"
)

// rpcScheduleCreate/Update/Delete are string constants re-used across envtests
// (satisfies goconst lint rule).
const (
	rpcScheduleCreate = "Schedule.Create"
	rpcScheduleUpdate = "Schedule.Update"
	rpcScheduleDelete = "Schedule.Delete"
	rpcScheduleList   = "Schedule.List"
	rpcShellyUpdate   = "Shelly.Update"
)

// scheduleCallSpec builds a ScheduleCallSpec with JSON params.
func scheduleCallSpec(method string, params any) shellyv1alpha1.ScheduleCallSpec {
	b, _ := json.Marshal(params)
	return shellyv1alpha1.ScheduleCallSpec{
		Method: method,
		Params: &apiextensionsv1.JSON{Raw: b},
	}
}

// nightlyOnSpec declares a Switch.Set job that turns switch on at 22:00.
// The timespec is fixed: every envtest that creates a Switch.Set/on job uses
// this same cron expression so a parameter would be unused (unparam).
func nightlyOnSpec() shellyv1alpha1.ScheduleJobSpec {
	return shellyv1alpha1.ScheduleJobSpec{
		Enable:   new(true),
		Timespec: "0 0 22 * * *",
		Calls:    []shellyv1alpha1.ScheduleCallSpec{scheduleCallSpec("Switch.Set", map[string]any{"id": 0, "on": true})},
	}
}

// morningOffSpec declares a Switch.Set job that turns switch off at 06:00.
func morningOffSpec() shellyv1alpha1.ScheduleJobSpec {
	return shellyv1alpha1.ScheduleJobSpec{
		Enable:   new(true),
		Timespec: "0 0 06 * * *",
		Calls:    []shellyv1alpha1.ScheduleCallSpec{scheduleCallSpec("Switch.Set", map[string]any{"id": 0, "on": false})},
	}
}

// deviceSwitchOnJob represents a Switch.Set job that turns the switch on as
// stored on the device (after JSON round-trip, numeric params are float64).
func deviceSwitchOnJob(timespec string) map[string]any {
	return map[string]any{
		"enable":   true,
		"timespec": timespec,
		"calls": []any{map[string]any{
			"method": "Switch.Set",
			"params": map[string]any{"id": float64(0), "on": true},
		}},
	}
}

// firmwareUpdateJob returns a schedule job fixture matching a firmware
// auto-update job (Shelly.Update).
func firmwareUpdateJob() map[string]any {
	return map[string]any{
		"enable":   true,
		"timespec": "0 0 0 * * SUN,MON,TUE,WED,THU,FRI,SAT",
		"calls": []any{map[string]any{
			"method": rpcShellyUpdate,
			"params": map[string]any{"stage": "stable"},
		}},
	}
}

// countScheduleWrites counts how many Schedule.Create/Update/Delete calls were made.
func countScheduleWrites(calls []shellytest.Call) int {
	n := 0
	for _, c := range calls {
		if c.Method == rpcScheduleCreate || c.Method == rpcScheduleUpdate || c.Method == rpcScheduleDelete {
			n++
		}
	}
	return n
}

// TestScheduleEnforceDeclaredJobCreated: declare a Switch.Set schedule job ->
// the operator must create it on the device.
func TestScheduleEnforceDeclaredJobCreated(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{
		ID:            "devsc1",
		MAC:           "AABBCCDDEE50",
		Gen:           2,
		InitialConfig: map[string]map[string]any{"sys": {"device": map[string]any{}}},
	}
	srv := shellytest.New(fake)
	defer srv.Close()

	createDevice(t, ns, "AABBCCDDEE50", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Schedules: &shellyv1alpha1.ScheduleSection{
			Jobs: []shellyv1alpha1.ScheduleJobSpec{nightlyOnSpec()},
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee50")

	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition = %+v, want True after schedule create", cond)
	}

	jobs := fake.ScheduleSnapshot()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job on device, got %d: %+v", len(jobs), jobs)
	}
	if jobs[0]["timespec"] != "0 0 22 * * *" {
		t.Errorf("job timespec = %v, want 0 0 22 * * *", jobs[0]["timespec"])
	}

	sawCreate := false
	for _, c := range fake.RecordedCalls() {
		if c.Method == rpcScheduleCreate {
			sawCreate = true
		}
	}
	if !sawCreate {
		t.Error("expected Schedule.Create call")
	}
}

// TestScheduleEnforceRemovedJobDeleted: an existing non-firmware job on the
// device that is not in the profile must be deleted.
func TestScheduleEnforceRemovedJobDeleted(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{
		ID:            "devsc2",
		MAC:           "AABBCCDDEE51",
		Gen:           2,
		InitialConfig: map[string]map[string]any{"sys": {"device": map[string]any{}}},
		InitialSchedules: []map[string]any{
			deviceSwitchOnJob("0 0 22 * * *"),
		},
	}
	srv := shellytest.New(fake)
	defer srv.Close()

	createDevice(t, ns, "AABBCCDDEE51", hostOf(srv.URL), true, false, "")
	// Empty schedule section = profile manages schedules but declares none ->
	// all existing non-firmware jobs must be deleted.
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Schedules: &shellyv1alpha1.ScheduleSection{Jobs: nil},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee51")

	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition = %+v, want True after schedule delete", cond)
	}

	if jobs := fake.ScheduleSnapshot(); len(jobs) != 0 {
		t.Fatalf("expected 0 jobs on device, got %d: %+v", len(jobs), jobs)
	}

	sawDelete := false
	for _, c := range fake.RecordedCalls() {
		if c.Method == rpcScheduleDelete {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Error("expected Schedule.Delete call")
	}
}

// TestScheduleEnforceEnableUpdated: a declared job exists on the device but
// with the wrong enable value -> operator must update it via Schedule.Update.
func TestScheduleEnforceEnableUpdated(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{
		ID:            "devsc3",
		MAC:           "AABBCCDDEE52",
		Gen:           2,
		InitialConfig: map[string]map[string]any{"sys": {"device": map[string]any{}}},
		InitialSchedules: []map[string]any{
			{
				"enable":   false, // disabled on device
				"timespec": "0 0 22 * * *",
				"calls": []any{map[string]any{
					"method": "Switch.Set",
					"params": map[string]any{"id": float64(0), "on": true},
				}},
			},
		},
	}
	srv := shellytest.New(fake)
	defer srv.Close()

	createDevice(t, ns, "AABBCCDDEE52", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Schedules: &shellyv1alpha1.ScheduleSection{
			Jobs: []shellyv1alpha1.ScheduleJobSpec{nightlyOnSpec()},
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee52")

	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition = %+v, want True after schedule enable update", cond)
	}

	jobs := fake.ScheduleSnapshot()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d: %+v", len(jobs), jobs)
	}
	if enabled, ok := jobs[0]["enable"].(bool); !ok || !enabled {
		t.Errorf("job enable = %v, want true", jobs[0]["enable"])
	}

	// Verify Schedule.Update was called (not Delete+Create).
	sawUpdate := false
	for _, c := range fake.RecordedCalls() {
		if c.Method == rpcScheduleUpdate {
			sawUpdate = true
		}
		if c.Method == rpcScheduleDelete || c.Method == rpcScheduleCreate {
			t.Errorf("enable update should use Schedule.Update, not Delete/Create, got %s", c.Method)
		}
	}
	if !sawUpdate {
		t.Error("expected Schedule.Update call")
	}
}

// TestScheduleEnforceFirmwareJobUntouched: the schedule section MUST NOT
// touch an existing firmware auto-update job (Shelly.Update), even when it
// manages the device's non-firmware jobs.
func TestScheduleEnforceFirmwareJobUntouched(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{
		ID:            "devsc4",
		MAC:           "AABBCCDDEE53",
		Gen:           2,
		InitialConfig: map[string]map[string]any{"sys": {"device": map[string]any{}}},
		InitialSchedules: []map[string]any{
			firmwareUpdateJob(),
		},
	}
	srv := shellytest.New(fake)
	defer srv.Close()

	createDevice(t, ns, "AABBCCDDEE53", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Schedules: &shellyv1alpha1.ScheduleSection{Jobs: nil},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee53")

	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition = %+v, want True", cond)
	}

	jobs := fake.ScheduleSnapshot()
	if len(jobs) != 1 {
		t.Fatalf("firmware job must survive; got %d jobs: %+v", len(jobs), jobs)
	}
	calls, _ := jobs[0]["calls"].([]any)
	if len(calls) == 0 {
		t.Fatal("surviving job has no calls")
	}
	if method, _ := calls[0].(map[string]any)["method"].(string); method != rpcShellyUpdate {
		t.Errorf("surviving job method = %q, want %s", method, rpcShellyUpdate)
	}

	for _, c := range fake.RecordedCalls() {
		if c.Method == rpcScheduleDelete || c.Method == rpcScheduleUpdate {
			t.Errorf("schedule section must not touch firmware job, saw %s", c.Method)
		}
	}
}

// TestScheduleEnforceFirmwareAndScheduleCoexist: when both firmware and
// schedule sections are declared, they must not fight. The firmware job is
// preserved and the declared Switch.Set job is created alongside it.
func TestScheduleEnforceFirmwareAndScheduleCoexist(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{
		ID:               "devsc5",
		MAC:              "AABBCCDDEE54",
		Gen:              2,
		InitialConfig:    map[string]map[string]any{"sys": {"device": map[string]any{}}},
		InitialSchedules: []map[string]any{firmwareUpdateJob()},
	}
	srv := shellytest.New(fake)
	defer srv.Close()

	createDevice(t, ns, "AABBCCDDEE54", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Firmware: &shellyv1alpha1.FirmwareSection{AutoUpdate: new(true)},
		Schedules: &shellyv1alpha1.ScheduleSection{
			Jobs: []shellyv1alpha1.ScheduleJobSpec{nightlyOnSpec()},
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee54")

	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition = %+v, want True", cond)
	}

	jobs := fake.ScheduleSnapshot()
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs (firmware + Switch.Set), got %d: %+v", len(jobs), jobs)
	}

	methods := map[string]int{}
	for _, j := range jobs {
		if jcalls, ok := j["calls"].([]any); ok && len(jcalls) > 0 {
			if m, ok := jcalls[0].(map[string]any)["method"].(string); ok {
				methods[m]++
			}
		}
	}
	if methods[rpcShellyUpdate] != 1 {
		t.Errorf("expected 1 %s job, got %d", rpcShellyUpdate, methods[rpcShellyUpdate])
	}
	if methods["Switch.Set"] != 1 {
		t.Errorf("expected 1 Switch.Set job, got %d", methods["Switch.Set"])
	}
}

// TestScheduleEnforceUndeclaredNonUpdateJobDeleted: a device has a Switch.Set
// job that is NOT in the profile; a different job IS declared. The undeclared
// job is deleted and the declared one is created.
func TestScheduleEnforceUndeclaredNonUpdateJobDeleted(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{
		ID:            "devsc6",
		MAC:           "AABBCCDDEE55",
		Gen:           2,
		InitialConfig: map[string]map[string]any{"sys": {"device": map[string]any{}}},
		InitialSchedules: []map[string]any{
			{
				"enable":   true,
				"timespec": "@sunset",
				"calls": []any{map[string]any{
					"method": "Switch.Set",
					"params": map[string]any{"id": float64(0), "on": true},
				}},
			},
		},
	}
	srv := shellytest.New(fake)
	defer srv.Close()

	createDevice(t, ns, "AABBCCDDEE55", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Schedules: &shellyv1alpha1.ScheduleSection{
			Jobs: []shellyv1alpha1.ScheduleJobSpec{morningOffSpec()},
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee55")

	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition = %+v, want True", cond)
	}

	jobs := fake.ScheduleSnapshot()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job (declared), got %d: %+v", len(jobs), jobs)
	}
	if jobs[0]["timespec"] != "0 0 06 * * *" {
		t.Errorf("surviving job timespec = %v, want 0 0 06 * * *", jobs[0]["timespec"])
	}
}

// TestScheduleEnforceIdempotentNoWrites: device already has exactly what the
// profile declares; enforcement must produce zero schedule writes.
func TestScheduleEnforceIdempotentNoWrites(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{
		ID:            "devsc7",
		MAC:           "AABBCCDDEE56",
		Gen:           2,
		InitialConfig: map[string]map[string]any{"sys": {"device": map[string]any{}}},
		InitialSchedules: []map[string]any{
			{
				"enable":   true,
				"timespec": "0 0 22 * * *",
				"calls": []any{map[string]any{
					"method": "Switch.Set",
					"params": map[string]any{"id": float64(0), "on": true},
				}},
			},
		},
	}
	srv := shellytest.New(fake)
	defer srv.Close()

	createDevice(t, ns, "AABBCCDDEE56", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Schedules: &shellyv1alpha1.ScheduleSection{
			Jobs: []shellyv1alpha1.ScheduleJobSpec{nightlyOnSpec()},
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee56")

	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition = %+v, want True (already in sync)", cond)
	}

	for _, c := range fake.RecordedCalls() {
		if c.Method == rpcScheduleCreate || c.Method == rpcScheduleUpdate || c.Method == rpcScheduleDelete {
			t.Errorf("idempotent cycle must produce no writes, saw %s", c.Method)
		}
	}
}

// TestScheduleNoSectionLeavesSchedulesAlone: a profile WITHOUT a schedule
// section must leave all schedules untouched and must not call Schedule.List.
func TestScheduleNoSectionLeavesSchedulesAlone(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{
		ID:            "devsc8",
		MAC:           "AABBCCDDEE57",
		Gen:           2,
		InitialConfig: map[string]map[string]any{"cloud": {"enable": false}},
		InitialSchedules: []map[string]any{
			deviceSwitchOnJob("0 0 22 * * *"),
		},
	}
	srv := shellytest.New(fake)
	defer srv.Close()

	createDevice(t, ns, "AABBCCDDEE57", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Cloud: &shellyv1alpha1.CloudSection{Enable: new(false)},
	})

	r, _ := newReconciler()
	reconcile(t, r, ns, "aabbccddee57")

	if jobs := fake.ScheduleSnapshot(); len(jobs) != 1 {
		t.Fatalf("schedule must be untouched when no section declared, got %d jobs", len(jobs))
	}

	for _, c := range fake.RecordedCalls() {
		if c.Method == rpcScheduleList {
			t.Fatal("Schedule.List must not be called when neither firmware nor schedule section is declared")
		}
	}
}

// TestScheduleObserveModeReportsDriftWithoutWrites: in observe mode the
// operator must report drift but must not modify any jobs.
func TestScheduleObserveModeReportsDriftWithoutWrites(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{
		ID:            "devsc9",
		MAC:           "AABBCCDDEE58",
		Gen:           2,
		InitialConfig: map[string]map[string]any{"sys": {"device": map[string]any{}}},
	}
	srv := shellytest.New(fake)
	defer srv.Close()

	createDevice(t, ns, "AABBCCDDEE58", hostOf(srv.URL), true, false, "")
	createProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Schedules: &shellyv1alpha1.ScheduleSection{
			Jobs: []shellyv1alpha1.ScheduleJobSpec{nightlyOnSpec()},
		},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee58")

	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != shellyv1alpha1.ReasonDrifted {
		t.Fatalf("condition = %+v, want False/Drifted in observe mode", cond)
	}

	found := false
	for _, s := range dev.Status.DriftedSections {
		if s == sectionSchedule {
			found = true
		}
	}
	if !found {
		t.Errorf("driftedSections = %v, want 'schedule' listed", dev.Status.DriftedSections)
	}

	for _, c := range fake.RecordedCalls() {
		if c.Method == rpcScheduleCreate || c.Method == rpcScheduleUpdate || c.Method == rpcScheduleDelete {
			t.Errorf("observe mode must not write schedules, saw %s", c.Method)
		}
	}

	if jobs := fake.ScheduleSnapshot(); len(jobs) != 0 {
		t.Fatalf("observe mode must not create jobs, got %d", len(jobs))
	}
}

// TestScheduleEnforceMixedFirmwareAndCustom: device has both a firmware
// auto-update job AND an undeclared custom Switch.Set job. The schedule
// section manages custom jobs; the firmware section manages the update job.
// Custom undeclared job must be deleted; firmware job must be preserved.
func TestScheduleEnforceMixedFirmwareAndCustom(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{
		ID:            "devsc10",
		MAC:           "AABBCCDDEE59",
		Gen:           2,
		InitialConfig: map[string]map[string]any{"sys": {"device": map[string]any{}}},
		InitialSchedules: []map[string]any{
			firmwareUpdateJob(),
			{
				"enable":   true,
				"timespec": "@sunrise",
				"calls": []any{map[string]any{
					"method": "Switch.Set",
					"params": map[string]any{"id": float64(0), "on": false},
				}},
			},
		},
	}
	srv := shellytest.New(fake)
	defer srv.Close()

	createDevice(t, ns, "AABBCCDDEE59", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Firmware:  &shellyv1alpha1.FirmwareSection{AutoUpdate: new(true)},
		Schedules: &shellyv1alpha1.ScheduleSection{Jobs: nil},
	})

	r, _ := newReconciler()
	dev := reconcile(t, r, ns, "aabbccddee59")

	cond := meta.FindStatusCondition(dev.Status.Conditions, shellyv1alpha1.ConditionInSync)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition = %+v, want True", cond)
	}

	jobs := fake.ScheduleSnapshot()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job (firmware only), got %d: %+v", len(jobs), jobs)
	}
	if jcalls, ok := jobs[0]["calls"].([]any); !ok || len(jcalls) == 0 {
		t.Fatal("surviving job has no calls")
	} else if m, _ := jcalls[0].(map[string]any)["method"].(string); m != rpcShellyUpdate {
		t.Errorf("surviving job = %q, want %s", m, rpcShellyUpdate)
	}
}

// TestScheduleEnforceSecondReconcileIdempotent: a second reconcile after
// convergence must produce zero additional writes.
func TestScheduleEnforceSecondReconcileIdempotent(t *testing.T) {
	ns := newNamespace(t)
	fake := &shellytest.Device{
		ID:            "devsc11",
		MAC:           "AABBCCDDEE5A",
		Gen:           2,
		InitialConfig: map[string]map[string]any{"sys": {"device": map[string]any{}}},
	}
	srv := shellytest.New(fake)
	defer srv.Close()

	createDevice(t, ns, "AABBCCDDEE5A", hostOf(srv.URL), true, false, "")
	createEnforceProfile(t, ns, shellyv1alpha1.ProfileConfig{
		Schedules: &shellyv1alpha1.ScheduleSection{
			Jobs: []shellyv1alpha1.ScheduleJobSpec{nightlyOnSpec()},
		},
	})

	r, _ := newReconciler()
	devName := types.NamespacedName{Namespace: ns, Name: "aabbccddee5a"}.Name

	reconcile(t, r, ns, devName)
	writesAfterFirst := countScheduleWrites(fake.RecordedCalls())
	if writesAfterFirst == 0 {
		t.Fatal("first reconcile should have created the job")
	}

	reconcile(t, r, ns, devName)
	writesAfterSecond := countScheduleWrites(fake.RecordedCalls())
	if writesAfterSecond != writesAfterFirst {
		t.Errorf("second reconcile must be a no-op: writes %d -> %d", writesAfterFirst, writesAfterSecond)
	}
}
