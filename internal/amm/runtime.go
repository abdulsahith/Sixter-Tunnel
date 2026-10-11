// Package amm implements the Sixter Application Machine Monitor — the runtime
// that executes deployed applications.
//
// Design note (read this before changing anything here):
//
// AMM is NOT trying to beat containers on CPU. A container is a process; in
// steady state its CPU overhead is ~0. The costs worth attacking are startup
// latency, the I/O path, and memory density. Everything in this package is
// organised around those three, not around raw compute.
//
// The core abstraction is a single Runtime interface with swappable backends:
//
//	ProcessRuntime    — today's model (systemd on the host). No isolation.
//	ContainerRuntime  — namespaces + cgroups v2 + overlayfs. Semi-trusted code.
//	VMRuntime         — future: Firecracker for untrusted code. INTEGRATE,
//	                    never hand-roll a VMM: a bug there is host compromise.
//
// Callers only ever see Runtime, so the isolation tier is a policy decision
// (see Tier) rather than a code change.
package amm

import (
	"context"
	"errors"
	"io"
	"time"
)

// Tier is the isolation level an application runs under. The supervisor picks
// a tier from the workload's trust level; the caller never selects a backend
// directly.
type Tier string

const (
	// TierProcess: no isolation beyond unix users. Fastest, least safe.
	// Only for first-party code, and the fallback when the host can't do
	// better (non-Linux, no cgroup v2, unprivileged).
	TierProcess Tier = "process"

	// TierContainer: namespaces + cgroups + seccomp. Shared kernel, so this
	// is MEDIUM isolation — good for your own and teammates' code, NOT a
	// boundary against hostile multi-tenancy.
	TierContainer Tier = "container"

	// TierVM: hardware-virtualised boundary. Reserved for untrusted code.
	TierVM Tier = "vm"
)

// Errors callers are expected to handle.
var (
	ErrUnsupported = errors.New("amm: runtime not supported on this host")
	ErrNotFound    = errors.New("amm: instance not found")
	ErrAlreadyRuns = errors.New("amm: instance already running")
)

// Spec describes one application instance to run. It is deliberately small:
// everything here must be expressible by every backend, so no backend-specific
// knobs leak into the interface.
type Spec struct {
	ID      string // stable instance id, e.g. "myapp" or "pr-login-page--api"
	Project string // owning project (for metrics/attribution)

	// RootFS is the application's directory on the host. For TierContainer it
	// becomes the overlay lowerdir and the instance gets a private writable
	// layer on top, so the source tree is never mutated by the running app.
	RootFS string

	// Image is an OCI reference ("node:20-slim", "ghcr.io/org/app:v2") that
	// provides the container's base filesystem.
	//
	// This is what makes a container PORTABLE. With an image the root comes
	// from a content-addressed artifact any machine can pull, so the same
	// container runs identically on any node — the prerequisite for scheduling
	// across machines. Without it we fall back to bind-mounting the host's
	// system directories, which works on one box and cannot be moved.
	Image string

	// Command is executed inside the instance. Runs through a shell so
	// existing project run-commands ("npm start") keep working unchanged.
	Command string

	Env  map[string]string
	Port int // port the app listens on inside the instance

	// Resource limits. Zero means "no explicit limit".
	MemoryMB  int
	CPUPercent int // 100 = one full core
	MaxPIDs   int  // fork-bomb guard; defaults applied if zero

	// ReadOnlyRoot mounts the rootfs read-only, forcing writes to declared
	// volumes. Strongly recommended for anything not first-party.
	ReadOnlyRoot bool

	// HostNetwork shares the host's network namespace instead of creating a
	// private one.
	//
	// Why this exists: with a private netns the container has only loopback,
	// so nothing can reach the app until veth/bridge/NAT is built (R2). Host
	// networking lets the app bind its port exactly as it does today, so the
	// existing nginx/tunnel routing keeps working unchanged while we still get
	// pid/mount/ipc/uts isolation and full cgroup limits.
	//
	// This is the same trade as `docker run --network=host`. It weakens
	// network isolation only — the app can see host interfaces and bind host
	// ports. Acceptable for first-party/teammate code (our stated threat
	// model); must be false before untrusted code ever runs here.
	HostNetwork bool

	// PodIP and PodCIDR give the container its own address on the pod network.
	// Required when HostNetwork is false: a container with a private namespace
	// and no interface has only loopback, so nothing can reach it.
	PodIP   string
	PodCIDR string

	Tier Tier
}

// State is an instance's lifecycle position.
type State string

const (
	StateCreated State = "created"
	StateRunning State = "running"
	StateStopped State = "stopped"
	StateFailed  State = "failed"
)

// Instance is a handle to something AMM is running.
type Instance struct {
	ID        string
	Project   string
	Tier      Tier
	State     State
	PID       int // host-visible pid (container init for TierContainer)
	Port      int
	StartedAt time.Time
	ExitCode  int
	Err       string

	// LogPath is where the app's stdout/stderr land on this host. The agent
	// tails it when a workload dies — a crash inside a container is invisible
	// otherwise, because Destroy removes the overlay together with the log.
	LogPath string
}

// Stats are live resource numbers, read from cgroup counters rather than
// estimated. These are what the dashboard should display — the repo dashboard
// previously showed fabricated CPU/RAM because the process model had no real
// per-project accounting. Containers finally give us honest numbers.
type Stats struct {
	CPUPercent  float64
	MemoryBytes int64
	MemoryMax   int64
	PIDs        int
	Uptime      time.Duration

	// Throttling is the signal that a cgroup CPU quota is causing latency
	// cliffs — the single most common cause of mysterious p99 spikes on
	// container platforms. Surfacing it is a feature, not a diagnostic.
	ThrottledPeriods int64
	ThrottledTime    time.Duration
}

// Stream is an attached exec session (web terminal, debugging).
type Stream struct {
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	Stderr io.ReadCloser
	Close  func() error
}

// Runtime is the single seam between Sixter and however applications actually
// execute. Adding a backend must never require touching callers.
type Runtime interface {
	// Name identifies the backend for logs and the dashboard.
	Name() string

	// Available reports whether this backend can run on the current host.
	// The supervisor uses it to degrade gracefully rather than fail.
	Available() error

	Create(ctx context.Context, spec Spec) (*Instance, error)
	Start(ctx context.Context, id string) error
	Stop(ctx context.Context, id string, grace time.Duration) error
	Destroy(ctx context.Context, id string) error

	Get(id string) (*Instance, bool)
	List() []*Instance
	Stats(id string) (*Stats, error)

	// Exec attaches a process inside a running instance (web terminal).
	Exec(ctx context.Context, id string, cmd []string, tty bool) (*Stream, error)
}

// Snapshotter is optional. Implemented by backends that can checkpoint an
// instance's full state.
//
// Honest warning: checkpointing a PROCESS is far harder than it looks —
// open fds, established TCP connections, timers and pid/inode identity all
// live in the kernel and must be recreated exactly (this is why CRIU is a
// decade-long project with caveats). Checkpointing a VM is tractable because
// the guest kernel owns that state internally. Expect Snapshot to be
// implemented properly by VMRuntime first, and treat any process-level
// snapshot as best-effort.
type Snapshotter interface {
	Snapshot(ctx context.Context, id, dest string) error
	Restore(ctx context.Context, src string, spec Spec) (*Instance, error)
}

// defaultsFor fills in safe limits for anything the caller left at zero.
func defaultsFor(s *Spec) {
	if s.MaxPIDs <= 0 {
		s.MaxPIDs = 512 // fork-bomb guard; generous for app workloads
	}
	if s.Tier == "" {
		s.Tier = TierContainer
	}
}
