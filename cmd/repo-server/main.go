// Sixter Repo Server — runs on the USER's server.
//
//   - receives project uploads from `sixter repo`
//   - serves the React/Vite dashboard (embedded in this binary)
//   - live traffic stats + packet-style HTTP capture
//   - custom domain → nginx config + DNS verification (green tick)
//   - per-project web terminal
//
// The dashboard frontend lives in dashboard/ (React+Vite). `npm run build`
// produces dist/, which is copied next to this file and embedded via go:embed
// — so users who run sixter repo-server automatically get the full UI.
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/subtle"
	"embed"
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
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"sixter.in/tunnel/internal/amm"
	"sixter.in/tunnel/internal/prompt"
)

//go:embed all:dist
var distFS embed.FS

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

const maxUploadGB = 10

var (
	uploadToken  string
	serverName   string
	certbotEmail string
	localPort    = 8080
	publicIP     atomic.Value // string

	installDir string
	dataDir    string

	// ammSupervisor owns application execution (AMM). Nil until startup
	// finishes; the deploy path falls back to systemd while the container
	// tier is being rolled out.
	ammSupervisor *amm.Supervisor

	startedAt = time.Now()

	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
)

func defaultInstallDir() string {
	if runtime.GOOS == "windows" {
		return `C:\sixter`
	}
	return "/opt/sixter"
}

/* ═══════════════ project meta ═══════════════ */

type ProjectMeta struct {
	Name      string    `json:"name"`
	Files     int       `json:"files"`
	Bytes     int64     `json:"bytes"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func metaPathForProject(projPath string) string {
	return filepath.Join(projPath, ".sixter.json")
}

func writeProjectMeta(projPath string, meta ProjectMeta) error {
	meta.UpdatedAt = time.Now()
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(metaPathForProject(projPath), b, 0644)
}

func readProjectMeta(projPath string) (ProjectMeta, bool) {
	b, err := os.ReadFile(metaPathForProject(projPath))
	if err != nil {
		return ProjectMeta{}, false
	}
	var m ProjectMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return ProjectMeta{}, false
	}
	return m, true
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, fmt.Sprintf(".tmp-%d", time.Now().UnixNano()))
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

/* ═══════════════ domain store ═══════════════ */

type DomainCfg struct {
	Domain    string    `json:"domain"`
	Port      int       `json:"port"`
	Mode      string    `json:"mode"`               // "public" (nginx + public IP, default) or "tunnel" (via Sixter gateway)
	ServeDir  string    `json:"serveDir,omitempty"` // tunnel mode, static site: folder to serve directly (no port)
	DNSOk     bool      `json:"dnsOk"`
	HTTPS     bool      `json:"https"`
	ServerIP  string    `json:"serverIP"`
	DomainIP  string    `json:"domainIP"`
	NginxPath string    `json:"nginxPath"`
	Note      string    `json:"note,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
}

/* ─────────── certbot (automatic HTTPS) ─────────── */

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) == 0 {
		return s
	}
	return strings.TrimSpace(lines[len(lines)-1])
}

// maybeRunCertbot requests a Let's Encrypt certificate in the background
// once DNS is verified. Requires -certbot-email and certbot installed (Linux).
// reapplyCertbot re-installs HTTPS onto a freshly written nginx conf. A deploy
// overwrites sites-available/<domain>.conf with a plain HTTP block, wiping the
// 443 server block certbot added — so after any redeploy of a domain that
// already has HTTPS, we re-run certbot --nginx (idempotent; it reuses the
// existing certificate and just re-applies the config + redirect).
// hasCert reports whether a Let's Encrypt certificate already exists for a domain.
func hasCert(domain string) bool {
	if runtime.GOOS != "linux" || domain == "" {
		return false
	}
	_, err := os.Stat("/etc/letsencrypt/live/" + domain + "/fullchain.pem")
	return err == nil
}

// reapplyCertbot re-installs HTTPS onto a freshly written nginx conf. It fires
// whenever the domain HAS a certificate (the cfg.HTTPS flag OR a cert on disk),
// so a redeploy never drops HTTPS — even if the cert was issued outside Sixter
// or the flag is stale. Reinstalling an existing cert needs no email. Returns
// true on success. Safe to call every deploy (idempotent, --keep-until-expiring).
func reapplyCertbot(cfg *DomainCfg) bool {
	if runtime.GOOS != "linux" || cfg == nil || cfg.Domain == "" {
		return false
	}
	if !cfg.HTTPS && !hasCert(cfg.Domain) {
		return false // no HTTPS to restore
	}
	if _, err := exec.LookPath("certbot"); err != nil {
		return false
	}
	var out string
	var err error
	if isRoot() {
		args := []string{"--nginx", "-d", cfg.Domain,
			"--non-interactive", "--agree-tos", "--redirect", "--keep-until-expiring"}
		if certbotEmail != "" {
			args = append(args, "-m", certbotEmail)
		}
		b, e := exec.Command("certbot", args...).CombinedOutput()
		out, err = string(b), e
	} else {
		out, err = sudoHelper("certbot", cfg.Domain, certbotEmail)
	}
	if err != nil {
		log.Printf("re-apply HTTPS for %s failed: %s", cfg.Domain, lastLine(out))
		return false
	}
	domainsMu.Lock()
	cfg.HTTPS = true
	domainsMu.Unlock()
	saveDomains()
	log.Printf("✓ HTTPS re-applied for %s after deploy", cfg.Domain)
	return true
}

func maybeRunCertbot(cfg *DomainCfg) {
	if runtime.GOOS != "linux" || !cfg.DNSOk || cfg.HTTPS {
		return
	}
	if certbotEmail == "" {
		domainsMu.Lock()
		if cfg.Note == "" {
			cfg.Note = "for automatic HTTPS, restart with -certbot-email you@example.com"
		}
		domainsMu.Unlock()
		return
	}
	if _, err := exec.LookPath("certbot"); err != nil {
		domainsMu.Lock()
		cfg.Note = "certbot not installed — run: apt install certbot python3-certbot-nginx"
		domainsMu.Unlock()
		saveDomains()
		return
	}

	domainsMu.Lock()
	cfg.Note = "requesting HTTPS certificate — certbot running…"
	domainsMu.Unlock()
	saveDomains()

	go func() {
		var out string
		var err error
		if isRoot() {
			b, e := exec.Command("certbot", "--nginx",
				"-d", cfg.Domain,
				"--non-interactive", "--agree-tos", "--redirect",
				"-m", certbotEmail,
			).CombinedOutput()
			out, err = string(b), e
		} else {
			out, err = sudoHelper("certbot", cfg.Domain, certbotEmail)
		}

		domainsMu.Lock()
		if err != nil {
			cfg.Note = "certbot failed: " + lastLine(out)
		} else {
			cfg.HTTPS = true
			cfg.Note = "HTTPS active — certificate from Let's Encrypt (auto-renews)"
			log.Printf("✓ HTTPS enabled for %s", cfg.Domain)
		}
		domainsMu.Unlock()
		saveDomains()
	}()
}

var (
	domainsMu sync.Mutex
	domains   = map[string]*DomainCfg{} // project → cfg
)

func domainsFile() string { return filepath.Join(installDir, "domains.json") }

// releaseDomainFromOthers ensures a domain belongs to exactly ONE project:
// it clears that domain from any other project that currently holds it.
// Caller must hold domainsMu.
func releaseDomainFromOthers(domain, keepProject string) {
	for proj, d := range domains {
		if proj != keepProject && d != nil && strings.EqualFold(d.Domain, domain) {
			delete(domains, proj)
		}
	}
}

func loadDomains() {
	b, err := os.ReadFile(domainsFile())
	if err != nil {
		return
	}
	domainsMu.Lock()
	_ = json.Unmarshal(b, &domains)
	changed := dedupeDomains() // [domaindedupe] self-heal old duplicate entries
	domainsMu.Unlock()
	if changed {
		saveDomains()
	}
}

// dedupeDomains enforces one-domain-belongs-to-one-project across the WHOLE map.
// Historic data (saved before releaseDomainFromOthers existed) could have the
// same domain under several project keys, which made every such project show
// the same domain in the dashboard. Here we keep, for each domain, only the
// project whose entry was checked most recently and drop the stale duplicates.
// Caller must hold domainsMu. Returns true if anything was removed.
func dedupeDomains() bool {
	winner := map[string]string{} // lower(domain) → project to keep
	for proj, d := range domains {
		if d == nil || d.Domain == "" {
			continue
		}
		key := strings.ToLower(d.Domain)
		cur, ok := winner[key]
		if !ok || domains[proj].CheckedAt.After(domains[cur].CheckedAt) {
			winner[key] = proj
		}
	}
	removed := false
	for proj, d := range domains {
		if d == nil || d.Domain == "" {
			continue
		}
		if winner[strings.ToLower(d.Domain)] != proj {
			log.Printf("dedupe: dropping duplicate domain %q from project %q", d.Domain, proj)
			delete(domains, proj)
			removed = true
		}
	}
	return removed
}

func saveDomains() {
	domainsMu.Lock()
	b, _ := json.MarshalIndent(domains, "", "  ")
	domainsMu.Unlock()
	_ = atomicWriteFile(domainsFile(), b, 0644)
}

/* ═══════════════ traffic + packet capture ═══════════════ */

type pktEntry struct {
	ID        int64   `json:"id"`
	Time      string  `json:"time"`
	Src       string  `json:"src"`
	Method    string  `json:"method"`
	Host      string  `json:"host"`
	Path      string  `json:"path"`
	Status    int     `json:"status"`
	RespBytes int64   `json:"respBytes"`
	DurMs     float64 `json:"durMs"`
}

type minuteStat struct {
	T        int64 `json:"t"`
	Requests int64 `json:"requests"`
	Bytes    int64 `json:"bytes"`
}

var (
	capMu    sync.Mutex
	packets  []pktEntry
	pktSeq   int64
	minutes  = map[int64]*minuteStat{}
	topPaths = map[string]int64{}
	topIPs   = map[string]int64{}
)

func clientIPOf(r *http.Request) string {
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return strings.TrimSpace(strings.Split(ip, ",")[0])
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

// paths excluded from capture (the dashboard's own polling — avoids feedback noise)
func captureExcluded(p string) bool {
	switch p {
	case "/api/packets", "/api/traffic", "/api/performance", "/api/projects":
		return true
	}
	return false
}

func recordRequest(r *http.Request, status int, respBytes int64, dur time.Duration) {
	capMu.Lock()
	defer capMu.Unlock()

	pktSeq++
	packets = append(packets, pktEntry{
		ID:        pktSeq,
		Time:      time.Now().Format("15:04:05.000"),
		Src:       clientIPOf(r),
		Method:    r.Method,
		Host:      r.Host,
		Path:      r.URL.Path,
		Status:    status,
		RespBytes: respBytes,
		DurMs:     float64(dur.Microseconds()) / 1000.0,
	})
	if len(packets) > 1000 {
		packets = packets[len(packets)-1000:]
	}

	minKey := time.Now().Unix() / 60
	m, ok := minutes[minKey]
	if !ok {
		m = &minuteStat{T: minKey * 60}
		minutes[minKey] = m
		// prune older than 60 minutes
		cutoff := minKey - 60
		for k := range minutes {
			if k < cutoff {
				delete(minutes, k)
			}
		}
	}
	m.Requests++
	m.Bytes += respBytes

	if len(topPaths) < 500 || topPaths[r.URL.Path] > 0 {
		topPaths[r.URL.Path]++
	}
	ip := clientIPOf(r)
	if len(topIPs) < 500 || topIPs[ip] > 0 {
		topIPs[ip]++
	}
}

/* ═══════════════ main ═══════════════ */

func main() {
	// AMM container init MUST be first. When we start a container we re-exec
	// this same binary with argv[0]="amm-init" inside fresh namespaces; that
	// process is the container's PID 1 and must not touch flags, config, the
	// listener or anything else. It sets up the rootfs and execs the app.
	if amm.IsInit() {
		amm.RunInit() // never returns
	}

	token := flag.String("token", "", "Secret dashboard/upload token")
	server := flag.String("server", "", "Your repo subdomain from `sixter login` (e.g. sahith-repo)")
	port := flag.Int("port", 8080, "Local HTTP port")
	dir := flag.String("dir", defaultInstallDir(), "Install directory")
	pubIP := flag.String("public-ip", "", "Public IP of this server (auto-detected if empty)")
	noTunnel := flag.Bool("no-tunnel", false, "don't start the main sixter tunnel (local testing)")
	certEmail := flag.String("certbot-email", "", "email for Let's Encrypt — enables automatic HTTPS via certbot (Linux)")
	sshPort := flag.Int("ssh-port", 2222, "SSH service port — ssh <project>@server -p <port>, password = token (0 = disabled, Linux only)")
	setup := flag.Bool("setup", false, "interactive setup: create & start the 'sixter' systemd service")
	gw := flag.String("gateway", "", "gateway base URL override for team push verification (local testing, e.g. http://localhost:8008)")
	flag.Parse()
	gatewayOverride = *gw

	// one-command setup wizard
	if *setup {
		runSetup()
		return
	}

	// resolve config: flag → env → interactive prompt (git/ssh style)
	if *token == "" {
		*token = os.Getenv("SIXTER_TOKEN")
	}
	if *server == "" {
		*server = os.Getenv("SIXTER_SERVER")
	}
	if *certEmail == "" {
		*certEmail = os.Getenv("SIXTER_CERTBOT_EMAIL")
	}
	if *server == "" && prompt.IsTTY() {
		*server = prompt.Line("Repo subdomain (e.g. sahith-repo)", "")
	}
	if *token == "" && prompt.IsTTY() {
		if t, err := prompt.Secret("Token: "); err == nil {
			*token = t
		}
	}

	if *token == "" || *server == "" {
		fmt.Println(`Sixter Repo Server

Quick start (creates a managed service):
  sudo sixter repo-server -setup

Or run directly (you'll be prompted for the token):
  sixter repo-server -server "yourname-repo" -certbot-email you@example.com

Then open:  https://yourname-repo.sixter.in`)
		os.Exit(1)
	}

	uploadToken = *token
	serverName = *server
	localPort = *port
	installDir = *dir
	certbotEmail = *certEmail
	dataDir = filepath.Join(installDir, "projects")

	if err := setupInstallation(); err != nil {
		log.Fatal("Setup failed: ", err)
	}
	loadDomains()
	loadPipeline()   // restore deploy history + auto-deploy toggles
	loadDatabases()  // restore created databases
	reconcileNginx() // remove orphaned/typo'd Sixter nginx configs at startup
	go detectPublicIP(*pubIP)

	// AMM: bring up the runtime supervisor. It probes the host and reports
	// which isolation tier is actually available, so we never silently run
	// applications unisolated while implying otherwise.
	ammSupervisor = amm.NewSupervisor(filepath.Join(installDir, "amm"))
	loadAMMConfigs()    // per-project runtime preferences
	loadSourceTokens() // scoped tokens letting nodes fetch project source
	loadServices()     // re-join load-balancing pools for scaled projects
	loadReleaseCmds()  // per-project migration step
	initOverlay()      // join the private overlay so replicas can reach this database

	// Restore tunnel routes FIRST: startProjectTail needs to know which domains
	// we serve ourselves (those have no nginx log to tail — we capture inline).
	rebuildTunnelRoutes()

	// start a log tailer for every public-mode project that has a domain
	domainsMu.Lock()
	for proj, d := range domains {
		if d.Domain != "" {
			startProjectTail(proj, d.Domain)
		}
	}
	domainsMu.Unlock()
	loadTracks()     // restore per-project frontend/backend/shared tracks
	loadWorkspaces() // restore workspaces + per-project teams
	loadActivity()   // restore the team activity timeline
	loadPending()    // restore pending member pushes awaiting deploy
	loadProjWS()     // restore project→workspace bindings from pushes
	loadComponents() // restore per-member component ownership
	loadPRs()        // restore open pull requests (branches awaiting review)

	mux := http.NewServeMux()

	// dashboard SPA (embedded dist)
	mux.HandleFunc("/", serveDashboard)

	// APIs
	mux.HandleFunc("/api/projects", handleProjects)
	mux.HandleFunc("/api/project-stats/", handleProjectStats)
	mux.HandleFunc("/api/projects/", handleProjectDelete)
	mux.HandleFunc("/api/performance", handlePerformance)
	mux.HandleFunc("/api/traffic", handleTraffic)
	mux.HandleFunc("/api/packets", handlePackets)
	mux.HandleFunc("/api/domain", handleDomainSave)
	mux.HandleFunc("/api/domain/check", handleDomainCheck)
	mux.HandleFunc("/api/detect/", handleDetect)
	mux.HandleFunc("/api/deploy/", handleDeploy)
	mux.HandleFunc("/api/env/", handleEnv)
	mux.HandleFunc("/api/versions/", handleVersions)
	mux.HandleFunc("/api/rollback/", handleRollback)
	mux.HandleFunc("/api/deploy-status/", handleDeployStatus)
	mux.HandleFunc("/api/deploy-stream/", handleDeployStream)
	mux.HandleFunc("/api/autodeploy/", handleAutoDeploy)
	mux.HandleFunc("/api/rename/", handleRename)
	mux.HandleFunc("/api/whoami", handleWhoami)
	mux.HandleFunc("/api/track/", handleTrack)
	mux.HandleFunc("/api/activity", handleActivity)
	mux.HandleFunc("/api/presence", handlePresence)
	mux.HandleFunc("/api/workspaces", handleWorkspaces)
	mux.HandleFunc("/api/workspaces/", handleWorkspaceItem)
	mux.HandleFunc("/api/project-members/", handleProjectMembers)
	mux.HandleFunc("/api/member-deploy", handleMemberDeploy)
	mux.HandleFunc("/api/components", handleComponents)
	mux.HandleFunc("/api/prs", handlePRs)
	mux.HandleFunc("/api/pr/diff", handlePRDiff)
	mux.HandleFunc("/api/pr/merge", handlePRMerge)
	mux.HandleFunc("/api/pr/close", handlePRClose)
	mux.HandleFunc("/api/pull", handlePull)
	mux.HandleFunc("/api/branches", handleBranches)
	mux.HandleFunc("/api/amm/", handleAMM)
	mux.HandleFunc("/api/scale/", handleScale)
	mux.HandleFunc("/api/service/", handleService)
	mux.HandleFunc("/api/release/", handleRelease)
	mux.HandleFunc("/api/overlay", handleOverlay)
	mux.HandleFunc("/api/security/", handleSecurity)
	mux.HandleFunc("/api/databases", handleDatabases)
	mux.HandleFunc("/api/databases/install", handleDBInstall)
	mux.HandleFunc("/api/databases/create", handleDBCreate)
	mux.HandleFunc("/api/databases/", handleDBItem)
	mux.HandleFunc("/api/terminal/", handleTerminal)

	// upload endpoint (sixter repo client)
	mux.HandleFunc("/upload", uploadHandler)

	// capture middleware
	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Tunnel-mode hosting: if this request's Host is an attached custom
		// domain, reverse-proxy it to the project's local port instead of
		// serving the dashboard. (Dashboard host falls through to the mux.)
		if isTunnelHost(r.Host) {
			start := time.Now()
			sr := &statusRecorderFull{ResponseWriter: w}
			if tunnelProxy(sr, r) {
				status := sr.status
				if status == 0 {
					status = 200
				}
				took := time.Since(start)
				recordRequest(r, status, sr.bytes, took)
				// Tunnel traffic never reaches nginx, so there's no access log
				// to tail — record it here so the project's Web traffic, Top
				// paths and Live packets work exactly like in public mode.
				recordTunnelRequest(r, status, sr.bytes, took)
				return
			}
		}

		// Security baseline for the dashboard's own responses. Deliberately after
		// the tunnel branch: a hosted user app must never get our headers.
		applySecurityHeaders(w, r)
		// RBAC: gate every /api request by role capability (owner token = full).
		// /api/whoami is open (it reports your own identity); /upload is the CLI
		// push path and stays on its own token check in uploadHandler.
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/whoami" &&
			r.URL.Path != "/api/member-deploy" && r.URL.Path != "/api/pull" &&
			r.URL.Path != "/api/branches" {
			if !rbacGate(w, r) {
				return
			}
		}
		if captureExcluded(r.URL.Path) || strings.HasPrefix(r.URL.Path, "/api/terminal/") {
			mux.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sr := &statusRecorderFull{ResponseWriter: w}
		mux.ServeHTTP(sr, r)
		status := sr.status
		if status == 0 {
			status = 200
		}
		recordRequest(r, status, sr.bytes, time.Since(start))

		// audit log: record successful mutating actions for the team timeline
		if action, ok := auditAction(r); ok && status < 400 {
			actor := "system"
			if p := principal(r); p != nil {
				if p.IsOwner {
					actor = "owner"
				} else {
					actor = p.Email
				}
			}
			logActivity(actor, action, projectFromPath(r.URL.Path), status)
		}
	})

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", localPort),
		Handler:           root,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("Dashboard HTTP server on port %d", localPort)
		if err := srv.ListenAndServe(); err != nil {
			log.Fatal("HTTP server error: ", err)
		}
	}()

	if !*noTunnel {
		if err := startMainTunnel(); err != nil {
			log.Fatal("Failed to start main tunnel: ", err)
		}
	}

	if *sshPort > 0 {
		startSSHServer(*sshPort)
	}

	log.Println("╔════════════════════════════════════════════════════════╗")
	log.Println("║              Sixter Repo Server — Running              ║")
	log.Println("╚════════════════════════════════════════════════════════╝")
	log.Printf("✓ Dashboard: https://%s.sixter.in  (local: http://localhost:%d)", serverName, localPort)
	log.Printf("✓ Projects:  %s", dataDir)
	log.Println("Press Ctrl+C to stop...")

	select {}
}

/* ═══════════════ statusRecorderFull (with Hijacker) ═══════════════ */

type statusRecorderFull struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (sr *statusRecorderFull) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorderFull) Write(b []byte) (int, error) {
	if sr.status == 0 {
		sr.status = 200
	}
	n, err := sr.ResponseWriter.Write(b)
	sr.bytes += int64(n)
	return n, err
}

// Flush passthrough (SSE/streaming)
func (sr *statusRecorderFull) Flush() {
	if f, ok := sr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

/* ═══════════════ installation & tunnel ═══════════════ */

func exeName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func setupInstallation() error {
	log.Println("Setting up Sixter installation...")

	for _, d := range []string{installDir, dataDir, filepath.Join(installDir, "nginx"), logDir()} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("failed to create %s: %v", d, err)
		}
	}

	// install the nginx JSON log_format so per-project access_log works (Linux)
	if runtime.GOOS == "linux" {
		dst := "/etc/nginx/conf.d/sixter_log_format.conf"
		if _, err := os.Stat("/etc/nginx/conf.d"); err == nil {
			_ = os.WriteFile(dst, []byte(nginxLogFormat), 0644)
		}
	}

	// copy sibling binaries into installDir, but NEVER overwrite an existing
	// one — otherwise a freshly-deployed binary gets clobbered on restart by a
	// stale copy sitting next to the reposerver. Only fill in missing binaries.
	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)
	binaries := []string{exeName("sixter"), exeName("sixter-client"), exeName("sixter-repo"), exeName("sixter-reposerver")}
	if !strings.EqualFold(exeDir, installDir) {
		for _, bin := range binaries {
			src := filepath.Join(exeDir, bin)
			dst := filepath.Join(installDir, bin)
			if _, err := os.Stat(dst); err == nil {
				continue // already present — leave the deployed version alone
			}
			if _, err := os.Stat(src); err == nil {
				_ = copyFile(src, dst)
			}
		}
	}

	// extract the embedded dashboard to disk (visibility/debugging)
	dashDir := filepath.Join(installDir, "dashboard")
	_ = extractDist(dashDir)

	log.Printf("✓ Install dir: %s", installDir)
	return nil
}

func extractDist(target string) error {
	return fs.WalkDir(distFS, "dist", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, "dist")
		rel = strings.TrimPrefix(rel, "/")
		out := filepath.Join(target, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(out, 0755)
		}
		b, err := distFS.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0644)
	})
}

func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}

func startMainTunnel() error {
	log.Printf("Starting main tunnel to %s.sixter.in...", serverName)

	clientExe := filepath.Join(installDir, exeName("sixter-client"))
	if _, err := os.Stat(clientExe); err != nil {
		return fmt.Errorf("%s not found in %s", exeName("sixter-client"), installDir)
	}

	// subdomain/token/server come from ~/.sixter/config.yaml automatically;
	// we pass the subdomain explicitly so it uses the repo one.
	cmd := exec.Command(clientExe,
		"-subdomain", serverName,
		"-port", fmt.Sprintf("%d", localPort),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start tunnel: %v", err)
	}

	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("Main tunnel exited: %v", err)
			os.Exit(1)
		}
	}()

	time.Sleep(2 * time.Second)
	log.Printf("✓ Main tunnel started")
	return nil
}

/* ═══════════════ auth ═══════════════ */

// requireAuth accepts EITHER the owner token (CLI + owner) OR a valid SSO member
// token. Capability enforcement happens centrally in the root middleware; this
// just gates "is this a known caller at all".
func requireAuth(w http.ResponseWriter, r *http.Request) bool {
	if principal(r) != nil {
		return true
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}

/* ═══════════════ dashboard (embedded SPA) ═══════════════ */

func serveDashboard(w http.ResponseWriter, r *http.Request) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		http.Error(w, "dashboard not built", http.StatusInternalServerError)
		return
	}

	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		p = "index.html"
	}

	// hashed assets (index-abc123.js/.css) are immutable → cache them hard;
	// index.html must NEVER be cached or browsers keep loading the old bundle.
	if f, err := sub.Open(p); err == nil {
		f.Close()
		if strings.HasSuffix(p, ".html") {
			w.Header().Set("Cache-Control", "no-store, must-revalidate")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.FileServerFS(sub).ServeHTTP(w, r)
		return
	}

	// SPA fallback → index.html (never cache)
	b, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		http.Error(w, "dashboard not built — run npm run build in dashboard/", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	_, _ = w.Write(b)
}

/* ═══════════════ projects API ═══════════════ */

type Project struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Files     int    `json:"files"`
	Size      string `json:"size"`
	SizeBytes int64  `json:"sizeBytes"`
	Domain    string `json:"domain,omitempty"`
	Port      int    `json:"port,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Track     string `json:"track,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Pending   bool   `json:"pending,omitempty"`
	PendingBy string `json:"pendingBy,omitempty"`
	DNSOk     bool   `json:"dnsOk"`
	HTTPS     bool   `json:"https"`
	ServerIP  string `json:"serverIP,omitempty"`
	DomainIP  string `json:"domainIP,omitempty"`
	NginxPath string `json:"nginxPath,omitempty"`
	Note      string `json:"note,omitempty"`
}

func handleProjects(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}

	projects := []Project{}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		http.Error(w, "failed to read data dir", http.StatusInternalServerError)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		projPath := filepath.Join(dataDir, entry.Name())

		fileCount := 0
		totalBytes := int64(0)
		if m, ok := readProjectMeta(projPath); ok {
			fileCount = m.Files
			totalBytes = m.Bytes
		}

		proj := Project{
			Name:      entry.Name(),
			Path:      projPath,
			Files:     fileCount,
			Size:      formatSize(totalBytes),
			SizeBytes: totalBytes,
		}

		domainsMu.Lock()
		if d, ok := domains[entry.Name()]; ok {
			proj.Domain = d.Domain
			proj.Port = d.Port
			proj.Mode = d.Mode
			proj.DNSOk = d.DNSOk
			proj.HTTPS = d.HTTPS
			proj.ServerIP = d.ServerIP
			proj.DomainIP = d.DomainIP
			proj.NginxPath = d.NginxPath
			proj.Note = d.Note
		}
		domainsMu.Unlock()
		proj.Track = projectTrack(entry.Name())
		// projectWorkspace returns the local workspace ID — resolve it to the
		// display name so the sidebar shows "customers", not "ws_762a4494482c".
		proj.Workspace = workspaceName(projectWorkspace(entry.Name()))
		if pw := getProjWorkspace(entry.Name()); pw != "" {
			proj.Workspace = pw // gateway workspace name (recorded at push)
		}
		if pp, ok := getPendingPush(entry.Name()); ok {
			proj.Pending = true
			proj.PendingBy = pp.Author
		}

		projects = append(projects, proj)
	}

	// Scope for SSO members. Workspace members see only projects in their
	// workspaces, on their track. Legacy members fall back to role+track.
	// Owner/token sees everything.
	if p := principal(r); p != nil && !p.IsOwner {
		scoped := projects[:0]
		for _, pr := range projects {
			var role string
			var ok bool
			if len(p.Workspaces) > 0 {
				role, ok = p.Workspaces[pr.Workspace]
				ok = ok && pr.Workspace != ""
			} else {
				role, ok = p.Role, (p.Role == "frontend" || p.Role == "backend" || p.Role == "viewer" || p.Role == "lead")
			}
			if ok && roleSeesTrack(role, pr.Track) {
				scoped = append(scoped, pr)
			}
		}
		projects = scoped
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"projects": projects})
}

func handleProjectDelete(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	if r.Method != http.MethodDelete {
		http.Error(w, "DELETE only", http.StatusMethodNotAllowed)
		return
	}

	projectName := strings.TrimPrefix(r.URL.Path, "/api/projects/")
	if !isSafeName(projectName) {
		http.Error(w, "invalid project name", http.StatusBadRequest)
		return
	}

	// Deliberate second confirmation. The request is already authenticated, so
	// this isn't about proving identity — it guards against deleting the wrong
	// project by reflex. The caller must echo back the exact project name.
	// (`password` is still accepted so older clients and scripts keep working.)
	var body struct {
		Confirm  string `json:"confirm"`
		Password string `json:"password"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	confirmed := subtle.ConstantTimeCompare([]byte(body.Confirm), []byte(projectName)) == 1
	if !confirmed && body.Password != "" {
		confirmed = subtle.ConstantTimeCompare([]byte(body.Password), []byte(uploadToken)) == 1
	}
	if !confirmed {
		http.Error(w, "type the project name exactly to confirm deletion", http.StatusForbidden)
		return
	}

	// STOP the application before removing its files. Deleting only the
	// directory leaves the old process running and bound to its port — a ghost
	// that keeps serving the deleted app (and blocks the port for whatever
	// project replaces it).
	_ = ammDestroy(projectName)                                                    // AMM container, if any
	_, _ = runShell("/", "systemctl disable --now sixter-"+projectName, time.Minute) // host-process deploys
	_, _ = runShell("/", "systemctl daemon-reload", time.Minute)

	// Withdraw everything that lets OTHER machines run this project: the
	// scoped source tokens and this server's membership in the replica pool.
	// Fleet workloads on the gateway are the user's to remove from the Nodes
	// page, but without a source token a node can no longer fetch the code.
	revokeSourceTokens(projectName)
	setReleaseCmd(projectName, "")
	unpublishService(projectName)
	svcMu.Lock()
	delete(svcCfgs, projectName)
	saveServicesLocked()
	svcMu.Unlock()

	projPath := filepath.Join(dataDir, projectName)
	if err := os.RemoveAll(projPath); err != nil {
		http.Error(w, "failed to delete: "+err.Error(), http.StatusInternalServerError)
		return
	}

	domainsMu.Lock()
	var oldDomain, oldMode string
	if d, ok := domains[projectName]; ok && d != nil {
		oldDomain = d.Domain
		oldMode = d.Mode
	}
	delete(domains, projectName)
	delete(projCaps, projectName)
	domainsMu.Unlock()
	saveDomains()
	if oldMode == "tunnel" {
		if oldDomain != "" {
			_ = detachTunnelDomain(oldDomain) // remove gateway route
		}
		rebuildTunnelRoutes()
	} else {
		removeNginxConf(oldDomain) // drop this project's nginx config too
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// GET /api/project-stats/<name> — per-project traffic + packets (from nginx log)
func handleProjectStats(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/project-stats/")
	if !isSafeName(name) {
		http.Error(w, "invalid project", http.StatusBadRequest)
		return
	}

	domainsMu.Lock()
	d, hasDomain := domains[name]
	domainsMu.Unlock()

	resp := map[string]any{"hasCapture": false}
	if hasDomain && d.Domain != "" {
		// public mode: make sure the nginx tailer is running. Tunnel mode is
		// captured inline on every proxied request, so this is a no-op there.
		startProjectTail(name, d.Domain)
		snap := captureForProject(name).snapshot()
		snap["hasCapture"] = true
		snap["domain"] = d.Domain
		snap["dnsOk"] = d.DNSOk
		snap["https"] = d.HTTPS
		resp = snap
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

/* ═══════════════ upload ═══════════════ */

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	// A team member pushes with an enrollment token (verified via the gateway).
	// Anyone else uses the owner token / SSO. Member pushes are staged, not
	// deployed — a lead promotes them.
	memberPush := false
	pushAuthor := ""
	pushWS := ""
	pushRole := ""
	if enroll := strings.TrimSpace(r.Header.Get("X-Enroll-Token")); enroll != "" {
		email, role, wsName, ok := verifyEnroll(enroll)
		if !ok {
			http.Error(w, "invalid or expired enrollment token", http.StatusUnauthorized)
			return
		}
		if !roleCanPush(role) {
			http.Error(w, "your role ("+role+") can't push code — testers and viewers have read-only access", http.StatusForbidden)
			return
		}
		memberPush = true
		pushAuthor = email
		pushWS = wsName
		pushRole = role
	} else if !requireAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	project := r.URL.Query().Get("project")
	if project == "" {
		project = fmt.Sprintf("project-%d", time.Now().Unix())
	}
	if !isSafeName(project) {
		http.Error(w, "invalid project name", http.StatusBadRequest)
		return
	}

	// Decide the push target. A workspace lead or the component's owner writes
	// straight to main; anyone else — or anyone who names a branch — opens a
	// pull request instead of touching main. Resolved BEFORE any disk write, so
	// a branch push (or a rejected one) can never wipe main or a teammate's work.
	if memberPush {
		branch := strings.TrimSpace(r.URL.Query().Get("branch"))
		// Only a workspace lead writes straight to main; everyone else opens a
		// pull request for review — regardless of whether they own the
		// component. (The repo owner uses the owner token, not an enroll token,
		// so memberPush is false for them and they bypass this entirely.)
		canMain := pushRole == "lead"
		if branch != "" || !canMain {
			if branch == "" {
				branch = defaultBranchName(pushAuthor)
			}
			if !safeSegment(branch) {
				http.Error(w, "invalid branch name", http.StatusBadRequest)
				return
			}
			handleBranchUpload(w, r, project, branch, pushWS, pushAuthor, pushRole)
			return
		}
		// writing straight to main → claim/confirm component ownership
		component := strings.TrimSpace(r.URL.Query().Get("component"))
		if ok, reason := claimOrCheckComponent(project, pushWS, component, pushAuthor, pushRole); !ok {
			http.Error(w, reason, http.StatusForbidden)
			return
		}
	}

	maxBytes := int64(maxUploadGB) * 1024 * 1024 * 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	defer r.Body.Close()

	projPath := filepath.Join(dataDir, project)
	// version control: archive the previous upload before replacing it
	if err := snapshotProject(project); err != nil {
		log.Println("snapshot before upload failed:", err)
	}
	_ = os.RemoveAll(projPath)

	if err := os.MkdirAll(projPath, 0755); err != nil {
		http.Error(w, "failed to create project dir: "+err.Error(), http.StatusInternalServerError)
		return
	}

	start := time.Now()
	files, byteCount, err := untarGzSafeWithStats(r.Body, projPath)
	if err != nil {
		_ = os.RemoveAll(projPath)
		http.Error(w, "extract failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := writeProjectMeta(projPath, ProjectMeta{Name: project, Files: files, Bytes: byteCount}); err != nil {
		log.Println("meta write failed:", err)
	}

	// Member pushes are STAGED, not deployed: mark the project pending + who
	// pushed it, and let a lead promote it. Owner/CI uploads auto-deploy as before.
	autoDeployed := false
	pendingReview := false
	if memberPush {
		pendingReview = true
		setPendingPush(project, pushAuthor)
		setProjWorkspace(project, pushWS)
		logActivity(pushAuthor, "Pushed (pending review)", project, 200)
	} else if autoDeployEnabled(project) {
		autoDeployed = true
		go runPipeline(project, "upload", "ci")
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":            true,
		"project":       project,
		"path":          projPath,
		"files":         files,
		"sizeBytes":     byteCount,
		"sizeHuman":     formatSize(byteCount),
		"took":          time.Since(start).Round(time.Millisecond).String(),
		"autoDeployed":  autoDeployed,
		"pendingReview": pendingReview,
	})
}

func untarGzSafeWithStats(reader io.Reader, dest string) (files int, byteCount int64, err error) {
	gz, err := gzip.NewReader(reader)
	if err != nil {
		return 0, 0, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return files, byteCount, nil
		}
		if err != nil {
			return files, byteCount, err
		}

		name := filepath.Clean(hdr.Name)
		name = filepath.ToSlash(name)

		if name == "." ||
			strings.HasPrefix(name, "/") ||
			strings.HasPrefix(name, "../") ||
			strings.Contains(name, "/../") {
			return files, byteCount, fmt.Errorf("invalid archive path: %s", hdr.Name)
		}

		target, err := safeJoinUnder(dest, filepath.FromSlash(name))
		if err != nil {
			return files, byteCount, err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return files, byteCount, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return files, byteCount, err
			}
			f, err := os.Create(target)
			if err != nil {
				return files, byteCount, err
			}
			n, cpErr := io.Copy(f, tr)
			closeErr := f.Close()
			if cpErr != nil {
				return files, byteCount, cpErr
			}
			if closeErr != nil {
				return files, byteCount, closeErr
			}
			files++
			byteCount += n
		default:
			// ignore other types for safety
		}
	}
}

func safeJoinUnder(base, rel string) (string, error) {
	baseClean := filepath.Clean(base)
	full := filepath.Clean(filepath.Join(baseClean, rel))

	relToBase, err := filepath.Rel(baseClean, full)
	if err != nil {
		return "", err
	}
	if relToBase == "." {
		return full, nil
	}
	if strings.HasPrefix(relToBase, "..") {
		return "", fmt.Errorf("path escapes base dir: %s", rel)
	}
	return full, nil
}

/* ═══════════════ domain / nginx / DNS ═══════════════ */

func detectPublicIP(override string) {
	if override != "" {
		publicIP.Store(override)
		return
	}
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		log.Printf("public IP auto-detect failed: %v (use -public-ip flag)", err)
		publicIP.Store("")
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	ip := strings.TrimSpace(string(b))
	if net.ParseIP(ip) == nil {
		publicIP.Store("")
		return
	}
	publicIP.Store(ip)
	log.Printf("✓ Public IP: %s", ip)
}

func getPublicIP() string {
	v := publicIP.Load()
	if v == nil {
		return ""
	}
	return v.(string)
}

func validDomain(d string) bool {
	if len(d) < 4 || len(d) > 253 || !strings.Contains(d, ".") {
		return false
	}
	for _, ch := range d {
		ok := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '.'
		if !ok {
			return false
		}
	}
	return !strings.HasPrefix(d, "-") && !strings.HasPrefix(d, ".") &&
		!strings.HasSuffix(d, "-") && !strings.HasSuffix(d, ".")
}

// nginxMarker identifies configs generated by this server, so startup
// reconciliation can safely clean up orphans WITHOUT touching configs that
// belong to the tunnel server or were hand-written by the user.
const nginxMarker = "# Generated by Sixter Repo Server"

// nginxZone derives a unique, nginx-safe rate-limit zone name from a domain.
// It must be unique per conf file — every sites-enabled file is included in the
// http{} block, so two files declaring the same zone name would make
// `nginx -t` fail with "duplicate zone".
func nginxZone(domain string) string {
	var b strings.Builder
	for _, ch := range strings.ToLower(domain) {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			b.WriteRune(ch)
		} else {
			b.WriteByte('_')
		}
	}
	return "sxt_" + b.String()
}

func nginxConfig(domain string, port int) string {
	// NOTE: access_log uses the built-in "combined" format (no custom log_format),
	// so the config is always valid even if no extra nginx snippets are installed.
	// A missing custom log_format used to make `nginx -t` fail, which silently
	// left the site disabled → requests fell through to the default server → 404.
	//
	// Rate limiting: a per-domain zone (rate=30 req/s, burst=60) caps abusive
	// clients at the edge, complementing the app-layer WAF on the tunnel side.
	zone := nginxZone(domain)
	return fmt.Sprintf(`%s [deploy2] — %s
limit_req_zone $binary_remote_addr zone=%s:10m rate=30r/s;

server {
    listen 80;
    server_name %s;

    access_log %s combined;

    location / {
        limit_req zone=%s burst=60 nodelay;
        limit_req_status 429;

        proxy_pass http://127.0.0.1:%d;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $http_connection;
    }
}
`, nginxMarker, time.Now().Format(time.RFC3339), zone, domain, projectLogPath(domain), zone, port)
}

// writeNginx writes a proxy config for the given port (used by the domain form).
func writeNginx(domain string, port int) (path string, note string, err error) {
	return writeNginxConf(domain, nginxConfig(domain, port))
}

// writeNginxConf stages the config in a sixter-owned dir, then activates it —
// directly if running as root, otherwise via the locked-down sudo helper.
func writeNginxConf(domain string, conf string) (path string, note string, err error) {
	// always stage to a sixter-writable file first
	staging := filepath.Join(installDir, "nginx", domain+".conf")
	_ = os.MkdirAll(filepath.Dir(staging), 0755)
	if werr := os.WriteFile(staging, []byte(conf), 0644); werr != nil {
		return "", "", werr
	}

	if runtime.GOOS != "linux" {
		return staging, "test mode (" + runtime.GOOS + "): config staged — deploy on Linux for live nginx", nil
	}

	avail := "/etc/nginx/sites-available/" + domain + ".conf"
	enabled := "/etc/nginx/sites-enabled/" + domain + ".conf"

	// Path 1: running as root → do it directly.
	if isRoot() {
		if werr := os.WriteFile(avail, []byte(conf), 0644); werr != nil {
			return staging, "couldn't write /etc/nginx: " + werr.Error(), nil
		}
		_ = os.Remove(enabled)
		_ = os.Symlink(avail, enabled)
		if out, terr := exec.Command("nginx", "-t").CombinedOutput(); terr != nil {
			return avail, "nginx -t failed: " + lastLine(string(out)), nil
		}
		_ = exec.Command("systemctl", "reload", "nginx").Run()
		return avail, "nginx reloaded", nil
	}

	// Path 2: non-root → use the sudo helper (installs from the staged file).
	if helperInstalled() {
		if out, herr := sudoHelper("nginx", domain); herr != nil {
			return staging, "sudo helper failed: " + lastLine(out), nil
		}
		return avail, "nginx reloaded (via sudo helper)", nil
	}

	// Path 3: no root, no helper → leave staged + tell the user how to enable.
	return staging, "config staged — install the sudo helper (see packaging/) or run the server as root to apply automatically", nil
}

// removeNginxConf deletes a single domain's config (both sites-available and the
// sites-enabled symlink) and reloads nginx. Used when a project's domain changes
// or the project is deleted, so stale/typo'd configs don't keep serving.
func removeNginxConf(domain string) {
	if domain == "" || runtime.GOOS != "linux" {
		return
	}
	_ = os.Remove(filepath.Join(installDir, "nginx", domain+".conf"))

	if isRoot() {
		_ = os.Remove("/etc/nginx/sites-enabled/" + domain + ".conf")
		_ = os.Remove("/etc/nginx/sites-available/" + domain + ".conf")
		_ = exec.Command("systemctl", "reload", "nginx").Run()
		return
	}
	if helperInstalled() {
		_, _ = sudoHelper("rmnginx", domain)
	}
}

// reconcileNginx removes orphaned Sixter-generated configs at startup: any
// sites-available/*.conf that WE generated (verified by the marker) whose domain
// is no longer claimed by any project. This self-cleans the typo'd duplicates
// that accumulated before per-project conf cleanup existed. It never touches
// configs lacking our marker (e.g. sixter.in or hand-written sites).
func reconcileNginx() {
	if runtime.GOOS != "linux" || !isRoot() {
		return
	}
	active := map[string]bool{}
	domainsMu.Lock()
	for _, d := range domains {
		if d != nil && d.Domain != "" {
			active[strings.ToLower(d.Domain)] = true
		}
	}
	domainsMu.Unlock()

	entries, err := os.ReadDir("/etc/nginx/sites-available")
	if err != nil {
		return
	}
	removed := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".conf") {
			continue
		}
		avail := "/etc/nginx/sites-available/" + e.Name()
		b, rerr := os.ReadFile(avail)
		if rerr != nil || !strings.Contains(string(b), nginxMarker) {
			continue // not ours → leave it alone
		}
		domain := strings.TrimSuffix(e.Name(), ".conf")
		if active[strings.ToLower(domain)] {
			continue // still in use
		}
		log.Printf("nginx reconcile: removing orphaned config %s", e.Name())
		_ = os.Remove("/etc/nginx/sites-enabled/" + e.Name())
		_ = os.Remove(avail)
		removed = true
	}
	if removed {
		if out, terr := exec.Command("nginx", "-t").CombinedOutput(); terr != nil {
			log.Printf("nginx reconcile: nginx -t after cleanup: %s", lastLine(string(out)))
			return
		}
		_ = exec.Command("systemctl", "reload", "nginx").Run()
	}
}

func checkDNS(domain string) (dnsOk bool, domainIP string) {
	ips, err := net.LookupHost(domain)
	if err != nil || len(ips) == 0 {
		return false, ""
	}
	server := getPublicIP()
	for _, ip := range ips {
		if ip == server && server != "" {
			return true, ip
		}
	}
	return false, ips[0]
}

func handleDomainSave(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Project string `json:"project"`
		Domain  string `json:"domain"`
		Port    int    `json:"port"`
		Mode    string `json:"mode"` // "public" (default) or "tunnel"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	req.Mode = strings.ToLower(strings.TrimSpace(req.Mode))
	if req.Mode != "tunnel" {
		req.Mode = "public"
	}

	if !isSafeName(req.Project) {
		http.Error(w, "invalid project name", http.StatusBadRequest)
		return
	}
	if !validDomain(req.Domain) {
		http.Error(w, "invalid domain name", http.StatusBadRequest)
		return
	}
	if req.Port <= 0 || req.Port > 65535 {
		http.Error(w, "invalid port", http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(filepath.Join(dataDir, req.Project)); os.IsNotExist(err) {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	// ── Tunnel Mode ──────────────────────────────────────────────────────
	// No public IP / DNS / nginx / certbot. We register the domain with the
	// Sixter gateway, which routes it through this server's tunnel; locally we
	// reverse-proxy the domain to the project's port (see tunnelProxy). TLS is
	// terminated at the gateway, so the box can sit behind NAT with no open port.
	if req.Mode == "tunnel" {
		// drop any previous public nginx config for this project's old domain
		domainsMu.Lock()
		if prev, ok := domains[req.Project]; ok && prev != nil && prev.Domain != "" &&
			!strings.EqualFold(prev.Domain, req.Domain) {
			old := prev.Domain
			oldMode := prev.Mode
			domainsMu.Unlock()
			if oldMode == "tunnel" {
				_ = detachTunnelDomain(old)
			} else {
				removeNginxConf(old)
			}
		} else {
			domainsMu.Unlock()
		}
		// switching public→tunnel on the SAME domain: remove the stale public
		// nginx config + cert so the two modes never run side by side.
		removeNginxConf(req.Domain)

		// Static sites have no listening port — we serve their files directly
		// over the tunnel. Backend apps reverse-proxy to their port.
		plan := detectProject(filepath.Join(dataDir, req.Project), req.Port)
		serveDir := ""
		if plan.Port == 0 && plan.ServeDir != "" {
			serveDir = plan.ServeDir
		}

		tid, err := attachTunnelDomain(req.Domain)
		if err != nil {
			http.Error(w, "gateway attach failed: "+err.Error(), http.StatusBadGateway)
			return
		}

		cfg := &DomainCfg{
			Domain:    req.Domain,
			Port:      req.Port,
			Mode:      "tunnel",
			ServeDir:  serveDir,
			HTTPS:     true, // gateway terminates TLS
			DNSOk:     true, // tunnel is the source of truth for "live", not an A-record check
			Note:      "point " + req.Domain + " to " + apexDomain() + " — tunnel id " + tid,
			CheckedAt: time.Now(),
		}
		domainsMu.Lock()
		releaseDomainFromOthers(req.Domain, req.Project)
		domains[req.Project] = cfg
		domainsMu.Unlock()
		saveDomains()
		rebuildTunnelRoutes()
		startProjectTail(req.Project, req.Domain)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"domain": cfg.Domain, "port": cfg.Port, "mode": "tunnel",
			"https": true, "tunnelId": tid, "note": cfg.Note,
		})
		return
	}

	// If this project previously used a DIFFERENT domain (or was in tunnel mode),
	// tear that down so stale configs/routes don't keep serving.
	domainsMu.Lock()
	if prev, ok := domains[req.Project]; ok && prev != nil && prev.Domain != "" &&
		(!strings.EqualFold(prev.Domain, req.Domain) || prev.Mode == "tunnel") {
		old := prev.Domain
		wasTunnel := prev.Mode == "tunnel"
		domainsMu.Unlock()
		if wasTunnel {
			_ = detachTunnelDomain(old)
		} else {
			removeNginxConf(old)
		}
	} else {
		domainsMu.Unlock()
	}

	// Write the RIGHT nginx config for this project: static sites get a root +
	// try_files block; backend apps get a reverse proxy. Previously we always
	// wrote a proxy to the port, which 502'd for static sites (nothing listening).
	projPath := filepath.Join(dataDir, req.Project)
	plan := detectProject(projPath, req.Port)
	var conf string
	if plan.Port == 0 && plan.ServeDir != "" {
		conf = nginxStaticConfig(req.Domain, plan.ServeDir)
	} else {
		conf = nginxConfig(req.Domain, req.Port)
	}
	nginxPath, note, err := writeNginxConf(req.Domain, conf)
	if err != nil {
		http.Error(w, "nginx config failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	dnsOk, domainIP := checkDNS(req.Domain)

	cfg := &DomainCfg{
		Domain:    req.Domain,
		Port:      req.Port,
		Mode:      "public",
		DNSOk:     dnsOk,
		ServerIP:  getPublicIP(),
		DomainIP:  domainIP,
		NginxPath: nginxPath,
		Note:      note,
		CheckedAt: time.Now(),
	}

	domainsMu.Lock()
	releaseDomainFromOthers(req.Domain, req.Project)
	domains[req.Project] = cfg
	domainsMu.Unlock()
	saveDomains()
	rebuildTunnelRoutes() // drop any tunnel route this project may have had

	startProjectTail(req.Project, req.Domain) // begin capturing this project's traffic
	maybeRunCertbot(cfg)

	domainsMu.Lock()
	resp := map[string]any{
		"domain":    cfg.Domain,
		"port":      cfg.Port,
		"mode":      "public",
		"dnsOk":     cfg.DNSOk,
		"https":     cfg.HTTPS,
		"serverIP":  cfg.ServerIP,
		"domainIP":  cfg.DomainIP,
		"nginxPath": cfg.NginxPath,
		"note":      cfg.Note,
	}
	domainsMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func handleDomainCheck(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	project := r.URL.Query().Get("project")

	domainsMu.Lock()
	cfg, ok := domains[project]
	domainsMu.Unlock()

	if !ok {
		http.Error(w, "no domain configured for this project", http.StatusNotFound)
		return
	}

	// Tunnel mode has no public IP / DNS / certbot — routing is at the gateway.
	if cfg.Mode == "tunnel" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"domain": cfg.Domain, "port": cfg.Port, "mode": "tunnel",
			"https": true, "note": cfg.Note,
		})
		return
	}

	dnsOk, domainIP := checkDNS(cfg.Domain)

	domainsMu.Lock()
	cfg.DNSOk = dnsOk
	cfg.DomainIP = domainIP
	cfg.ServerIP = getPublicIP()
	cfg.CheckedAt = time.Now()
	domainsMu.Unlock()
	saveDomains()

	maybeRunCertbot(cfg)

	domainsMu.Lock()
	resp := map[string]any{
		"domain":    cfg.Domain,
		"port":      cfg.Port,
		"mode":      "public",
		"dnsOk":     dnsOk,
		"https":     cfg.HTTPS,
		"serverIP":  cfg.ServerIP,
		"domainIP":  domainIP,
		"nginxPath": cfg.NginxPath,
		"note":      cfg.Note,
	}
	domainsMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

/* ═══════════════ traffic & packets APIs ═══════════════ */

func topN(m map[string]int64, n int) []map[string]any {
	type kv struct {
		K string
		V int64
	}
	list := make([]kv, 0, len(m))
	for k, v := range m {
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].V > list[j].V })
	if len(list) > n {
		list = list[:n]
	}
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		out = append(out, map[string]any{"key": e.K, "count": e.V})
	}
	return out
}

func handleTraffic(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}

	capMu.Lock()
	now := time.Now().Unix() / 60
	series := make([]minuteStat, 0, 60)
	for i := int64(59); i >= 0; i-- {
		key := now - i
		if m, ok := minutes[key]; ok {
			series = append(series, *m)
		} else {
			series = append(series, minuteStat{T: key * 60})
		}
	}
	paths := topN(topPaths, 10)
	ips := topN(topIPs, 10)
	capMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"minutes":  series,
		"topPaths": paths,
		"topIPs":   ips,
	})
}

func handlePackets(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}

	capMu.Lock()
	out := make([]pktEntry, len(packets))
	copy(out, packets)
	capMu.Unlock()

	// newest first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if len(out) > 300 {
		out = out[:300]
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"packets": out})
}

func handlePerformance(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	cpuPct, memPct, memUsed, memTotal, load := systemMetrics()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"uptime":     time.Since(startedAt).Round(time.Second).String(),
		"goroutines": runtime.NumGoroutine(),
		"heap":       formatSize(int64(ms.HeapAlloc)),
		"os":         runtime.GOOS,
		"arch":       runtime.GOARCH,
		"projects":   countProjectDirs(),
		"server":     serverName,
		// host-wide metrics for the sidebar meters (Linux; 0 elsewhere)
		"cpuPct":   cpuPct,
		"memPct":   memPct,
		"memUsed":  formatSize(memUsed),
		"memTotal": formatSize(memTotal),
		"loadAvg":  load,
		"cpus":     runtime.NumCPU(),
		// AMM runtime status — tells the dashboard (and the user) which
		// isolation tier applications on this host actually run under.
		"runtime": ammStatus(),
	})
}

// ammStatus reports the runtime tier so the UI can be honest about isolation.
func ammStatus() map[string]any {
	if ammSupervisor == nil {
		return map[string]any{"tier": "process", "isolated": false, "reason": "starting"}
	}
	if ammSupervisor.ContainerReady() {
		return map[string]any{
			"tier": "container", "isolated": true,
			"detail": "namespaces + cgroup v2 + overlayfs",
			"instances": len(ammSupervisor.List()),
		}
	}
	return map[string]any{
		"tier": "process", "isolated": false,
		"reason":    ammSupervisor.Degraded,
		"instances": len(ammSupervisor.List()),
	}
}

func countProjectDirs() int {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	return n
}

/* ═══════════════ terminal (WebSocket) ═══════════════ */

func handleTerminal(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}

	projectName := strings.TrimPrefix(r.URL.Path, "/api/terminal/")
	if projectName == "" || !isSafeName(projectName) {
		http.Error(w, "invalid project", http.StatusBadRequest)
		return
	}

	projRoot := filepath.Join(dataDir, projectName)
	if _, err := os.Stat(projRoot); os.IsNotExist(err) {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	sess, err := newShellSession(projRoot)
	if err != nil {
		_ = conn.WriteMessage(websocket.BinaryMessage, []byte("failed to start shell: "+err.Error()+"\r\n"))
		return
	}
	defer sess.Close()

	var wsMu sync.Mutex

	// shell output → websocket
	go func() {
		buf := make([]byte, 8192)
		for {
			n, rerr := sess.Read(buf)
			if n > 0 {
				wsMu.Lock()
				werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n])
				wsMu.Unlock()
				if werr != nil {
					return
				}
			}
			if rerr != nil {
				wsMu.Lock()
				_ = conn.WriteMessage(websocket.BinaryMessage, []byte("\r\n[session ended]\r\n"))
				wsMu.Unlock()
				_ = conn.Close()
				return
			}
		}
	}()

	// websocket → shell input (+ resize control messages)
	for {
		mt, msg, rerr := conn.ReadMessage()
		if rerr != nil {
			return
		}
		if mt == websocket.TextMessage {
			var ctl struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if err := json.Unmarshal(msg, &ctl); err == nil && ctl.Type == "resize" && ctl.Cols > 0 && ctl.Rows > 0 {
				sess.Resize(ctl.Cols, ctl.Rows)
			}
			continue
		}
		if _, werr := sess.Write(msg); werr != nil {
			return
		}
	}
}

// shellSession is an interactive shell bound to a project directory.
// Implementations: PTY-backed bash (Linux/macOS), emulated PowerShell
// line shell (Windows). See shell_unix.go / shell_windows.go.
type shellSession interface {
	io.ReadWriteCloser
	Resize(cols, rows int)
}

/* ═══════════════ utils ═══════════════ */

func isSafeName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, ch := range s {
		ok := (ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') ||
			ch == '-' || ch == '_' || ch == '.'
		if !ok {
			return false
		}
	}
	return true
}

func formatSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
