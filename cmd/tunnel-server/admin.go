package main

// Platform admin API, served on admin.<apex>. The control plane: see every
// account, force-logout or delete users, grant/revoke admin, and watch
// system-wide traffic + firewall (security) activity. Admin-only, behind the
// same login + 2FA as the rest of the platform.

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"sixter.in/tunnel/internal/auth"
)

var serverStart = time.Now()

// adminRouter handles everything on the admin host: /api/admin/* goes to the
// admin handlers; everything else (SPA, /api/login, /api/2fa, /api/me) falls
// through to the normal apex mux so the admin can log in with the same flow.
func adminRouter(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/admin/") {
		adminAPI(w, r)
		return
	}
	authMux.ServeHTTP(w, r)
}

// requireAdmin gates an admin endpoint; returns the admin's email on success.
func requireAdmin(w http.ResponseWriter, r *http.Request) (string, bool) {
	email := auth.ResolveSession(store, r)
	if email == "" {
		http.Error(w, "not logged in", http.StatusUnauthorized)
		return "", false
	}
	u, ok := store.GetUser(email)
	if !ok || !u.IsAdmin {
		// 404, not 403 — don't confirm the admin surface exists to non-admins
		http.NotFound(w, r)
		return "", false
	}
	return email, true
}

func adminJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func adminAPI(w http.ResponseWriter, r *http.Request) {
	admin, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/admin/")

	switch {
	case path == "overview":
		adminOverview(w, r)
	case path == "users":
		adminUsers(w, r)
	case path == "security":
		adminSecurity(w, r)
	case path == "threats":
		adminThreats(w, r)
	case path == "business":
		adminBusiness(w, r)
	case path == "audit":
		adminJSON(w, map[string]any{"events": recentAudit(200)})
	case path == "bans":
		adminBans(w, r, admin)
	case strings.HasPrefix(path, "user/"):
		adminUserDetail(w, r, strings.TrimPrefix(path, "user/"))
	case strings.HasPrefix(path, "users/"):
		adminUserAction(w, r, admin, strings.TrimPrefix(path, "users/"))
	default:
		http.NotFound(w, r)
	}
}

/* ─────────── overview ─────────── */

func adminOverview(w http.ResponseWriter, r *http.Request) {
	users := store.ListUsers()
	admins, twofa := 0, 0
	for _, u := range users {
		if u.IsAdmin {
			admins++
		}
		if u.TOTPEnabled {
			twofa++
		}
	}

	tunnelsMu.RLock()
	live := len(tunnels)
	var totalReq int64
	for _, t := range tunnels {
		totalReq += t.reqCount.Load()
	}
	tunnelsMu.RUnlock()

	// recent signups (last 7)
	type sign struct {
		Email     string `json:"email"`
		CreatedAt string `json:"createdAt"`
	}
	recent := []sign{}
	for i := len(users) - 1; i >= 0 && len(recent) < 7; i-- {
		recent = append(recent, sign{users[i].Email, users[i].CreatedAt.UTC().Format(time.RFC3339)})
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	adminJSON(w, map[string]any{
		"users":         len(users),
		"admins":        admins,
		"twoFactor":     twofa,
		"tunnelsLive":   live,
		"totalRequests": totalReq,
		"recentSignups": recent,
		"system": map[string]any{
			"uptimeSecs": int64(time.Since(serverStart).Seconds()),
			"goroutines": runtime.NumGoroutine(),
			"memMB":      ms.Alloc / 1024 / 1024,
			"cpus":       runtime.NumCPU(),
			"loadAvg":    loadAvg(),
		},
		"guard": fw.Guard.Stats(),
	})
}

// loadAvg reads the 1-minute load average on Linux ("" elsewhere).
func loadAvg() string {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return ""
	}
	f := strings.Fields(string(b))
	if len(f) > 0 {
		return f[0]
	}
	return ""
}

/* ─────────── users ─────────── */

func adminUsers(w http.ResponseWriter, r *http.Request) {
	users := store.ListUsers()
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		tunnelsMu.RLock()
		t, live := tunnels[u.TunnelSub]
		var up int64
		var reqs int64
		if live {
			up = int64(time.Since(t.connectedAt).Seconds())
			reqs = t.reqCount.Load()
		}
		tunnelsMu.RUnlock()
		out = append(out, map[string]any{
			"email":      u.Email,
			"tunnelSub":  u.TunnelSub,
			"repoSub":    u.RepoSub,
			"createdAt":  u.CreatedAt.UTC().Format(time.RFC3339),
			"twoFactor":  u.TOTPEnabled,
			"isAdmin":    u.IsAdmin,
			"suspended":  u.Suspended,
			"tunnelId":   store.TunnelID(u.TunnelSub),
			"hasToken":   u.TokenHash != "",
			"tunnelLive": live,
			"uptimeSecs": up,
			"requests":   reqs,
		})
	}
	adminJSON(w, map[string]any{"users": out})
}

// adminUserAction handles users/<email>, users/<email>/revoke, users/<email>/admin
func adminUserAction(w http.ResponseWriter, r *http.Request, admin, rest string) {
	parts := strings.SplitN(rest, "/", 2)
	email := strings.ToLower(parts[0])
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	if email == "" {
		http.Error(w, "email required", http.StatusBadRequest)
		return
	}

	switch {
	case action == "" && r.Method == http.MethodDelete:
		if email == admin {
			http.Error(w, "you can't delete your own admin account", http.StatusBadRequest)
			return
		}
		// disconnect a live tunnel first
		if u, ok := store.GetUser(email); ok {
			disconnectTunnel(u.TunnelSub)
			disconnectTunnel(u.RepoSub)
		}
		if err := store.DeleteUserFull(email); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		auditLog(admin, "Deleted account", email, "")
		adminJSON(w, map[string]any{"ok": true})

	case action == "revoke" && r.Method == http.MethodPost:
		if err := store.RevokeUserSessions(email); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		auditLog(admin, "Forced logout", email, "")
		adminJSON(w, map[string]any{"ok": true})

	// Kick the live tunnel. The CLI can reconnect — use suspend to stop that.
	case action == "disconnect" && r.Method == http.MethodPost:
		u, ok := store.GetUser(email)
		if !ok {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		disconnectTunnel(u.TunnelSub)
		disconnectTunnel(u.RepoSub)
		auditLog(admin, "Disconnected tunnel", email, u.TunnelSub)
		adminJSON(w, map[string]any{"ok": true})

	// Suspend: disconnect AND block reconnection (login, tunnel auth and API
	// token all fail while suspended). Reversible.
	case action == "suspend" && r.Method == http.MethodPost:
		if email == admin {
			http.Error(w, "you can't suspend your own admin account", http.StatusBadRequest)
			return
		}
		var body struct {
			On bool `json:"on"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if err := store.SetSuspended(email, body.On); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.On {
			if u, ok := store.GetUser(email); ok {
				disconnectTunnel(u.TunnelSub)
				disconnectTunnel(u.RepoSub)
			}
			auditLog(admin, "Suspended account", email, "tunnel + login blocked")
		} else {
			auditLog(admin, "Restored account", email, "")
		}
		adminJSON(w, map[string]any{"ok": true, "suspended": body.On})

	case action == "admin" && r.Method == http.MethodPost:
		var body struct {
			On bool `json:"on"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !body.On && email == admin {
			http.Error(w, "you can't remove your own admin role", http.StatusBadRequest)
			return
		}
		if err := store.SetAdmin(email, body.On); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		adminJSON(w, map[string]any{"ok": true})

	default:
		http.NotFound(w, r)
	}
}

// disconnectTunnel force-closes a user's live tunnel if connected.
func disconnectTunnel(sub string) {
	tunnelsMu.Lock()
	t, ok := tunnels[sub]
	if ok {
		delete(tunnels, sub)
	}
	tunnelsMu.Unlock()
	if ok && t.conn != nil {
		_ = t.conn.Close()
	}
}

/* ─────────── audit log ─────────── */

// AuditEvent records one privileged action. Kept in memory (ring buffer) and
// mirrored to the server log so it survives a restart in journald.
type AuditEvent struct {
	Time   string `json:"time"`
	Admin  string `json:"admin"`
	Action string `json:"action"`
	Target string `json:"target"`
	Detail string `json:"detail,omitempty"`
}

var (
	auditMu  sync.Mutex
	auditBuf []AuditEvent
)

func auditLog(admin, action, target, detail string) {
	e := AuditEvent{
		Time:   time.Now().UTC().Format(time.RFC3339),
		Admin:  admin,
		Action: action,
		Target: target,
		Detail: detail,
	}
	auditMu.Lock()
	auditBuf = append(auditBuf, e)
	if len(auditBuf) > 1000 {
		auditBuf = auditBuf[len(auditBuf)-1000:]
	}
	auditMu.Unlock()
	log.Printf("AUDIT %s → %s (%s) %s", admin, action, target, detail)
}

func recentAudit(n int) []AuditEvent {
	auditMu.Lock()
	defer auditMu.Unlock()
	if n > len(auditBuf) {
		n = len(auditBuf)
	}
	out := make([]AuditEvent, n)
	copy(out, auditBuf[len(auditBuf)-n:])
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 { // newest first
		out[i], out[j] = out[j], out[i]
	}
	return out
}

/* ─────────── threat feed + edge bans ─────────── */

// adminThreats merges the recent blocked requests from every subdomain on the
// platform into one feed, attributed to the account that owns each target.
func adminThreats(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	type threat struct {
		Time      time.Time `json:"time"`
		IP        string    `json:"ip"`
		Method    string    `json:"method"`
		Path      string    `json:"path"`
		Reason    string    `json:"reason"`
		Category  string    `json:"category"`
		Status    int       `json:"status"`
		Subdomain string    `json:"subdomain"`
		Owner     string    `json:"owner"`
		Kind      string    `json:"kind"` // tunnel | repo
	}

	out := []threat{}
	byIP := map[string]int{}
	byCategory := map[string]int{}

	for _, u := range store.ListUsers() {
		for kind, sub := range map[string]string{"tunnel": u.TunnelSub, "repo": u.RepoSub} {
			if sub == "" {
				continue
			}
			for _, e := range fw.For(sub).Stats(limit).Recent {
				out = append(out, threat{
					Time: e.Time, IP: e.IP, Method: e.Method, Path: e.Path,
					Reason: e.Reason, Category: e.Category, Status: e.Status,
					Subdomain: sub, Owner: u.Email, Kind: kind,
				})
				byIP[e.IP]++
				if e.Category != "" {
					byCategory[e.Category]++
				}
			}
		}
	}

	// newest first, then trim
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	if len(out) > limit {
		out = out[:limit]
	}

	// top attacker IPs
	type ipCount struct {
		IP    string `json:"ip"`
		Count int    `json:"count"`
	}
	tops := make([]ipCount, 0, len(byIP))
	for ip, n := range byIP {
		tops = append(tops, ipCount{ip, n})
	}
	sort.Slice(tops, func(i, j int) bool { return tops[i].Count > tops[j].Count })
	if len(tops) > 20 {
		tops = tops[:20]
	}

	adminJSON(w, map[string]any{
		"threats":    out,
		"topIPs":     tops,
		"byCategory": byCategory,
		"banned":     fw.Guard.BannedList(),
		"guard":      fw.Guard.Stats(),
	})
}

// adminBans lists / adds / lifts platform-wide edge IP bans.
//
//	GET                    → current bans
//	POST   {ip, hours}     → ban across every tunnel (hours 0 = permanent)
//	DELETE ?ip=            → lift
func adminBans(w http.ResponseWriter, r *http.Request, admin string) {
	switch r.Method {
	case http.MethodGet:
		adminJSON(w, map[string]any{"banned": fw.Guard.BannedList()})

	case http.MethodPost:
		var body struct {
			IP    string `json:"ip"`
			Hours int    `json:"hours"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		ip := strings.TrimSpace(body.IP)
		if net.ParseIP(ip) == nil {
			http.Error(w, "enter a valid IP address", http.StatusBadRequest)
			return
		}
		d := time.Duration(body.Hours) * time.Hour
		fw.Guard.Ban(ip, d)
		detail := "permanent"
		if body.Hours > 0 {
			detail = strconv.Itoa(body.Hours) + "h"
		}
		auditLog(admin, "Banned IP at edge", ip, detail)
		adminJSON(w, map[string]any{"ok": true, "banned": fw.Guard.BannedList()})

	case http.MethodDelete:
		ip := strings.TrimSpace(r.URL.Query().Get("ip"))
		fw.Guard.Unban(ip)
		auditLog(admin, "Lifted IP ban", ip, "")
		adminJSON(w, map[string]any{"ok": true, "banned": fw.Guard.BannedList()})

	default:
		http.NotFound(w, r)
	}
}

/* ─────────── business metrics ─────────── */

// adminBusiness answers "who is actually using this?" — company vs individual
// accounts, the company list, signup trend and the heaviest users.
func adminBusiness(w http.ResponseWriter, r *http.Request) {
	users := store.ListUsers()

	type company struct {
		Company  string `json:"company"`
		Email    string `json:"email"`
		Name     string `json:"name"`
		Website  string `json:"website"`
		Location string `json:"location"`
		Since    string `json:"since"`
	}
	companies := []company{}
	individuals, noProfile, suspended, withTunnel := 0, 0, 0, 0

	for _, u := range users {
		if u.Suspended {
			suspended++
		}
		if u.TunnelSub != "" {
			withTunnel++
		}
		p, ok := store.GetProfile(u.Email)
		switch {
		case !ok || (p.Name == "" && p.Company == "" && p.AccountType == ""):
			noProfile++
		case p.AccountType == "company":
			companies = append(companies, company{
				Company: p.Company, Email: u.Email, Name: p.Name,
				Website: p.Website, Location: p.Location,
				Since:   u.CreatedAt.UTC().Format(time.RFC3339),
			})
		default:
			individuals++
		}
	}
	sort.Slice(companies, func(i, j int) bool { return companies[i].Company < companies[j].Company })

	// signups per day for the last 30 days
	daily := map[string]int{}
	cut := time.Now().AddDate(0, 0, -30)
	for _, u := range users {
		if u.CreatedAt.After(cut) {
			daily[u.CreatedAt.UTC().Format("2006-01-02")]++
		}
	}

	// heaviest tunnels by request count
	type heavy struct {
		Email     string `json:"email"`
		Subdomain string `json:"subdomain"`
		Requests  int64  `json:"requests"`
		Live      bool   `json:"live"`
	}
	top := []heavy{}
	tunnelsMu.RLock()
	for _, u := range users {
		if t, ok := tunnels[u.TunnelSub]; ok {
			top = append(top, heavy{u.Email, u.TunnelSub, t.reqCount.Load(), true})
		}
	}
	tunnelsMu.RUnlock()
	sort.Slice(top, func(i, j int) bool { return top[i].Requests > top[j].Requests })
	if len(top) > 10 {
		top = top[:10]
	}

	adminJSON(w, map[string]any{
		"totalAccounts": len(users),
		"companies":     len(companies),
		"individuals":   individuals,
		"noProfile":     noProfile,
		"suspended":     suspended,
		"withTunnel":    withTunnel,
		"companyList":   companies,
		"signupsDaily":  daily,
		"topTunnels":    top,
	})
}

/* ─────────── per-user drill-down ─────────── */

// adminUserDetail is everything about one account: identity, both subdomains
// with their tunnel IDs and live state, and the attacks aimed at them.
func adminUserDetail(w http.ResponseWriter, r *http.Request, email string) {
	email = strings.ToLower(strings.TrimSpace(email))
	u, ok := store.GetUser(email)
	if !ok {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	p, _ := store.GetProfile(email)

	subInfo := func(sub, kind string) map[string]any {
		if sub == "" {
			return nil
		}
		m := map[string]any{
			"subdomain": sub,
			"kind":      kind,
			"tunnelId":  store.TunnelID(sub), // stable routing handle
			"live":      false,
		}
		tunnelsMu.RLock()
		if t, live := tunnels[sub]; live {
			m["live"] = true
			m["uptimeSecs"] = int64(time.Since(t.connectedAt).Seconds())
			m["requests"] = t.reqCount.Load()
		}
		tunnelsMu.RUnlock()
		st := fw.For(sub).Stats(40)
		m["allowed"] = st.Allowed
		m["blocked"] = st.Blocked
		m["bannedIPs"] = st.BannedIPs
		m["byCategory"] = st.ByCategory
		m["recent"] = st.Recent
		return m
	}

	subs := []map[string]any{}
	if s := subInfo(u.TunnelSub, "tunnel"); s != nil {
		subs = append(subs, s)
	}
	if s := subInfo(u.RepoSub, "repo"); s != nil {
		subs = append(subs, s)
	}

	wss := []map[string]any{}
	for _, ws := range store.ListWorkspaces(email) {
		wss = append(wss, map[string]any{
			"id": ws.ID, "name": ws.Name,
			"members": len(store.ListWorkspaceMembers(email, ws.ID)),
		})
	}

	adminJSON(w, map[string]any{
		"email":      u.Email,
		"createdAt":  u.CreatedAt.UTC().Format(time.RFC3339),
		"twoFactor":  u.TOTPEnabled,
		"isAdmin":    u.IsAdmin,
		"suspended":  u.Suspended,
		"hasToken":   u.TokenHash != "",
		"profile":    p,
		"subdomains": subs,
		"workspaces": wss,
	})
}

/* ─────────── security (firewall) ─────────── */

// adminSecurity aggregates per-subdomain firewall stats across all accounts —
// the platform-wide view of attacks blocked, rate-limits, scanner probes, etc.
func adminSecurity(w http.ResponseWriter, r *http.Request) {
	limit := 60
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	users := store.ListUsers()
	perSub := []map[string]any{}
	// BOTH subdomains: tunnel-mode custom domains resolve to the REPO
	// subdomain, so listing only TunnelSub hid that traffic entirely.
	for _, u := range users {
		for _, s := range []struct{ sub, kind string }{
			{u.TunnelSub, "tunnel"}, {u.RepoSub, "repo"},
		} {
			if s.sub == "" {
				continue
			}
			perSub = append(perSub, map[string]any{
				"subdomain": s.sub,
				"kind":      s.kind,
				"email":     u.Email,
				"suspended": u.Suspended,
				"tunnelId":  store.TunnelID(s.sub),
				"stats":     fw.For(s.sub).Stats(limit),
			})
		}
	}
	adminJSON(w, map[string]any{"subdomains": perSub})
}
