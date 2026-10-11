package amm

import (
	"context"
	"log"
	"sync"
	"time"
)

/*
Supervisor — the tier-selection policy layer.

Callers ask for an application to run at a trust level; the supervisor picks
the strongest backend the host can actually provide and records what it chose.
This is the "adaptive isolation" idea: one interface, several enforcement
strengths, selected by policy rather than by editing call sites.

It also guarantees we never silently downgrade. If a workload asked for
container isolation and the host can't provide it, that fact is logged and
exposed on the Instance, so the dashboard can show "running unisolated" rather
than implying a boundary that isn't there.
*/

type Supervisor struct {
	container Runtime
	process   Runtime

	mu       sync.RWMutex
	placedIn map[string]Runtime // instance id → the backend actually used

	// Degraded records why the container tier is unavailable, if it is.
	Degraded string
}

func NewSupervisor(stateDir string) *Supervisor {
	s := &Supervisor{
		container: NewContainerRuntime(stateDir),
		process:   NewProcessRuntime(stateDir),
		placedIn:  map[string]Runtime{},
	}
	if err := s.container.Available(); err != nil {
		s.Degraded = err.Error()
		log.Printf("AMM: container tier unavailable (%v) — falling back to unisolated processes", err)
	} else {
		log.Printf("AMM: container tier ready (namespaces + cgroup v2 + overlayfs)")
	}
	return s
}

// backendFor picks the strongest available runtime for a requested tier.
func (s *Supervisor) backendFor(t Tier) Runtime {
	switch t {
	case TierProcess:
		return s.process
	case TierVM:
		// Not implemented yet. Deliberately falls back rather than pretending:
		// a VM tier will be an INTEGRATION (Firecracker), never a hand-rolled
		// VMM, because a bug in a VMM is full host compromise.
		fallthrough
	case TierContainer:
		if s.container.Available() == nil {
			return s.container
		}
		return s.process
	}
	return s.process
}

// Run creates and starts an application in one call — the common path for a
// deploy. Returns the instance with Tier set to what was ACTUALLY used.
func (s *Supervisor) Run(ctx context.Context, spec Spec) (*Instance, error) {
	defaultsFor(&spec)
	rt := s.backendFor(spec.Tier)

	inst, err := rt.Create(ctx, spec)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.placedIn[spec.ID] = rt
	s.mu.Unlock()

	if err := rt.Start(ctx, spec.ID); err != nil {
		_ = rt.Destroy(ctx, spec.ID)
		s.forget(spec.ID)
		return nil, err
	}
	log.Printf("AMM: started %s on the %s tier (pid %d)", spec.ID, rt.Name(), inst.PID)
	return inst, nil
}

func (s *Supervisor) runtimeFor(id string) (Runtime, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rt, ok := s.placedIn[id]
	return rt, ok
}

func (s *Supervisor) forget(id string) {
	s.mu.Lock()
	delete(s.placedIn, id)
	s.mu.Unlock()
}

func (s *Supervisor) Stop(ctx context.Context, id string, grace time.Duration) error {
	rt, ok := s.runtimeFor(id)
	if !ok {
		return ErrNotFound
	}
	return rt.Stop(ctx, id, grace)
}

func (s *Supervisor) Destroy(ctx context.Context, id string) error {
	rt, ok := s.runtimeFor(id)
	if !ok {
		return ErrNotFound
	}
	err := rt.Destroy(ctx, id)
	s.forget(id)
	return err
}

func (s *Supervisor) Get(id string) (*Instance, bool) {
	rt, ok := s.runtimeFor(id)
	if !ok {
		return nil, false
	}
	return rt.Get(id)
}

func (s *Supervisor) Stats(id string) (*Stats, error) {
	rt, ok := s.runtimeFor(id)
	if !ok {
		return nil, ErrNotFound
	}
	return rt.Stats(id)
}

func (s *Supervisor) Exec(ctx context.Context, id string, cmd []string, tty bool) (*Stream, error) {
	rt, ok := s.runtimeFor(id)
	if !ok {
		return nil, ErrNotFound
	}
	return rt.Exec(ctx, id, cmd, tty)
}

// List returns every instance across all backends.
func (s *Supervisor) List() []*Instance {
	out := []*Instance{}
	out = append(out, s.container.List()...)
	out = append(out, s.process.List()...)
	return out
}

// ContainerReady reports whether real isolation is available on this host.
// The dashboard should surface this — users deserve to know which tier their
// application is actually running under.
func (s *Supervisor) ContainerReady() bool { return s.Degraded == "" }
