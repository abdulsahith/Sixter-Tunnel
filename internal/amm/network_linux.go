//go:build linux

package amm

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"sixter.in/tunnel/internal/wg"
)

/*
Pod networking — giving every container its own address.

Until now every container shared the host's network namespace. That works for
one copy of one application and blocks everything interesting:

  - two replicas of the same app collide on the same port, so a machine can
    only ever run one of them;
  - a rolling deploy is impossible, because the new container cannot bind the
    port until the old one has already released it — meaning downtime by
    construction;
  - a container can reach every service on the host, including the database
    socket and the agent's own control plane.

Each container now gets a network namespace, a veth pair into a host bridge,
and an address from this machine's pod subnet. Because the overlay advertises
that subnet as a route, a container here can reach a container on another
machine directly — the same property that makes Kubernetes' network model
work, built on the WireGuard mesh instead of a cloud VPC.

	container netns          host
	┌──────────────┐        ┌──────────────────────────────┐
	│ eth0         │◄──veth─┤ sixter-br ──► overlay ──► ... │
	│ 100.68.3.7   │        │ 100.68.3.1                    │
	└──────────────┘        └──────────────────────────────┘

The ordering here is the subtle part. A namespace only exists once the child
process has been cloned, but the child must not start the application until its
interface is configured — otherwise the app binds before it has an address and
fails on a network that appears seconds later. Parent and child are therefore
synchronised over a pipe: the child blocks reading one byte, the parent builds
the interface and then writes it.
*/

// Bridge is the host-side switch every local container attaches to.
const Bridge = "sixter-br"

var netMu sync.Mutex

// netAvailable reports whether pod networking can be configured here.
func netAvailable() error {
	if _, err := exec.LookPath("ip"); err != nil {
		return fmt.Errorf("iproute2 not installed (apt install iproute2)")
	}
	return nil
}

// EnsureBridge creates the host bridge for a pod subnet, idempotently.
//
// The bridge holds .1 of the subnet and is every container's default route.
// Called once per agent start and again whenever the assigned subnet changes.
func EnsureBridge(podCIDR string) error {
	if err := netAvailable(); err != nil {
		return err
	}
	gw := wg.GatewayIP(podCIDR)
	if gw == "" {
		return fmt.Errorf("amm: malformed pod subnet %q", podCIDR)
	}

	netMu.Lock()
	defer netMu.Unlock()

	if _, err := os.Stat("/sys/class/net/" + Bridge); err != nil {
		if out, err := exec.Command("ip", "link", "add", Bridge, "type", "bridge").CombinedOutput(); err != nil &&
			!strings.Contains(string(out), "File exists") {
			return fmt.Errorf("amm: create bridge: %v: %s", err, strings.TrimSpace(string(out)))
		}
	}
	// Replace any stale address: if the machine's pod subnet changed, leaving
	// the old one attached would keep matching routes and blackhole traffic.
	out, _ := exec.Command("ip", "-o", "-4", "addr", "show", "dev", Bridge).Output()
	want := gw + "/" + itoaN(wg.PodsPerNode)
	found := false
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		for i, v := range f {
			if v == "inet" && i+1 < len(f) {
				if f[i+1] == want {
					found = true
				} else {
					_ = exec.Command("ip", "addr", "del", f[i+1], "dev", Bridge).Run()
				}
			}
		}
	}
	if !found {
		if out, err := exec.Command("ip", "addr", "add", want, "dev", Bridge).CombinedOutput(); err != nil &&
			!strings.Contains(string(out), "File exists") {
			return fmt.Errorf("amm: address bridge: %v: %s", err, strings.TrimSpace(string(out)))
		}
	}
	if out, err := exec.Command("ip", "link", "set", Bridge, "up").CombinedOutput(); err != nil {
		return fmt.Errorf("amm: bridge up: %v: %s", err, strings.TrimSpace(string(out)))
	}

	// Containers must be able to reach the world (package installs, APIs) and
	// the overlay. Forwarding is off by default on most distributions, and
	// without it every container is silently isolated.
	_ = os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1\n"), 0644)

	// Masquerade pod traffic leaving for the internet, but NOT traffic staying
	// inside the overlay: peers route the pod subnet directly, and rewriting
	// the source there would break return paths and hide which container an
	// audit log refers to.
	return ensureMasquerade(podCIDR)
}

// ensureMasquerade adds the NAT rule for egress, once.
func ensureMasquerade(podCIDR string) error {
	if _, err := exec.LookPath("iptables"); err != nil {
		return nil // no iptables: containers still talk locally and over the overlay
	}
	check := exec.Command("iptables", "-t", "nat", "-C", "POSTROUTING",
		"-s", podCIDR, "!", "-d", wg.OverlayCIDR, "-j", "MASQUERADE")
	if check.Run() == nil {
		return nil // already present
	}
	out, err := exec.Command("iptables", "-t", "nat", "-A", "POSTROUTING",
		"-s", podCIDR, "!", "-d", wg.OverlayCIDR, "-j", "MASQUERADE").CombinedOutput()
	if err != nil {
		return fmt.Errorf("amm: masquerade rule: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

/* ─────────── per-container interface ─────────── */

// attachPod moves a veth into the container's namespace and configures it.
//
// Runs in the PARENT, using the child's pid to enter its namespace. Doing the
// work from outside means the container image needs no networking tools of its
// own — a scratch or distroless image gets a working interface exactly like a
// full distribution would.
func attachPod(pid int, podIP, podCIDR, id string) error {
	if err := netAvailable(); err != nil {
		return err
	}
	gw := wg.GatewayIP(podCIDR)
	if gw == "" {
		return fmt.Errorf("amm: malformed pod subnet %q", podCIDR)
	}

	// Interface names are capped at 15 characters by the kernel, and a project
	// name can be much longer — truncate deterministically rather than letting
	// `ip` fail on a name the user chose.
	host := "sxv" + shortID(id)
	peer := "sxp" + shortID(id)

	netMu.Lock()
	defer netMu.Unlock()

	// A crashed container can leave its veth behind; recreate cleanly.
	_ = exec.Command("ip", "link", "del", host).Run()

	if out, err := exec.Command("ip", "link", "add", host, "type", "veth",
		"peer", "name", peer).CombinedOutput(); err != nil {
		return fmt.Errorf("amm: create veth: %v: %s", err, strings.TrimSpace(string(out)))
	}
	cleanup := func() { _ = exec.Command("ip", "link", "del", host).Run() }

	if out, err := exec.Command("ip", "link", "set", host, "master", Bridge).CombinedOutput(); err != nil {
		cleanup()
		return fmt.Errorf("amm: attach to bridge: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("ip", "link", "set", host, "up").CombinedOutput(); err != nil {
		cleanup()
		return fmt.Errorf("amm: host veth up: %v: %s", err, strings.TrimSpace(string(out)))
	}

	spid := itoaN(pid)
	// `ip link set ... netns <pid>` DOES accept a pid — this call is fine.
	if out, err := exec.Command("ip", "link", "set", peer, "netns", spid).CombinedOutput(); err != nil {
		cleanup()
		return fmt.Errorf("amm: move veth into container: %v: %s", err, strings.TrimSpace(string(out)))
	}

	// But `ip -n` does NOT. It resolves its argument as a NAMED namespace under
	// /var/run/netns, so passing a pid fails with "Cannot open network
	// namespace". Our container's namespace was created by clone() and has no
	// name — only /proc/<pid>/ns/net.
	//
	// The fix is the standard one every CNI plugin uses: symlink the proc entry
	// into /var/run/netns so iproute2 can find it by name, then remove the link.
	// The namespace itself is unaffected; only the name is temporary.
	nsName := "sxc" + shortID(id)
	nsLink := "/var/run/netns/" + nsName
	if err := os.MkdirAll("/var/run/netns", 0755); err != nil {
		cleanup()
		return fmt.Errorf("amm: netns dir: %w", err)
	}
	_ = os.Remove(nsLink) // a crashed container can leave one behind
	if err := os.Symlink("/proc/"+spid+"/ns/net", nsLink); err != nil {
		cleanup()
		return fmt.Errorf("amm: name container namespace: %w", err)
	}
	defer os.Remove(nsLink)

	// Configure the container's side from out here, so the image needs no
	// networking tools of its own — a scratch or distroless image gets a
	// working interface exactly like a full distribution would.
	steps := [][]string{
		{"ip", "-n", nsName, "link", "set", peer, "name", "eth0"},
		{"ip", "-n", nsName, "addr", "add", podIP + "/" + itoaN(wg.PodsPerNode), "dev", "eth0"},
		{"ip", "-n", nsName, "link", "set", "eth0", "up"},
		{"ip", "-n", nsName, "link", "set", "lo", "up"},
		{"ip", "-n", nsName, "route", "add", "default", "via", gw},
	}
	for _, st := range steps {
		if out, err := exec.Command(st[0], st[1:]...).CombinedOutput(); err != nil {
			cleanup()
			return fmt.Errorf("amm: %s: %v: %s", strings.Join(st, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// detachPod removes a container's host-side veth. The container side goes away
// with its namespace, so only this end can leak.
func detachPod(id string) {
	netMu.Lock()
	defer netMu.Unlock()
	_ = exec.Command("ip", "link", "del", "sxv"+shortID(id)).Run()
}

// shortID produces a stable 11-char suffix, keeping interface names inside the
// kernel's 15-character limit.
func shortID(id string) string {
	var h uint32 = 2166136261
	for i := 0; i < len(id); i++ {
		h ^= uint32(id[i])
		h *= 16777619
	}
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	out := make([]byte, 0, 11)
	for i := 0; i < 7; i++ {
		out = append(out, digits[h%36])
		h /= 36
	}
	return string(out)
}

func itoaN(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
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
