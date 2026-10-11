package auth

import "time"

/*
Node registry — the control-plane side of a Sixter fleet.

A node is a machine running the Sixter agent. Enrolment mirrors the workspace
join-link design: ONE permanent token per account enrols the whole fleet, and
each machine exchanges it for its own credentials. That matters because a
fleet token in a cloud-init script is effectively public within your org — it
must not be usable to impersonate an existing node.

Connection direction is deliberate: agents dial OUT to the gateway. Nothing
here ever connects into a customer's network, so there are no inbound firewall
rules, no VPC peering, and NAT is irrelevant.
*/

// Node is one enrolled machine.
type Node struct {
	ID     string `json:"id"`
	Owner  string `json:"owner"`
	Name   string `json:"name"`
	Secret string `json:"-"` // sha256 of the node's own token; never serialised

	// Reported once at enrolment.
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	CPUs      int    `json:"cpus"`
	MemTotal  int64  `json:"mem_total"`
	AgentVer  string `json:"agent_version"`
	Isolation string `json:"isolation"` // container | process
	// IsolationReason explains WHY a node can't isolate (missing cgroup v2,
	// no overlayfs, not root). Without it "unisolated" is a dead end for the
	// operator — the agent knows the cause, so it should travel with the node.
	IsolationReason string `json:"isolation_reason,omitempty"`

	// Network identity. Needed to answer the question every stateful deploy
	// runs into: can THIS node actually reach the database?
	//
	// PrivateIPs are the node's own RFC1918 addresses; PublicIP is the address
	// the gateway saw it connect from. Same public IP usually means the same
	// NAT, and overlapping private ranges HINT at a shared LAN — but neither
	// proves reachability, so a probe result is stored alongside rather than
	// inferred from these.
	PrivateIPs []string `json:"private_ips,omitempty"`
	PublicIP   string   `json:"public_ip,omitempty"`

	// Refreshed on every heartbeat.
	CPUPct    int   `json:"cpu_pct"`
	MemPct    int   `json:"mem_pct"`
	Instances int   `json:"instances"`
	LoadAvg   string `json:"load_avg"`

	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"last_seen"`
}

// Online reports whether the node has heartbeated recently. Agents beat every
// 10s; 45s of silence means the machine, the network or the agent is gone.
func (n Node) Online() bool {
	return time.Since(n.LastSeen) < 45*time.Second
}

// NodeStore is the node-registry half of Storage. Kept as its own interface so
// the surface is obvious, but implemented by the same stores.
type NodeStore interface {
	// NodeJoinToken returns the account's permanent fleet-enrolment token,
	// creating it on first read.
	NodeJoinToken(owner string) (string, error)
	RotateNodeJoin(owner string) (string, error)

	// EnrollNode exchanges a fleet token for per-node credentials. The
	// returned Node carries the plaintext secret exactly once — it is stored
	// hashed, so a leaked database can't be used to impersonate a node.
	EnrollNode(joinToken string, n Node) (node Node, secret string, err error)

	// AuthNode validates a node's own credentials (used by heartbeat).
	AuthNode(nodeID, secret string) (Node, bool)

	// TouchNode records a heartbeat.
	TouchNode(nodeID string, cpuPct, memPct, instances int, load string) error

	ListNodes(owner string) []Node
	DeleteNode(owner, nodeID string) error
}
