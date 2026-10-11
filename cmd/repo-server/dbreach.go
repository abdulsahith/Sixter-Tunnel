package main

import (
	"net"
	"strconv"
	"regexp"
	"sort"
	"strings"
)

/*
Making a database reachable from another machine.

A project's config is written for THIS host: DB_URL points at 127.0.0.1,
because that's where Postgres is. Ship that config to a replica on another
machine and it means "this node's own loopback", where nothing is listening.
The app starts cleanly and fails on its first query — the worst kind of
failure, because it looks like the deploy worked.

There are exactly three network situations, and they need different answers:

	same host        127.0.0.1 is correct        nothing to do
	same LAN         the DB host's private IP    rewrite, allow in pg_hba
	different site   no direct path exists       encrypted overlay (WireGuard)

This file handles the first two: it finds the address a LAN peer would use,
rewrites config to it, and reports what it changed. Guessing is deliberately
avoided — the rewritten address is handed to the agent, which probes it before
starting anything and refuses with a clear reason if it can't connect.
*/

// hostPrivateIPs returns addresses on this machine that ANOTHER machine could
// plausibly connect to.
//
// Filtering is by INTERFACE, not by address range. A container bridge like
// docker0 usually sits on 172.17.0.1, and that address is meaningful only on
// this host — handing it to a replica guarantees a connection refused. But
// 172.16.0.0/12 as a whole is legitimate private space that plenty of
// companies run their LAN on, so excluding the range would break real
// deployments. The interface name is the honest signal.
func hostPrivateIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if !usableIface(iface.Name) {
			continue
		}
		addrs, aerr := iface.Addrs()
		if aerr != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil || !ip.IsPrivate() || ip.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, ip.String())
		}
	}
	sort.Strings(out)
	return out
}

// virtualPrefixes are interfaces whose addresses exist only on this host:
// container bridges, their veth legs, VM bridges and CNI plumbing. An address
// on any of them is useless to a peer.
var virtualPrefixes = []string{
	"docker", "br-", "veth", "virbr", "cni", "flannel", "cali",
	"tun", "tap", "sixter-br", "sixter0", "wg",
}

func usableIface(name string) bool {
	n := strings.ToLower(name)
	for _, p := range virtualPrefixes {
		if strings.HasPrefix(n, p) {
			return false
		}
	}
	return true
}

// localHostRe matches loopback references inside a connection string, in both
// URL form (@127.0.0.1:5432/db) and key-value DSN form (host=localhost).
var localHostRe = regexp.MustCompile(`(?i)\b(127\.0\.0\.1|localhost|::1)\b`)

// rewriteForFleet returns a copy of env with loopback addresses replaced by an
// address other machines could use, plus a description of every change.
//
// Nothing is rewritten when this host has no private address — inventing one
// would replace a clearly-wrong value with a subtly-wrong one, which is worse.
func rewriteForFleet(env map[string]string) (map[string]string, []string, string) {
	// Prefer the OVERLAY address when this machine is on the mesh: it is
	// reachable from every peer regardless of location, whereas a LAN address
	// only works for machines on the same network. Falling back to the LAN
	// address keeps same-network fleets working without the overlay at all.
	target := overlayIP()
	if target == "" {
		ips := hostPrivateIPs()
		if len(ips) == 0 {
			return env, nil, ""
		}
		target = ips[0]
	}

	out := make(map[string]string, len(env))
	var changed []string
	for k, v := range env {
		if localHostRe.MatchString(v) {
			out[k] = localHostRe.ReplaceAllString(v, target)
			changed = append(changed, k)
		} else {
			out[k] = v
		}
	}
	sort.Strings(changed)
	return out, changed, target
}

// dbEndpoints extracts host:port pairs a replica will have to connect to, so
// the agent can verify reachability BEFORE starting an app against them.
//
// Deliberately conservative: only values that look like a database connection
// string are considered. Probing every URL in the environment would stall
// deploys on unrelated third-party APIs.
func dbEndpoints(env map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(host, port string) {
		if host == "" || port == "" {
			return
		}
		ep := host + ":" + port
		if !seen[ep] {
			seen[ep] = true
			out = append(out, ep)
		}
	}

	urlRe := regexp.MustCompile(`(?i)\b(?:postgres|postgresql|mysql|mongodb|redis|amqp)(?:\+\w+)?://[^@\s]*@?([\w.\-]+):(\d+)`)
	kvHost := regexp.MustCompile(`(?i)\bhost=([\w.\-]+)`)
	kvPort := regexp.MustCompile(`(?i)\bport=(\d+)`)

	for k, v := range env {
		for _, m := range urlRe.FindAllStringSubmatch(v, -1) {
			add(m[1], m[2])
		}
		// key-value DSN: "host=10.0.0.5 user=app port=5432"
		if h := kvHost.FindStringSubmatch(v); h != nil {
			port := "5432"
			if p := kvPort.FindStringSubmatch(v); p != nil {
				port = p[1]
			}
			add(h[1], port)
		}
		// discrete DB_HOST / DB_PORT pairs
		if strings.EqualFold(k, "DB_HOST") || strings.EqualFold(k, "PGHOST") {
			port := env["DB_PORT"]
			if port == "" {
				port = env["PGPORT"]
			}
			if port == "" {
				port = "5432"
			}
			add(v, port)
		}
	}
	sort.Strings(out)
	return out
}


// fleetBlocker explains why a scaled replica cannot possibly reach this
// machine's private services, or "" when it can.
//
// A cloud VPS typically has one public address and no private one at all. Its
// database is on 127.0.0.1 and there is no LAN for a replica elsewhere to join,
// so no rewrite is possible and no amount of retrying will help. Saying that
// plainly beats letting the deploy fail later with "cannot reach 127.0.0.1".
func fleetBlocker(env map[string]string) string {
	usesLoopback := false
	for _, v := range env {
		if localHostRe.MatchString(v) {
			usesLoopback = true
			break
		}
	}
	if !usesLoopback {
		return "" // nothing here is host-local; replicas are fine
	}
	if overlayIP() != "" {
		return "" // on the mesh: every peer can reach us
	}
	if len(hostPrivateIPs()) > 0 {
		return "" // a LAN address exists; peers on that network can reach us
	}
	return "This server has no private network address, so a replica on another " +
		"machine has no way to reach its database. Install wireguard-tools here " +
		"and on each node to enable the private overlay — it gives every machine " +
		"an address that works across providers."
}


// hostLANSubnet returns the CIDR of the network this machine sits on, e.g.
// "172.31.16.0/20" — the range its LAN peers will connect FROM.
//
// Needed because a firewall rule has to name a network, not a host. Derived
// from the interface's own prefix length rather than assumed, since a /20 VPC
// and a /24 office LAN look identical from the address alone.
func hostLANSubnet() string {
	best := hostPrivateIPs()
	if len(best) == 0 {
		return ""
	}
	target := best[0]

	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || !usableIface(iface.Name) {
			continue
		}
		addrs, aerr := iface.Addrs()
		if aerr != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil || ipnet.IP.String() != target {
				continue
			}
			return ipnet.IP.Mask(ipnet.Mask).String() + "/" + maskBits(ipnet.Mask)
		}
	}
	return ""
}

func maskBits(m net.IPMask) string {
	ones, _ := m.Size()
	return strconv.Itoa(ones)
}
