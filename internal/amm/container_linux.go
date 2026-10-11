//go:build linux

package amm

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

/*
ContainerRuntime — namespaces + cgroups v2 + overlayfs, in pure Go.

How a container is actually made here:

	 parent (this process)
	   │ 1. build overlay rootfs   lower=project (ro), upper=writable
	   │ 2. create cgroup v2, write memory.max / cpu.max / pids.max
	   │ 3. exec /proc/self/exe with argv[0]="amm-init"
	   │    + Cloneflags: NEWNS|NEWPID|NEWNET|NEWUTS|NEWIPC
	   ▼
	 child = PID 1 inside the new namespaces  (see init_linux.go)
	   │ 4. make mounts private, pivot_root into the overlay
	   │ 5. mount /proc, /sys, /dev, /tmp
	   │ 6. drop capabilities, no_new_privs
	   │ 7. exec the application
	   ▼
	 application

Go cannot fork() safely (the runtime is multi-threaded and only the calling
thread survives a fork), so the standard trick is to re-exec our own binary and
do namespace setup in the fresh child. That is what "amm-init" is.
*/

const cgroupRoot = "/sys/fs/cgroup"

type containerState struct {
	inst    *Instance
	spec    Spec
	cmd     *exec.Cmd
	cgPath  string
	overlay overlayDirs
	started time.Time
	mu      sync.Mutex
}

type overlayDirs struct {
	base   string // <state>/<id>
	upper  string // writable layer for the APP's files
	work   string // overlayfs bookkeeping (must be on same fs as upper)
	merged string // copy-on-write view of the project, bind-mounted at /app
	root   string // the container's root filesystem skeleton
	image  string // extracted OCI rootfs ("" = fall back to host system dirs)

	// Writable layer for the ROOT filesystem (image mode). Backed by disk,
	// not tmpfs, so a module cache or apt install doesn't eat the memory
	// limit; destroyed with the container, so nothing leaks between runs.
	rootUpper string
	rootWork  string
}

// ContainerRuntime runs applications in Linux namespaces with cgroup limits.
type ContainerRuntime struct {
	// StateDir holds per-instance overlay layers, e.g. /var/lib/sixter/amm
	StateDir string

	// Images pulls and caches OCI base images. When a Spec names an Image the
	// container's root comes from it (portable); otherwise we fall back to
	// bind-mounting the host's system directories (single-machine only).
	Images *ImageStore

	mu        sync.RWMutex
	instances map[string]*containerState
}

func NewContainerRuntime(stateDir string) *ContainerRuntime {
	if stateDir == "" {
		stateDir = "/var/lib/sixter/amm"
	}
	return &ContainerRuntime{
		StateDir:  stateDir,
		Images:    NewImageStore(filepath.Join(stateDir, "images")),
		instances: map[string]*containerState{},
	}
}

func (c *ContainerRuntime) Name() string { return "container" }

// Available checks the three things this backend genuinely requires. We fail
// with a specific reason so the supervisor can log why it degraded to
// ProcessRuntime instead of silently running unisolated.
func (c *ContainerRuntime) Available() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("%w: needs root (namespaces + cgroups)", ErrUnsupported)
	}
	// cgroup v2 unified hierarchy
	if _, err := os.Stat(filepath.Join(cgroupRoot, "cgroup.controllers")); err != nil {
		return fmt.Errorf("%w: cgroup v2 unified hierarchy not mounted at %s", ErrUnsupported, cgroupRoot)
	}
	// overlayfs. On fresh cloud images the module ships but isn't loaded, so a
	// brand-new node would degrade to unisolated processes for want of one
	// modprobe — try it ourselves (once) before giving up. We're already
	// root here per the check above.
	if !overlayInProc() {
		modprobeOnce.Do(func() {
			_ = exec.Command("modprobe", "overlay").Run()
		})
	}
	if !overlayInProc() {
		return fmt.Errorf("%w: overlayfs not available in this kernel", ErrUnsupported)
	}
	return nil
}

var modprobeOnce sync.Once

func overlayInProc() bool {
	b, err := os.ReadFile("/proc/filesystems")
	return err == nil && strings.Contains(string(b), "overlay")
}

/* ─────────── lifecycle ─────────── */

func (c *ContainerRuntime) Create(ctx context.Context, spec Spec) (*Instance, error) {
	if err := c.Available(); err != nil {
		return nil, err
	}
	if spec.ID == "" || spec.RootFS == "" {
		return nil, errors.New("amm: spec needs ID and RootFS")
	}
	defaultsFor(&spec)

	c.mu.Lock()
	if _, exists := c.instances[spec.ID]; exists {
		c.mu.Unlock()
		return nil, ErrAlreadyRuns
	}
	c.mu.Unlock()

	// An image makes the container portable: its root comes from a
	// content-addressed artifact rather than this host's filesystem. Pull it
	// before anything else so a bad reference fails fast and cheaply.
	imageRoot := ""
	if spec.Image != "" {
		if c.Images == nil {
			c.Images = NewImageStore(filepath.Join(c.StateDir, "images"))
		}
		log.Printf("AMM: pulling image %s for %s…", spec.Image, spec.ID)
		root, err := c.Images.Pull(ctx, spec.Image)
		if err != nil {
			return nil, fmt.Errorf("pull %s: %w", spec.Image, err)
		}
		imageRoot = root
		log.Printf("AMM: image %s ready", spec.Image)
	}

	ov, err := c.prepareOverlay(spec)
	if err != nil {
		return nil, fmt.Errorf("rootfs: %w", err)
	}
	ov.image = imageRoot

	cgPath, err := c.createCgroup(spec)
	if err != nil {
		_ = c.teardownOverlay(ov)
		return nil, fmt.Errorf("cgroup: %w", err)
	}

	inst := &Instance{
		ID: spec.ID, Project: spec.Project, Tier: TierContainer,
		State: StateCreated, Port: spec.Port,
	}
	st := &containerState{inst: inst, spec: spec, cgPath: cgPath, overlay: ov}

	c.mu.Lock()
	c.instances[spec.ID] = st
	c.mu.Unlock()
	return inst, nil
}

func (c *ContainerRuntime) Start(ctx context.Context, id string) error {
	c.mu.RLock()
	st, ok := c.instances[id]
	c.mu.RUnlock()
	if !ok {
		return ErrNotFound
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.inst.State == StateRunning {
		return ErrAlreadyRuns
	}

	// Re-exec ourselves as the container's PID 1. argv[0] is the marker that
	// init_linux.go looks for; everything the child needs travels in env vars
	// because argv is visible in host `ps` output.
	cmd := exec.Command("/proc/self/exe")
	cmd.Args = []string{ammInitArgv0}
	cmd.Env = st.buildInitEnv()
	cmd.Dir = "/"

	// Network namespace is opt-in. With a private netns the container has only
	// loopback, so nothing can reach the app until veth/bridge/NAT exists.
	// Host networking keeps the app reachable through the existing nginx/tunnel
	// routing while we still get pid/mount/ipc/uts isolation + cgroup limits.
	flags := uintptr(syscall.CLONE_NEWNS | // mount namespace: private rootfs
		syscall.CLONE_NEWPID | // pid namespace: app becomes PID 1
		syscall.CLONE_NEWUTS | // hostname
		syscall.CLONE_NEWIPC) // sysv ipc / posix mq
	if !st.spec.HostNetwork {
		flags |= syscall.CLONE_NEWNET
	}

	// Place the child directly into its cgroup AT CLONE TIME. Attaching after
	// Start() is racy: if init forks before the write lands, those children
	// stay in the parent's cgroup and limits silently never apply (symptom:
	// the app runs fine but memory.current/pids.current read 0). CgroupFD
	// makes the kernel do the placement, so there is no window.
	cgFD, cgErr := syscall.Open(st.cgPath, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: flags,

		// Kill the whole container if the supervisor dies, so we never leak
		// orphaned application processes onto the host.
		Pdeathsig: syscall.SIGKILL,

		// New session so the app can't signal the supervisor's process group.
		Setsid: true,
	}
	if cgErr == nil {
		cmd.SysProcAttr.UseCgroupFD = true
		cmd.SysProcAttr.CgroupFD = cgFD
		defer syscall.Close(cgFD)
	}

	// Pod networking needs the child to EXIST (so its namespace can be entered)
	// but not yet RUNNING the app (so it never binds before it has an address).
	// A pipe gives us that window: the child blocks reading one byte as its
	// first act, and we write it once the interface is configured.
	var netR, netW *os.File
	if !st.spec.HostNetwork {
		r, w, perr := os.Pipe()
		if perr != nil {
			return fmt.Errorf("amm: network sync pipe: %w", perr)
		}
		netR, netW = r, w
		// Becomes fd 3 in the child — see waitForNetwork in init_linux.go.
		// The env flag is what tells the child a pipe is actually there.
		cmd.ExtraFiles = []*os.File{netR}
		cmd.Env = append(cmd.Env, ammEnvNetNS+"=1")
		defer netR.Close()
	}

	logPath := filepath.Join(st.overlay.base, "output.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		st.inst.LogPath = logPath
	}

	if err := cmd.Start(); err != nil {
		st.inst.State = StateFailed
		st.inst.Err = err.Error()
		return fmt.Errorf("start container init: %w", err)
	}

	// The child is cloned but parked on the pipe. Give it an interface, then
	// let it continue. A failure here must kill the container rather than
	// release it: an app started with only loopback looks healthy and is
	// unreachable, which is the hardest kind of outage to diagnose.
	if netW != nil {
		nerr := attachPod(cmd.Process.Pid, st.spec.PodIP, st.spec.PodCIDR, st.spec.ID)
		if nerr == nil {
			_, nerr = netW.Write([]byte{1})
		}
		netW.Close()
		if nerr != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
			detachPod(st.spec.ID)
			st.inst.State = StateFailed
			st.inst.Err = nerr.Error()
			return fmt.Errorf("amm: pod network: %w", nerr)
		}
	}

	// Fallback for kernels/Go versions where CgroupFD wasn't usable. Racy by
	// nature (see above), so it's best-effort rather than fatal — a container
	// without limits is better than a failed deploy, and Stats() will show
	// zeros, which is the visible symptom.
	if cgErr != nil {
		if err := c.attachToCgroup(st.cgPath, cmd.Process.Pid); err != nil {
			log.Printf("AMM: %s started but cgroup attach failed (%v) — limits not applied", id, err)
		}
	}

	st.cmd = cmd
	st.started = time.Now()
	st.inst.PID = cmd.Process.Pid
	st.inst.State = StateRunning
	st.inst.StartedAt = st.started

	// Reap asynchronously so a crashed app is reflected in state rather than
	// leaving a zombie and a stale "running" instance.
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
		if logFile != nil {
			_ = logFile.Close()
		}
		if !st.spec.HostNetwork {
			// The container side vanishes with its namespace; the host side
			// would otherwise accumulate one dead interface per restart.
			detachPod(st.spec.ID)
		}
	}()

	return nil
}

// Stop sends SIGTERM to the container's PID 1 and escalates to SIGKILL.
// Killing PID 1 in a pid namespace terminates every process inside it, so we
// never have to hunt down children.
func (c *ContainerRuntime) Stop(ctx context.Context, id string, grace time.Duration) error {
	c.mu.RLock()
	st, ok := c.instances[id]
	c.mu.RUnlock()
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

	_ = cmd.Process.Signal(syscall.SIGTERM)

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

func (c *ContainerRuntime) Destroy(ctx context.Context, id string) error {
	_ = c.Stop(ctx, id, 5*time.Second)

	c.mu.Lock()
	st, ok := c.instances[id]
	delete(c.instances, id)
	c.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	_ = c.removeCgroup(st.cgPath)
	return c.teardownOverlay(st.overlay)
}

func (c *ContainerRuntime) Get(id string) (*Instance, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	st, ok := c.instances[id]
	if !ok {
		return nil, false
	}
	cp := *st.inst
	return &cp, true
}

func (c *ContainerRuntime) List() []*Instance {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*Instance, 0, len(c.instances))
	for _, st := range c.instances {
		cp := *st.inst
		out = append(out, &cp)
	}
	return out
}

/* ─────────── rootfs (overlayfs) ─────────── */

// prepareOverlay builds a copy-on-write root: the project directory is the
// read-only lower layer, and the instance writes into its own upper layer.
// Two consequences that matter:
//   - the deployed source tree is never mutated by a running app;
//   - N instances of the same project share one page-cache copy of the lower
//     layer, which is the main lever on memory density.
func (c *ContainerRuntime) prepareOverlay(spec Spec) (overlayDirs, error) {
	base := filepath.Join(c.StateDir, spec.ID)
	ov := overlayDirs{
		base:      base,
		upper:     filepath.Join(base, "upper"),
		work:      filepath.Join(base, "work"),
		merged:    filepath.Join(base, "app"),
		root:      filepath.Join(base, "root"),
		rootUpper: filepath.Join(base, "rootfs-upper"),
		rootWork:  filepath.Join(base, "rootfs-work"),
	}
	for _, d := range []string{ov.upper, ov.work, ov.merged, ov.root, ov.rootUpper, ov.rootWork} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return ov, err
		}
	}

	// Copy-on-write view of the PROJECT only. The upper layer lives beside the
	// project rather than inside it, so overlayfs won't reject it (upperdir
	// must not be within lowerdir). Writes land in the upper layer, so a
	// running app can never mutate the deployed source tree.
	// The lower layer must exist before overlayfs will mount. On a fleet node
	// this is where the downloaded source lands — if it's missing, the source
	// fetch didn't happen (usually an out-of-date agent), so say so instead of
	// surfacing a bare ENOENT.
	if st, err := os.Stat(spec.RootFS); err != nil || !st.IsDir() {
		return ov, fmt.Errorf("application directory %s does not exist — "+
			"the project source was not fetched (is the agent up to date?)", spec.RootFS)
	}

	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", spec.RootFS, ov.upper, ov.work)
	flags := uintptr(0)
	if spec.ReadOnlyRoot {
		flags |= syscall.MS_RDONLY
	}
	if err := syscall.Mount("overlay", ov.merged, "overlay", flags, opts); err != nil {
		return ov, fmt.Errorf("mount overlay (lower=%s upper=%s): %w", spec.RootFS, ov.upper, err)
	}

	// Root filesystem skeleton. The child fills these in with read-only bind
	// mounts of the host's system directories — the app needs /bin/sh, its
	// language runtime and libc, none of which exist in a project folder.
	// (This was the bug: a project directory is not a root filesystem.)
	for _, d := range containerRootDirs {
		if err := os.MkdirAll(filepath.Join(ov.root, d), 0755); err != nil {
			_ = syscall.Unmount(ov.merged, syscall.MNT_DETACH)
			return ov, err
		}
	}
	return ov, nil
}

// containerRootDirs is the skeleton created inside the container's root.
// System dirs are bind-mounted read-only from the host at start; the rest are
// mount points the init fills with pseudo-filesystems.
var containerRootDirs = []string{
	"bin", "sbin", "lib", "lib64", "usr", "etc", // bind-mounted read-only
	"app",                     // the project (copy-on-write)
	"proc", "sys", "dev/pts", "dev/shm", "tmp", "run", "var",
	".oldroot",
}

func (c *ContainerRuntime) teardownOverlay(ov overlayDirs) error {
	if ov.merged != "" {
		// lazy unmount: succeeds even if something still holds a reference
		_ = syscall.Unmount(ov.merged, syscall.MNT_DETACH)
	}
	// The root's bind mounts live in the container's private mount namespace
	// and vanish with it, so there is nothing to unmount here — only the
	// directory skeleton to delete.
	if ov.base != "" {
		return os.RemoveAll(ov.base)
	}
	return nil
}

/* ─────────── cgroups v2 ─────────── */

func (c *ContainerRuntime) cgroupPath(id string) string {
	return filepath.Join(cgroupRoot, "sixter", id)
}

// createCgroup makes the instance's cgroup and applies limits.
//
// Deliberate choice on CPU: we set cpu.max (a hard quota) only when the caller
// asks for one. A hard quota causes the app to be stalled until the next 100ms
// period once its budget is spent, which shows up as brutal p99 latency
// cliffs. cpu.weight (proportional share) is the better default because it
// only bites under contention. Stats() surfaces throttling so this is visible
// rather than mysterious.
func (c *ContainerRuntime) createCgroup(spec Spec) (string, error) {
	path := c.cgroupPath(spec.ID)
	if err := os.MkdirAll(path, 0755); err != nil {
		return "", err
	}

	// Enable controllers on the parent so children can use them.
	parent := filepath.Join(cgroupRoot, "sixter")
	_ = os.WriteFile(filepath.Join(parent, "cgroup.subtree_control"), []byte("+cpu +memory +pids"), 0644)

	// ALWAYS write the complete desired state, including the "no limit" case.
	//
	// A cgroup directory outlives a crashed container or a failed teardown, so
	// only writing limits when they're non-zero leaves a PREVIOUS run's values
	// in place. Symptom: the UI shows "0" for both limits while the kernel is
	// still enforcing an old quota — throttling with no limit set. Writing
	// "max" explicitly makes this idempotent.
	if spec.MemoryMB > 0 {
		bytes := int64(spec.MemoryMB) * 1024 * 1024
		// memory.high throttles + reclaims before memory.max OOM-kills. For a
		// GC'd runtime this is the difference between "collected and carried
		// on" and "killed for a spike it would have cleaned up".
		high := bytes * 90 / 100
		_ = writeCg(path, "memory.high", strconv.FormatInt(high, 10))
		if err := writeCg(path, "memory.max", strconv.FormatInt(bytes, 10)); err != nil {
			return path, err
		}
	} else {
		_ = writeCg(path, "memory.high", "max")
		_ = writeCg(path, "memory.max", "max")
	}

	if spec.CPUPercent > 0 {
		// cpu.max = "<quota> <period>", quota in µs of CPU per period
		quota := spec.CPUPercent * 1000 // 100% → 100000µs of a 100000µs period
		_ = writeCg(path, "cpu.max", fmt.Sprintf("%d 100000", quota))
	} else {
		_ = writeCg(path, "cpu.max", "max 100000") // clear any previous quota
		_ = writeCg(path, "cpu.weight", "100")     // proportional share, no cliff
	}

	if spec.MaxPIDs > 0 {
		_ = writeCg(path, "pids.max", strconv.Itoa(spec.MaxPIDs))
	} else {
		_ = writeCg(path, "pids.max", "max")
	}
	return path, nil
}

func (c *ContainerRuntime) attachToCgroup(cgPath string, pid int) error {
	return writeCg(cgPath, "cgroup.procs", strconv.Itoa(pid))
}

// removeCgroup deletes the cgroup. rmdir only succeeds when the cgroup is
// empty, so we first ask the kernel to kill anything still inside it
// (cgroup.kill, cgroup v2 >= 5.14). Without this a lingering process keeps the
// directory alive — and with it the previous run's limits and its cumulative
// throttling counters, which then show up against the next container.
func (c *ContainerRuntime) removeCgroup(cgPath string) error {
	if cgPath == "" {
		return nil
	}
	_ = writeCg(cgPath, "cgroup.kill", "1")

	// Give the kernel a moment to reap before rmdir.
	for i := 0; i < 20; i++ {
		if err := os.Remove(cgPath); err == nil {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return os.Remove(cgPath)
}

func writeCg(dir, file, val string) error {
	return os.WriteFile(filepath.Join(dir, file), []byte(val), 0644)
}

func readCg(dir, file string) string {
	b, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

/* ─────────── stats ─────────── */

// Stats reads real cgroup counters. These are measured, not estimated — which
// is what finally lets the dashboard show honest per-project CPU and memory.
func (c *ContainerRuntime) Stats(id string) (*Stats, error) {
	c.mu.RLock()
	st, ok := c.instances[id]
	c.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}

	s := &Stats{Uptime: time.Since(st.started)}
	if v := readCg(st.cgPath, "memory.current"); v != "" {
		s.MemoryBytes, _ = strconv.ParseInt(v, 10, 64)
	}
	if v := readCg(st.cgPath, "memory.max"); v != "" && v != "max" {
		s.MemoryMax, _ = strconv.ParseInt(v, 10, 64)
	}
	if v := readCg(st.cgPath, "pids.current"); v != "" {
		s.PIDs, _ = strconv.Atoi(v)
	}

	// cpu.stat is a key/value list: usage_usec, throttled_usec, nr_throttled…
	for _, line := range strings.Split(readCg(st.cgPath, "cpu.stat"), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		n, _ := strconv.ParseInt(f[1], 10, 64)
		switch f[0] {
		case "usage_usec":
			if s.Uptime > 0 {
				s.CPUPercent = float64(n) / float64(s.Uptime.Microseconds()) * 100
			}
		case "nr_throttled":
			s.ThrottledPeriods = n
		case "throttled_usec":
			s.ThrottledTime = time.Duration(n) * time.Microsecond
		}
	}
	return s, nil
}

/* ─────────── exec (web terminal) ─────────── */

// Exec joins an existing container's namespaces via nsenter and runs a command
// inside it. Using nsenter rather than re-implementing setns keeps this short
// and correct; it is the same mechanism `docker exec` uses underneath.
func (c *ContainerRuntime) Exec(ctx context.Context, id string, cmdv []string, tty bool) (*Stream, error) {
	c.mu.RLock()
	st, ok := c.instances[id]
	c.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	st.mu.Lock()
	pid := st.inst.PID
	running := st.inst.State == StateRunning
	st.mu.Unlock()
	if !running || pid == 0 {
		return nil, errors.New("amm: instance is not running")
	}
	if len(cmdv) == 0 {
		cmdv = []string{"/bin/sh"}
	}

	args := append([]string{
		"--target", strconv.Itoa(pid),
		"--mount", "--uts", "--ipc", "--net", "--pid",
		"--",
	}, cmdv...)
	cmd := exec.CommandContext(ctx, "nsenter", args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Stream{
		Stdin: stdin, Stdout: stdout, Stderr: stderr,
		Close: func() error {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			return nil
		},
	}, nil
}

/* ─────────── init handoff ─────────── */

// buildInitEnv passes the container config to our re-exec'd self. Env is used
// rather than argv because argv is world-readable in host `ps`.
func (st *containerState) buildInitEnv() []string {
	// Precedence: defaults < image config < per-project env. The image knows
	// where its own binaries live — golang:* puts go in /usr/local/go/bin,
	// which no hardcoded PATH includes — so its Env must override ours, and
	// the user's must override both.
	merged := map[string]string{
		"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME": "/root",
	}
	order := []string{"PATH", "HOME"}
	set := func(kv string) {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			return
		}
		k := kv[:i]
		if _, seen := merged[k]; !seen {
			order = append(order, k)
		}
		merged[k] = kv[i+1:]
	}
	for _, kv := range ReadImageEnv(st.overlay.image) {
		set(kv)
	}
	for k, v := range st.spec.Env {
		set(k + "=" + v)
	}
	if st.spec.Port > 0 {
		set("PORT=" + strconv.Itoa(st.spec.Port))
	}

	env := []string{
		ammEnvRoot + "=" + st.overlay.root,
		ammEnvApp + "=" + st.overlay.merged,
		ammEnvImage + "=" + st.overlay.image,
		ammEnvCmd + "=" + st.spec.Command,
		ammEnvHostname + "=" + st.spec.ID,
		ammEnvPort + "=" + strconv.Itoa(st.spec.Port),
		ammEnvRootUpper + "=" + st.overlay.rootUpper,
		ammEnvRootWork + "=" + st.overlay.rootWork,
	}
	for _, k := range order {
		env = append(env, k+"="+merged[k])
	}
	return env
}
