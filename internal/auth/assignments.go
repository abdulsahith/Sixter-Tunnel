package auth

import (
	"sort"
	"strings"
	"time"
)

/*
Assignments — desired state for the fleet.

An Assignment says "this application should be running on this node". The
gateway owns desired state; agents reconcile toward it. That direction matters:
agents dial out and may be behind NAT, so the gateway can never push. Instead
every heartbeat carries the node's current assignment list, and the agent
starts or stops containers to match.

This is the same control loop Kubernetes uses (desired vs actual), minus the
distributed consensus — a single gateway is the source of truth, which is
appropriate at this scale and vastly simpler to reason about.
*/

// AssignState is the agent-reported status of an assignment.
type AssignState string

const (
	AssignPending AssignState = "pending" // scheduled, agent hasn't acted yet
	AssignRunning AssignState = "running"
	AssignFailed  AssignState = "failed"
	AssignStopped AssignState = "stopped"
)

// Assignment is one application placed on one node.
type Assignment struct {
	ID      string `json:"id"`
	Owner   string `json:"owner"`
	NodeID  string `json:"node_id"`
	Project string `json:"project"`

	// What to run. Image is required for fleet workloads: without a portable
	// artifact the node has nothing to pull, which is exactly why images had
	// to come first.
	Image   string            `json:"image"`
	Command string            `json:"command"`
	Port    int               `json:"port"`
	Env     map[string]string `json:"env,omitempty"`

	// Probe lists host:port dependencies (the database) that the agent must
	// verify it can reach before starting the workload.
	Probe []string `json:"probe,omitempty"`

	MemoryMB   int `json:"memory_mb"`
	CPUPercent int `json:"cpu_percent"`

	// Routing. A fleet workload runs on a machine that dials out and has no
	// inbound port, so traffic can't be proxied to an IP. Instead the agent
	// opens a reverse tunnel for this subdomain using RouteToken, and the
	// gateway's existing subdomain routing carries requests through it —
	// no new proxy path, no NAT traversal problem.
	Subdomain  string `json:"subdomain"`
	RouteToken string `json:"route_token,omitempty"` // sent to the agent only

	// Source lets a node run YOUR code, not just a public image.
	//
	// A fleet node has no copy of your project. Rather than invent a
	// distribution channel, the agent downloads the same tarball the CLI
	// already pulls from the repo-server, using a scoped token that grants
	// read access to exactly one project. The image supplies the runtime
	// (node, python, go); the source supplies the app.
	SourceURL   string `json:"source_url,omitempty"`
	SourceToken string `json:"source_token,omitempty"`

	// Reported back by the agent.
	State   AssignState `json:"state"`
	Message string      `json:"message,omitempty"`

	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// AssignmentStore is the desired-state half of the control plane.
type AssignmentStore interface {
	CreateAssignment(owner string, a Assignment) (Assignment, error)
	ListAssignments(owner string) []Assignment
	AssignmentsForNode(nodeID string) []Assignment
	// AllAssignments is used for subdomain → route-token lookup at the edge.
	AllAssignments() []Assignment
	SetAssignmentState(id string, state AssignState, msg string) error
	DeleteAssignment(owner, id string) error
}

// pickNode chooses where to place a workload.
//
// Least-loaded-first: among online nodes that can actually isolate, pick the
// one with the most free capacity, weighting memory over CPU because memory is
// the hard constraint (you can share a core; you cannot share a byte). Nodes
// that can't run containers are used only as a last resort, and never silently
// preferred just because they're idle.
func pickNode(nodes []Node, existing []Assignment) (Node, bool) {
	load := map[string]int{}
	for _, a := range existing {
		if a.State != AssignStopped {
			load[a.NodeID]++
		}
	}

	var eligible, fallback []Node
	for _, n := range nodes {
		if !n.Online() {
			continue
		}
		if n.Isolation == "container" {
			eligible = append(eligible, n)
		} else {
			fallback = append(fallback, n)
		}
	}
	pool := eligible
	if len(pool) == 0 {
		pool = fallback
	}
	if len(pool) == 0 {
		return Node{}, false
	}

	score := func(n Node) int {
		// higher is better: free memory and CPU headroom, minus current placements
		return (100-n.MemPct)*2 + (100 - n.CPUPct) - load[n.ID]*10
	}
	sort.Slice(pool, func(i, j int) bool {
		si, sj := score(pool[i]), score(pool[j])
		if si != sj {
			return si > sj
		}
		return pool[i].Name < pool[j].Name // stable for equal scores
	})
	return pool[0], true
}

// Schedule places a workload on the best available node and gives it a public
// route. Exposed so callers don't reimplement placement policy.
func Schedule(s Storage, owner string, a Assignment) (Assignment, error) {
	if a.NodeID == "" {
		n, ok := pickNode(s.ListNodes(owner), s.ListAssignments(owner))
		if !ok {
			return Assignment{}, errNoCapacity
		}
		a.NodeID = n.ID
	}

	// Derive a subdomain from the owner's own so fleet apps live under a name
	// they already control: project "api" for owner sub "acme" → "api-acme".
	if a.Subdomain == "" {
		base := strings.ToLower(strings.TrimSpace(a.Project))
		if u, ok := s.GetUser(owner); ok && u.TunnelSub != "" {
			a.Subdomain = base + "-" + u.TunnelSub
		} else {
			a.Subdomain = base
		}
	}

	// A caller can name its own subdomain, so refuse one that already belongs to
	// somebody else. Otherwise an account could publish into a stranger's pool
	// and take a share of their traffic.
	for _, ex := range s.AllAssignments() {
		if strings.EqualFold(ex.Subdomain, a.Subdomain) && !strings.EqualFold(ex.Owner, owner) {
			return Assignment{}, errSubTaken
		}
	}

	// Every replica of a project must join ONE pool, so they have to share both
	// the address and the credential for it. The gateway authorises a reverse
	// tunnel by matching the route token registered for that subdomain — mint a
	// fresh token per replica and the second machine is rejected, which is
	// exactly the difference between "scaled" and "a second deployment".
	if a.RouteToken == "" {
		for _, ex := range s.ListAssignments(owner) {
			if strings.EqualFold(ex.Subdomain, a.Subdomain) && ex.RouteToken != "" {
				a.RouteToken = ex.RouteToken
				break
			}
		}
	}
	return s.CreateAssignment(owner, a)
}

// RouteTokenFor finds the assignment that owns a subdomain and returns its
// route token. Used by the gateway to authorise an agent's reverse tunnel.
func RouteTokenFor(s Storage, sub string) (string, bool) {
	for _, a := range s.AllAssignments() {
		if strings.EqualFold(a.Subdomain, sub) && a.RouteToken != "" {
			return a.RouteToken, true
		}
	}
	return "", false
}

type schedErr string

func (e schedErr) Error() string { return string(e) }

const errNoCapacity = schedErr("no online node available — install the agent on a machine first")
const errSubTaken = schedErr("that service address is already in use by another account")

func normalizeAssignment(a *Assignment) {
	a.Project = strings.TrimSpace(a.Project)
	a.Image = strings.TrimSpace(a.Image)
	a.Command = strings.TrimSpace(a.Command)
	a.Subdomain = strings.ToLower(strings.TrimSpace(a.Subdomain))
	if a.RouteToken == "" {
		a.RouteToken = genEnrollToken() // credential for this app's reverse tunnel
	}
	if a.State == "" {
		a.State = AssignPending
	}
	now := time.Now()
	if a.Created.IsZero() {
		a.Created = now
	}
	a.Updated = now
}
