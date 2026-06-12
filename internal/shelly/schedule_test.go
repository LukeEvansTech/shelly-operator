package shelly_test

import (
	"context"
	"testing"

	"github.com/LukeEvansTech/shelly-operator/internal/shelly"
	"github.com/LukeEvansTech/shelly-operator/internal/shelly/shellytest"
)

func TestCreateScheduleRequiresCalls(t *testing.T) {
	d := &shellytest.Device{ID: "dev1", MAC: "AABBCCDDEEFF", Gen: 2}
	srv := shellytest.New(d)
	defer srv.Close()
	c := shelly.NewClient(hostOf(srv.URL))
	ctx := context.Background()

	_, err := c.CreateSchedule(ctx, shelly.ScheduleJob{
		Enable:   true,
		Timespec: "0 0 0 * * SUN",
		Calls:    nil,
	})
	if err == nil {
		t.Fatal("expected error when Calls is nil, got nil")
	}

	calls := d.RecordedCalls()
	for _, call := range calls {
		if call.Method == "Schedule.Create" {
			t.Errorf("Schedule.Create was sent to the device despite empty Calls; recorded calls: %+v", calls)
		}
	}
}

func TestScheduleRoundTrip(t *testing.T) {
	d := &shellytest.Device{ID: "dev1", MAC: "AABBCCDDEEFF", Gen: 2}
	srv := shellytest.New(d)
	defer srv.Close()
	c := shelly.NewClient(hostOf(srv.URL))
	ctx := context.Background()

	jobs, err := c.ListSchedules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("initial jobs = %+v", jobs)
	}

	id, err := c.CreateSchedule(ctx, shelly.ScheduleJob{
		Enable:   true,
		Timespec: "0 0 0 * * SUN,MON,TUE,WED,THU,FRI,SAT",
		Calls:    []shelly.ScheduleCall{{Method: "Shelly.Update", Params: map[string]any{"stage": "stable"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != 1 {
		t.Fatalf("id = %d", id)
	}

	jobs, err = c.ListSchedules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != 1 || !jobs[0].Enable ||
		jobs[0].Timespec != "0 0 0 * * SUN,MON,TUE,WED,THU,FRI,SAT" ||
		len(jobs[0].Calls) != 1 || jobs[0].Calls[0].Method != "Shelly.Update" ||
		jobs[0].Calls[0].Params["stage"] != "stable" {
		t.Fatalf("jobs = %+v", jobs)
	}

	if err := c.DeleteSchedule(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteSchedule(ctx, 1); err == nil {
		t.Fatal("expected error deleting missing job")
	}
}
