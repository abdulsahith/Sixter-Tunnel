package main

// Auto-deploy ("buildpack") engine. On demand it inspects an uploaded project,
// decides how it should run, and produces a deploy plan:
//
//   static site   → nginx serves the folder directly (root + try_files), no process
//   node server   → npm install, run via systemd, nginx proxies to its port
//   node SPA      → npm install + build, nginx serves the built dist statically
//   python app    → pip install, gunicorn/uvicorn via systemd, nginx proxies
//   go app        → go build, run binary via systemd, nginx proxies
//
// Everything is deterministic file detection — no AI, no guessing at runtime.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

/* ═══════════════ deploy plan ═══════════════ */

type DeployKind string

const (
	KindStatic DeployKind = "static" // serve files directly
	KindNode   DeployKind = "node"   // node server, proxied
	KindNodeS  DeployKind = "node-spa"
	KindPython DeployKind = "python"
	KindGo     DeployKind = "go"
	KindUnknown DeployKind = "unknown"
)

// DeployPlan is what we detected + what we'll do.
type DeployPlan struct {
	Kind      DeployKind `json:"kind"`
	Title     string     `json:"title"`     // human label
	ServeDir  string     `json:"serveDir"`  // for static: folder nginx serves
	BuildCmd  string     `json:"buildCmd"`  // shell run before serving (optional)
	RunCmd    string     `json:"runCmd"`    // long-running command (port apps)
	Port      int        `json:"port"`      // port the app listens on (0 = static)
	NeedsProc bool       `json:"needsProc"` // true → systemd service needed
	WorkDir   string     `json:"workDir"`   // resolved project root (may be a subfolder)
	Notes     string     `json:"notes"`
}

/* ═══════════════ detection ═══════════════ */

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func readJSONFile(p string) map[string]any {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

// findStaticRoot returns the folder containing index.html (root, or dist/ or build/).
func findStaticRoot(dir string) (string, bool) {
	for _, sub := range []string{"", "dist", "build", "public", "out"} {
		cand := filepath.Join(dir, sub)
		if exists(filepath.Join(cand, "index.html")) {
			return cand, true
		}
	}
	return "", false
}

// hasMarkers reports whether dir looks like a project root.
func hasMarkers(dir string) bool {
	for _, n := range []string{"index.html", "package.json", "requirements.txt", "go.mod"} {
		if exists(filepath.Join(dir, n)) {
			return true
		}
	}
	if hasPyFiles(dir) {
		return true
	}
	_, ok := findStaticRoot(dir)
	return ok
}

// resolveProjectRoot descends into a single wrapping subfolder. Users often
// upload "myproject/" which contains one folder "myproject-final/" with the
// real files inside — this finds that real root (up to 3 levels deep).
func resolveProjectRoot(dir string) string {
	for i := 0; i < 3; i++ {
		if hasMarkers(dir) {
			return dir
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return dir
		}
		var subdirs []string
		files := 0
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == ".sixter.json" {
				continue // ignore hidden/meta
			}
			if e.IsDir() {
				subdirs = append(subdirs, name)
			} else {
				files++
			}
		}
		if files == 0 && len(subdirs) == 1 {
			dir = filepath.Join(dir, subdirs[0])
			continue
		}
		return dir
	}
	return dir
}

// detectProject resolves the real root then detects the project type.
func detectProject(dir string, port int) DeployPlan {
	root := resolveProjectRoot(dir)
	plan := detectAt(root, port)
	plan.WorkDir = root
	return plan
}

// detectAt inspects a known project root and returns a plan.
func detectAt(dir string, port int) DeployPlan {
	hasPkg := exists(filepath.Join(dir, "package.json"))
	hasReq := exists(filepath.Join(dir, "requirements.txt"))
	hasGoMod := exists(filepath.Join(dir, "go.mod"))
	staticRoot, hasStatic := findStaticRoot(dir)

	// 1. Node project — decide SPA vs server by its package.json
	if hasPkg {
		pkg := readJSONFile(filepath.Join(dir, "package.json"))
		scripts, _ := pkg["scripts"].(map[string]any)
		deps := mergedDeps(pkg)

		// Next.js / Nuxt are servers, not static — handle first
		if hasDep(deps, "next") || hasDep(deps, "nuxt") {
			return DeployPlan{
				Kind: KindNode, Title: "Node.js server (Next/Nuxt)",
				BuildCmd: "npm install && npm run build",
				RunCmd:   "npm run start", Port: port, NeedsProc: true,
				Notes: "Server-rendered Node app — proxied to port " + itoaPort(port),
			}
		}
		_, hasBuild := scripts["build"]
		isSPA := hasBuild && (hasDep(deps, "vite") || hasDep(deps, "react-scripts") ||
			hasDep(deps, "@angular/core") || hasDep(deps, "vue") || hasDep(deps, "react"))
		if isSPA {
			built := filepath.Join(dir, "dist")
			if exists(filepath.Join(dir, "build")) {
				built = filepath.Join(dir, "build")
			}
			return DeployPlan{
				Kind: KindNodeS, Title: "Node SPA (built & served statically)",
				BuildCmd: "npm install && npm run build",
				ServeDir: built, Port: 0, NeedsProc: false,
				Notes: "Single-page app — built, then served as static files.",
			}
		}
		// generic node server (express etc.)
		start := "npm start"
		if s, ok := scripts["start"].(string); ok && s != "" {
			start = "npm start"
		}
		return DeployPlan{
			Kind: KindNode, Title: "Node.js server",
			BuildCmd: "npm install", RunCmd: start, Port: port, NeedsProc: true,
			Notes: "Node server — proxied to port " + itoaPort(port),
		}
	}

	// 2. Python
	if hasReq || hasPyFiles(dir) {
		run, note := pythonRunCmd(dir, port)
		return DeployPlan{
			Kind: KindPython, Title: "Python app",
			BuildCmd: "python3 -m venv .venv && .venv/bin/python -m pip install --upgrade pip && .venv/bin/python -m pip install -r requirements.txt" + gunicornInstall(dir),
			RunCmd:   run, Port: port, NeedsProc: true,
			Notes: note,
		}
	}

	// 3. Go
	if hasGoMod {
		return DeployPlan{
			Kind: KindGo, Title: "Go application",
			BuildCmd: "go build -o app .",
			RunCmd:   "./app", Port: port, NeedsProc: true,
			Notes: "Go binary — proxied to port " + itoaPort(port),
		}
	}

	// 4. Plain static site (index.html with no build system)
	if hasStatic {
		return DeployPlan{
			Kind: KindStatic, Title: "Static site",
			ServeDir: staticRoot, Port: 0, NeedsProc: false,
			Notes: "Static files served directly by nginx — no process needed.",
		}
	}

	return DeployPlan{Kind: KindUnknown, Title: "Unknown project type",
		Notes: "Couldn't detect the type. Add an index.html, package.json, requirements.txt or go.mod."}
}

func mergedDeps(pkg map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"dependencies", "devDependencies"} {
		if d, ok := pkg[k].(map[string]any); ok {
			for name, v := range d {
				out[name] = v
			}
		}
	}
	return out
}

func hasDep(deps map[string]any, name string) bool {
	_, ok := deps[name]
	return ok
}

func hasPyFiles(dir string) bool {
	for _, n := range []string{"app.py", "main.py", "wsgi.py", "manage.py", "server.py"} {
		if exists(filepath.Join(dir, n)) {
			return true
		}
	}
	return false
}

// pythonRunCmd picks gunicorn (Flask/Django wsgi) or uvicorn (FastAPI/ASGI).
func pythonRunCmd(dir string, port int) (string, string) {
	p := itoaPort(port)
	bind := "0.0.0.0:" + p
	// FastAPI / ASGI?
	if exists(filepath.Join(dir, "main.py")) && fileContains(filepath.Join(dir, "main.py"), "FastAPI") {
		return ".venv/bin/python -m uvicorn main:app --host 0.0.0.0 --port " + p,
			"FastAPI app — run with uvicorn, proxied to port " + p
	}
	// Django?
	if exists(filepath.Join(dir, "manage.py")) {
		return ".venv/bin/python -m gunicorn $(basename $(dirname $(find . -name wsgi.py | head -1))).wsgi:application --bind " + bind,
			"Django app — run with gunicorn, proxied to port " + p
	}
	// Flask / generic wsgi (app.py with `app`)
	entry := "app:app"
	if exists(filepath.Join(dir, "wsgi.py")) {
		entry = "wsgi:app"
	}
	return ".venv/bin/python -m gunicorn " + entry + " --bind " + bind,
		"Python WSGI app — run with gunicorn, proxied to port " + p
}

func gunicornInstall(dir string) string {
	// ensure gunicorn/uvicorn present even if not in requirements
	if exists(filepath.Join(dir, "main.py")) && fileContains(filepath.Join(dir, "main.py"), "FastAPI") {
		return " && .venv/bin/python -m pip install uvicorn"
	}
	return " && .venv/bin/python -m pip install gunicorn"
}

func fileContains(path, needle string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(b), needle)
}

func itoaPort(p int) string {
	if p <= 0 {
		return "3000"
	}
	return fmt.Sprintf("%d", p)
}

/* ═══════════════ nginx generation (static + proxy) ═══════════════ */

func nginxStaticConfig(domain, root string) string {
	zone := nginxZone(domain)
	return fmt.Sprintf(`# Generated by Sixter Repo Server [deploy2] — %s
limit_req_zone $binary_remote_addr zone=%s:10m rate=30r/s;

server {
    listen 80;
    server_name %s;

    root %s;
    index index.html;

    access_log %s combined;

    location / {
        limit_req zone=%s burst=60 nodelay;
        limit_req_status 429;
        try_files $uri $uri/ /index.html;
    }
}
`, time.Now().Format(time.RFC3339), zone, domain, root, projectLogPath(domain), zone)
}

/* ═══════════════ systemd unit ═══════════════ */

func systemdUnit(project, workdir, runCmd string, port int) string {
	// EnvironmentFile (leading '-' = optional) injects the developer's saved
	// production env vars; values there override the defaults below.
	return fmt.Sprintf(`# Generated by Sixter — service for project %s
[Unit]
Description=Sixter app: %s
After=network.target

[Service]
Type=simple
WorkingDirectory=%s
Environment=PORT=%d
Environment=NODE_ENV=production
EnvironmentFile=-%s
ExecStart=/bin/sh -lc '%s'
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
`, project, project, workdir, port, envFilePath(project), runCmd)
}

// removeService stops, disables and deletes a project's systemd unit (used on
// rename/delete). Best-effort; safe if no unit exists.
func removeService(project string) {
	// Tear down any AMM container for this project too, so delete/rename
	// never leaves an orphaned container holding the port.
	_ = ammDestroy(project)

	if runtime.GOOS != "linux" {
		return
	}
	if _, err := os.Stat(systemdUnitPath(project)); err != nil {
		return
	}
	if isRoot() {
		_, _ = runShell("/", "systemctl disable --now sixter-"+project, time.Minute)
		_ = os.Remove(systemdUnitPath(project))
		_, _ = runShell("/", "systemctl daemon-reload", time.Minute)
		return
	}
	if helperInstalled() {
		_, _ = sudoHelper("rmservice", project)
	}
}

// restartService restarts a project's systemd service if it exists. Returns true
// if a restart was actually issued (used after env-var changes).
func restartService(project string) bool {
	// AMM-managed projects restart as a fresh container — this is what makes
	// an env-var change take effect for them too.
	if ammEnabled(project) {
		return ammRestart(project) == nil
	}
	if runtime.GOOS != "linux" {
		return false
	}
	if _, err := os.Stat(systemdUnitPath(project)); err != nil {
		return false // no service for this project
	}
	if isRoot() {
		_, err := runShell("/", "systemctl restart sixter-"+project, time.Minute)
		return err == nil
	}
	if helperInstalled() {
		_, err := sudoHelper("restart", project)
		return err == nil
	}
	return false
}

func systemdUnitPath(project string) string {
	return "/etc/systemd/system/sixter-" + project + ".service"
}

/* ═══════════════ runner ═══════════════ */

// runShell runs a build/setup command in the project dir, capturing output.
func runShell(dir, command string, timeout time.Duration) (string, error) {
	return runShellEnv(dir, command, nil, timeout)
}

// runShellEnv runs a command with the project's production environment merged
// in.
//
// Build and release steps need the same config the running app gets. Without
// it a migration reads whatever .env happens to sit in the repo — usually the
// developer's laptop credentials — and either fails or, far worse, succeeds
// against the wrong database.
func runShellEnv(dir, command string, extra map[string]string, timeout time.Duration) (string, error) {
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.Command("cmd.exe", "/C", command)
	} else {
		c = exec.Command("sh", "-lc", command)
	}
	c.Dir = dir
	if len(extra) > 0 {
		c.Env = os.Environ()
		for k, v := range extra {
			c.Env = append(c.Env, k+"="+v)
		}
	}
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = c.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
		return string(out), err
	case <-time.After(timeout):
		if c.Process != nil {
			_ = c.Process.Kill()
		}
		return string(out), fmt.Errorf("build timed out after %s", timeout)
	}
}

// deployProject runs the full plan: build → (systemd for proc apps) → nginx → reload.
// Returns a log of what happened.
func deployProject(project string, plan DeployPlan, domain string) (string, error) {
	// run build + service in the resolved root (may be a nested subfolder)
	projPath := plan.WorkDir
	if projPath == "" {
		projPath = filepath.Join(dataDir, project)
	}
	var log strings.Builder
	logf := func(f string, a ...any) { fmt.Fprintf(&log, f+"\n", a...) }

	logf("Deploying %s as: %s", project, plan.Title)
	logf("working dir: %s", projPath)

	// The project's production config, given to every step — not just the
	// running app. A build or migration that can't see DB_URL is running
	// against the wrong database, or none at all.
	projEnv := loadEnv(project)

	// 1. build step
	if plan.BuildCmd != "" {
		logf("$ %s", plan.BuildCmd)
		out, err := runShellEnv(projPath, plan.BuildCmd, projEnv, 8*time.Minute)
		logf("%s", strings.TrimSpace(out))
		if err != nil {
			return log.String(), fmt.Errorf("build failed: %v", err)
		}
		logf("✓ build complete")
	}

	// 2. release step — runs ONCE, here, before anything serves traffic.
	//
	// This is the phase every database-backed app needs and that nothing else
	// provides: migrations, schema sync, seed data. It must not live in the
	// app's startup, because with N replicas that means N concurrent migrations
	// racing on the same tables — Postgres deadlocks on concurrent DDL. Running
	// it on the repo server, before replicas start, makes it exactly-once.
	//
	// A failure ABORTS the deploy: starting an app against a schema that didn't
	// migrate is how you get 500s on every request instead of a clear error.
	if rel := releaseCmdFor(project); rel != "" {
		logf("$ %s", rel)
		out, err := runShellEnv(projPath, rel, projEnv, 10*time.Minute)
		logf("%s", strings.TrimSpace(out))
		if err != nil {
			return log.String(), fmt.Errorf("release command failed (deploy stopped before starting the app): %v", err)
		}
		logf("✓ release complete")
	}

	// 3. long-running process → AMM container, or systemd (Linux only)
	if plan.NeedsProc {
		if ammEnabled(project) {
			// AMM owns this project: run it in a namespaced, cgroup-limited
			// container instead of a host systemd unit. Any leftover unit is
			// removed first so the two runtimes never both hold the port.
			removeService(project)
			logf("AMM: starting %s in a container (namespaces + cgroups)…", project)
			if err := ammStart(project, projPath, plan.RunCmd, plan.Port); err != nil {
				return log.String(), fmt.Errorf("AMM start: %v", err)
			}
			cfg := ammConfigFor(project)
			limits := "cpu: shared"
			if cfg.CPUPercent > 0 {
				limits = fmt.Sprintf("cpu: %d%%", cfg.CPUPercent)
			}
			if cfg.MemoryMB > 0 {
				limits += fmt.Sprintf(", memory: %d MB", cfg.MemoryMB)
			}
			logf("✓ AMM container running on port %d (%s)", plan.Port, limits)
		} else if runtime.GOOS != "linux" {
			logf("(test mode: would create systemd service sixter-%s on Linux)", project)
		} else {
			unit := systemdUnit(project, projPath, plan.RunCmd, plan.Port)
			// stage the unit in a sixter-owned dir
			_ = os.MkdirAll(servicesDir(), 0755)
			staged := filepath.Join(servicesDir(), "sixter-"+project+".service")
			if err := os.WriteFile(staged, []byte(unit), 0644); err != nil {
				return log.String(), fmt.Errorf("write systemd unit: %v", err)
			}
			if isRoot() {
				_ = os.WriteFile(systemdUnitPath(project), []byte(unit), 0644)
				_, _ = runShell(projPath, "systemctl daemon-reload", time.Minute)
				out, err := runShell(projPath, "systemctl enable --now sixter-"+project, time.Minute)
				logf("%s", strings.TrimSpace(out))
				if err != nil {
					return log.String(), fmt.Errorf("start service: %v", err)
				}
			} else if helperInstalled() {
				out, err := sudoHelper("service", project)
				logf("%s", strings.TrimSpace(out))
				if err != nil {
					return log.String(), fmt.Errorf("start service (sudo helper): %v", err)
				}
			} else {
				logf("⚠ service staged at %s — install the sudo helper or run as root to start it", staged)
			}
			logf("✓ service sixter-%s configured on port %d", project, plan.Port)
		}
	}

	// 4. nginx config (static root or proxy) — only if a domain is set
	if domain != "" {
		var conf string
		if plan.Port == 0 && plan.ServeDir != "" {
			conf = nginxStaticConfig(domain, plan.ServeDir)
		} else {
			conf = nginxConfig(domain, plan.Port)
		}
		path, note, err := writeNginxConf(domain, conf)
		if err != nil {
			return log.String(), fmt.Errorf("nginx: %v", err)
		}
		logf("✓ nginx config → %s", path)
		if note != "" {
			logf("  %s", note)
		}
	} else {
		logf("(no domain set — configure one to expose this project)")
	}

	logf("✓ deploy finished")
	return log.String(), nil
}

/* ═══════════════ API ═══════════════ */

func planPort(r *http.Request) int {
	if v := r.URL.Query().Get("port"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			return p
		}
	}
	return 3000
}

// GET /api/detect/<project>?port=3000 — returns the detected deploy plan
func handleDetect(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/detect/")
	if !isSafeName(name) {
		http.Error(w, "invalid project", http.StatusBadRequest)
		return
	}
	plan := detectProject(filepath.Join(dataDir, name), planPort(r))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(plan)
}

// POST /api/deploy/<project> {port, domain} — detect + build + run + nginx
func handleDeploy(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/deploy/")
	if !isSafeName(name) {
		http.Error(w, "invalid project", http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(filepath.Join(dataDir, name)); err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	var req struct {
		Port   int    `json:"port"`
		Domain string `json:"domain"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Port <= 0 {
		req.Port = 3000
	}

	// fall back to a previously-saved domain if none provided
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	if domain == "" {
		domainsMu.Lock()
		if d, ok := domains[name]; ok {
			domain = d.Domain
		}
		domainsMu.Unlock()
	}

	plan := detectProject(filepath.Join(dataDir, name), req.Port)
	if plan.Kind == KindUnknown {
		writeJSONDeploy(w, map[string]any{"ok": false, "plan": plan, "error": plan.Notes})
		return
	}

	// Persist the domain/port FIRST so the pipeline (and future auto-deploys)
	// pick them up, then run the deploy through the pipeline (build → restart →
	// health check → auto-rollback) in the background.
	if domain != "" {
		dnsOk, domainIP := checkDNS(domain)
		cfg := &DomainCfg{
			Domain: domain, Port: req.Port, DNSOk: dnsOk,
			ServerIP: getPublicIP(), DomainIP: domainIP,
			NginxPath: "/etc/nginx/sites-available/" + domain + ".conf",
			CheckedAt: time.Now(),
		}
		domainsMu.Lock()
		releaseDomainFromOthers(domain, name)
		domains[name] = cfg
		domainsMu.Unlock()
		saveDomains()
		startProjectTail(name, domain)
	} else {
		// no domain — still remember the chosen port for the pipeline
		domainsMu.Lock()
		if d := domains[name]; d != nil {
			d.Port = req.Port
		} else {
			domains[name] = &DomainCfg{Port: req.Port}
		}
		domainsMu.Unlock()
		saveDomains()
	}

	holder := "owner"
	if p := principal(r); p != nil && !p.IsOwner {
		holder = p.Email
	}
	clearPendingPush(name) // a lead is promoting this push → clear the pending flag
	go runPipeline(name, "manual", holder)
	writeJSONDeploy(w, map[string]any{"ok": true, "started": true, "plan": plan})
}

func writeJSONDeploy(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

/* ═══════════════ privileged ops (root or sudo helper) ═══════════════ */

const helperPath = "/usr/local/bin/sixter-helper"

func isRoot() bool { return os.Geteuid() == 0 }

func helperInstalled() bool { _, err := os.Stat(helperPath); return err == nil }

// sudoHelper runs the locked-down root helper via passwordless sudo. It lets the
// non-root sixter user perform exactly the privileged deploy steps (nginx,
// certbot, systemd) without running the whole server as root.
func sudoHelper(args ...string) (string, error) {
	full := append([]string{"-n", helperPath}, args...)
	out, err := exec.Command("sudo", full...).CombinedOutput()
	return string(out), err
}

func servicesDir() string { return filepath.Join(installDir, "services") }
