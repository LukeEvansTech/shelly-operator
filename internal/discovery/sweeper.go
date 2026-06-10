package discovery

import (
	"context"
	"errors"
	"net/http"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// +kubebuilder:rbac:groups=shelly.thirdimpact.io,resources=shellydevices,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=shelly.thirdimpact.io,resources=shellydevices/status,verbs=get;update;patch

// Sweeper periodically probes the configured subnets and records results
// as ShellyDevice objects. It is a manager.Runnable, not a Reconciler:
// the source of truth is the network, so there is nothing to watch.
type Sweeper struct {
	Client    client.Client
	Namespace string

	CIDRs      []string // IPv4 CIDRs to sweep
	ExtraHosts []string // extra host[:port] targets (tests, one-offs)

	Interval     time.Duration // between sweeps; default 5m
	ProbeTimeout time.Duration // per-probe HTTP timeout; default 3s
	Concurrency  int           // concurrent probes; default 32
	OfflineAfter time.Duration // mark offline when unseen this long; default 3*Interval

	hc *http.Client
	// targets is computed once at first use; changing CIDRs requires a restart (flag-driven config restarts the pod anyway).
	targets []string
}

// NeedLeaderElection ensures only the elected leader sweeps.
func (s *Sweeper) NeedLeaderElection() bool { return true }

// Start runs sweeps until ctx is cancelled. Implements manager.Runnable.
func (s *Sweeper) Start(ctx context.Context) error {
	if err := s.init(); err != nil {
		return err
	}
	log := logf.FromContext(ctx).WithName("discovery")
	log.Info("starting sweeper", "targets", len(s.targets), "interval", s.Interval.String())
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	for {
		if err := s.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error(err, "sweep finished with errors")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (s *Sweeper) init() error {
	if s.Interval <= 0 {
		s.Interval = 5 * time.Minute
	}
	if s.ProbeTimeout <= 0 {
		s.ProbeTimeout = 3 * time.Second
	}
	if s.Concurrency <= 0 {
		s.Concurrency = defaultProbeConcurrency
	}
	if s.OfflineAfter <= 0 {
		s.OfflineAfter = 3 * s.Interval
	}
	if s.OfflineAfter < time.Second {
		// metav1.Time has one-second wire precision; a sub-second window
		// would let a sweep mark devices it just refreshed offline.
		s.OfflineAfter = time.Second
	}
	if s.hc == nil {
		s.hc = &http.Client{Timeout: s.ProbeTimeout}
	}
	if s.targets == nil {
		hosts, err := ExpandCIDRs(s.CIDRs)
		if err != nil {
			return err
		}
		s.targets = append(hosts, s.ExtraHosts...)
	}
	return nil
}

// RunOnce performs a single sweep: probe all targets, upsert answers,
// mark long-unseen devices offline. Per-device errors are joined, not
// fatal — one bad device must not stop the sweep.
// Not safe to call concurrently with Start.
func (s *Sweeper) RunOnce(ctx context.Context) error {
	if err := s.init(); err != nil {
		return err
	}
	now := time.Now()
	found := probeAll(ctx, s.hc, s.targets, s.Concurrency)
	if ctx.Err() != nil {
		return ctx.Err() // shutting down: don't upsert partial results
	}
	var errs []error
	for _, f := range found {
		if err := applyDevice(ctx, s.Client, s.Namespace, now, f); err != nil {
			errs = append(errs, err)
		}
	}
	if err := markStale(ctx, s.Client, s.Namespace, now.Add(-s.OfflineAfter)); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
