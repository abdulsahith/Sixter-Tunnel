// Sixter tunnel server — company-side service.
//
// Serves three things on one port:
//  1. apex domain (sixter.in)  → landing SPA + account APIs + device-flow API
//  2. /connect                 → authenticated WebSocket tunnel endpoint
//  3. *.sixter.in / custom domains → reverse proxy into the matching tunnel
//
// Auth: every tunnel client must present the API token issued via
// "sixter login" (device flow). Tokens are stored hashed (SHA-256).
package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/acme/autocert"
	"sixter.in/tunnel/internal/auth"
	"sixter.in/tunnel/internal/firewall"
	"sixter.in/tunnel/internal/tunnel"
)

// The landing page SPA (React/Vite build) is embedded into the binary —
// no -static configuration needed. A disk folder (default ./static) acts
// as an optional overlay for big downloadables like installers.
//
//go:embed all:dist
var landingFS embed.FS

// Windows registry entries can override Go's MIME table with text/plain,
// breaking strict MIME checking in browsers — force the correct types.
func init() {
	_ = mime.AddExtensionType(".js", "text/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".mjs", "text/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".css", "text/css; charset=utf-8")
	_ = mime.AddExtensionType(".html", "text/html; charset=utf-8")
	_ = mime.AddExtensionType(".svg", "image/svg+xml")
	_ = mime.AddExtensionType(".json", "application/json")
	_ = mime.AddExtensionType(".png", "image/png")
}

/* ─────────── tunnel session ─────────── */

type Tunnel struct {
	conn        *websocket.Conn
	writeMu     sync.Mutex
	mu          sync.Mutex
	requests    map[string]chan []byte
	wsChans     map[string]chan tunnel.WSMessageData
	subdomain   string
	connectedAt time.Time
	reqCount    atomic.Int64

	inflight  atomic.Int64 // in-flight requests, drives least-connections balancing
	tunnelID  string       // stable routing handle (UUID)
	lastBeat  atomic.Int64 // unix-nano of last heartbeat/pong
	latencyMs atomic.Int64 // measured RTT in ms
	pingAt    atomic.Int64 // unix-nano when the last ping was sent

	// Passive health: a live tunnel whose APP is broken still heartbeats, so
	// real request outcomes decide whether it keeps receiving traffic.
	fails        atomic.Int64 // consecutive failures
	ejectedUntil atomic.Int64 // unix-nano; 0 = in rotation
}

/* ─────────── rate limiter (sliding window per subdomain) ─────────── */

type rateLimiter struct {
	mu      sync.Mutex
	windows map[string][]time.Time
	limit   int
	window  time.Duration
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		windows: make(map[string][]time.Time),
		limit:   limit,
		window:  window,
	}
}

func (r *rateLimiter) Allow(sub string) bool {
	if r.limit <= 0 {
		return true
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()

	cutoff := now.Add(-r.window)
	ts := r.windows[sub]
	valid := ts[:0]
	for _, t := range ts {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	if len(valid) >= r.limit {
		r.windows[sub] = valid
		return false
	}
	r.windows[sub] = append(valid, now)
	return true
}

/* ─────────── custom-domain mapper ─────────── */

type domainMapper struct {
	mu      sync.RWMutex
	apex    string
	mapping map[string]string // custom host → sixter subdomain
	static  map[string]bool   // hosts that came from the -domains flag (not persisted)
	path    string            // JSON file for dynamically-attached domains
}

func newDomainMapper(apex, spec string) *domainMapper {
	dm := &domainMapper{apex: apex, mapping: make(map[string]string), static: make(map[string]bool)}
	for _, pair := range strings.Split(spec, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			host := strings.ToLower(strings.TrimSpace(parts[0]))
			dm.mapping[host] = strings.TrimSpace(parts[1])
			dm.static[host] = true
		}
	}
	return dm
}

// load reads previously attached custom domains from disk (path must be set).
func (dm *domainMapper) load() {
	if dm.path == "" {
		return
	}
	b, err := os.ReadFile(dm.path)
	if err != nil {
		return
	}
	var saved map[string]string
	if json.Unmarshal(b, &saved) != nil {
		return
	}
	dm.mu.Lock()
	for host, sub := range saved {
		if !dm.static[host] { // never let the file override an operator-pinned mapping
			dm.mapping[strings.ToLower(host)] = sub
		}
	}
	dm.mu.Unlock()
}

// save persists only the dynamically-attached domains (not the -domains flag ones).
// Caller must hold dm.mu.
func (dm *domainMapper) saveLocked() {
	if dm.path == "" {
		return
	}
	out := map[string]string{}
	for host, sub := range dm.mapping {
		if !dm.static[host] {
			out[host] = sub
		}
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return
	}
	tmp := fmt.Sprintf("%s.tmp%d", dm.path, time.Now().UnixNano())
	if os.WriteFile(tmp, b, 0600) == nil {
		_ = os.Rename(tmp, dm.path)
	}
}

// Add attaches a custom domain to a subdomain (tunnel-mode hosting) and persists it.
func (dm *domainMapper) Add(host, sub string) {
	host = strings.ToLower(strings.TrimSpace(host))
	sub = strings.ToLower(strings.TrimSpace(sub))
	if host == "" || sub == "" {
		return
	}
	dm.mu.Lock()
	dm.mapping[host] = sub
	dm.saveLocked()
	dm.mu.Unlock()
}

// Remove detaches a custom domain (no-op for operator-pinned static domains).
func (dm *domainMapper) Remove(host string) {
	host = strings.ToLower(strings.TrimSpace(host))
	dm.mu.Lock()
	if !dm.static[host] {
		delete(dm.mapping, host)
		dm.saveLocked()
	}
	dm.mu.Unlock()
}

func (dm *domainMapper) Resolve(host string) (string, bool) {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	if sub, ok := dm.mapping[host]; ok {
		return sub, true
	}
	if strings.HasSuffix(host, "."+dm.apex) {
		return strings.TrimSuffix(host, "."+dm.apex), true
	}
	return "", false
}

// IsCustom reports whether a host is an attached custom (tunnel-mode) domain.
func (dm *domainMapper) IsCustom(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	_, ok := dm.mapping[host]
	return ok
}

// DomainsFor returns the custom domains attached to a subdomain (reverse lookup
// over the routing table). The default <sub>.<apex> domain is not included here.
func (dm *domainMapper) DomainsFor(sub string) []string {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	var out []string
	for host, s := range dm.mapping {
		if s == sub {
			out = append(out, host)
		}
	}
	return out
}

/* ─────────── global state ─────────── */

var (
	upgrader = websocket.Upgrader{
		CheckOrigin:       func(r *http.Request) bool { return true },
		EnableCompression: true, // compress the tunnel pipe (matches the client dialer)
		ReadBufferSize:    64 * 1024,
		WriteBufferSize:   64 * 1024,
	}

	tunnels     = make(map[string]*Tunnel)
	tunnelsByID = make(map[string]*Tunnel)      // tunnel id → live tunnel (spec: route by id)
	pools       = make(map[string]*tunnelPool)  // subdomain → replica pool (load balancing)
	tunnelsMu   sync.RWMutex

	startedAt = time.Now()

	rl            *rateLimiter
	dm            *domainMapper
	store         auth.Storage
	fw            *firewall.Manager
	authMux       http.Handler // apex mux, wrapped by auth.Secure
	apexHost      string
	openMode      bool
	staticOverlay string
)

/* ─────────── landing SPA (embedded + disk overlay) ─────────── */

func spaHandler() http.HandlerFunc {
	sub, _ := fs.Sub(landingFS, "dist")
	fileServer := http.FileServerFS(sub)
	return func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+r.URL.Path)), "/")
		if p == "" {
			p = "index.html"
		}
		// 1. disk overlay (installers, updated assets — no rebuild needed)
		if staticOverlay != "" {
			fp := filepath.Join(staticOverlay, filepath.FromSlash(p))
			if st, err := os.Stat(fp); err == nil && !st.IsDir() {
				http.ServeFile(w, r, fp)
				return
			}
		}
		// 2. embedded dist
		if f, err := sub.Open(p); err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// 3. asset-looking paths (have an extension) must 404, never fallback —
		// otherwise stale cached HTML gets served as JS/CSS and breaks the page
		if filepath.Ext(p) != "" {
			http.NotFound(w, r)
			return
		}
		// 4. SPA fallback (client-side routes: /dashboard, /docs, /activate…)
		b, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.Error(w, "landing page not built", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	}
}

// serveErrorPage serves notunnel.html / badgateway.html from the overlay
// or the embedded dist.
func serveErrorPage(w http.ResponseWriter, name string) {
	if staticOverlay != "" {
		if b, err := os.ReadFile(filepath.Join(staticOverlay, name)); err == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(b)
			return
		}
	}
	if b, err := landingFS.ReadFile("dist/" + name); err == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
		return
	}
	http.Error(w, "tunnel offline", http.StatusServiceUnavailable)
}

/* ─────────── main ─────────── */

func main() {
	addr := flag.String("addr", ":8008", "listen address (plain HTTP; used when -tls is off)")
	enableTLS := flag.Bool("tls", false, "terminate HTTPS with automatic Let's Encrypt certificates (binds :80 + :443 directly — remove any front nginx first)")
	tlsCache := flag.String("tls-cache", "./sixter-certs", "directory to cache Let's Encrypt certificates")
	tlsEmail := flag.String("tls-email", "", "contact email for Let's Encrypt registration (recommended)")
	tlsAddr := flag.String("tls-addr", ":443", "HTTPS listen address. Use a local addr like 127.0.0.1:8443 to sit behind an nginx SNI stream (then certs use TLS-ALPN-01, no port 80 needed)")
	autoTLSFlag := flag.Bool("auto-tls", false, "auto-provision an nginx vhost + Let's Encrypt cert for each attached tunnel domain (run the gateway as root on the nginx box; needs nginx + certbot). Keeps nginx as the TLS terminator — no -tls / port conflict.")
	apex := flag.String("apex", "sixter.in", "apex domain")
	domains := flag.String("domains", "", "custom domain map: custom.com=sub1,other.io=sub2")
	rateLimit := flag.Int("rate-limit", 0, "max requests/second per subdomain (0 = unlimited)")
	dataFile := flag.String("data", "./sixter-auth.json", "auth store file (used when -db is not set)")
	dbDSN := flag.String("db", "", "PostgreSQL DSN (e.g. postgres://user:pass@localhost/sixter?sslmode=disable)")
	staticDir := flag.String("static", "./static", "optional overlay folder for installers/extra files (landing is embedded)")
	open := flag.Bool("open", false, "DEV ONLY: allow tunnels without authentication")
	adminEmail := flag.String("admin-email", "", "grant platform admin to this account email on startup")
	flag.Parse()

	apexHost = *apex
	auth.SetApexHost(*apex) // lets the gateway grant credentialed CORS to *.apex
	openMode = *open
	autoTLS = *autoTLSFlag
	acmeEmail = *tlsEmail
	if _, port, err := net.SplitHostPort(*addr); err == nil && port != "" {
		gatewayHTTPPort = port
	}
	rl = newRateLimiter(*rateLimit, time.Second)
	dm = newDomainMapper(*apex, *domains)
	dm.path = filepath.Join(filepath.Dir(*dataFile), "sixter-domains.json")
	dm.load()
	if autoTLS {
		log.Println("Auto-TLS: nginx vhost + certbot per attached tunnel domain (gateway must run as root)")
	}

	if *dbDSN != "" {
		pg, err := auth.OpenPostgres(*dbDSN)
		if err != nil {
			log.Fatal("postgres: ", err)
		}
		store = pg
		log.Println("Auth store: PostgreSQL")
	} else {
		js, err := auth.Open(*dataFile)
		if err != nil {
			log.Fatal("auth store: ", err)
		}
		store = js
		log.Printf("Auth store: JSON file (%s) — use -db for PostgreSQL", *dataFile)
	}

	// bootstrap a platform admin (idempotent)
	if *adminEmail != "" {
		if err := store.SetAdmin(*adminEmail, true); err != nil {
			log.Printf("admin bootstrap: %v (does %s have an account yet?)", err, *adminEmail)
		} else {
			log.Printf("✓ admin granted to %s", *adminEmail)
		}
	}

	// live tunnel status for the dashboard
	statusFn := func(sub string) (bool, int64, int64) {
		tunnelsMu.RLock()
		t, ok := tunnels[sub]
		tunnelsMu.RUnlock()
		if !ok {
			return false, 0, 0
		}
		return true, int64(time.Since(t.connectedAt).Seconds()), t.reqCount.Load()
	}

	// firewall: load each subdomain's saved settings from the account store
	fw = firewall.NewManager(func(sub string) (firewall.Settings, bool) {
		data, ok := store.GetFirewall(sub)
		if !ok {
			return firewall.Settings{}, false
		}
		var s firewall.Settings
		if err := json.Unmarshal([]byte(data), &s); err != nil {
			return firewall.Settings{}, false
		}
		return s, true
	})

	// disk overlay only if the folder actually exists — zero config required
	staticOverlay = ""
	if st, err := os.Stat(*staticDir); err == nil && st.IsDir() {
		staticOverlay = *staticDir
		log.Printf("Static overlay: %s", staticOverlay)
	}

	securityFn := func(sub string) interface{} {
		return fw.For(sub).Stats(40)
	}
	fwUpdateFn := func(sub, settingsJSON string) {
		var s firewall.Settings
		if json.Unmarshal([]byte(settingsJSON), &s) == nil {
			fw.Update(sub, s)
		}
	}

	// detailFn powers the tunnel dashboard: stable id, live status (online/
	// offline), connected-since, latency and last-heartbeat, plus attached domains.
	detailFn := func(sub string) map[string]interface{} {
		id := store.TunnelID(sub) // stable even while offline
		domains := []string{sub + "." + dm.apex}
		domains = append(domains, dm.DomainsFor(sub)...)
		out := map[string]interface{}{
			"tunnel_id": id,
			"status":    "offline",
			"latency_ms": int64(0),
			"domains":   domains,
		}
		tunnelsMu.RLock()
		t, ok := tunnels[sub]
		tunnelsMu.RUnlock()
		if !ok {
			return out
		}
		last := t.lastBeat.Load()
		// online only if a heartbeat landed within the last 45s
		if last > 0 && time.Since(time.Unix(0, last)) <= 45*time.Second {
			out["status"] = "online"
		} else {
			out["status"] = "connecting"
		}
		out["latency_ms"] = t.latencyMs.Load()
		out["connected_since"] = t.connectedAt.UTC().Format(time.RFC3339)
		out["replicas"] = replicaCount(sub) // live copies behind the load balancer
		if last > 0 {
			out["last_heartbeat"] = time.Unix(0, last).UTC().Format(time.RFC3339)
		}
		return out
	}

	// auth.Secure adds the security-header / cache / anti-brute-force baseline.
	// It wraps ONLY the apex mux — proxied user tunnels must pass through
	// untouched, or our CSP would break other people's apps.
	authMux = auth.Secure(auth.NewMux(store, spaHandler(), auth.Hooks{
		Status:         statusFn,
		Security:       securityFn,
		FirewallUpdate: fwUpdateFn,
		Detail:         detailFn,
	}))

	mux := http.NewServeMux()
	mux.HandleFunc("/connect", wsConnect)
	mux.HandleFunc("/api/status", apiStatus)
	mux.HandleFunc("/api/tunnel/domain", handleTunnelDomain)
	mux.HandleFunc("/api/mesh/register", handleMeshRegister)
	mux.HandleFunc("/api/tunnel/ban", handleTunnelBan)
	mux.HandleFunc("/api/sso/grant", handleSSOGrant)
	mux.HandleFunc("/dl/", handleBinaryDownload)
	mux.HandleFunc("/api/sso/verify", handleSSOVerify)
	mux.HandleFunc("/", proxy)

	log.Printf("Sixter tunnel server on %s (apex: %s)", *addr, *apex)
	if openMode {
		log.Println("⚠ OPEN MODE: tunnel auth disabled — do not use in production")
	} else {
		log.Println("Auth: device-flow tokens required for tunnels")
	}
	if *rateLimit > 0 {
		log.Printf("Rate limit: %d req/s per subdomain", *rateLimit)
	}
	if *domains != "" {
		log.Printf("Custom domains: %s", *domains)
	}

	if !*enableTLS {
		log.Fatal(http.ListenAndServe(*addr, mux))
		return
	}

	// TLS mode still keeps the plain listener alive on -addr (:8008): tunnel
	// clients connect there, and any nginx vhost that proxies to it keeps working.
	// The HTTPS listener below is additional, for direct/SNI-streamed traffic.
	go func() {
		log.Printf("Plain HTTP listener on %s (tunnel clients + internal proxy)", *addr)
		log.Fatal(http.ListenAndServe(*addr, mux))
	}()

	// ── Automatic HTTPS (Let's Encrypt) ──────────────────────────────────
	// On-demand certificates for the apex, every *.sixter.in subdomain, and any
	// custom tunnel domain a user attaches. This is the "Cloudflare for SaaS"
	// model: a customer points their domain at us and we issue + renew its cert.
	certManager := &autocert.Manager{
		Cache:  autocert.DirCache(*tlsCache),
		Prompt: autocert.AcceptTOS,
		Email:  *tlsEmail,
		HostPolicy: func(_ context.Context, host string) error {
			host = strings.ToLower(strings.TrimSpace(host))
			switch {
			case host == apexHost, host == "www."+apexHost:
				return nil
			case strings.HasSuffix(host, "."+apexHost): // any user subdomain
				return nil
			case dm.IsCustom(host): // attached tunnel-mode domain
				return nil
			}
			return fmt.Errorf("sixter: host %q is not configured for TLS", host)
		},
	}

	// When the gateway OWNS :443 it also serves :80 for ACME http-01 + redirect.
	// Behind an nginx SNI stream (tls-addr is a local port), nginx already holds
	// :80/:443, so we skip our own :80 and let autocert use TLS-ALPN-01 over the
	// TLS connection nginx forwards to us.
	if *tlsAddr == ":443" {
		go func() {
			h := certManager.HTTPHandler(nil) // nil → redirect non-challenge traffic to https
			log.Println("HTTP→HTTPS redirect + ACME challenge on :80")
			log.Fatal(http.ListenAndServe(":80", h))
		}()
	} else {
		log.Printf("Behind nginx SNI stream — certs via TLS-ALPN-01 (no :80 listener)")
	}

	tlsConf := certManager.TLSConfig() // includes the acme-tls/1 ALPN challenge handler
	httpsSrv := &http.Server{
		Addr:      *tlsAddr,
		Handler:   mux,
		TLSConfig: tlsConf,
	}
	log.Printf("Automatic HTTPS enabled on %s (certs cached in %s)", *tlsAddr, *tlsCache)
	log.Fatal(httpsSrv.ListenAndServeTLS("", ""))
}

/* ─────────── custom-domain attach (tunnel-mode hosting) ─────────── */

// handleTunnelDomain lets an authenticated repo-server attach/detach a custom
// domain to its tunnel. POST {domain,sub} maps domain→sub in the routing table;
// DELETE {domain,sub} removes it. The caller proves ownership of the subdomain
// with its API token (same token used to open the tunnel).
// ownsServiceSub authorizes a token against a SERVICE subdomain — the stable
// address a scaled project is published on.
//
// A service subdomain belongs to no user account, so ValidateToken can never
// match it. But it is created by an assignment, and that assignment has an
// owner. Accepting the owner's token is what lets a project's domain point at
// a load-balanced pool instead of being pinned to one machine.
func ownsServiceSub(sub, tok string) bool {
	if sub == "" || tok == "" {
		return false
	}
	for _, a := range store.AllAssignments() {
		if strings.EqualFold(a.Subdomain, sub) {
			u, found := store.GetUser(a.Owner)
			return found && store.ValidateToken(u.TunnelSub, tok)
		}
	}
	return false
}

func handleTunnelDomain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, "POST or DELETE only", http.StatusMethodNotAllowed)
		return
	}

	tok := strings.TrimSpace(r.Header.Get("X-Token"))
	if tok == "" {
		if a := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(a), "bearer ") {
			tok = strings.TrimSpace(a[7:])
		}
	}

	var req struct {
		Domain string `json:"domain"`
		Sub    string `json:"sub"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	req.Sub = strings.ToLower(strings.TrimSpace(req.Sub))

	if req.Domain == "" || req.Sub == "" {
		http.Error(w, "domain and sub required", http.StatusBadRequest)
		return
	}
	// never let someone hijack the apex or a reserved control host
	if req.Domain == apexHost || strings.HasSuffix(req.Domain, "."+apexHost) {
		http.Error(w, "cannot attach a *.sixter.in domain — that is automatic", http.StatusBadRequest)
		return
	}
	if !openMode && !store.ValidateToken(req.Sub, tok) {
		if !ownsServiceSub(req.Sub, tok) {
			http.Error(w, "unauthorized: token does not match this tunnel", http.StatusUnauthorized)
			return
		}
	}

	if r.Method == http.MethodDelete {
		dm.Remove(req.Domain)
		if autoTLS {
			go func(d string) { _ = deprovisionDomain(d) }(req.Domain)
		}
		writeJSONResp(w, map[string]any{"ok": true, "detached": req.Domain})
		return
	}

	dm.Add(req.Domain, req.Sub)
	logInfo("Custom domain attached: ", req.Domain+" → "+req.Sub, extractIP(r))

	// Auto-provision nginx vhost + Let's Encrypt cert (runs in the background;
	// certbot can take several seconds). The route already works over HTTP
	// immediately; HTTPS comes online once the cert is issued.
	tlsState := "manual"
	if autoTLS {
		tlsState = "provisioning"
		go func(d string) {
			if err := provisionDomain(d); err != nil {
				logError("auto-tls failed: ", d, err.Error())
			} else {
				logInfo("auto-tls ready: ", d, "HTTPS issued")
			}
		}(req.Domain)
	}

	writeJSONResp(w, map[string]any{
		"ok":        true,
		"domain":    req.Domain,
		"sub":       req.Sub,
		"tunnel_id": store.TunnelID(req.Sub),
		"tls":       tlsState,
	})
}

// handleTunnelBan lets an authenticated repo-server block an attacker IP at the
// gateway for its subdomain. This is the ONLY place a ban works for tunnel-mode
// apps: the attacker connects to the gateway, not the customer's box, so an
// nftables ban on the box can never see them. Bans here apply to the sub's
// firewall, so they cover every tunnel domain routed through it.
func handleTunnelBan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	tok := strings.TrimSpace(r.Header.Get("X-Token"))
	if tok == "" {
		if a := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(a), "bearer ") {
			tok = strings.TrimSpace(a[7:])
		}
	}
	var req struct {
		Sub     string `json:"sub"`
		IP      string `json:"ip"`
		Minutes int    `json:"minutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	req.Sub = strings.ToLower(strings.TrimSpace(req.Sub))
	req.IP = strings.TrimSpace(req.IP)
	if req.Sub == "" || req.IP == "" {
		http.Error(w, "sub and ip required", http.StatusBadRequest)
		return
	}
	if !openMode && !store.ValidateToken(req.Sub, tok) {
		if !ownsServiceSub(req.Sub, tok) {
			http.Error(w, "unauthorized: token does not match this tunnel", http.StatusUnauthorized)
			return
		}
	}
	mins := req.Minutes
	if mins <= 0 {
		mins = 60
	}
	fw.For(req.Sub).BanIP(req.IP, time.Duration(mins)*time.Minute)
	logInfo("Gateway ban: ", req.IP+" on "+req.Sub, fmt.Sprintf("%dm", mins))
	writeJSONResp(w, map[string]any{"ok": true, "ip": req.IP, "minutes": mins})
}

func writeJSONResp(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

/* ─────────── WebSocket: client connect ─────────── */

func wsConnect(w http.ResponseWriter, r *http.Request) {
	sub := r.URL.Query().Get("subdomain")
	if sub == "" {
		http.Error(w, "subdomain required", http.StatusBadRequest)
		return
	}

	if !openMode {
		tok := r.URL.Query().Get("token")
		// Two kinds of caller may open a tunnel for a subdomain:
		//   1. the account owner, with their API token (sixter tunnel);
		//   2. a node agent, with an assignment's route token — this is how a
		//      fleet workload becomes reachable at all, since agents dial out
		//      from behind NAT and have no inbound port to proxy to.
		ok := store.ValidateToken(sub, tok)
		if !ok {
			if want, found := auth.RouteTokenFor(store, sub); found && want != "" && tok == want {
				ok = true
			}
		}
		if !ok {
			logError("Auth failed: ", sub, extractIP(r))
			http.Error(w, "unauthorized: run `sixter login` and use your assigned subdomains", http.StatusUnauthorized)
			return
		}
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}

	clientIP := extractIP(r)

	tid := store.TunnelID(sub) // stable routing handle (UUID), persists across reconnects

	t := &Tunnel{
		conn:        conn,
		requests:    make(map[string]chan []byte),
		wsChans:     make(map[string]chan tunnel.WSMessageData),
		subdomain:   sub,
		connectedAt: time.Now(),
		tunnelID:    tid,
	}
	t.lastBeat.Store(time.Now().UnixNano())

	tunnelsMu.Lock()
	// Join the subdomain's pool rather than replacing whatever was there.
	// Closing the previous tunnel would make horizontal scaling impossible —
	// replica 2 would disconnect replica 1 the moment it came up.
	addTunnelLocked(sub, t)
	tunnelsByID[tid] = t
	replicas := len(pools[sub].members)
	tunnelsMu.Unlock()
	if replicas > 1 {
		logInfo("Replica joined: ", sub+" ("+itoaLB(replicas)+" live)", clientIP)
	}

	logInfo("Tunnel connected: ", sub+" ["+tid+"]", clientIP)

	// Keepalive + heartbeat latency probe: time each ping→pong round-trip.
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			t.pingAt.Store(time.Now().UnixNano())
			t.writeMu.Lock()
			err := t.conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
			t.writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}()

	go func() {
		defer func() {
			tunnelsMu.Lock()
			removeTunnelLocked(sub, t)
			if tunnelsByID[tid] == t {
				delete(tunnelsByID, tid)
			}
			tunnelsMu.Unlock()
			_ = conn.Close()
			logError("Tunnel disconnected: ", sub+" ["+tid+"]", clientIP)
		}()

		conn.SetReadLimit(1020 << 20)
		conn.SetPongHandler(func(string) error {
			now := time.Now().UnixNano()
			t.lastBeat.Store(now)
			if sent := t.pingAt.Load(); sent > 0 {
				if ms := (now - sent) / int64(time.Millisecond); ms >= 0 {
					t.latencyMs.Store(ms)
				}
			}
			return nil
		})

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var tmsg tunnel.Message
			if err := json.Unmarshal(msg, &tmsg); err != nil {
				continue
			}

			switch tmsg.Type {
			case "http_response":
				t.mu.Lock()
				if ch, ok := t.requests[tmsg.RequestID]; ok {
					ch <- msg
					delete(t.requests, tmsg.RequestID)
				}
				t.mu.Unlock()

			case "ws_message":
				b, _ := json.Marshal(tmsg.Data)
				var ws tunnel.WSMessageData
				if err := json.Unmarshal(b, &ws); err != nil {
					continue
				}
				t.mu.Lock()
				ch := t.wsChans[tmsg.RequestID]
				t.mu.Unlock()
				if ch != nil {
					select {
					case ch <- ws:
					default:
					}
				}

			case "ws_close":
				t.mu.Lock()
				ch := t.wsChans[tmsg.RequestID]
				if ch != nil {
					close(ch)
					delete(t.wsChans, tmsg.RequestID)
				}
				t.mu.Unlock()
			}
		}
	}()
}

/* ─────────── proxy ─────────── */

func isApexHost(host string) bool {
	return host == apexHost || host == "www."+apexHost ||
		host == "localhost" || host == "127.0.0.1"
}

func proxy(w http.ResponseWriter, r *http.Request) {
	hostOnly := hostWithoutPort(r.Host)

	// apex domain → landing SPA + account APIs
	if isApexHost(hostOnly) {
		authMux.ServeHTTP(w, r)
		return
	}

	sub, ok := dm.Resolve(hostOnly)
	if !ok {
		// unknown host → landing
		authMux.ServeHTTP(w, r)
		return
	}

	// reserved control-plane host: admin.<apex> → admin dashboard + admin APIs.
	// "admin" is in reservedSubs, so no user can ever own this subdomain.
	if sub == "admin" {
		adminRouter(w, r)
		return
	}

	// edge DDoS / L7-flood guard (runs before the per-tunnel firewall)
	clientIP := extractIP(r)
	guard := fw.Guard.Check(clientIP)
	if guard.Block {
		w.Header().Set("Retry-After", "60")
		w.Header().Set("X-Sixter-Firewall", "rate-limited")
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return
	}
	// under attack: challenge untrusted browsers (skip websockets — can't run the page)
	if guard.Attack && !isWebSocketRequest(r) && !hasValidPass(r, clientIP) {
		serveChallenge(w, clientIP)
		return
	}

	// pickTunnel takes tunnelsMu itself — do NOT wrap it in another RLock:
	// Go's RWMutex deadlocks on a recursive read lock once a writer is queued.
	t, exists := pickTunnel(sub)
	if !exists {
		serveErrorPage(w, "notunnel.html")
		return
	}

	// In-flight count drives least-connections balancing across replicas.
	t.inflight.Add(1)
	defer t.inflight.Add(-1)

	if !rl.Allow(sub) {
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return
	}

	// Baseline WAF for tunnel-mode custom domains: an attached domain is public
	// hosting, so we always block obvious attacks (SQLi / XSS / traversal) even
	// if the owner turned the firewall off on their subdomain.
	if dm.IsCustom(hostOnly) {
		if cat, bad := firewall.Classify(r.URL.Path, r.URL.RawQuery, r.Header.Get("User-Agent")); bad {
			w.Header().Set("X-Sixter-Firewall", "blocked")
			http.Error(w, "Blocked by Sixter firewall: "+cat, http.StatusForbidden)
			return
		}
	}

	// L7 firewall — inspect before the request ever reaches the user's machine.
	// The body is buffered (bounded) and restored, so POSTed payloads — where
	// most real SQLi/XSS/RCE actually lives — are inspected too.
	body, truncated := peekBody(r, 128*1024)
	d := fw.For(sub).InspectFull(firewall.Request{
		IP: extractIP(r), Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
		UA: r.Header.Get("User-Agent"), Country: r.Header.Get("CF-IPCountry"),
		Body: body, Truncated: truncated,
		Headers: map[string]string{
			"referer":         r.Header.Get("Referer"),
			"cookie":          r.Header.Get("Cookie"),
			"x-forwarded-for": r.Header.Get("X-Forwarded-For"),
			"content-type":    r.Header.Get("Content-Type"),
			"origin":          r.Header.Get("Origin"),
		},
	})
	if d.Blocked {
		w.Header().Set("X-Sixter-Firewall", "blocked")
		http.Error(w, "Blocked by Sixter firewall: "+d.Reason, d.Status)
		return
	}

	t.reqCount.Add(1)

	if isWebSocketRequest(r) {
		handleWebSocketProxy(w, r, t, sub, hostOnly)
		return
	}
	handleHTTPProxy(w, r, t, sub, hostOnly)
}

func handleHTTPProxy(w http.ResponseWriter, r *http.Request, t *Tunnel, sub string, hostOnly string) {
	body, _ := io.ReadAll(r.Body)

	// Try replicas in turn. A failure here means the app never SAW the request
	// — the tunnel write failed, or the client couldn't reach the local port —
	// so re-sending is safe even for POST, and turns a 502 into a served
	// request whenever any healthy replica remains.
	tried := map[*Tunnel]bool{}
	cur := t
	extra := []*Tunnel{}
	defer func() {
		for _, e := range extra {
			e.inflight.Add(-1)
		}
	}()

	for {
		tried[cur] = true
		resp, timedOut, err := forwardOnce(cur, r, body, hostOnly)

		if timedOut {
			// The app may well have processed this; retrying could duplicate a
			// side effect. Report it instead.
			http.Error(w, "Gateway Timeout", http.StatusGatewayTimeout)
			return
		}
		if err == nil && resp.Status != http.StatusBadGateway {
			cur.recordSuccess()
			for k, v := range resp.Headers {
				lk := strings.ToLower(k)
				if lk == "content-length" || lk == "connection" || lk == "transfer-encoding" {
					continue
				}
				w.Header()[k] = v
			}
			w.WriteHeader(resp.Status)
			_, _ = w.Write(resp.Body)
			return
		}

		cur.recordFailure()
		if err != nil {
			log.Printf("[%s] replica failed (%v) — trying another", sub, err)
		}

		next := pickExcluding(sub, tried)
		if next == nil {
			break // no replica left to try
		}
		next.inflight.Add(1)
		extra = append(extra, next)
		cur = next
	}

	serveErrorPage(w, "badgateway.html")
}

// forwardOnce sends the request down ONE tunnel and waits for its reply.
// timedOut is reported separately because it is the one failure that must not
// be retried: the app may already have acted on the request.
func forwardOnce(t *Tunnel, r *http.Request, body []byte, hostOnly string) (
	resp tunnel.HTTPResponse, timedOut bool, err error) {

	requestID := generateID()
	respChan := make(chan []byte, 1)

	t.mu.Lock()
	t.requests[requestID] = respChan
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.requests, requestID)
		t.mu.Unlock()
	}()

	data, _ := json.Marshal(tunnel.Message{
		Type:      "http_request",
		RequestID: requestID,
		Data: tunnel.HTTPRequest{
			Method:  r.Method,
			Path:    r.URL.Path,
			Query:   r.URL.RawQuery,
			Headers: r.Header.Clone(),
			Body:    body,
			Host:    hostOnly,
		},
	})

	t.writeMu.Lock()
	werr := t.conn.WriteMessage(websocket.BinaryMessage, data)
	t.writeMu.Unlock()
	if werr != nil {
		return resp, false, werr
	}

	select {
	case respBytes := <-respChan:
		var tmsg tunnel.Message
		if e := json.Unmarshal(respBytes, &tmsg); e != nil {
			return resp, false, e
		}
		respData, _ := json.Marshal(tmsg.Data)
		if e := json.Unmarshal(respData, &resp); e != nil {
			return resp, false, e
		}
		return resp, false, nil

	case <-time.After(5 * time.Minute):
		return resp, true, nil
	}
}

func handleWebSocketProxy(w http.ResponseWriter, r *http.Request, t *Tunnel, sub string, hostOnly string) {
	requestID := generateID()
	wsChan := make(chan tunnel.WSMessageData, 512)

	t.mu.Lock()
	t.wsChans[requestID] = wsChan
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		ch := t.wsChans[requestID]
		if ch != nil {
			close(ch)
			delete(t.wsChans, requestID)
		}
		t.mu.Unlock()
	}()

	msg := tunnel.Message{
		Type:      "ws_upgrade",
		RequestID: requestID,
		Data: tunnel.WSUpgrade{
			Path:    r.URL.Path,
			Query:   r.URL.RawQuery,
			Headers: r.Header.Clone(),
			Host:    hostOnly,
		},
	}

	data, _ := json.Marshal(msg)

	t.writeMu.Lock()
	err := t.conn.WriteMessage(websocket.BinaryMessage, data)
	t.writeMu.Unlock()

	if err != nil {
		http.Error(w, "WebSocket proxy failed", http.StatusBadGateway)
		return
	}

	time.Sleep(100 * time.Millisecond)

	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		sendWSClose(t, requestID, "browser upgrade failed")
		return
	}
	defer clientConn.Close()

	done := make(chan struct{})

	// Browser → Tunnel → Client
	go func() {
		defer close(done)
		for {
			mt, md, err := clientConn.ReadMessage()
			if err != nil {
				sendWSClose(t, requestID, "browser closed")
				return
			}

			out := tunnel.Message{
				Type:      "ws_message",
				RequestID: requestID,
				Data: tunnel.WSMessageData{
					MessageType: mt,
					Data:        md,
				},
			}
			b, _ := json.Marshal(out)

			t.writeMu.Lock()
			err = t.conn.WriteMessage(websocket.BinaryMessage, b)
			t.writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}()

	// Client → Tunnel → Browser
	for {
		select {
		case <-done:
			return
		case ws, ok := <-wsChan:
			if !ok {
				return
			}
			if err := clientConn.WriteMessage(ws.MessageType, ws.Data); err != nil {
				sendWSClose(t, requestID, "write to browser failed")
				return
			}
		}
	}
}

/* ─────────── status API ─────────── */

type tunnelStatus struct {
	Subdomain   string `json:"subdomain"`
	ConnectedAt string `json:"connected_at"`
	UptimeSecs  int64  `json:"uptime_secs"`
	Requests    int64  `json:"requests"`
}

type statusResponse struct {
	ServerUptime string         `json:"server_uptime"`
	ActiveCount  int            `json:"active_count"`
	Tunnels      []tunnelStatus `json:"tunnels"`
	Goroutines   int            `json:"goroutines"`
	HeapMB       float64        `json:"heap_mb"`
	OS           string         `json:"os"`
	Arch         string         `json:"arch"`
}

func apiStatus(w http.ResponseWriter, r *http.Request) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	tunnelsMu.RLock()
	list := make([]tunnelStatus, 0, len(tunnels))
	for sub, t := range tunnels {
		list = append(list, tunnelStatus{
			Subdomain:   sub,
			ConnectedAt: t.connectedAt.UTC().Format(time.RFC3339),
			UptimeSecs:  int64(time.Since(t.connectedAt).Seconds()),
			Requests:    t.reqCount.Load(),
		})
	}
	tunnelsMu.RUnlock()

	resp := statusResponse{
		ServerUptime: time.Since(startedAt).Round(time.Second).String(),
		ActiveCount:  len(list),
		Tunnels:      list,
		Goroutines:   runtime.NumGoroutine(),
		HeapMB:       float64(ms.HeapAlloc) / (1024 * 1024),
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_ = json.NewEncoder(w).Encode(resp)
}

/* ─────────── helpers ─────────── */

func sendWSClose(t *Tunnel, requestID, reason string) {
	msg := tunnel.Message{
		Type:      "ws_close",
		RequestID: requestID,
		Data:      tunnel.WSClose{Reason: reason},
	}
	b, _ := json.Marshal(msg)
	t.writeMu.Lock()
	_ = t.conn.WriteMessage(websocket.BinaryMessage, b)
	t.writeMu.Unlock()
}

func isWebSocketRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

func hostWithoutPort(h string) string {
	host, _, _ := net.SplitHostPort(h)
	if host == "" {
		return h
	}
	return host
}

func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b) + fmt.Sprintf("%d", time.Now().UnixNano())
}

func extractIP(r *http.Request) string {
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return strings.Split(ip, ",")[0]
	}
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func logError(msg, sub, ip string) {
	log.Println("\033[31m" + msg + sub + " | " + ip + "\033[0m")
}

func logInfo(msg, sub, ip string) {
	log.Println("\033[32m" + msg + sub + " | " + ip + "\033[0m")
}
