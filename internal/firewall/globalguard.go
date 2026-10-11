package firewall

// GlobalGuard is the edge-wide DDoS layer that sits IN FRONT of the per-subdomain
// engines. A single VPS can't stop volumetric (L3/L4) floods — those need the
// network provider / Cloudflare — but this absorbs application-layer (L7) floods:
//
//   - a global request-rate window: if total req/s across ALL tunnels spikes
//     past a threshold, the edge enters "under attack" mode for a cooldown;
//   - a per-IP global rate: an IP that floods across many subdomains is banned
//     edge-wide (not just on one tunnel);
//   - while under attack, the server can challenge un-trusted clients (handled
//     by the caller, which has the cookies).

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// GuardConfig tunes the global limiter (0 = sensible default).
type GuardConfig struct {
	GlobalPerSec  int           // total req/s across the edge that triggers attack mode
	PerIPPerMin   int           // req/min from one IP (across all tunnels) → edge ban
	AttackCooloff time.Duration // how long attack mode lasts after the last spike
	BanDuration   time.Duration // base edge-ban length (escalates with repeat strikes)
}

type guardIP struct {
	hits     []time.Time
	strikes  int
	bannedTo time.Time
}

// GlobalGuard is safe for concurrent use.
type GlobalGuard struct {
	mu  sync.Mutex
	cfg GuardConfig

	hits        []time.Time // global sliding window (last 1s)
	ips         map[string]*guardIP
	attackUntil time.Time

	// stats (for the admin dashboard)
	totalSeen    int64
	totalBlocked int64
	lastReqPerS  int
}

// NewGlobalGuard builds a guard, filling zero config fields with defaults sized
// for a single modest VPS.
func NewGlobalGuard(cfg GuardConfig) *GlobalGuard {
	if cfg.GlobalPerSec <= 0 {
		cfg.GlobalPerSec = 800 // edge-wide req/s before "under attack"
	}
	if cfg.PerIPPerMin <= 0 {
		cfg.PerIPPerMin = 1200 // 20 req/s sustained from ONE ip → ban
	}
	if cfg.AttackCooloff <= 0 {
		cfg.AttackCooloff = 90 * time.Second
	}
	if cfg.BanDuration <= 0 {
		cfg.BanDuration = 10 * time.Minute
	}
	return &GlobalGuard{cfg: cfg, ips: map[string]*guardIP{}}
}

// GuardResult is the verdict for one request at the edge.
type GuardResult struct {
	Block  bool // hard drop (edge-banned or per-IP flood)
	Attack bool // edge is under attack — caller should challenge untrusted clients
}

// Check accounts one request from ip and returns the edge verdict.
func (g *GlobalGuard) Check(ip string) GuardResult {
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.totalSeen++

	// already edge-banned?
	if st := g.ips[ip]; st != nil && now.Before(st.bannedTo) {
		g.totalBlocked++
		return GuardResult{Block: true, Attack: now.Before(g.attackUntil)}
	}

	// global rate over the last second
	cutoff := now.Add(-time.Second)
	kept := g.hits[:0]
	for _, t := range g.hits {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	g.hits = append(kept, now)
	g.lastReqPerS = len(g.hits)
	if g.lastReqPerS > g.cfg.GlobalPerSec {
		g.attackUntil = now.Add(g.cfg.AttackCooloff)
	}
	attack := now.Before(g.attackUntil)

	// per-IP rate over the last minute (across every tunnel)
	st := g.ips[ip]
	if st == nil {
		st = &guardIP{}
		g.ips[ip] = st
	}
	minCut := now.Add(-time.Minute)
	k2 := st.hits[:0]
	for _, t := range st.hits {
		if t.After(minCut) {
			k2 = append(k2, t)
		}
	}
	st.hits = append(k2, now)

	// under attack, tighten the per-IP budget to a quarter
	limit := g.cfg.PerIPPerMin
	if attack {
		limit = limit / 4
		if limit < 60 {
			limit = 60
		}
	}
	if len(st.hits) > limit {
		st.strikes++
		// escalating ban: 1x, 2x, 4x … capped at 24h
		mult := time.Duration(1) << uint(min(st.strikes-1, 7))
		ban := g.cfg.BanDuration * mult
		if ban > 24*time.Hour {
			ban = 24 * time.Hour
		}
		st.bannedTo = now.Add(ban)
		st.hits = nil
		g.totalBlocked++
		return GuardResult{Block: true, Attack: attack}
	}

	return GuardResult{Block: false, Attack: attack}
}

// Ban blocks an IP at the edge across EVERY tunnel on the platform. d <= 0 is
// an effectively permanent ban. Used by the admin control plane.
func (g *GlobalGuard) Ban(ip string, d time.Duration) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return
	}
	if d <= 0 {
		d = 100 * 365 * 24 * time.Hour
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.ips[ip]
	if st == nil {
		st = &guardIP{}
		g.ips[ip] = st
	}
	st.bannedTo = time.Now().Add(d)
}

// Unban lifts an edge ban immediately.
func (g *GlobalGuard) Unban(ip string) {
	ip = strings.TrimSpace(ip)
	g.mu.Lock()
	defer g.mu.Unlock()
	if st := g.ips[ip]; st != nil {
		st.bannedTo = time.Time{}
	}
}

// BannedIP is one active edge ban.
type BannedIP struct {
	IP    string `json:"ip"`
	Until string `json:"until"`
}

// BannedList returns every IP currently banned at the edge.
func (g *GlobalGuard) BannedList() []BannedIP {
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	out := []BannedIP{}
	for ip, st := range g.ips {
		if st != nil && now.Before(st.bannedTo) {
			out = append(out, BannedIP{IP: ip, Until: st.bannedTo.UTC().Format(time.RFC3339)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IP < out[j].IP })
	return out
}

// UnderAttack reports whether the edge is currently in attack mode.
func (g *GlobalGuard) UnderAttack() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return time.Now().Before(g.attackUntil)
}

// GuardStats is a snapshot for the admin dashboard.
type GuardStats struct {
	UnderAttack  bool  `json:"underAttack"`
	ReqPerSec    int   `json:"reqPerSec"`
	BannedIPs    int   `json:"bannedIPs"`
	TotalSeen    int64 `json:"totalSeen"`
	TotalBlocked int64 `json:"totalBlocked"`
}

// Stats returns a snapshot (also prunes nothing — cheap).
func (g *GlobalGuard) Stats() GuardStats {
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	banned := 0
	for _, st := range g.ips {
		if now.Before(st.bannedTo) {
			banned++
		}
	}
	return GuardStats{
		UnderAttack:  now.Before(g.attackUntil),
		ReqPerSec:    g.lastReqPerS,
		BannedIPs:    banned,
		TotalSeen:    g.totalSeen,
		TotalBlocked: g.totalBlocked,
	}
}
