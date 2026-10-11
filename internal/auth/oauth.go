package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

/*
OAuth 2.0 "sign in with Google / GitHub".

Credentials are read from the environment — never compiled into the binary and
never committed:

	SIXTER_GOOGLE_CLIENT_ID / SIXTER_GOOGLE_CLIENT_SECRET
	SIXTER_GITHUB_CLIENT_ID / SIXTER_GITHUB_CLIENT_SECRET
	SIXTER_BASE_URL          (optional, default https://<request host>)

A provider whose credentials are missing is simply not offered on the login
page, so the gateway runs fine without them.

Flow: /api/auth/<p>/start sets a signed, single-use state cookie and redirects
to the provider. /api/auth/<p>/callback verifies the state, exchanges the code
for a token, reads the verified email, then links or creates the account and
issues the normal Sixter session cookie.
*/

const (
	oauthStateCookie = "sixter_oauth_state"
	oauthNextCookie  = "sixter_oauth_next"
	oauthStateTTL    = 10 * time.Minute
)

// safeReturnURL only allows bouncing back to the apex or one of its
// subdomains (e.g. a repo dashboard at <sub>.sixter.in). Anything else — an
// attacker-supplied external URL — is discarded. Empty means "use the default".
func safeReturnURL(raw, apex string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || apex == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return ""
	}
	h := strings.ToLower(u.Hostname())
	apex = strings.ToLower(apex)
	if h == apex || strings.HasSuffix(h, "."+apex) {
		return u.String()
	}
	return ""
}

// apexHost is the registrable host this gateway serves (sixter.in).
func (c oauthConfig) apexHost(r *http.Request) string {
	base := c.baseURL(r)
	if u, err := url.Parse(base); err == nil {
		return u.Hostname()
	}
	return r.Host
}

// oauthProvider is one configured identity provider.
type oauthProvider struct {
	name         string
	clientID     string
	clientSecret string
	authURL      string
	tokenURL     string
	scope        string
}

func (p oauthProvider) configured() bool { return p.clientID != "" && p.clientSecret != "" }

// oauthConfig holds every provider the gateway knows about.
type oauthConfig struct {
	google oauthProvider
	github oauthProvider
	base   string // e.g. https://sixter.in — overrides the request host if set
}

func env(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// oauthFromEnv builds the provider config from environment variables.
func oauthFromEnv() oauthConfig {
	return oauthConfig{
		base: strings.TrimSuffix(env("SIXTER_BASE_URL"), "/"),
		google: oauthProvider{
			name:         "google",
			clientID:     env("SIXTER_GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_ID"),
			clientSecret: env("SIXTER_GOOGLE_CLIENT_SECRET", "GOOGLE_CLIENT_SECRET"),
			authURL:      "https://accounts.google.com/o/oauth2/v2/auth",
			tokenURL:     "https://oauth2.googleapis.com/token",
			scope:        "openid email profile",
		},
		github: oauthProvider{
			name:         "github",
			clientID:     env("SIXTER_GITHUB_CLIENT_ID", "GITHUB_CLIENT_ID"),
			clientSecret: env("SIXTER_GITHUB_CLIENT_SECRET", "GITHUB_CLIENT_SECRET"),
			authURL:      "https://github.com/login/oauth/authorize",
			tokenURL:     "https://github.com/login/oauth/access_token",
			scope:        "read:user user:email",
		},
	}
}

func (c oauthConfig) provider(name string) (oauthProvider, bool) {
	switch name {
	case "google":
		return c.google, c.google.configured()
	case "github":
		return c.github, c.github.configured()
	}
	return oauthProvider{}, false
}

// baseURL resolves the public origin for redirect URIs.
func (c oauthConfig) baseURL(r *http.Request) string {
	if c.base != "" {
		return c.base
	}
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" && strings.HasPrefix(r.Host, "localhost") {
		scheme = "http"
	}
	return scheme + "://" + r.Host
}

func (c oauthConfig) redirectURI(r *http.Request, provider string) string {
	return c.baseURL(r) + "/api/auth/" + provider + "/callback"
}

/* ─────────── HTTP handlers ─────────── */

// GET /api/auth/providers — which buttons the login page should render.
func (s *Server) handleAuthProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"google": s.oauth.google.configured(),
		"github": s.oauth.github.configured(),
	})
}

// GET /api/auth/{google,github}/start — redirect the browser to the provider.
func (s *Server) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	name := providerFromPath(r.URL.Path)
	p, ok := s.oauth.provider(name)
	if !ok {
		oauthFail(w, r, "this sign-in method isn't enabled")
		return
	}

	// CSRF: a random state echoed by the provider and matched against a cookie.
	state := randHex(24)
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    name + ":" + state,
		Path:     "/api/auth/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(oauthStateTTL / time.Second),
	})

	// Optional bounce-back target (a repo dashboard sending its owner here).
	if next := safeReturnURL(r.URL.Query().Get("next"), s.oauth.apexHost(r)); next != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     oauthNextCookie,
			Value:    next,
			Path:     "/api/auth/",
			HttpOnly: true,
			Secure:   isHTTPS(r),
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(oauthStateTTL / time.Second),
		})
	}

	q := url.Values{}
	q.Set("client_id", p.clientID)
	q.Set("redirect_uri", s.oauth.redirectURI(r, name))
	q.Set("response_type", "code")
	q.Set("scope", p.scope)
	q.Set("state", state)
	if name == "google" {
		q.Set("access_type", "online")
		q.Set("prompt", "select_account")
	}
	http.Redirect(w, r, p.authURL+"?"+q.Encode(), http.StatusFound)
}

// GET /api/auth/{google,github}/callback — finish the flow and log the user in.
func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	name := providerFromPath(r.URL.Path)
	p, ok := s.oauth.provider(name)
	if !ok {
		oauthFail(w, r, "this sign-in method isn't enabled")
		return
	}

	// clear the one-shot cookies no matter how this ends
	returnTo := ""
	if nc, nerr := r.Cookie(oauthNextCookie); nerr == nil {
		returnTo = safeReturnURL(nc.Value, s.oauth.apexHost(r))
	}
	for _, n := range []string{oauthStateCookie, oauthNextCookie} {
		http.SetCookie(w, &http.Cookie{
			Name: n, Value: "", Path: "/api/auth/", MaxAge: -1,
			HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode,
		})
	}

	if e := r.URL.Query().Get("error"); e != "" {
		oauthFail(w, r, "sign-in was cancelled")
		return
	}

	c, cerr := r.Cookie(oauthStateCookie)
	want := name + ":" + r.URL.Query().Get("state")
	if cerr != nil || c.Value == "" || !constEq(c.Value, want) {
		oauthFail(w, r, "sign-in expired or was tampered with — please try again")
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		oauthFail(w, r, "no authorization code returned")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	token, err := exchangeCode(ctx, p, code, s.oauth.redirectURI(r, name))
	if err != nil {
		oauthFail(w, r, "could not verify your identity with "+name)
		return
	}

	email, displayName, providerID, err := fetchIdentity(ctx, name, token)
	if err != nil {
		oauthFail(w, r, err.Error())
		return
	}

	u, err := s.store.UpsertOAuthUser(name, providerID, email, displayName)
	if err != nil {
		oauthFail(w, r, err.Error())
		return
	}

	// An account that already enabled TOTP must still pass it.
	if u.TOTPEnabled {
		dest := "/login?mfa=" + url.QueryEscape(s.newChallenge(u.Email))
		if returnTo != "" {
			dest += "&next=" + url.QueryEscape(returnTo)
		}
		http.Redirect(w, r, dest, http.StatusFound)
		return
	}

	sid, serr := s.store.NewSession(u.Email)
	if serr != nil {
		oauthFail(w, r, "session error")
		return
	}
	setSession(w, r, sid)

	// New OAuth accounts have no subdomain yet — send them to onboarding.
	// (Onboarding wins over a bounce-back: they have nothing to go back to.)
	if u.NeedsOnboarding() {
		http.Redirect(w, r, "/welcome", http.StatusFound)
		return
	}
	if returnTo != "" {
		http.Redirect(w, r, returnTo, http.StatusFound)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

func providerFromPath(p string) string {
	// /api/auth/<provider>/<action>
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// oauthFail sends the user back to the login page with a readable message.
func oauthFail(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/login?oauth_error="+url.QueryEscape(msg), http.StatusFound)
}

/* ─────────── provider calls ─────────── */

var oauthHTTP = &http.Client{Timeout: 15 * time.Second}

// exchangeCode swaps the authorization code for an access token.
func exchangeCode(ctx context.Context, p oauthProvider, code, redirectURI string) (string, error) {
	form := url.Values{}
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("grant_type", "authorization_code")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json") // GitHub defaults to form encoding

	resp, err := oauthHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var body struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.AccessToken == "" {
		if body.Error != "" {
			return "", fmt.Errorf("%s: %s", body.Error, body.ErrorDesc)
		}
		return "", errors.New("no access token returned")
	}
	return body.AccessToken, nil
}

// fetchIdentity reads the verified email, display name and stable provider id.
func fetchIdentity(ctx context.Context, provider, token string) (email, name, id string, err error) {
	switch provider {
	case "google":
		var g struct {
			Sub           string `json:"sub"`
			Email         string `json:"email"`
			EmailVerified bool   `json:"email_verified"`
			Name          string `json:"name"`
		}
		if err = getJSON(ctx, "https://www.googleapis.com/oauth2/v3/userinfo", token, &g); err != nil {
			return "", "", "", errors.New("could not read your Google profile")
		}
		if g.Email == "" || !g.EmailVerified {
			return "", "", "", errors.New("your Google account has no verified email address")
		}
		return g.Email, g.Name, g.Sub, nil

	case "github":
		var gh struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
			Name  string `json:"name"`
			Email string `json:"email"`
		}
		if err = getJSON(ctx, "https://api.github.com/user", token, &gh); err != nil || gh.ID == 0 {
			return "", "", "", errors.New("could not read your GitHub profile")
		}
		mail := gh.Email
		// The public profile email is often hidden — ask for the verified list.
		var emails []struct {
			Email    string `json:"email"`
			Primary  bool   `json:"primary"`
			Verified bool   `json:"verified"`
		}
		if getJSON(ctx, "https://api.github.com/user/emails", token, &emails) == nil {
			for _, e := range emails {
				if e.Primary && e.Verified {
					mail = e.Email
					break
				}
			}
			if mail == "" {
				for _, e := range emails {
					if e.Verified {
						mail = e.Email
						break
					}
				}
			}
		}
		if mail == "" {
			return "", "", "", errors.New("your GitHub account has no verified email address")
		}
		display := gh.Name
		if display == "" {
			display = gh.Login
		}
		return mail, display, strconv.FormatInt(gh.ID, 10), nil
	}
	return "", "", "", errors.New("unknown provider")
}

func getJSON(ctx context.Context, endpoint, token string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "sixter-gateway")

	resp, err := oauthHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %d", endpoint, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

/* ─────────── onboarding: claim a subdomain ─────────── */

// GET /api/subdomain/check?sub=name — availability for the onboarding step.
func (s *Server) handleSubdomainCheck(w http.ResponseWriter, r *http.Request) {
	if s.currentEmail(r) == "" {
		jsonError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	sub := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sub")))
	if !ValidSubdomain(sub) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"subdomain": sub, "valid": false, "available": false,
			"reason": "3–32 characters: lowercase letters, digits and hyphens",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"subdomain": sub,
		"valid":     true,
		"available": s.store.SubdomainAvailable(sub),
	})
}

// POST /api/me/subdomain {subdomain} — one-time allocation for OAuth signups.
func (s *Server) handleClaimSubdomain(w http.ResponseWriter, r *http.Request) {
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
		Subdomain string `json:"subdomain"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.store.ClaimSubdomain(email, req.Subdomain); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	u, _ := s.store.GetUser(email)
	writeJSON(w, http.StatusOK, userJSON(u))
}
