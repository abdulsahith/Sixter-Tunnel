//go:build linux

package wg

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

/*
Applying a configuration to the kernel.

We drive the standard `wg` and `ip` tools rather than speaking netlink
directly. That is a deliberate trade: netlink would remove a package
dependency, but wireguard-tools is packaged everywhere, is what every operator
already knows how to debug, and `wg show` is the first thing anyone types when
a tunnel misbehaves. Matching the ecosystem is worth more here than removing
one apt package.

The interface is reconfigured with `wg syncconf`, never `wg setconf`:
syncconf applies a diff, leaving untouched peers — and their live handshakes —
alone. setconf tears down and rebuilds every peer, which drops every open
database connection in the fleet. On a ten-second reconcile loop that would be
a permanent outage rather than a tunnel.
*/

// Interface is the overlay device name. Fixed so operators and firewall rules
// can refer to it, and so a leftover device from a previous run is reused
// rather than accumulating sxt0, sxt1, sxt2...
const Interface = "sixter0"

// Available reports whether this host can run the overlay, with a reason that
// names the fix rather than the symptom.
func Available() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("needs root to configure a network interface")
	}
	if _, err := exec.LookPath("wg"); err != nil {
		return fmt.Errorf("wireguard-tools not installed (apt install wireguard-tools)")
	}
	if _, err := exec.LookPath("ip"); err != nil {
		return fmt.Errorf("iproute2 not installed (apt install iproute2)")
	}
	// The kernel module is usually autoloaded on first use, but on minimal
	// cloud images it isn't present at all — better to say so now than to fail
	// at `ip link add` with a bare "operation not supported".
	if err := exec.Command("modprobe", "wireguard").Run(); err != nil {
		if _, serr := os.Stat("/sys/module/wireguard"); serr != nil {
			return fmt.Errorf("the wireguard kernel module is unavailable " +
				"(install linux-headers and wireguard, or use a kernel with WireGuard built in)")
		}
	}
	return nil
}

// Apply brings the overlay to the desired state, idempotently.
func Apply(cfg Config) error {
	if err := Available(); err != nil {
		return err
	}
	if cfg.OverlayIP == "" {
		return fmt.Errorf("wg: no overlay address assigned")
	}

	if err := ensureLink(); err != nil {
		return err
	}
	if err := ensureAddress(cfg.OverlayIP); err != nil {
		return err
	}

	// syncconf reads a file, and the file holds a private key — write it in a
	// root-only directory with 0600 and remove it immediately afterwards.
	dir := "/etc/sixter"
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, Interface+".conf")
	if err := os.WriteFile(path, []byte(cfg.Render()), 0600); err != nil {
		return fmt.Errorf("wg: write config: %w", err)
	}
	defer os.Remove(path)

	// `wg syncconf` requires a stripped config (no Address/MTU keys, which are
	// ip(8)'s business). `wg-quick strip` does that for us.
	stripped, err := exec.Command("wg-quick", "strip", path).Output()
	if err != nil {
		return fmt.Errorf("wg: strip config: %w", err)
	}
	tmp := path + ".stripped"
	if err := os.WriteFile(tmp, stripped, 0600); err != nil {
		return err
	}
	defer os.Remove(tmp)

	if out, err := exec.Command("wg", "syncconf", Interface, tmp).CombinedOutput(); err != nil {
		return fmt.Errorf("wg: syncconf: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("ip", "link", "set", "up", "dev", Interface).CombinedOutput(); err != nil {
		return fmt.Errorf("wg: link up: %v: %s", err, strings.TrimSpace(string(out)))
	}

	// Routes are NOT optional, and nothing else adds them.
	//
	// The interface holds a /32, which describes exactly one address and
	// therefore creates no subnet route. Without an explicit route per peer the
	// kernel has no path to 100.64.0.3 at all — packets are rejected before
	// they ever reach WireGuard, so the tunnel looks perfectly configured and
	// carries nothing. wg-quick does this step for you; driving ip/wg directly
	// means doing it ourselves.
	return ensureRoutes(cfg)
}

// ensureRoutes installs a route for every address reachable through the tunnel:
// each peer's own /32 plus any subnet it carries (its pod network).
func ensureRoutes(cfg Config) error {
	want := map[string]bool{}
	for _, p := range cfg.Peers {
		if p.OverlayIP != "" {
			want[p.OverlayIP+"/32"] = true
		}
		for _, r := range p.Routes {
			if validCIDR(r) {
				want[r] = true
			}
		}
	}

	for dst := range want {
		// `replace` rather than `add`: idempotent, and it corrects a route that
		// used to point somewhere else after a peer moved.
		if out, err := exec.Command("ip", "route", "replace", dst, "dev", Interface).CombinedOutput(); err != nil {
			return fmt.Errorf("wg: route %s: %v: %s", dst, err, strings.TrimSpace(string(out)))
		}
	}

	// Drop routes on this interface that no longer belong to any peer, so a
	// removed machine stops black-holing traffic aimed at its old address.
	out, err := exec.Command("ip", "-o", "route", "show", "dev", Interface).Output()
	if err != nil {
		return nil // routes are in place; pruning is best-effort
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		dst := f[0]
		if !strings.Contains(dst, "/") {
			dst += "/32"
		}
		if !want[dst] {
			_ = exec.Command("ip", "route", "del", f[0], "dev", Interface).Run()
		}
	}
	return nil
}

func ensureLink() error {
	if _, err := os.Stat("/sys/class/net/" + Interface); err == nil {
		return nil
	}
	out, err := exec.Command("ip", "link", "add", "dev", Interface, "type", "wireguard").CombinedOutput()
	if err != nil && !strings.Contains(string(out), "File exists") {
		return fmt.Errorf("wg: create %s: %v: %s", Interface, err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("ip", "link", "set", "mtu", fmt.Sprint(MTU), "dev", Interface).CombinedOutput(); err != nil {
		return fmt.Errorf("wg: set mtu: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ensureAddress makes cfgIP the only Sixter address on the interface.
//
// Replacing rather than adding matters: if an account is re-provisioned and the
// machine's overlay IP changes, a stale address left behind would keep matching
// routes and quietly blackhole traffic for the new one.
func ensureAddress(cfgIP string) error {
	want := cfgIP + "/32"
	out, err := exec.Command("ip", "-o", "-4", "addr", "show", "dev", Interface).Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			for i, f := range fields {
				if f == "inet" && i+1 < len(fields) {
					if fields[i+1] == want {
						return nil // already correct
					}
					_ = exec.Command("ip", "addr", "del", fields[i+1], "dev", Interface).Run()
				}
			}
		}
	}
	if out, err := exec.Command("ip", "addr", "add", want, "dev", Interface).CombinedOutput(); err != nil &&
		!strings.Contains(string(out), "File exists") {
		return fmt.Errorf("wg: add address %s: %v: %s", want, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Down removes the overlay interface entirely (used when a machine leaves).
func Down() error {
	if _, err := os.Stat("/sys/class/net/" + Interface); err != nil {
		return nil
	}
	return exec.Command("ip", "link", "del", "dev", Interface).Run()
}

// PeerStatus is what `wg show` reports about one peer — the ground truth for
// whether the mesh is actually working, as opposed to merely configured.
type PeerStatus struct {
	PublicKey     string
	Endpoint      string
	LastHandshake time.Time
	RxBytes       int64
	TxBytes       int64
}

// Status parses `wg show <iface> dump`.
//
// A configured peer that has never handshaken is the single most common
// failure — usually a blocked UDP port — and it is invisible unless something
// reads this. Surfacing it turns "the database is unreachable" into "no
// handshake with db-host: check UDP 51820".
func Status() ([]PeerStatus, error) {
	out, err := exec.Command("wg", "show", Interface, "dump").Output()
	if err != nil {
		return nil, err
	}
	var peers []PeerStatus
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i, line := range lines {
		if i == 0 {
			continue // first line describes the interface itself
		}
		f := strings.Split(line, "\t")
		if len(f) < 8 {
			continue
		}
		p := PeerStatus{PublicKey: f[0], Endpoint: f[2]}
		if secs := parseInt(f[4]); secs > 0 {
			p.LastHandshake = time.Unix(secs, 0)
		}
		p.RxBytes, p.TxBytes = parseInt(f[5]), parseInt(f[6])
		peers = append(peers, p)
	}
	return peers, nil
}

func parseInt(s string) int64 {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	return n
}
