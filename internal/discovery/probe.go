package discovery

import (
	"context"
	"net/http"
	"sync"

	"github.com/LukeEvansTech/shelly-operator/internal/shelly"
)

// defaultProbeConcurrency bounds simultaneous probes when the caller
// doesn't specify a limit.
const defaultProbeConcurrency = 32

// Found is one device that answered a probe during a sweep.
type Found struct {
	Host string // host[:port] the device answered at
	Info *shelly.DeviceInfo

	// AvailableFirmware is the pending stable firmware version from
	// Sys.GetStatus ("" = device is current). nil when the status read
	// failed (e.g. auth-enabled device; the sweeper has no credentials)
	// -- the upsert then keeps the previously recorded value.
	AvailableFirmware *string
}

// probeAll probes every target with bounded concurrency and returns the
// devices that answered. Unreachable and non-Shelly targets are skipped
// silently -- on a subnet sweep most addresses won't answer.
// On context cancellation it returns the partial results gathered so far;
// callers must not treat absence from the result as evidence a device is
// gone.
func probeAll(ctx context.Context, hc *http.Client, targets []string, concurrency int) []Found {
	if concurrency < 1 {
		concurrency = defaultProbeConcurrency
	}
	var (
		mu    sync.Mutex
		found []Found
		wg    sync.WaitGroup
		sem   = make(chan struct{}, concurrency)
	)
	for _, target := range targets {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(target string) {
			defer wg.Done()
			defer func() { <-sem }()
			info, err := shelly.Probe(ctx, hc, target)
			if err != nil || info.MAC == "" || info.Gen < 2 {
				return // unreachable, not a Shelly, or unsupported Gen1; skip
			}
			f := Found{Host: target, Info: info}
			// Best-effort second read: pending-update visibility. Failure
			// (auth-enabled device, flaky link) leaves the field nil so the
			// upsert keeps the previous value. Beta releases are ignored.
			if st, serr := shelly.NewClient(target, shelly.WithHTTPClient(hc)).GetSysStatus(ctx); serr == nil {
				v := ""
				if st.AvailableUpdates.Stable != nil {
					v = st.AvailableUpdates.Stable.Version
				}
				f.AvailableFirmware = &v
			}
			mu.Lock()
			found = append(found, f)
			mu.Unlock()
		}(target)
	}
	wg.Wait()
	return found
}
