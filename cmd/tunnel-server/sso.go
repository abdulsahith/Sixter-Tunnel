package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"sixter.in/tunnel/internal/auth"
)

/*
SSO for repo dashboards (Phase 2).

We do NOT use a domain-wide cookie — that would leak the gateway session to every
*.sixter.in tunnel (including other users' apps). Instead:

  1. The repo dashboard (browser at <repoSub>.sixter.in) calls /api/sso/grant on
     the gateway with credentials. Because it's same-site, the sixter.in session
     cookie rides along; CORS lets the cross-origin response through.
  2. The gateway returns a short-lived token bound to {email, repoSub, role}.
  3. The repo-server validates that token server-to-server via /api/sso/verify.

The owner of a repo subdomain is always "lead". Members get their team role.
*/

type ssoGrant struct {
	email   string
	repo    string
	role    string
	wsRoles map[string]string // workspace name → role
	isOwner bool
	expiry  time.Time
}

var (
	ssoMu     sync.Mutex
	ssoTokens = map[string]ssoGrant{}
)

func ssoNewToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// caller must hold ssoMu
func ssoGCLocked(now time.Time) {
	for k, g := range ssoTokens {
		if now.After(g.expiry) {
			delete(ssoTokens, k)
		}
	}
}

// roleForRepo resolves the caller's role(s) on a repo subdomain. The owner is
// lead of every workspace; a member gets their per-workspace roles. Returns the
// primary (strongest) role, the per-workspace role map, and whether they belong.
func roleForRepo(email, repoSub string) (role string, wsRoles map[string]string, isOwner, ok bool) {
	owner, found := store.OwnerOf(repoSub)
	if !found {
		return "", nil, false, false
	}
	if strings.EqualFold(email, owner) {
		wr := map[string]string{}
		for _, ws := range store.ListWorkspaces(owner) {
			wr[ws.Name] = "lead"
		}
		return "lead", wr, true, true
	}
	// per-workspace membership (the coworkspace model)
	wr := map[string]string{}
	for _, ws := range store.ListWorkspaces(owner) {
		for _, m := range store.ListWorkspaceMembers(owner, ws.ID) {
			if strings.EqualFold(m.Email, email) {
				wr[ws.Name] = m.Role
			}
		}
	}
	if len(wr) > 0 {
		return primaryRole(wr), wr, false, true
	}
	// backwards compatibility: legacy global team membership
	if r, member := store.MemberRole(owner, email); member {
		return r, map[string]string{}, false, true
	}
	return "", nil, false, false
}

// primaryRole picks the strongest role across a member's workspaces.
func primaryRole(wr map[string]string) string {
	rank := map[string]int{"lead": 5, "deployment": 4, "backend": 3, "frontend": 2, "tester": 1, "viewer": 1}
	best, bestRank := "viewer", 0
	for _, r := range wr {
		if rank[r] > bestRank {
			bestRank = rank[r]
			best = r
		}
	}
	return best
}

// ssoCORS allows credentialed cross-origin calls from any *.sixter.in origin
// (same registrable site) — needed so a repo dashboard can fetch a grant.
func ssoCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}
	host := origin
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if host == apexHost || strings.HasSuffix(host, "."+apexHost) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Add("Vary", "Origin")
	}
}

// GET /api/sso/grant?repo=<sub> — browser, credentialed. Returns a token + role.
func handleSSOGrant(w http.ResponseWriter, r *http.Request) {
	ssoCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	repoSub := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("repo")))
	email := auth.ResolveSession(store, r)
	if email == "" {
		writeJSONResp(w, map[string]any{"logged_in": false})
		return
	}
	role, wsRoles, isOwner, ok := roleForRepo(email, repoSub)
	if !ok {
		// logged in, but not a member of this repo's team
		writeJSONResp(w, map[string]any{"logged_in": true, "member": false, "email": email})
		return
	}
	tok := ssoNewToken()
	ssoMu.Lock()
	ssoGCLocked(time.Now())
	ssoTokens[tok] = ssoGrant{email: email, repo: repoSub, role: role, wsRoles: wsRoles, isOwner: isOwner, expiry: time.Now().Add(8 * time.Hour)}
	ssoMu.Unlock()
	writeJSONResp(w, map[string]any{
		"logged_in": true, "member": true,
		"token": tok, "email": email, "role": role, "workspaces": wsRoles, "is_owner": isOwner,
	})
}

// GET /api/sso/verify?token=&repo= — server-to-server (repo-server). No CORS.
func handleSSOVerify(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimSpace(r.URL.Query().Get("token"))
	repoSub := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("repo")))
	ssoMu.Lock()
	ssoGCLocked(time.Now())
	g, ok := ssoTokens[tok]
	ssoMu.Unlock()
	if !ok || time.Now().After(g.expiry) || (repoSub != "" && g.repo != repoSub) {
		writeJSONResp(w, map[string]any{"ok": false})
		return
	}
	writeJSONResp(w, map[string]any{"ok": true, "email": g.email, "role": g.role, "workspaces": g.wsRoles, "is_owner": g.isOwner})
}
