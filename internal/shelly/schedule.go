package shelly

import (
	"context"
	"errors"
)

// ScheduleCall is one RPC invocation a schedule job performs. Origin
// (present on app-created jobs) is deliberately not modeled: the
// operator never writes it and ignores it when reading.
type ScheduleCall struct {
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

// ScheduleJob is one entry in the device's schedule (Schedule.List).
type ScheduleJob struct {
	// ID is assigned by the device; it is read-only on the wire.
	// CreateSchedule ignores this field; the device sets it in the response.
	ID       int            `json:"id"`
	Enable   bool           `json:"enable"`
	Timespec string         `json:"timespec"`
	Calls    []ScheduleCall `json:"calls"`
}

// ListSchedules returns the device's schedule jobs.
func (c *Client) ListSchedules(ctx context.Context) ([]ScheduleJob, error) {
	var res struct {
		Jobs []ScheduleJob `json:"jobs"`
	}
	if err := c.Call(ctx, "Schedule.List", nil, &res); err != nil {
		return nil, err
	}
	return res.Jobs, nil
}

// CreateSchedule adds a job and returns its device-assigned id. job.ID
// is ignored (the device assigns ids).
func (c *Client) CreateSchedule(ctx context.Context, job ScheduleJob) (int, error) {
	if len(job.Calls) == 0 {
		return 0, errors.New("shelly: CreateSchedule requires at least one call")
	}
	var res struct {
		ID int `json:"id"`
	}
	params := map[string]any{"enable": job.Enable, "timespec": job.Timespec, "calls": job.Calls}
	if err := c.Call(ctx, "Schedule.Create", params, &res); err != nil {
		return 0, err
	}
	return res.ID, nil
}

// DeleteSchedule removes one schedule job by id.
func (c *Client) DeleteSchedule(ctx context.Context, id int) error {
	return c.Call(ctx, "Schedule.Delete", map[string]any{"id": id}, nil)
}

// UpdateSchedule updates an existing schedule job in place by id. The job's
// enable, timespec, and calls are updated; the id is preserved. This maps to
// the Schedule.Update RPC introduced in Gen2 firmware 1.x.
func (c *Client) UpdateSchedule(ctx context.Context, job ScheduleJob) error {
	if len(job.Calls) == 0 {
		return errors.New("shelly: UpdateSchedule requires at least one call")
	}
	params := map[string]any{
		"id":       job.ID,
		"enable":   job.Enable,
		"timespec": job.Timespec,
		"calls":    job.Calls,
	}
	return c.Call(ctx, "Schedule.Update", params, nil)
}
