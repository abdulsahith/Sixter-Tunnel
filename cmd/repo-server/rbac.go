package main

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

/*
Phase 2 RBAC for the repo dashboard.

Identity comes from one of two places:
  - the owner token (X-Token / Bearer / ?token) → the OWNER, role "lead", full
    access. Validated locally, so the owner is NEVER locked out (works even if
    the gateway is unreachable). The CLI push path keeps using this.
  - an SSO token (X-Sixter-SSO header, or ?sso= for websockets) minted by the
    gateway → a team member with their assigned role. Verified server-to-server
    against the gateway and cached briefly.

Capabilities are gated centrally (see capFor + the root middleware). Read APIs
are open to any authenticated member; mutating APIs require the matching cap.
*/

type Principal struct {
	Email      string
	Role       string            // primary role (strongest across workspaces)
	IsOwner    bool
	Workspaces map[string]string // workspace name → role (for scoped SSO members)
}

func (p *Principal) has(cap string) bool { return roleHasCap(p.Role, cap) }

// Workspace-model capabilities. Developers (backend/frontend) do NOT deploy from
// the dashboard — they push via the CLI; lead/deployment promote.
var roleCaps = map[string]map[string]bool{
	"lead":       {"view": true, "deploy": true, "merge": true, "env": true, "database": true, "terminal": true, "domains": true, "firewall": true, "delete": true, "track": true, "workspace": true},
	"deployment": {"view": true, "deploy": true, "env": true, "database": true, "terminal": true},
	"backend":    {"view": true, "env": true, "database": true, "terminal": true},
	"frontend":   {"view": true, "env": true},
	"viewer":     {"view": true},
}

func roleHasCap(role, cap string) bool {
	if m := roleCaps[role]; m != nil {
		return m[cap]
	}
	return false
}

func reqToken(r *http.Request) string {
	tok := strings.TrimSpace(r.Header.Get("X-Token"))
	if tok == "" {
		if a := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(a), "bearer ") {
			tok = strings.TrimSpace(a[7:])
		}
	}
	if tok == "" {
		tok = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	return tok
}

func reqSSO(r *http.Request) string {
	if t := strings.TrimSpace(r.Header.Get("X-Sixter-SSO")); t != "" {
		return t
	}
	return strings.TrimSpace(r.URL.Query().Get("sso")) // websockets can't set headers
}

type ssoCacheEntry struct {
	p   *Principal
	exp time.Time
}

var (
	ssoCacheMu sync.Mutex
	ssoCache   = map[string]ssoCacheEntry{}
)

// verifySSO checks an SSO token with the gateway (cached ~30s).
func verifySSO(token string) *Principal {
	if token == "" {
		return nil
	}
	ssoCacheMu.Lock()
	if e, ok := ssoCache[token]; ok && time.Now().Before(e.exp) {
		ssoCacheMu.Unlock()
		return e.p
	}
	ssoCacheMu.Unlock()

	u := gatewayBase() + "/api/sso/verify?token=" + url.QueryEscape(token) + "&repo=" + url.QueryEscape(serverName)
	cl := &http.Client{Timeout: 8 * time.Second}
	resp, err := cl.Get(u)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var v struct {
		OK         bool              `json:"ok"`
		Email      string            `json:"email"`
		Role       string            `json:"role"`
		Workspaces map[string]string `json:"workspaces"`
		IsOwner    bool              `json:"is_owner"`
	}
	if json.Unmarshal(body, &v) != nil || !v.OK || v.Role == "" {
		return nil
	}
	p := &Principal{Email: v.Email, Role: v.Role, Workspaces: v.Workspaces, IsOwner: v.IsOwner}
	ssoCacheMu.Lock()
	ssoCache[token] = ssoCacheEntry{p: p, exp: time.Now().Add(30 * time.Second)}
	ssoCacheMu.Unlock()
	return p
}

// enrollCache keeps gateway round-trips down for CLI members, who hit several
// endpoints per command.
var (
	enrollCacheMu sync.Mutex
	enrollCache   = map[string]ssoCacheEntry{}
)

// verifyEnrollPrincipal resolves a member's personal enroll token (used by the
// CLI: sixter status/branch/pr/log) into a Principal, so those requests get the
// same role-based capabilities as the dashboard.
func verifyEnrollPrincipal(token string) *Principal {
	if token == "" {
		return nil
	}
	enrollCacheMu.Lock()
	if e, ok := enrollCache[token]; ok && time.Now().Before(e.exp) {
		enrollCacheMu.Unlock()
		return e.p
	}
	enrollCacheMu.Unlock()

	email, role, wsName, ok := verifyEnroll(token)
	if !ok || role == "" {
		return nil
	}
	p := &Principal{Email: email, Role: role, Workspaces: map[string]string{}}
	if wsName != "" {
		p.Workspaces[wsName] = role
	}
	enrollCacheMu.Lock()
	enrollCache[token] = ssoCacheEntry{p: p, exp: time.Now().Add(30 * time.Second)}
	enrollCacheMu.Unlock()
	return p
}

// principal resolves who is calling. Owner token wins (local, always available);
// then an SSO member token (browser); then a CLI enroll token.
func principal(r *http.Request) *Principal {
	if tok := reqToken(r); tok != "" &&
		subtle.ConstantTimeCompare([]byte(tok), []byte(uploadToken)) == 1 {
		return &Principal{Email: "owner", Role: "lead", IsOwner: true}
	}
	if sso := reqSSO(r); sso != "" {
		if p := verifySSO(sso); p != nil {
			return p
		}
	}
	if enroll := strings.TrimSpace(r.Header.Get("X-Enroll-Token")); enroll != "" {
		if p := verifyEnrollPrincipal(enroll); p != nil {
			return p
		}
	}
	return nil
}

// capFor maps a request to the capability it requires. Anything not explicitly
// privileged is "view" (readable by any authenticated member).
func capFor(r *http.Request) string {
	p, m := r.URL.Path, r.Method
	switch {
	case strings.HasPrefix(p, "/api/workspaces") || strings.HasPrefix(p, "/api/project-members/"):
		return "workspace"
	case strings.HasPrefix(p, "/api/track/"):
		return "track"
	case strings.HasPrefix(p, "/api/domain") || strings.HasPrefix(p, "/api/rename"):
		return "domains"
	case strings.HasPrefix(p, "/api/security/") && strings.HasSuffix(p, "/ban"):
		return "firewall"
	case strings.HasPrefix(p, "/api/databases"):
		return "database"
	case strings.HasPrefix(p, "/api/terminal"):
		return "terminal"
	case strings.HasPrefix(p, "/api/env/") && m != http.MethodGet:
		return "env"
	case strings.HasPrefix(p, "/api/deploy/") || strings.HasPrefix(p, "/api/rollback/") || strings.HasPrefix(p, "/api/autodeploy/"):
		return "deploy"
	case strings.HasPrefix(p, "/api/pr/merge") || strings.HasPrefix(p, "/api/pr/close"):
		return "merge" // lead-only: review + merge pull requests
	case strings.HasPrefix(p, "/api/amm/") && m != http.MethodGet:
		return "deploy" // changing a project's runtime is a deploy-class action
	case strings.HasPrefix(p, "/api/projects/") && m == http.MethodDelete:
		return "delete"
	}
	return "view"
}

// projectFromPath extracts the target project for project-scoped /api routes
// of the form /api/<verb>/<project>[/...]. Returns "" for non-project routes
// (and for routes that carry the project in the body, which are lead-only).
func projectFromPath(path string) string {
	if !strings.HasPrefix(path, "/api/") {
		return ""
	}
	parts := strings.Split(strings.Trim(path, "/"), "/") // ["api","deploy","proj",...]
	if len(parts) < 3 {
		return ""
	}
	switch parts[1] {
	case "deploy", "env", "versions", "rollback", "security", "rename",
		"project-stats", "projects", "detect", "autodeploy",
		"deploy-status", "deploy-stream", "terminal", "amm":
		return parts[2]
	}
	return ""
}

// effectiveRole returns the role to enforce for a request. The owner (token) is
// always lead. For an SSO member acting on a specific project, the role is their
// membership in THAT project's workspace ("" if the project isn't in any of
// their workspaces → access denied).
func (p *Principal) effectiveRole(r *http.Request) (role string, denied bool) {
	if p.IsOwner {
		return "lead", false
	}
	proj := projectFromPath(r.URL.Path)
	if proj == "" {
		return p.Role, false // non-project route → primary role
	}
	if len(p.Workspaces) == 0 {
		return p.Role, false // legacy global-team member → primary role (track-gated)
	}
	ws := getProjWorkspace(proj)
	if ws == "" {
		return "", true // project not tied to any workspace → members can't touch it
	}
	role, ok := p.Workspaces[ws]
	if !ok {
		return "", true // not a member of this project's workspace
	}
	return role, false
}

// rbacGate enforces workspace scope + role capability + track for an /api request.
func rbacGate(w http.ResponseWriter, r *http.Request) bool {
	p := principal(r)
	if p == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	role, denied := p.effectiveRole(r)
	if denied {
		http.Error(w, "forbidden: this project is outside your workspace", http.StatusForbidden)
		return false
	}
	if !roleHasCap(role, capFor(r)) {
		http.Error(w, "forbidden: your role ("+role+") cannot perform this action", http.StatusForbidden)
		return false
	}
	// track scoping within the workspace
	if proj := projectFromPath(r.URL.Path); proj != "" && !p.IsOwner {
		if !roleSeesTrack(role, projectTrack(proj)) {
			http.Error(w, "forbidden: this project is outside your track", http.StatusForbidden)
			return false
		}
	}
	return true
}

// handleWhoami reports the caller's identity + role + capabilities (for the UI).
func handleWhoami(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	w.Header().Set("Content-Type", "application/json")
	if p == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"authed": false})
		return
	}
	caps := []string{}
	for c := range roleCaps[p.Role] {
		caps = append(caps, c)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"authed": true, "email": p.Email, "role": p.Role, "is_owner": p.IsOwner,
		"caps": caps, "workspaces": p.Workspaces,
	})
}
