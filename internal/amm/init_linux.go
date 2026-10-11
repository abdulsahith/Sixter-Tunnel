//go:build linux

package amm

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

/*
Container PID 1.

The parent re-execs /proc/self/exe with argv[0] == "amm-init" and the
CLONE_NEW* flags set, so this code is already inside fresh namespaces when it
starts. Its job, in order:

	1. make mount propagation private   (or our mounts leak to the host)
	2. pivot_root into the overlay      (the app can no longer see the host fs)
	3. mount /proc /sys /dev /tmp       (inside the new namespaces)
	4. drop privileges                  (no_new_privs, capability bounding set)
	5. exec the application             (which becomes PID 1's own image)

Step 1 is the one people forget: without MS_PRIVATE the container's mounts
propagate back into the host namespace and you slowly wallpaper the host with
stray mounts.

Note on PID 1 semantics: PID 1 in a pid namespace does NOT get default signal
handlers, and it is responsible for reaping orphans. We exec the app directly
so it *becomes* PID 1 — simple and correct for single-process apps. A
multi-process app that orphans children would need a reaping loop here instead;
that's a deliberate R1 limitation, noted rather than hidden.
*/

const (
	ammInitArgv0   = "amm-init"
	ammEnvRoot     = "AMM_ROOT"
	ammEnvApp      = "AMM_APP"
	ammEnvImage    = "AMM_IMAGE"
	ammEnvCmd      = "AMM_CMD"
	ammEnvHostname = "AMM_HOSTNAME"
	ammEnvPort     = "AMM_PORT"

	// Per-container writable layer for the ROOT filesystem (image mode).
	ammEnvRootUpper = "AMM_ROOT_UPPER"
	ammEnvRootWork  = "AMM_ROOT_WORK"

	// ammEnvNetNS is set ONLY when the parent passes a network-sync pipe on
	// fd 3. It cannot be inferred: os.NewFile succeeds for any non-negative
	// descriptor, so a host-networked container would happily "open" fd 3,
	// read from something that isn't a pipe, and fail with EBADF.
	ammEnvNetNS = "AMM_NETNS"
)

// hostSystemDirs are bind-mounted read-only from the host into the container
// root. The application needs a shell, its language runtime and libc; a
// project directory contains none of those.
//
// This is a "host-derived rootfs": no image to download, the app gets exactly
// the runtime the host already has (so the same node/python that built it),
// and everything is read-only so the container can't modify the host system.
// Real image layers are the next phase; this makes containers work today.
var hostSystemDirs = []string{
	"/bin", "/sbin", "/lib", "/lib64", "/usr", "/etc",
}

// IsInit reports whether this process was re-exec'd as a container init.
// main() must call this FIRST, before any other setup, and hand off to
// RunInit if true.
func IsInit() bool {
	return len(os.Args) > 0 && os.Args[0] == ammInitArgv0
}

// RunInit is the container's PID 1. It never returns: it either execs the
// application or exits non-zero.
func RunInit() {
	root := os.Getenv(ammEnvRoot)
	app := os.Getenv(ammEnvApp)
	command := os.Getenv(ammEnvCmd)
	hostname := os.Getenv(ammEnvHostname)

	if root == "" || command == "" {
		fatal("amm-init: missing AMM_ROOT or AMM_CMD")
	}

	// PID 1 gets no default signal dispositions; make sure a stray signal
	// during setup doesn't leave a half-built container wedged.
	signal.Ignore(syscall.SIGTTOU, syscall.SIGTTIN)

	if hostname != "" {
		_ = syscall.Sethostname([]byte(hostname))
	}

	// 0. Wait for the parent to build our network interface.
	//
	//    We are already inside a fresh network namespace, which contains only a
	//    down loopback device. The parent can't configure it until we exist, and
	//    we must not start the application until it has — an app that binds
	//    before its address arrives either fails outright or listens on nothing.
	//    One byte on fd 3 is the handshake.
	waitForNetwork()

	// 1. Detach mount propagation. Without this, everything we mount below
	//    propagates back into the host mount namespace.
	if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		fatal("amm-init: make root private: %v", err)
	}

	// 2. pivot_root requires the new root to be a mount point distinct from
	//    the old root, so bind-mount the skeleton onto itself first.
	if err := syscall.Mount(root, root, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
		fatal("amm-init: bind new root: %v", err)
	}

	// 2b. Populate the root filesystem.
	//
	//   image set → bind each top-level entry of the extracted OCI rootfs.
	//               PORTABLE: identical on every machine that pulls it.
	//   no image  → bind the host's system dirs. Works, but pins the container
	//               to this host and cannot be scheduled elsewhere.
	image := os.Getenv(ammEnvImage)
	sources := hostSystemDirs
	fromImage := false
	rootMounted := false
	if image != "" {
		// Preferred: ONE copy-on-write overlay of the whole image. The shared
		// extracted rootfs stays pristine underneath while this container
		// writes into its own upper layer. This is what lets a build write to
		// /root/.cache and /go — read-only bind mounts made any image whose
		// command writes outside /app die instantly, which on a golang image
		// is every single build.
		upper, work := os.Getenv(ammEnvRootUpper), os.Getenv(ammEnvRootWork)
		if upper != "" && work != "" {
			opts := "lowerdir=" + image + ",upperdir=" + upper + ",workdir=" + work
			if err := syscall.Mount("overlay", root, "overlay", 0, opts); err == nil {
				rootMounted = true
			}
		}
		if !rootMounted {
			// Fallback (old host runtime, or overlay refused): read-only binds
			// of each top-level entry. Portable but unwritable outside /app.
			if entries, err := os.ReadDir(image); err == nil && len(entries) > 0 {
				sources = nil
				for _, e := range entries {
					if n := e.Name(); n != "app" && !strings.HasPrefix(n, ".") {
						sources = append(sources, filepath.Join(image, n))
					}
				}
				fromImage = true
			} else {
				fatal("amm-init: image rootfs %s is empty or unreadable: %v", image, err)
			}
		}
	}

	if !rootMounted {
		for _, d := range sources {
			if _, err := os.Stat(d); err != nil {
				continue // e.g. /lib64 is absent on some distros
			}
			name := filepath.Base(d)
			if !fromImage {
				name = strings.TrimPrefix(d, "/")
			}
			target := filepath.Join(root, name)
			if err := os.MkdirAll(target, 0755); err != nil {
				fatal("amm-init: mkdir %s: %v", target, err)
			}
			if err := syscall.Mount(d, target, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
				fatal("amm-init: bind %s: %v", d, err)
			}
			// Remount read-only. A bind mount ignores flags on the first call,
			// so the RDONLY has to be applied by a second remount — a classic
			// gotcha. These sources are shared (host dirs or the image cache),
			// so they MUST stay read-only.
			_ = syscall.Mount("", target, "",
				syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY|syscall.MS_REC, "")
		}
	}

	// An OCI image ships no resolv.conf — DNS configuration belongs to the
	// machine, not the image. Copy the host's in (Docker does the same),
	// otherwise the first outbound lookup inside the container fails:
	// "go mod download" and "npm install" die on 'no such host'. The host
	// filesystem is still visible here because pivot_root hasn't happened yet.
	// Best-effort: writes land in the container's writable layer, and the
	// host-derived rootfs path already carries the host's /etc.
	if image != "" {
		_ = os.MkdirAll(filepath.Join(root, "etc"), 0755)
		for _, f := range []string{"/etc/hosts", "/etc/nsswitch.conf"} {
			if b, err := os.ReadFile(f); err == nil {
				_ = os.WriteFile(filepath.Join(root, "etc", filepath.Base(f)), b, 0644)
			}
		}
		_ = os.WriteFile(filepath.Join(root, "etc", "resolv.conf"), []byte(containerResolvConf()), 0644)
	}

	if app != "" {
		target := filepath.Join(root, "app")
		if err := os.MkdirAll(target, 0755); err != nil {
			fatal("amm-init: mkdir /app: %v", err)
		}
		if err := syscall.Mount(app, target, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
			fatal("amm-init: bind app dir: %v", err)
		}
	}

	oldRoot := filepath.Join(root, ".oldroot")
	if err := os.MkdirAll(oldRoot, 0700); err != nil {
		fatal("amm-init: mkdir oldroot: %v", err)
	}
	if err := syscall.PivotRoot(root, oldRoot); err != nil {
		fatal("amm-init: pivot_root: %v", err)
	}
	if err := os.Chdir("/"); err != nil {
		fatal("amm-init: chdir /: %v", err)
	}

	// Detach and remove the old root — after this the host filesystem is
	// genuinely unreachable from inside, not merely hidden.
	if err := syscall.Unmount("/.oldroot", syscall.MNT_DETACH); err != nil {
		fatal("amm-init: unmount oldroot: %v", err)
	}
	_ = os.Remove("/.oldroot")

	// 3. Pseudo-filesystems, now inside our namespaces. /proc must be mounted
	//    AFTER the pid namespace exists so it shows container pids, not host.
	mountAll()

	// 4. Drop privileges. no_new_privs makes this irreversible: even a setuid
	//    binary inside the container cannot regain what we removed.
	if err := prctlNoNewPrivs(); err != nil {
		fatal("amm-init: no_new_privs: %v", err)
	}
	dropCapabilities()

	// 5. Run from the application directory, exactly as the systemd unit did.
	if err := os.Chdir("/app"); err != nil {
		fatal("amm-init: chdir /app: %v (is the project directory mounted?)", err)
	}

	// 6. Become the application. Using a shell keeps existing project run
	//    commands ("npm start", "gunicorn app:app") working unchanged.
	sh := "/bin/sh"
	if _, err := os.Stat(sh); err != nil {
		fatal("amm-init: no /bin/sh inside the container — the host system "+
			"directories failed to mount (%v)", err)
	}
	// -lc would run login scripts that may not exist in this root; -c is
	// enough and avoids spurious failures.
	args := []string{sh, "-c", command}
	if err := syscall.Exec(sh, args, os.Environ()); err != nil {
		fatal("amm-init: exec %q: %v", command, err)
	}
}

// containerResolvConf builds a resolver config the container can actually use.
//
// Copying the host's /etc/resolv.conf verbatim is wrong on any systemd-resolved
// system — which is most of Debian and Ubuntu. That file contains a single
// nameserver, 127.0.0.53, the stub listener resolved runs on the HOST's
// loopback. A container has its own network namespace and therefore its own
// loopback, where nothing is listening, so every lookup fails with "connection
// refused" and the build dies fetching dependencies.
//
// The real upstream servers are in /run/systemd/resolve/resolv.conf, which
// resolved maintains for exactly this purpose. Docker does the same
// substitution. Public resolvers are the last resort so a container is never
// left with no DNS at all.
func containerResolvConf() string {
	// systemd-resolved's uplink file: the actual upstream servers.
	if b, err := os.ReadFile("/run/systemd/resolve/resolv.conf"); err == nil {
		if s := usableResolv(string(b)); s != "" {
			return s
		}
	}
	if b, err := os.ReadFile("/etc/resolv.conf"); err == nil {
		if s := usableResolv(string(b)); s != "" {
			return s
		}
	}
	return "nameserver 1.1.1.1\nnameserver 8.8.8.8\n"
}

// usableResolv keeps only nameserver lines that a container can reach, i.e.
// anything that isn't a loopback stub.
func usableResolv(src string) string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "nameserver ") {
			addr := strings.TrimSpace(strings.TrimPrefix(t, "nameserver "))
			if strings.HasPrefix(addr, "127.") || addr == "::1" {
				continue // a stub on the HOST's loopback; unreachable from here
			}
			out = append(out, t)
			continue
		}
		if strings.HasPrefix(t, "search ") || strings.HasPrefix(t, "options ") {
			out = append(out, t)
		}
	}
	// Only useful if at least one real server survived the filter.
	for _, l := range out {
		if strings.HasPrefix(l, "nameserver ") {
			return strings.Join(out, "\n") + "\n"
		}
	}
	return ""
}

// waitForNetwork blocks until the parent signals that this container's
// interface is ready, or gives up after a bounded wait.
//
// fd 3 is passed by the parent (cmd.ExtraFiles). Its absence means host
// networking, where there is nothing to wait for. A closed pipe with no byte
// means the parent failed and is about to kill us — proceeding would start an
// application that can never be reached, so we exit with a message instead.
func waitForNetwork() {
	if os.Getenv(ammEnvNetNS) != "1" {
		return // host networking: this container has no interface to wait for
	}
	f := os.NewFile(3, "netsync")
	if f == nil {
		return
	}
	defer f.Close()

	// A plain blocking read, deliberately. An inherited descriptor is not
	// registered with Go's poller, so SetReadDeadline on it is unreliable —
	// and a deadline isn't needed: the parent either writes one byte on
	// success, or kills us and closes the pipe on failure, which surfaces here
	// as EOF. Either way we never block forever.
	buf := make([]byte, 1)
	if _, err := f.Read(buf); err != nil {
		fatal("amm-init: network setup did not complete (%v) — the container "+
			"would have no reachable address", err)
	}
}

// mountAll sets up the minimum pseudo-filesystems an app expects. Failures are
// tolerated individually: a static Go binary needs almost none of these, and
// refusing to start over a missing /sys would be worse than continuing.
func mountAll() {
	type mnt struct {
		src, dst, fs string
		flags        uintptr
		data         string
	}
	nosuid := uintptr(syscall.MS_NOSUID)
	nodev := uintptr(syscall.MS_NODEV)
	noexec := uintptr(syscall.MS_NOEXEC)

	for _, m := range []mnt{
		// /proc must be a fresh mount so it reflects the new pid namespace
		{"proc", "/proc", "proc", nosuid | nodev | noexec, ""},
		{"sysfs", "/sys", "sysfs", nosuid | nodev | noexec | syscall.MS_RDONLY, ""},
		{"tmpfs", "/dev", "tmpfs", nosuid, "mode=755,size=64k"},
		{"devpts", "/dev/pts", "devpts", nosuid | noexec, "gid=5,mode=620"},
		{"tmpfs", "/dev/shm", "tmpfs", nosuid | nodev, "mode=1777,size=64m"},
		{"tmpfs", "/tmp", "tmpfs", nosuid | nodev, "mode=1777"},
	} {
		_ = os.MkdirAll(m.dst, 0755)
		_ = syscall.Mount(m.src, m.dst, m.fs, m.flags, m.data)
	}

	// Minimal device nodes. mknod needs CAP_MKNOD, which we still hold at
	// this point (capabilities are dropped after this).
	for _, d := range []struct {
		path string
		mode uint32
		dev  int
	}{
		{"/dev/null", syscall.S_IFCHR | 0666, makedev(1, 3)},
		{"/dev/zero", syscall.S_IFCHR | 0666, makedev(1, 5)},
		{"/dev/random", syscall.S_IFCHR | 0666, makedev(1, 8)},
		{"/dev/urandom", syscall.S_IFCHR | 0666, makedev(1, 9)},
		{"/dev/tty", syscall.S_IFCHR | 0666, makedev(5, 0)},
	} {
		_ = syscall.Mknod(d.path, d.mode, d.dev)
	}
}

func makedev(major, minor int) int {
	return (major << 8) | minor
}

// prctlNoNewPrivs sets PR_SET_NO_NEW_PRIVS, which prevents this process and
// all its descendants from ever gaining privileges via setuid/setgid binaries
// or file capabilities.
func prctlNoNewPrivs() error {
	const PR_SET_NO_NEW_PRIVS = 38
	_, _, errno := syscall.RawSyscall6(syscall.SYS_PRCTL, PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// dropCapabilities removes dangerous capabilities from the bounding set so the
// application can never acquire them.
//
// R1 keeps this deliberately conservative and readable: we drop the
// capabilities that most directly enable container escape and host damage.
// A full default-deny allowlist plus a seccomp-BPF syscall filter is the R1.5
// hardening step — noted here so it isn't quietly forgotten.
func dropCapabilities() {
	const PR_CAPBSET_DROP = 24
	dangerous := []uintptr{
		21, // CAP_SYS_ADMIN   — mount, namespace ops; the big one
		16, // CAP_SYS_MODULE  — load kernel modules
		23, // CAP_SYS_NICE
		24, // CAP_SYS_RESOURCE
		19, // CAP_SYS_PTRACE  — inspect other processes
		18, // CAP_SYS_CHROOT
		17, // CAP_SYS_RAWIO   — raw hardware access
		9,  // CAP_LINUX_IMMUTABLE
		12, // CAP_NET_ADMIN   — reconfigure networking
		8,  // CAP_SETPCAP
		2,  // CAP_DAC_READ_SEARCH
		1,  // CAP_DAC_OVERRIDE
	}
	for _, c := range dangerous {
		_, _, _ = syscall.RawSyscall6(syscall.SYS_PRCTL, PR_CAPBSET_DROP, c, 0, 0, 0, 0)
	}
}

func fatal(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}
