package shelly

import "context"

// ScheduleCall is one RPC invocation a schedule job performs. Origin
// (present on app-created jobs) is deliberately not modeled: the
// operator never writes it and ignores it when reading.
type ScheduleCall struct {
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

// ScheduleJob is one entry in the device's schedule (Schedule.List).
type ScheduleJob struct {
	ID       int            `json:"id,omitempty"`
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
