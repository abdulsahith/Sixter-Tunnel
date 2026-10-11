package main

import (
	"sync/atomic"
	"time"
)

/*
Load balancing across replicas.

Originally one subdomain meant one tunnel: a second connection for the same
subdomain CLOSED the first. That's correct for a laptop reconnecting, but it
makes horizontal scaling impossible — replica 2 would kill replica 1.

Now each subdomain owns a POOL of live tunnels. Every replica that publishes
the subdomain joins the pool, and the gateway spreads requests across them.
Because the gateway already terminates every request, this needs no separate
load balancer: the routing table IS the balancer.

Selection is least-connections with round-robin as the tie-break:

  - least-conn beats plain round-robin when requests have uneven cost (one slow
    upload shouldn't keep receiving traffic just because it's "next");
  - round-robin breaks ties so equal-load replicas alternate fairly.

Unhealthy replicas are skipped: a tunnel that hasn't heartbeated within the
liveness window is passed over, and removed entirely when its socket closes.
*/

// tunnelPool holds every live tunnel publishing one subdomain.
type tunnelPool struct {
	members []*Tunnel
	rr      atomic.Uint64 // round-robin cursor for tie-breaks
}

// healthyWindow is how long a tunnel may go silent before we stop routing to
// it. Clients ping well inside this, so a miss means the process or network is
// gone — not that it's merely busy.
const healthyWindow = 45 * time.Second

// Passive health. A heartbeat only proves the TUNNEL is alive — it says
// nothing about the app behind it. A replica whose app is crashed, still
// building, or wedged keeps beating happily while failing every request.
//
// So we also learn from real traffic: consecutive failures eject a replica for
// a short cooldown, and any success clears the count. This is what makes a
// rolling deploy invisible — the machine being redeployed stops receiving
// traffic within a couple of requests instead of serving errors for a minute.
const (
	ejectAfterFails = 3
	ejectFor        = 15 * time.Second
)

func (t *Tunnel) recordFailure() {
	if t.fails.Add(1) >= ejectAfterFails {
		t.ejectedUntil.Store(time.Now().Add(ejectFor).UnixNano())
	}
}

func (t *Tunnel) recordSuccess() {
	if t.fails.Load() != 0 {
		t.fails.Store(0)
		t.ejectedUntil.Store(0)
	}
}

func (t *Tunnel) ejected() bool {
	until := t.ejectedUntil.Load()
	if until == 0 {
		return false
	}
	if time.Now().UnixNano() >= until {
		// Cooldown over — let it back in on probation. If it fails again it is
		// ejected immediately rather than after another full streak.
		t.ejectedUntil.Store(0)
		t.fails.Store(ejectAfterFails - 1)
		return false
	}
	return true
}

func (p *tunnelPool) healthy(t *Tunnel) bool {
	if t.ejected() {
		return false
	}
	last := t.lastBeat.Load()
	if last == 0 {
		// Never beaten yet. Trust it briefly after connecting, but not forever:
		// a client that connects and immediately goes silent would otherwise
		// stay "healthy" permanently and keep absorbing traffic.
		return time.Since(t.connectedAt) < healthyWindow
	}
	return time.Since(time.Unix(0, last)) < healthyWindow
}

// pick returns the tunnel that should serve the next request, or nil if the
// pool is empty. Falls back to an unhealthy member rather than failing the
// request outright — a stale replica is better than a 502.
func (p *tunnelPool) pick() *Tunnel {
	if p == nil || len(p.members) == 0 {
		return nil
	}
	if len(p.members) == 1 {
		return p.members[0]
	}

	var best *Tunnel
	bestInflight := int64(-1)
	for i := range p.members {
		t := p.members[i]
		if !p.healthy(t) {
			continue
		}
		n := t.inflight.Load()
		if bestInflight < 0 || n < bestInflight {
			best, bestInflight = t, n
		}
	}
	if best != nil {
		// Tie-break: if several are equally idle, rotate so we don't pin the
		// first one in the slice.
		tied := make([]*Tunnel, 0, len(p.members))
		for i := range p.members {
			t := p.members[i]
			if p.healthy(t) && t.inflight.Load() == bestInflight {
				tied = append(tied, t)
			}
		}
		if len(tied) > 1 {
			// Stay in uint64 for the modulo: converting the counter to int
			// first would eventually overflow to a negative index.
			return tied[(p.rr.Add(1)-1)%uint64(len(tied))]
		}
		return best
	}

	// nothing healthy — round-robin over everything rather than dropping traffic
	return p.members[(p.rr.Add(1)-1)%uint64(len(p.members))]
}

/* ─────────── pool registry (caller holds tunnelsMu) ─────────── */

// addTunnelLocked joins a tunnel to its subdomain's pool.
func addTunnelLocked(sub string, t *Tunnel) {
	p := pools[sub]
	if p == nil {
		p = &tunnelPool{}
		pools[sub] = p
	}
	p.members = append(p.members, t)
	// `tunnels` stays the "representative" tunnel for status/admin views.
	tunnels[sub] = t
}

// removeTunnelLocked drops a tunnel from its pool when its socket closes.
func removeTunnelLocked(sub string, t *Tunnel) {
	p := pools[sub]
	if p == nil {
		return
	}
	out := p.members[:0]
	for _, m := range p.members {
		if m != t {
			out = append(out, m)
		}
	}
	p.members = out

	if len(p.members) == 0 {
		delete(pools, sub)
		if tunnels[sub] == t {
			delete(tunnels, sub)
		}
		return
	}
	// keep the representative pointing at a live member
	if tunnels[sub] == t {
		tunnels[sub] = p.members[0]
	}
}

// pickTunnel is the proxy's entry point: the balanced choice for a subdomain.
func pickTunnel(sub string) (*Tunnel, bool) {
	tunnelsMu.RLock()
	p := pools[sub]
	t := p.pick()
	tunnelsMu.RUnlock()
	return t, t != nil
}

// pickExcluding returns a replica that hasn't been tried for this request.
//
// This is what turns a dead replica into a served request instead of a 502:
// when one member fails, the SAME request is re-sent to a different one. Only
// safe because a failure here means the app never saw the request — the tunnel
// write failed, or the client couldn't reach the local port.
func pickExcluding(sub string, tried map[*Tunnel]bool) *Tunnel {
	tunnelsMu.RLock()
	defer tunnelsMu.RUnlock()
	p := pools[sub]
	if p == nil {
		return nil
	}
	// Healthy and untried first.
	var fallback *Tunnel
	best := int64(-1)
	for _, t := range p.members {
		if tried[t] {
			continue
		}
		if !p.healthy(t) {
			if fallback == nil {
				fallback = t
			}
			continue
		}
		if n := t.inflight.Load(); best < 0 || n < best {
			best, fallback = n, t
		}
	}
	return fallback
}

// itoaLB is a tiny int→string used in log lines here.
func itoaLB(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// replicaCount reports how many live replicas serve a subdomain (for the UI).
func replicaCount(sub string) int {
	tunnelsMu.RLock()
	defer tunnelsMu.RUnlock()
	if p := pools[sub]; p != nil {
		return len(p.members)
	}
	return 0
}
