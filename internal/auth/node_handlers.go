package auth

import (
	"net"
	"net/http"
	"strings"
	"time"

	"sixter.in/tunnel/internal/wg"
)

/*
Node API — two audiences, deliberately separated.

	AGENT (no session, dials out from a machine)
	  POST /api/node/enroll     fleet token → per-node credentials
	  POST /api/node/heartbeat  node credentials → liveness + capacity

	CONSOLE (browser session, account owner)
	  GET    /api/nodes         list the fleet
	  GET    /api/nodes/token   the permanent join token
	  POST   /api/nodes/token   rotate it
	  DELETE /api/nodes?id=     remove a node

The split matters: agent endpoints must never accept a browser session, and
console endpoints must never accept a node secret.
*/

// POST /api/node/enroll {token, name, os, arch, cpus, memTotal, agent, isolation}
func (s *Server) handleNodeEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		Token     string `json:"token"`
		Name      string `json:"name"`
		OS        string `json:"os"`
		Arch      string `json:"arch"`
		CPUs      int    `json:"cpus"`
		MemTotal  int64  `json:"memTotal"`
		Agent     string `json:"agent"`
		Isolation  string   `json:"isolation"`
		Reason     string   `json:"isolation_reason"`
		PrivateIPs []string `json:"private_ips"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		req.Name = "unnamed-node"
	}

	node, secret, err := s.store.EnrollNode(strings.TrimSpace(req.Token), Node{
		Name: req.Name, OS: req.OS, Arch: req.Arch, CPUs: req.CPUs,
		MemTotal: req.MemTotal, AgentVer: req.Agent, Isolation: req.Isolation,
		IsolationReason: req.Reason,
		PrivateIPs:      sanitizeIPs(req.PrivateIPs),
		// The node can't know how it appears from outside its own NAT, so the
		// gateway records the address it actually connected from. Two nodes
		// sharing a public IP are almost certainly behind the same router —
		// the first real hint that a direct LAN path might exist.
		PublicIP: remoteIP(r),
	})
	if err != nil {
		jsonError(w, http.StatusForbidden, err.Error())
		return
	}

	// The plaintext secret is returned exactly once — we only store its hash.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "node_id": node.ID, "secret": secret, "name": node.Name,
	})
}

// remoteIP returns the client address, honouring X-Forwarded-For only for its
// first entry (the original client) since anything later is appended by proxies.
func remoteIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			xff = xff[:i]
		}
		if ip := net.ParseIP(strings.TrimSpace(xff)); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	return host
}

// sanitizeIPs keeps only well-formed private IPv4 addresses. An agent is
// untrusted input at enrolment time, and these values end up in firewall
// rules — never store something that wasn't parsed.
func sanitizeIPs(in []string) []string {
	var out []string
	for _, v := range in {
		ip := net.ParseIP(strings.TrimSpace(v))
		if ip == nil {
			continue
		}
		if v4 := ip.To4(); v4 != nil && v4.IsPrivate() {
			out = append(out, v4.String())
		}
		if len(out) >= 8 {
			break // a machine with more addresses than this is reporting noise
		}
	}
	return out
}

// POST /api/node/heartbeat {node_id, secret, cpuPct, memPct, instances, load}
//
// Also the control channel: the response is where the gateway will hand back
// work to run. Today it just acknowledges — placement is the next phase — but
// the shape is fixed now so agents don't need a protocol change later.
func (s *Server) handleNodeHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		NodeID    string `json:"node_id"`
		Secret    string `json:"secret"`
		CPUPct    int    `json:"cpuPct"`
		MemPct    int    `json:"memPct"`
		Instances int    `json:"instances"`
		Load      string `json:"load"`
		// Status the agent reports back for work it was given.
		Report []struct {
			ID      string `json:"id"`
			State   string `json:"state"`
			Message string `json:"message"`
		} `json:"report"`

		// Overlay registration. Carried on the heartbeat because the node
		// secret already proves identity here — a separate mesh endpoint would
		// mean a second authentication path to design, secure and get wrong.
		WGPublicKey string `json:"wg_public_key,omitempty"`
		WGPort      int    `json:"wg_port,omitempty"`
		// WGReachable is the agent's own assessment of whether its UDP port is
		// reachable from outside. Only a machine that says so is advertised
		// with an endpoint; advertising an unreachable one makes every peer
		// wait for a handshake that can never complete.
		WGReachable bool `json:"wg_reachable,omitempty"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	node, ok := s.store.AuthNode(req.NodeID, req.Secret)
	if !ok {
		// 401 tells the agent to re-enrol rather than retry forever — this is
		// what happens after an admin removes a node.
		jsonError(w, http.StatusUnauthorized, "unknown node — re-enrol with a join token")
		return
	}
	_ = s.store.TouchNode(req.NodeID, req.CPUPct, req.MemPct, req.Instances, req.Load)

	// Accept the agent's status report for its assignments.
	for _, rep := range req.Report {
		_ = s.store.SetAssignmentState(rep.ID, AssignState(rep.State), rep.Message)
	}

	// Overlay membership, refreshed on every beat so a fleet converges without
	// any push channel: a machine that joins is known to all the others within
	// one heartbeat.
	var overlay map[string]interface{}
	if req.WGPublicKey != "" {
		ep := ""
		if req.WGReachable {
			// The node cannot know how it looks from outside its own NAT, so
			// the address the gateway observed is authoritative. The node only
			// supplies the port it listens on.
			// Only a routable address is usable. A node behind NAT or a
			// proxy that reports 127.0.0.1 or a private address must be left
			// endpoint-less so peers wait for it to dial them instead.
			if ip := remoteIP(r); ip != "" && wg.IsRoutable(net.ParseIP(ip)) {
				ep = net.JoinHostPort(ip, itoa(req.WGPort))
			}
		}
		self, merr := s.store.RegisterMesh(node.Owner, MeshMember{
			ID: node.ID, Name: node.Name, PublicKey: req.WGPublicKey,
			Endpoint: ep, ListenPort: req.WGPort, Role: "node",
		})
		if merr == nil {
			members := s.store.AllMeshMembers(node.Owner)
			ok, why := MeshReachable(members)
			overlay = map[string]interface{}{
				"overlay_ip": self.OverlayIP,
				"pod_cidr":   self.PodCIDR,
				"peers":      ToPeers(members, node.ID),
				"reachable":  ok,
				"warning":    why,
			}
		}
	}

	// The heartbeat response IS the control channel: agents dial out and may
	// be behind NAT, so the gateway can never push. It answers with the full
	// desired state for this node and the agent reconciles toward it.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":          true,
		"interval":    10, // seconds until the next expected beat
		"assignments": s.store.AssignmentsForNode(req.NodeID),
		"overlay":     overlay,
	})
}

// GET/DELETE /api/nodes — console view of the fleet.
func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	sameSiteCORS(w, r) // repo dashboard shows the fleet on the project page
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	owner := s.currentEmail(r)
	if owner == "" {
		jsonError(w, http.StatusUnauthorized, "not logged in")
		return
	}

	if r.Method == http.MethodDelete {
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if err := s.store.DeleteNode(owner, id); err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}

	nodes := s.store.ListNodes(owner)
	out := make([]map[string]interface{}, 0, len(nodes))
	online, cpus := 0, 0
	var mem int64
	for _, n := range nodes {
		if n.Online() {
			online++
			cpus += n.CPUs
			mem += n.MemTotal
		}
		out = append(out, map[string]interface{}{
			"id": n.ID, "name": n.Name, "os": n.OS, "arch": n.Arch,
			"cpus": n.CPUs, "memTotal": n.MemTotal, "agent": n.AgentVer,
			"isolation": n.Isolation, "isolationReason": n.IsolationReason,
			"cpuPct": n.CPUPct, "memPct": n.MemPct,
			"instances": n.Instances, "load": n.LoadAvg,
			"online":   n.Online(),
			"lastSeen": n.LastSeen.UTC().Format(time.RFC3339),
			"created":  n.Created.UTC().Format(time.RFC3339),
			// Network identity — what a database host needs to decide whether
			// a direct connection from this node is even possible.
			"privateIPs": n.PrivateIPs,
			"publicIP":   n.PublicIP,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"nodes": out,
		"fleet": map[string]interface{}{
			"total": len(nodes), "online": online,
			"cpus": cpus, "memTotal": mem,
		},
	})
}

// GET/POST/DELETE /api/fleet/apps — desired state for the fleet.
//
//	GET                      list placements + status
//	POST   {project,image,…} schedule onto the best node (or a named one)
//	DELETE ?id=              remove a placement (agent stops the container)
func (s *Server) handleFleetApps(w http.ResponseWriter, r *http.Request) {
	// The repo dashboard lives on <sub>.sixter.in and drives scaling from the
	// project page, so it needs credentialed cross-origin access to this API.
	sameSiteCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	owner := s.currentEmail(r)
	if owner == "" {
		jsonError(w, http.StatusUnauthorized, "not logged in")
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"apps": s.store.ListAssignments(owner),
		})

	case http.MethodPost:
		var a Assignment
		if !readJSON(w, r, &a) {
			return
		}
		if strings.TrimSpace(a.Project) == "" {
			jsonError(w, http.StatusBadRequest, "project name required")
			return
		}
		// A fleet workload MUST have an image: the target node has to pull the
		// filesystem from somewhere. Without one there is nothing to run.
		// A node needs SOMETHING to build a filesystem from: either a base
		// image, or your project's source (which it downloads from the repo
		// server). Requiring neither would schedule a container with nothing
		// in it.
		if strings.TrimSpace(a.Image) == "" && strings.TrimSpace(a.SourceURL) == "" {
			jsonError(w, http.StatusBadRequest,
				"set a base image (e.g. node:20-slim) or scale an existing project so the node can fetch its source")
			return
		}
		placed, err := Schedule(s.store, owner, a)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "app": placed})

	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if err := s.store.DeleteAssignment(owner, id); err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})

	default:
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// GET/POST /api/nodes/token — read or rotate the fleet join token.
func (s *Server) handleNodeToken(w http.ResponseWriter, r *http.Request) {
	owner := s.currentEmail(r)
	if owner == "" {
		jsonError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	var tok string
	var err error
	if r.Method == http.MethodPost {
		tok, err = s.store.RotateNodeJoin(owner)
	} else {
		tok, err = s.store.NodeJoinToken(owner)
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "token": tok})
}
