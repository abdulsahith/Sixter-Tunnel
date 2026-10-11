package auth

import (
	"net"
	"sort"
	"strings"
	"sync"

	"sixter.in/tunnel/internal/wg"
)

/*
Mesh coordination.

The gateway is an INTRODUCER, not a relay. It learns each machine's public key
and reachable endpoint, assigns it a stable overlay address, and tells every
other machine in the same account how to find it. Packets then flow directly
between peers; a database round trip never touches this server.

That distinction is the entire point. Terminating the overlay centrally would
be simpler to build and would put every customer's database traffic through
one host — a latency tax on every query and a spectacular breach if it were
ever compromised. Here the gateway holds only public keys: it cannot decrypt
anything, and losing it costs coordination, not confidentiality.

Membership is per ACCOUNT. A machine only ever learns about peers belonging to
the same owner, so one customer's fleet is invisible to another's even though
they share a coordinator.
*/

// MeshMember is one machine's registration on the overlay.
type MeshMember struct {
	ID        string `json:"id"`   // node id, or "repo:<sub>" for a repo server
	Name      string `json:"name"` // human label
	Owner     string `json:"owner"`
	PublicKey string `json:"public_key"` // WireGuard public key — never private
	OverlayIP string `json:"overlay_ip"`

	// Endpoint is where this machine can be reached from outside, as
	// host:port. Empty when it is behind NAT with no forwarded port: such a
	// machine cannot be dialled, but it can still dial others, and WireGuard
	// makes the session bidirectional once either side connects.
	Endpoint   string `json:"endpoint,omitempty"`
	ListenPort int    `json:"listen_port"`

	// Role distinguishes a machine that OFFERS a private service (the database
	// host) from one that merely consumes it. A fleet needs at least one
	// reachable member to bootstrap, and this is how the UI explains which.
	Role string `json:"role,omitempty"` // "db" | "node" | ""

	// PodCIDR is the slice of the pod network this machine's containers are
	// addressed from. Advertised to every peer as a route, which is what lets
	// a container on one machine reach a container on another.
	PodCIDR string `json:"pod_cidr,omitempty"`
}

// MeshStore is the coordination half of the overlay.
type MeshStore interface {
	// RegisterMesh records a machine's public key and endpoint, assigning a
	// stable overlay address on first call and preserving it afterwards.
	RegisterMesh(owner string, m MeshMember) (MeshMember, error)
	// MeshPeers returns every OTHER member in the account.
	MeshPeers(owner, selfID string) []MeshMember
	// AllMeshMembers lists an account's mesh (for the console).
	AllMeshMembers(owner string) []MeshMember
	// RemoveMesh drops a machine from the overlay.
	RemoveMesh(owner, id string) error
}

/* ─────────── account index allocation ─────────── */

// Each account gets its own /24 inside 100.64.0.0/10, so two customers can
// never be handed the same overlay address. The index is assigned once and
// persisted with the account; deriving it from a hash of the email would be
// stateless and would collide, and a collision here means two companies'
// machines think they are on the same network.
var (
	acctIdxMu sync.Mutex
	acctIdx   = map[string]int{}
)

// accountIndex returns a stable 0–255 slot for an owner.
func accountIndex(owner string, existing map[string]int) int {
	acctIdxMu.Lock()
	defer acctIdxMu.Unlock()
	owner = strings.ToLower(strings.TrimSpace(owner))
	if i, ok := acctIdx[owner]; ok {
		return i
	}
	for k, v := range existing {
		acctIdx[k] = v
	}
	if i, ok := acctIdx[owner]; ok {
		return i
	}
	used := map[int]bool{}
	for _, v := range acctIdx {
		used[v] = true
	}
	for i := 0; i <= 255; i++ {
		if !used[i] {
			acctIdx[owner] = i
			return i
		}
	}
	return 0
}

// buildMesh assigns an address and merges a member into an account's mesh.
//
// Shared by both stores so the allocation rules can't drift between the JSON
// and Postgres implementations — a difference there would produce fleets that
// work on one backend and collide on the other.
func buildMesh(owner string, m MeshMember, current []MeshMember, acctSlot int) (MeshMember, []MeshMember, error) {
	taken := map[string]string{}
	for _, ex := range current {
		if ex.OverlayIP != "" {
			taken[ex.OverlayIP] = ex.ID
		}
	}
	ip, err := wg.AllocateIP(acctSlot, taken, m.ID)
	if err != nil {
		return MeshMember{}, nil, err
	}

	// Pod subnets are allocated globally, not per account: they are routed
	// between machines, so two fleets must never be handed the same range even
	// though they never talk to each other. Cheap insurance against a support
	// case nobody would ever diagnose.
	pods := map[string]string{}
	for _, ex := range current {
		if ex.PodCIDR != "" {
			pods[ex.PodCIDR] = ex.ID
		}
	}
	podCIDR, perr := wg.AllocatePodCIDR(pods, m.ID)
	if perr != nil {
		return MeshMember{}, nil, perr
	}
	m.PodCIDR = podCIDR
	m.Owner = strings.ToLower(strings.TrimSpace(owner))
	m.OverlayIP = ip
	if m.ListenPort <= 0 || m.ListenPort > 65535 {
		m.ListenPort = wg.DefaultPort
	}

	out := make([]MeshMember, 0, len(current)+1)
	replaced := false
	for _, ex := range current {
		if ex.ID == m.ID {
			out = append(out, m)
			replaced = true
			continue
		}
		out = append(out, ex)
	}
	if !replaced {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OverlayIP < out[j].OverlayIP })
	return m, out, nil
}

// ToPeers converts mesh members into the peer list a machine applies locally.
func ToPeers(members []MeshMember, selfID string) []wg.Peer {
	var peers []wg.Peer
	for _, m := range members {
		if m.ID == selfID || m.PublicKey == "" || m.OverlayIP == "" {
			continue
		}
		p := wg.Peer{
			Name:      m.Name,
			PublicKey: wg.Key(m.PublicKey),
			OverlayIP: m.OverlayIP,
		}
		// Drop an endpoint that is no longer valid under current rules. Rows
		// persist across upgrades, so a stored IPv6 or loopback address would
		// otherwise keep being handed out forever.
		if host, _, err := net.SplitHostPort(m.Endpoint); err == nil &&
			wg.IsRoutable(net.ParseIP(host)) {
			p.Endpoint = m.Endpoint
		}
		if m.PodCIDR != "" {
			p.Routes = append(p.Routes, m.PodCIDR)
		}
		peers = append(peers, p)
	}
	return peers
}

// MeshReachable reports whether an account's overlay can form at all.
//
// WireGuard needs at least ONE member with a reachable endpoint to bootstrap:
// peers that can all only dial out will never find each other. Saying so
// explicitly is far better than a mesh that configures cleanly and silently
// never handshakes.
func MeshReachable(members []MeshMember) (bool, string) {
	if len(members) < 2 {
		return true, "" // nothing to connect yet
	}
	for _, m := range members {
		if strings.TrimSpace(m.Endpoint) != "" {
			return true, ""
		}
	}
	return false, "no machine in this fleet has a reachable address, so they cannot " +
		"establish a direct connection. Forward UDP " +
		itoa(wg.DefaultPort) + " to one machine — usually the database host — and the rest will dial it."
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
