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
//
// Probing is deliberately unauthenticated: it uses only Shelly.GetDeviceInfo,
// which needs no credentials. Pending-firmware visibility
// (status.availableFirmware) is NOT collected here -- Sys.GetStatus goes
// through POST /rpc and the sweeper holds no password, so on an auth-enabled
// device it could only 401. On firmware 2.0.0 each such 401 mints a nonce
// into a 32-entry circular buffer that the sweeper cannot use; once the
// buffer saturates the device throttles everyone with HTTP 429. The
// reconciler resolves the profile's password and reads it there instead.
type Found struct {
	Host string // host[:port] the device answered at
	Info *shelly.DeviceInfo
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
			mu.Lock()
			found = append(found, f)
			mu.Unlock()
		}(target)
	}
	wg.Wait()
	return found
}
