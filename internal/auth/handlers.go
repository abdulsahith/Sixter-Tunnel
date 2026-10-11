package auth

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const sessionCookie = "sixter_session"
const (
	mfaChallengeTTL = 5 * time.Minute
	mfaMaxAttempts  = 5
)

// mfaChallenge is a short-lived, post-password login challenge awaiting a 2FA code.
type mfaChallenge struct {
	email    string
	expires  time.Time
	attempts int
}

// StatusFunc lets the auth API report live tunnel state for a subdomain
// (provided by the tunnel server).
type StatusFunc func(sub string) (connected bool, uptimeSecs int64, requests int64)

// SecurityFunc returns a firewall stats JSON blob for a subdomain.
type SecurityFunc func(sub string) interface{}

// FirewallUpdateFunc hot-applies new firewall settings to the live engine.
type FirewallUpdateFunc func(sub, settingsJSON string)

// TunnelDetailFunc returns live tunnel metadata (id, status, latency, heartbeat,
// connected-since, domains) for a subdomain — used by the tunnel dashboard.
type TunnelDetailFunc func(sub string) map[string]interface{}

// Hooks are optional callbacks the tunnel server provides.
type Hooks struct {
	Status         StatusFunc
	Security       SecurityFunc
	FirewallUpdate FirewallUpdateFunc
	Detail         TunnelDetailFunc
}

// Server holds the auth HTTP handlers.
type Server struct {
	store Storage
	hooks Hooks
	oauth oauthConfig // Google/GitHub sign-in, configured from the environment

	mfaMu sync.Mutex
	mfa   map[string]*mfaChallenge // challenge token → pending 2FA login
}

// NewMux builds the apex-domain mux: JSON auth APIs + the landing-page SPA.
// spa serves the React landing (embedded dist + optional disk overlay).
func NewMux(store Storage, spa http.Handler, hooks Hooks) *http.ServeMux {
	srv := &Server{store: store, hooks: hooks, mfa: map[string]*mfaChallenge{}, oauth: oauthFromEnv()}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/signup", srv.handleSignup)
	mux.HandleFunc("/api/login", srv.handleLogin)
	mux.HandleFunc("/api/logout", srv.handleLogout)
	mux.HandleFunc("/api/me", srv.handleMe)
	mux.HandleFunc("/api/me/status", srv.handleMeStatus)
	mux.HandleFunc("/api/me/security", srv.handleMeSecurity)
	mux.HandleFunc("/api/me/firewall", srv.handleMeFirewall)
	mux.HandleFunc("/api/me/profile", srv.handleMeProfile)
	mux.HandleFunc("/api/me/subdomain", srv.handleClaimSubdomain)
	mux.HandleFunc("/api/subdomain/check", srv.handleSubdomainCheck)
	// OAuth sign-in (only the configured providers actually work)
	mux.HandleFunc("/api/auth/providers", srv.handleAuthProviders)
	mux.HandleFunc("/api/auth/google/start", srv.handleOAuthStart)
	mux.HandleFunc("/api/auth/google/callback", srv.handleOAuthCallback)
	mux.HandleFunc("/api/auth/github/start", srv.handleOAuthStart)
	mux.HandleFunc("/api/auth/github/callback", srv.handleOAuthCallback)
	mux.HandleFunc("/api/team/members", srv.handleTeamMembers)
	mux.HandleFunc("/api/workspaces", srv.handleWorkspaces)
	mux.HandleFunc("/api/workspaces/", srv.handleWorkspaceItem)
	mux.HandleFunc("/api/enroll/redeem", srv.handleEnrollRedeem)
	mux.HandleFunc("/api/workspaces/join", srv.handleWorkspaceJoin)
	mux.HandleFunc("/api/member/profile", srv.handleMemberProfile)
	// node fleet: agent endpoints (no session) + console endpoints (session)
	mux.HandleFunc("/api/node/enroll", srv.handleNodeEnroll)
	mux.HandleFunc("/api/node/heartbeat", srv.handleNodeHeartbeat)
	mux.HandleFunc("/api/nodes", srv.handleNodes)
	mux.HandleFunc("/api/nodes/token", srv.handleNodeToken)
	mux.HandleFunc("/api/fleet/apps", srv.handleFleetApps)
	mux.HandleFunc("/api/2fa/setup", srv.handle2FASetup)
	mux.HandleFunc("/api/2fa/enable", srv.handle2FAEnable)
	mux.HandleFunc("/api/2fa/verify", srv.handle2FAVerify)
	mux.HandleFunc("/api/2fa/disable", srv.handle2FADisable)
	mux.HandleFunc("/api/device/approve", srv.handleDeviceApprove)
	mux.HandleFunc("/api/device/code", srv.handleDeviceCode)
	mux.HandleFunc("/api/device/token", srv.handleDeviceToken)
	mux.Handle("/", spa)
	return mux
}

// newChallenge stores a short-lived post-password MFA challenge and returns its token.
func (s *Server) newChallenge(email string) string {
	tok := randHex(32)
	s.mfaMu.Lock()
	defer s.mfaMu.Unlock()
	// opportunistic cleanup of expired challenges
	now := time.Now()
	for k, c := range s.mfa {
		if now.After(c.expires) {
			delete(s.mfa, k)
		}
	}
	s.mfa[tok] = &mfaChallenge{email: email, expires: now.Add(mfaChallengeTTL)}
	return tok
}

/* ─────────── helpers ─────────── */

func (s *Server) currentEmail(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return s.store.SessionEmail(c.Value)
}

// SessionCookie is the browser session cookie name (exported for other packages
// — e.g. the tunnel-server admin API — that need to resolve the logged-in user).
const SessionCookie = sessionCookie

// ResolveSession returns the email of the logged-in user for a request, or "".
func ResolveSession(store Storage, r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return store.SessionEmail(c.Value)
}

func setSession(w http.ResponseWriter, r *http.Request, sid string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 3600,
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func userJSON(u User) map[string]interface{} {
	providers := make([]string, 0, len(u.Providers))
	for p := range u.Providers {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	return map[string]interface{}{
		"providers":         providers,             // linked OAuth providers
		"needs_onboarding":  u.NeedsOnboarding(),   // true until a subdomain is chosen
		"has_password":      u.PassHash != "",
		"email":            u.Email,
		"tunnel_subdomain": u.TunnelSub,
		"repo_subdomain":   u.RepoSub,
		"token_issued":     u.TokenHash != "",
		"created_at":       u.CreatedAt.UTC().Format(time.RFC3339),
		"totp_enabled":     u.TOTPEnabled,
		"is_admin":         u.IsAdmin,
	}
}

/* ─────────── account APIs ─────────── */

// POST /api/signup {email, password, subdomain}
func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		Email     string `json:"email"`
		Password  string `json:"password"`
		Subdomain string `json:"subdomain"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.store.CreateUser(req.Email, req.Password, req.Subdomain); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	sid, err := s.store.NewSession(req.Email)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "session error")
		return
	}
	setSession(w, r, sid)
	u, _ := s.store.GetUser(req.Email)
	writeJSON(w, http.StatusOK, userJSON(u))
}

// POST /api/login {email, password}
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if !s.store.Authenticate(req.Email, req.Password) {
		jsonError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	u, _ := s.store.GetUser(req.Email)

	// If 2FA is enabled, the password alone is NOT enough — return a challenge
	// and require a code via /api/2fa/verify before issuing a session.
	if u.TOTPEnabled {
		challenge := s.newChallenge(u.Email)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"mfa_required": true,
			"challenge":    challenge,
		})
		return
	}

	// No 2FA yet → log in, but the frontend forces enrollment (2FA is required)
	// because userJSON reports totp_enabled:false.
	sid, err := s.store.NewSession(req.Email)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "session error")
		return
	}
	setSession(w, r, sid)
	writeJSON(w, http.StatusOK, userJSON(u))
}

/* ─────────── 2FA (TOTP) ─────────── */

// POST /api/2fa/verify {challenge, code} or {challenge, backup_code}
// Completes a post-password login: validates the 2FA code, then issues a session.
func (s *Server) handle2FAVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		Challenge  string `json:"challenge"`
		Code       string `json:"code"`
		BackupCode string `json:"backup_code"`
	}
	if !readJSON(w, r, &req) {
		return
	}

	s.mfaMu.Lock()
	c, ok := s.mfa[req.Challenge]
	if ok && time.Now().After(c.expires) {
		delete(s.mfa, req.Challenge)
		ok = false
	}
	if ok {
		c.attempts++
		if c.attempts > mfaMaxAttempts {
			delete(s.mfa, req.Challenge)
			ok = false
		}
	}
	email := ""
	if ok {
		email = c.email
	}
	s.mfaMu.Unlock()

	if !ok {
		jsonError(w, http.StatusUnauthorized, "challenge expired or too many attempts — please log in again")
		return
	}

	valid := false
	if req.BackupCode != "" {
		valid = s.store.ConsumeBackupCode(email, req.BackupCode)
	} else {
		valid = s.store.ValidateTOTP(email, req.Code)
	}
	if !valid {
		jsonError(w, http.StatusUnauthorized, "incorrect code")
		return
	}

	// success — consume the challenge and issue the session
	s.mfaMu.Lock()
	delete(s.mfa, req.Challenge)
	s.mfaMu.Unlock()

	sid, err := s.store.NewSession(email)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "session error")
		return
	}
	setSession(w, r, sid)
	u, _ := s.store.GetUser(email)
	writeJSON(w, http.StatusOK, userJSON(u))
}

// POST /api/2fa/setup — start enrollment (requires a session). Returns the secret
// + otpauth URI for the QR code. Not active until /api/2fa/enable succeeds.
func (s *Server) handle2FASetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	email := s.currentEmail(r)
	if email == "" {
		jsonError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	secret, uri, err := s.store.BeginTOTPSetup(email)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"secret":      secret,
		"otpauth_uri": uri,
	})
}

// POST /api/2fa/enable {code} — verify the first code, enable 2FA, return backup codes.
func (s *Server) handle2FAEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	email := s.currentEmail(r)
	if email == "" {
		jsonError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	codes, err := s.store.ConfirmTOTP(email, req.Code)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"enabled":      true,
		"backup_codes": codes,
	})
}

// POST /api/2fa/disable {password, code} — turn off 2FA (needs password AND a code).
func (s *Server) handle2FADisable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	email := s.currentEmail(r)
	if email == "" {
		jsonError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if !s.store.Authenticate(email, req.Password) {
		jsonError(w, http.StatusUnauthorized, "incorrect password")
		return
	}
	if !s.store.ValidateTOTP(email, req.Code) && !s.store.ConsumeBackupCode(email, req.Code) {
		jsonError(w, http.StatusUnauthorized, "incorrect code")
		return
	}
	if err := s.store.DisableTOTP(email); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// POST /api/logout
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.store.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GET /api/me
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	email := s.currentEmail(r)
	if email == "" {
		jsonError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	u, ok := s.store.GetUser(email)
	if !ok {
		jsonError(w, http.StatusUnauthorized, "account not found")
		return
	}
	writeJSON(w, http.StatusOK, userJSON(u))
}

// GET /api/me/status — live state of the user's two subdomains
func (s *Server) handleMeStatus(w http.ResponseWriter, r *http.Request) {
	email := s.currentEmail(r)
	if email == "" {
		jsonError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	u, ok := s.store.GetUser(email)
	if !ok {
		jsonError(w, http.StatusUnauthorized, "account not found")
		return
	}

	subStatus := func(sub string) map[string]interface{} {
		connected, uptime, requests := false, int64(0), int64(0)
		if s.hooks.Status != nil {
			connected, uptime, requests = s.hooks.Status(sub)
		}
		m := map[string]interface{}{
			"subdomain":   sub,
			"connected":   connected,
			"uptime_secs": uptime,
			"requests":    requests,
		}
		if s.hooks.Detail != nil {
			for k, v := range s.hooks.Detail(sub) {
				m[k] = v
			}
		}
		return m
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tunnel": subStatus(u.TunnelSub),
		"repo":   subStatus(u.RepoSub),
	})
}

// GET /api/me/security — transparent firewall stats for both subdomains
func (s *Server) handleMeSecurity(w http.ResponseWriter, r *http.Request) {
	email := s.currentEmail(r)
	if email == "" {
		jsonError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	u, ok := s.store.GetUser(email)
	if !ok {
		jsonError(w, http.StatusUnauthorized, "account not found")
		return
	}
	sec := func(sub string) interface{} {
		if s.hooks.Security != nil {
			return s.hooks.Security(sub)
		}
		return nil
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tunnel": sec(u.TunnelSub),
		"repo":   sec(u.RepoSub),
	})
}

// GET/POST /api/me/profile — the account's business/personal profile.
// GET also returns read-only account facts so the profile page needs one call.
func (s *Server) handleMeProfile(w http.ResponseWriter, r *http.Request) {
	// Allow the repo dashboard (a *.sixter.in subdomain) to read the signed-in
	// user's profile with their gateway cookie, so it can show real details
	// without a second login. Same-site credentialed CORS, like the SSO grant.
	sameSiteCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	email := s.currentEmail(r)
	if email == "" {
		jsonError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	u, ok := s.store.GetUser(email)
	if !ok {
		jsonError(w, http.StatusUnauthorized, "account not found")
		return
	}

	if r.Method == http.MethodPost {
		var p Profile
		if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&p); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		clip := func(v string, n int) string {
			v = strings.TrimSpace(v)
			if len(v) > n {
				v = v[:n]
			}
			return v
		}
		p.AccountType = strings.ToLower(strings.TrimSpace(p.AccountType))
		if p.AccountType != "company" {
			p.AccountType = "individual"
		}
		p.Name = clip(p.Name, 120)
		p.Company = clip(p.Company, 120)
		p.JobTitle = clip(p.JobTitle, 120)
		p.Website = clip(p.Website, 200)
		p.Location = clip(p.Location, 120)
		p.Bio = clip(p.Bio, 1000)

		if p.Website != "" && !strings.HasPrefix(p.Website, "http://") && !strings.HasPrefix(p.Website, "https://") {
			p.Website = "https://" + p.Website
		}
		if p.AccountType == "company" && p.Company == "" {
			jsonError(w, http.StatusBadRequest, "company name is required for company accounts")
			return
		}

		if err := s.store.SetProfile(email, p); err != nil {
			jsonError(w, http.StatusInternalServerError, "save failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "profile": p})
		return
	}

	p, found := s.store.GetProfile(email)
	if !found {
		p = Profile{AccountType: "individual"}
	}
	if p.AccountType == "" {
		p.AccountType = "individual"
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"profile": p,
		"account": userJSON(u),
	})
}

// GET/POST /api/me/firewall?sub=tunnel|repo — read or save firewall settings
func (s *Server) handleMeFirewall(w http.ResponseWriter, r *http.Request) {
	email := s.currentEmail(r)
	if email == "" {
		jsonError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	u, ok := s.store.GetUser(email)
	if !ok {
		jsonError(w, http.StatusUnauthorized, "account not found")
		return
	}

	// resolve which of the user's two subdomains this targets
	which := r.URL.Query().Get("sub")
	sub := u.TunnelSub
	if which == "repo" {
		sub = u.RepoSub
	}

	if r.Method == http.MethodPost {
		body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
		if err != nil {
			jsonError(w, http.StatusBadRequest, "read error")
			return
		}
		// validate it parses as JSON before persisting
		var probe map[string]interface{}
		if json.Unmarshal(body, &probe) != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := s.store.SetFirewall(sub, string(body)); err != nil {
			jsonError(w, http.StatusInternalServerError, "save failed")
			return
		}
		if s.hooks.FirewallUpdate != nil {
			s.hooks.FirewallUpdate(sub, string(body))
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}

	// GET — return saved settings, or empty so the client uses defaults
	data, found := s.store.GetFirewall(sub)
	w.Header().Set("Content-Type", "application/json")
	if !found {
		_, _ = w.Write([]byte(`{"settings":null}`))
		return
	}
	_, _ = w.Write([]byte(`{"settings":` + data + `}`))
}

// /api/team/members — manage the caller's team. The logged-in user is always
// the owner of their own team; they can add/role/remove members (by Sixter
// account email). Members get access once they sign in (enforced in Phase 2).
func (s *Server) handleTeamMembers(w http.ResponseWriter, r *http.Request) {
	owner := s.currentEmail(r)
	if owner == "" {
		jsonError(w, http.StatusUnauthorized, "not logged in")
		return
	}

	switch r.Method {
	case http.MethodGet:
		members := s.store.ListMembers(owner)
		// annotate whether each invited email already has a Sixter account
		type memberView struct {
			TeamMember
			Registered bool `json:"registered"`
		}
		out := make([]memberView, 0, len(members))
		for _, m := range members {
			_, ok := s.store.GetUser(m.Email)
			out = append(out, memberView{TeamMember: m, Registered: ok})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"owner": owner, "members": out})

	case http.MethodPost:
		var body struct {
			Email string `json:"email"`
			Role  string `json:"role"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			jsonError(w, http.StatusBadRequest, "invalid request")
			return
		}
		body.Email = strings.ToLower(strings.TrimSpace(body.Email))
		if !looksLikeEmail(body.Email) {
			jsonError(w, http.StatusBadRequest, "enter a valid email address")
			return
		}
		if body.Role == "" {
			body.Role = "viewer"
		}
		if !ValidRole(body.Role) {
			jsonError(w, http.StatusBadRequest, "invalid role")
			return
		}
		if err := s.store.AddMember(owner, body.Email, body.Role); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})

	case http.MethodDelete:
		email := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("email")))
		if email == "" {
			jsonError(w, http.StatusBadRequest, "email required")
			return
		}
		if err := s.store.RemoveMember(owner, email); err != nil {
			jsonError(w, http.StatusInternalServerError, "remove failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})

	default:
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// /api/workspaces — the coworkspace org layer, managed in the landing.
// GET  → list the owner's workspaces (with members). POST {name} → create.
func (s *Server) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	owner := s.currentEmail(r)
	if owner == "" {
		jsonError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	switch r.Method {
	case http.MethodGet:
		wss := s.store.ListWorkspaces(owner)
		out := make([]map[string]interface{}, 0, len(wss))
		for _, ws := range wss {
			out = append(out, map[string]interface{}{
				"id": ws.ID, "name": ws.Name, "created": ws.Created,
				"members": s.store.ListWorkspaceMembers(owner, ws.ID),
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"workspaces": out})
	case http.MethodPost:
		var b struct {
			Name string `json:"name"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil || strings.TrimSpace(b.Name) == "" {
			jsonError(w, http.StatusBadRequest, "name required")
			return
		}
		id, err := s.store.CreateWorkspace(owner, b.Name)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "id": id})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// /api/workspaces/<id>            DELETE → delete workspace
// /api/workspaces/<id>/members    GET → list · POST {email,role} → add (+token) · DELETE ?email= → remove
func (s *Server) handleWorkspaceItem(w http.ResponseWriter, r *http.Request) {
	owner := s.currentEmail(r)
	if owner == "" {
		jsonError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")

	// GET  /api/workspaces/<id>/join   → the permanent invite link
	// POST /api/workspaces/<id>/join   → rotate it (invalidates the old link)
	if i := strings.Index(rest, "/join"); i >= 0 {
		wsID := rest[:i]
		var tok string
		var err error
		if r.Method == http.MethodPost {
			tok, err = s.store.RotateWorkspaceJoin(owner, wsID)
		} else {
			tok, err = s.store.WorkspaceJoinToken(owner, wsID)
		}
		if err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "token": tok})
		return
	}

	if i := strings.Index(rest, "/members"); i >= 0 {
		wsID := rest[:i]
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]interface{}{"members": s.store.ListWorkspaceMembers(owner, wsID)})
		case http.MethodPost:
			var b struct {
				Email string `json:"email"`
				Role  string `json:"role"`
			}
			_ = json.NewDecoder(r.Body).Decode(&b)
			b.Email = strings.ToLower(strings.TrimSpace(b.Email))
			if !looksLikeEmail(b.Email) {
				jsonError(w, http.StatusBadRequest, "enter a valid email address")
				return
			}
			if b.Role == "" {
				b.Role = "viewer"
			}
			tok, err := s.store.AddWorkspaceMember(owner, wsID, b.Email, b.Role)
			if err != nil {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "token": tok})
		case http.MethodDelete:
			email := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("email")))
			if err := s.store.RemoveWorkspaceMember(owner, wsID, email); err != nil {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		default:
			jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	if r.Method == http.MethodDelete && rest != "" {
		if err := s.store.DeleteWorkspace(owner, rest); err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	jsonError(w, http.StatusNotFound, "not found")
}

// GET /api/enroll/redeem?token= — public: resolves a `sixter project init`
// token to the member + workspace + the owner's repo subdomain. The token is
// itself the secret, so no session is required (used by the CLI and the
// repo-server push verifier).
func (s *Server) handleEnrollRedeem(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	info, ok := s.store.ResolveEnrollToken(token)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false})
		return
	}
	repoSub := ""
	if u, found := s.store.GetUser(info.Owner); found {
		repoSub = u.RepoSub
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"email":    info.Email,
		"role":     info.Role,
		"ws_id":    info.WsID,
		"ws_name":  info.WsName,
		"owner":    info.Owner,
		"repo_sub": repoSub,
	})
}

// POST /api/workspaces/join {token, subdomain, api_token}
//
// The CLI's `sixter project init <link>`. The link identifies the WORKSPACE
// only — the caller proves who they are with their own API token, and we mint
// a PERSONAL enroll token for them. That's what keeps per-member identity
// (and per-component ownership) intact behind a single shared invite link.
func (s *Server) handleWorkspaceJoin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		Token     string `json:"token"`      // the shared workspace invite
		Subdomain string `json:"subdomain"`  // the caller's tunnel subdomain
		APIToken  string `json:"api_token"`  // the caller's CLI token
	}
	if !readJSON(w, r, &req) {
		return
	}

	// identify the caller: an API token is only valid for its own subdomain
	email := ""
	if sess := s.currentEmail(r); sess != "" {
		email = sess // browser session (rare, but harmless)
	} else {
		sub := strings.ToLower(strings.TrimSpace(req.Subdomain))
		if sub == "" || !s.store.ValidateToken(sub, strings.TrimSpace(req.APIToken)) {
			jsonError(w, http.StatusUnauthorized, "not logged in — run `sixter login` first")
			return
		}
		owner, ok := s.store.OwnerOf(sub)
		if !ok {
			jsonError(w, http.StatusUnauthorized, "unknown subdomain")
			return
		}
		email = owner
	}

	tok, info, err := s.store.JoinWorkspace(strings.TrimSpace(req.Token), email)
	if err != nil {
		jsonError(w, http.StatusForbidden, err.Error())
		return
	}
	repoSub := ""
	if u, found := s.store.GetUser(info.Owner); found {
		repoSub = u.RepoSub
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "token": tok, "email": info.Email, "role": info.Role,
		"ws_id": info.WsID, "ws_name": info.WsName, "owner": info.Owner, "repo_sub": repoSub,
	})
}

// GET /api/member/profile?email= — a workspace owner viewing a teammate's
// profile. Only works for members of one of the caller's workspaces.
func (s *Server) handleMemberProfile(w http.ResponseWriter, r *http.Request) {
	viewer := s.currentEmail(r)
	if viewer == "" {
		jsonError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	member := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("email")))
	p, u, ok := s.store.MemberProfile(viewer, member)
	if !ok {
		jsonError(w, http.StatusForbidden, "you can only view profiles of people in your workspaces")
		return
	}
	if p.AccountType == "" {
		p.AccountType = "individual"
	}
	// Minimum necessary: the details the member chose to publish, plus the
	// tunnel subdomain (already public). Deliberately NOT exposed: their 2FA
	// state (tells an attacker who to target), repo subdomain, or admin flag.
	out := map[string]interface{}{
		"email":            u.Email,
		"profile":          p,
		"tunnel_subdomain": u.TunnelSub,
		"created_at":       u.CreatedAt.UTC().Format(time.RFC3339),
		"is_self":          strings.EqualFold(viewer, member),
	}
	if strings.EqualFold(viewer, member) {
		out["totp_enabled"] = u.TOTPEnabled // your own security state is fine
		out["repo_subdomain"] = u.RepoSub
	}
	writeJSON(w, http.StatusOK, out)
}

// apexHostname is set by the tunnel server so CORS can recognise its own
// subdomains. Empty means "same-origin only" (the safe default).
var apexHostname string

// SetApexHost tells the auth package which registrable host it serves, so
// credentialed CORS can be granted to that host and its subdomains.
func SetApexHost(h string) { apexHostname = strings.ToLower(strings.TrimSpace(h)) }

// sameSiteCORS grants credentialed CORS to the apex and its subdomains only —
// e.g. a repo dashboard at <sub>.sixter.in calling the gateway at sixter.in.
func sameSiteCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" || apexHostname == "" {
		return
	}
	host := origin
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	host = strings.ToLower(host)
	if host == apexHostname || strings.HasSuffix(host, "."+apexHostname) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Add("Vary", "Origin")
	}
}

// looksLikeEmail is a light sanity check (not full RFC validation).
func looksLikeEmail(e string) bool {
	at := strings.IndexByte(e, '@')
	if at <= 0 || at == len(e)-1 {
		return false
	}
	return strings.Contains(e[at+1:], ".")
}

// POST /api/device/approve {code} — approve a CLI login from the dashboard
func (s *Server) handleDeviceApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	email := s.currentEmail(r)
	if email == "" {
		jsonError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.store.ApproveDevice(req.Code, email); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

/* ─────────── device flow (CLI) ─────────── */

func requestBase(r *http.Request) (scheme, host string) {
	scheme = "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme, r.Host
}

// POST /api/device/code → start a device login
func (s *Server) handleDeviceCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	deviceCode, userCode, err := s.store.NewDevice()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "store error")
		return
	}
	scheme, host := requestBase(r)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"device_code":      deviceCode,
		"user_code":        userCode,
		"verification_uri": scheme + "://" + host + "/activate",
		"interval":         3,
		"expires_in":       600,
	})
}

// POST /api/device/token {"device_code": "..."} → poll for approval
func (s *Server) handleDeviceToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		DeviceCode string `json:"device_code"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.DeviceCode == "" {
		jsonError(w, http.StatusBadRequest, "device_code required")
		return
	}

	res := s.store.PollDevice(req.DeviceCode)
	switch res.Status {
	case "pending":
		writeJSON(w, http.StatusOK, map[string]string{"status": "pending"})
	case "approved":
		scheme, host := requestBase(r)
		wsScheme := "ws"
		if scheme == "https" {
			wsScheme = "wss"
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":           "approved",
			"email":            res.Email,
			"token":            res.Token,
			"tunnel_subdomain": res.TunnelSub,
			"repo_subdomain":   res.RepoSub,
			"server":           wsScheme + "://" + host,
			"api":              scheme + "://" + host,
		})
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "expired"})
	}
}
