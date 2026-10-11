package amm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
)

/*
ProcessRuntime — the fallback tier.

This is what Sixter does today: run the application as a plain host process.
It provides NO isolation beyond unix file permissions. It exists for two
reasons:

  1. graceful degradation — if the host has no cgroup v2, no overlayfs, or we
     aren't root, we must keep deploying rather than fail. But we log loudly,
     because silently running unisolated is exactly the failure mode a runtime
     project is meant to eliminate.
  2. development on non-Linux machines.

Never present this tier to users as isolation.
*/

type procState struct {
	inst    *Instance
	spec    Spec
	cmd     *exec.Cmd
	started time.Time
	mu      sync.Mutex
}

type ProcessRuntime struct {
	StateDir  string
	mu        sync.RWMutex
	instances map[string]*procState
}

func NewProcessRuntime(stateDir string) *ProcessRuntime {
	if stateDir == "" {
		stateDir = filepath.Join(os.TempDir(), "sixter-amm")
	}
	return &ProcessRuntime{StateDir: stateDir, instances: map[string]*procState{}}
}

func (p *ProcessRuntime) Name() string     { return "process" }
func (p *ProcessRuntime) Available() error { return nil } // always works

func (p *ProcessRuntime) Create(ctx context.Context, spec Spec) (*Instance, error) {
	if spec.ID == "" || spec.Command == "" {
		return nil, errors.New("amm: spec needs ID and Command")
	}
	defaultsFor(&spec)
	spec.Tier = TierProcess

	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.instances[spec.ID]; exists {
		return nil, ErrAlreadyRuns
	}
	inst := &Instance{
		ID: spec.ID, Project: spec.Project, Tier: TierProcess,
		State: StateCreated, Port: spec.Port,
	}
	p.instances[spec.ID] = &procState{inst: inst, spec: spec}
	return inst, nil
}

func (p *ProcessRuntime) Start(ctx context.Context, id string) error {
	p.mu.RLock()
	st, ok := p.instances[id]
	p.mu.RUnlock()
	if !ok {
		return ErrNotFound
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.inst.State == StateRunning {
		return ErrAlreadyRuns
	}

	shell, flag := "sh", "-lc"
	if runtime.GOOS == "windows" {
		shell, flag = "cmd.exe", "/C"
	}
	cmd := exec.Command(shell, flag, st.spec.Command)
	cmd.Dir = st.spec.RootFS
	cmd.Env = os.Environ()
	for k, v := range st.spec.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if st.spec.Port > 0 {
		cmd.Env = append(cmd.Env, "PORT="+itoa(st.spec.Port))
	}
	setProcAttrs(cmd)

	_ = os.MkdirAll(p.StateDir, 0755)
	if lf, err := os.OpenFile(filepath.Join(p.StateDir, id+".log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		cmd.Stdout, cmd.Stderr = lf, lf
		st.inst.LogPath = filepath.Join(p.StateDir, id+".log")
	}

	if err := cmd.Start(); err != nil {
		st.inst.State = StateFailed
		st.inst.Err = err.Error()
		return err
	}
	st.cmd = cmd
	st.started = time.Now()
	st.inst.PID = cmd.Process.Pid
	st.inst.State = StateRunning
	st.inst.StartedAt = st.started

	go func() {
		err := cmd.Wait()
		st.mu.Lock()
		defer st.mu.Unlock()
		if ee, ok := err.(*exec.ExitError); ok {
			st.inst.ExitCode = ee.ExitCode()
		}
		if st.inst.State == StateRunning {
			st.inst.State = StateStopped
			if err != nil {
				st.inst.State = StateFailed
				st.inst.Err = err.Error()
			}
		}
	}()
	return nil
}

func (p *ProcessRuntime) Stop(ctx context.Context, id string, grace time.Duration) error {
	p.mu.RLock()
	st, ok := p.instances[id]
	p.mu.RUnlock()
	if !ok {
		return ErrNotFound
	}
	st.mu.Lock()
	cmd := st.cmd
	st.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if grace <= 0 {
		grace = 10 * time.Second
	}
	terminate(cmd)

	done := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(grace):
		_ = cmd.Process.Kill()
	case <-ctx.Done():
		_ = cmd.Process.Kill()
	}
	st.mu.Lock()
	st.inst.State = StateStopped
	st.mu.Unlock()
	return nil
}

func (p *ProcessRuntime) Destroy(ctx context.Context, id string) error {
	_ = p.Stop(ctx, id, 5*time.Second)
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.instances[id]; !ok {
		return ErrNotFound
	}
	delete(p.instances, id)
	return nil
}

func (p *ProcessRuntime) Get(id string) (*Instance, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	st, ok := p.instances[id]
	if !ok {
		return nil, false
	}
	cp := *st.inst
	return &cp, true
}

func (p *ProcessRuntime) List() []*Instance {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*Instance, 0, len(p.instances))
	for _, st := range p.instances {
		cp := *st.inst
		out = append(out, &cp)
	}
	return out
}

// Stats is honest about its limits: without a cgroup there is no cheap,
// accurate per-process CPU/memory accounting, so we report uptime only rather
// than inventing numbers. (Fabricated metrics are worse than absent ones.)
func (p *ProcessRuntime) Stats(id string) (*Stats, error) {
	p.mu.RLock()
	st, ok := p.instances[id]
	p.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	return &Stats{Uptime: time.Since(st.started), PIDs: 1}, nil
}

func (p *ProcessRuntime) Exec(ctx context.Context, id string, cmdv []string, tty bool) (*Stream, error) {
	return nil, errors.New("amm: exec requires the container runtime")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

var _ = syscall.Getpid
