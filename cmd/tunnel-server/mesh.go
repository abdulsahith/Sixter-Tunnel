package main

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"sixter.in/tunnel/internal/auth"
	"sixter.in/tunnel/internal/wg"
)

/*
Overlay registration for repo servers.

Nodes register through the heartbeat, which already authenticates them. A repo
server is not a node — it has no node credentials — but it is the machine that
usually HOSTS the database, so it has to be on the overlay too.

It authenticates the same way it does for domain attachment: the account's API
token. That keeps one credential per account rather than inventing a second
machine identity for a machine that already has one.
*/

// POST /api/mesh/register {sub, public_key, port, reachable, role}
//
//	→ {overlay_ip, peers, reachable, warning}
func handleMeshRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	tok := strings.TrimSpace(r.Header.Get("X-Token"))
	if tok == "" {
		if a := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(a), "bearer ") {
			tok = strings.TrimSpace(a[7:])
		}
	}

	var req struct {
		Sub       string `json:"sub"`
		PublicKey string `json:"public_key"`
		Port      int    `json:"port"`
		Reachable bool   `json:"reachable"`
		Role      string `json:"role"`
		// PublicIPs are the addresses this machine believes it has. Needed
		// because a repo server sharing a host with the gateway, or sitting
		// behind nginx, appears to connect from 127.0.0.1 — an endpoint that
		// would make every peer dial its own loopback.
		PublicIPs []string `json:"public_ips"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	req.Sub = strings.ToLower(strings.TrimSpace(req.Sub))
	if req.Sub == "" || !wg.Key(req.PublicKey).Valid() {
		http.Error(w, "sub and a valid public_key are required", http.StatusBadRequest)
		return
	}
	if !store.ValidateToken(req.Sub, tok) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	owner, ok := store.OwnerOf(req.Sub)
	if !ok {
		http.Error(w, "unknown subdomain", http.StatusNotFound)
		return
	}

	// The machine cannot know how it appears from outside its own NAT, so the
	// address we observed is authoritative; it supplies only the port. A
	// machine that says it isn't reachable is registered WITHOUT an endpoint,
	// so peers know to dial elsewhere rather than waiting on a handshake that
	// can never complete.
	endpoint := ""
	if req.Reachable {
		if host := routableAddr(r, req.PublicIPs); host != "" {
			endpoint = net.JoinHostPort(host, itoaLB(portOrDefault(req.Port)))
		}
	}

	role := req.Role
	if role == "" {
		role = "db"
	}
	self, err := store.RegisterMesh(owner, auth.MeshMember{
		ID:         "repo:" + req.Sub,
		Name:       req.Sub,
		PublicKey:  req.PublicKey,
		Endpoint:   endpoint,
		ListenPort: portOrDefault(req.Port),
		Role:       role,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	members := store.AllMeshMembers(owner)
	reachable, warning := auth.MeshReachable(members)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"overlay_ip": self.OverlayIP,
		"peers":      auth.ToPeers(members, self.ID),
		"reachable":  reachable,
		"warning":    warning,
	})
}

func portOrDefault(p int) int {
	if p <= 0 || p > 65535 {
		return wg.DefaultPort
	}
	return p
}


// routableAddr decides the address peers should dial for this machine.
//
// Preference order, and the order matters:
//
//  1. the address we OBSERVED, if it is publicly routable — the most
//     trustworthy signal, since we watched the packet arrive from it;
//  2. an address the machine CLAIMS, if it checks out as public — needed when
//     the observed address is useless, which happens whenever the caller shares
//     a host with the gateway (127.0.0.1) or arrives through a reverse proxy.
//
// Anything else yields "": no endpoint at all. That is deliberately better
// than a wrong one — a peer with no endpoint waits to be dialled, whereas a
// peer pointed at 127.0.0.1 dials itself forever while looking healthy.
func routableAddr(r *http.Request, claimed []string) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			xff = xff[:i]
		}
		if ip := net.ParseIP(strings.TrimSpace(xff)); wg.IsRoutable(ip) {
			return ip.String()
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		if ip := net.ParseIP(host); wg.IsRoutable(ip) {
			return ip.String()
		}
	}
	for _, c := range claimed {
		if ip := net.ParseIP(strings.TrimSpace(c)); wg.IsRoutable(ip) {
			return ip.String()
		}
	}
	return ""
}
