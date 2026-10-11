package main

// CI/CD pipeline. On every upload (and on manual deploy) the server runs a
// background pipeline: detect → build → restart service → nginx → HEALTH CHECK.
// If the new version fails to come up healthy, it is automatically rolled back
// to the previous version. Live logs stream to the dashboard via SSE; every run
// is recorded in a persisted history. Auto-deploy is on by default, per-project
// toggleable.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	maxHistory    = 20
	healthTimeout = 30 * time.Second
)

// DeployRecord is one entry in a project's deploy history.
type DeployRecord struct {
	ID         string    `json:"id"`
	Trigger    string    `json:"trigger"` // upload | manual | rollback
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	OK         bool      `json:"ok"`
	RolledBack bool      `json:"rolledBack"`
	Phase      string    `json:"phase"` // final phase reached
}

// pipeRun is the live state of a project's current/last pipeline run.
type pipeRun struct {
	mu         sync.Mutex
	running    bool
	phase      string
	log        strings.Builder
	ok         bool
	rolledBack bool
	trigger    string
	holder     string // who started this run (for the "deploy in progress by X" lock)
	startedAt  time.Time
	finishedAt time.Time
}

func (p *pipeRun) appendf(f string, a ...any) {
	p.mu.Lock()
	fmt.Fprintf(&p.log, f+"\n", a...)
	p.mu.Unlock()
}
func (p *pipeRun) setPhase(s string) { p.mu.Lock(); p.phase = s; p.mu.Unlock() }

var (
	pipeMu       sync.Mutex
	pipeRuns     = map[string]*pipeRun{}
	pipeHistory  = map[string][]DeployRecord{}
	pipeAuto     = map[string]bool{} // project → autodeploy (absent = default true)
	pipeAutoSeen = map[string]bool{} // whether we have an explicit setting
)

func pipelineFile() string { return filepath.Join(installDir, "pipeline.json") }

type pipelinePersist struct {
	History map[string][]DeployRecord `json:"history"`
	Auto    map[string]bool           `json:"auto"`
}

func loadPipeline() {
	b, err := os.ReadFile(pipelineFile())
	if err != nil {
		return
	}
	var p pipelinePersist
	if json.Unmarshal(b, &p) != nil {
		return
	}
	pipeMu.Lock()
	defer pipeMu.Unlock()
	if p.History != nil {
		pipeHistory = p.History
	}
	if p.Auto != nil {
		pipeAuto = p.Auto
		for k := range p.Auto {
			pipeAutoSeen[k] = true
		}
	}
}

// savePipeline persists history + toggles. Caller must NOT hold pipeMu.
func savePipeline() {
	pipeMu.Lock()
	out := pipelinePersist{History: pipeHistory, Auto: pipeAuto}
	b, _ := json.MarshalIndent(out, "", "  ")
	pipeMu.Unlock()
	_ = atomicWriteFile(pipelineFile(), b, 0644)
}

func autoDeployEnabled(project string) bool {
	pipeMu.Lock()
	defer pipeMu.Unlock()
	if pipeAutoSeen[project] {
		return pipeAuto[project]
	}
	return true // default on
}

func setAutoDeploy(project string, on bool) {
	pipeMu.Lock()
	pipeAuto[project] = on
	pipeAutoSeen[project] = true
	pipeMu.Unlock()
	savePipeline()
}

func getRun(project string) *pipeRun {
	pipeMu.Lock()
	defer pipeMu.Unlock()
	r := pipeRuns[project]
	if r == nil {
		r = &pipeRun{}
		pipeRuns[project] = r
	}
	return r
}

func addHistory(project string, rec DeployRecord) {
	pipeMu.Lock()
	h := append([]DeployRecord{rec}, pipeHistory[project]...)
	if len(h) > maxHistory {
		h = h[:maxHistory]
	}
	pipeHistory[project] = h
	pipeMu.Unlock()
	savePipeline()
}

// runPipeline executes a full deploy in the background. Safe to call in a goroutine.
func runPipeline(project, trigger, holder string) {
	run := getRun(project)

	run.mu.Lock()
	if run.running {
		run.mu.Unlock()
		return // a run is already in progress; ignore the trigger
	}
	run.running = true
	run.ok = false
	run.rolledBack = false
	run.trigger = trigger
	run.holder = holder
	run.startedAt = time.Now()
	run.finishedAt = time.Time{}
	run.log.Reset()
	run.phase = "starting"
	run.mu.Unlock()

	finalOK := false
	rolledBack := false
	defer func() {
		run.mu.Lock()
		run.running = false
		run.ok = finalOK
		run.rolledBack = rolledBack
		run.finishedAt = time.Now()
		phase := run.phase
		started := run.startedAt
		run.mu.Unlock()
		addHistory(project, DeployRecord{
			ID:         started.Format("20060102-150405"),
			Trigger:    trigger,
			StartedAt:  started,
			FinishedAt: time.Now(),
			OK:         finalOK,
			RolledBack: rolledBack,
			Phase:      phase,
		})
	}()

	projPath := filepath.Join(dataDir, project)

	// saved domain + port
	domainsMu.Lock()
	cfg := domains[project]
	domainsMu.Unlock()
	domain, port := "", 3000
	isTunnel := false
	if cfg != nil {
		domain = cfg.Domain
		if cfg.Port > 0 {
			port = cfg.Port
		}
		isTunnel = cfg.Mode == "tunnel"
	}

	// Tunnel mode has no nginx and no certbot — the Sixter gateway fronts the
	// domain and terminates TLS. We build + run the app exactly the same, but
	// skip the public-server steps and refresh the gateway route instead.
	deployDomain := domain
	if isTunnel {
		deployDomain = "" // tell deployProject not to write an nginx config
	}

	// postDeploy restores the right "front door" after a deploy/rollback rewrote it.
	postDeploy := func() {
		if isTunnel {
			rebuildTunnelRoutes() // pick up any port change
			if domain != "" {
				if _, err := attachTunnelDomain(domain); err != nil {
					run.appendf("⚠ gateway route refresh failed: %v", err)
				} else {
					run.appendf("✓ gateway route for %s refreshed", domain)
				}
			}
		} else {
			reapplyHTTPS(run, project) // restore the 443 block on public servers
		}
	}

	run.setPhase("detecting")
	run.appendf("▶ Pipeline started (%s, %s mode)", trigger, modeLabel(isTunnel))
	plan := detectProject(projPath, port)
	run.appendf("Detected: %s", plan.Title)

	run.setPhase("building")
	dlog, derr := deployProject(project, plan, deployDomain)
	run.appendf("%s", strings.TrimSpace(dlog))

	if derr != nil {
		run.appendf("✗ deploy failed: %v", derr)
		rolledBack = autoRollback(run, project, "deploy failed")
		postDeploy()
		return
	}

	postDeploy()

	// health check (only for long-running processes with a port)
	if plan.NeedsProc && plan.Port > 0 {
		run.setPhase("health check")
		run.appendf("Health-checking http://127.0.0.1:%d/ …", plan.Port)
		if healthCheck(plan.Port, healthTimeout) {
			run.appendf("✓ app is healthy")
			finalOK = true
			run.setPhase("done")
		} else {
			run.appendf("✗ app did not become healthy within %s", healthTimeout)
			rolledBack = autoRollback(run, project, "health check failed")
			postDeploy() // rollback rewrote the conf too
		}
		return
	}

	finalOK = true
	run.setPhase("done")
	run.appendf("✓ pipeline complete")
}

// reapplyHTTPS restores the 443 block after a deploy/rollback rewrote the conf.
// Fires whenever the domain has a certificate (flag OR cert on disk), so a
// second/third redeploy never leaves the site on plain HTTP.
func reapplyHTTPS(run *pipeRun, project string) {
	domainsMu.Lock()
	cfg := domains[project]
	domainsMu.Unlock()
	if cfg == nil || cfg.Domain == "" {
		return
	}
	if !cfg.HTTPS && !hasCert(cfg.Domain) {
		return // this domain has no HTTPS to restore
	}
	run.appendf("Re-applying HTTPS for %s…", cfg.Domain)
	if reapplyCertbot(cfg) {
		run.appendf("✓ HTTPS restored for %s", cfg.Domain)
	} else {
		run.appendf("⚠ could not restore HTTPS — ensure certbot is installed and the server runs as root (or the sudo helper is set up)")
	}
}

// autoRollback restores the most recent previous snapshot and redeploys it.
// Returns true if a rollback was performed.
func autoRollback(run *pipeRun, project, reason string) bool {
	vs := listVersions(project)
	if len(vs) == 0 {
		run.appendf("⚠ %s, but no previous version exists to roll back to.", reason)
		run.setPhase("failed")
		return false
	}
	prev := vs[0].ID
	run.setPhase("rolling back")
	run.appendf("↩ %s — rolling back to previous version %s …", reason, prev)
	rlog, rerr := rollbackProject(project, prev)
	run.appendf("%s", strings.TrimSpace(rlog))
	if rerr != nil {
		run.appendf("✗ rollback also failed: %v", rerr)
		run.setPhase("failed")
		return true
	}
	run.appendf("✓ rolled back to %s — previous version is live again", prev)
	run.setPhase("rolled back")
	return true
}

// healthCheck polls the app's port until it responds (any non-5xx) or times out.
func healthCheck(port int, timeout time.Duration) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			code := resp.StatusCode
			resp.Body.Close()
			if code < 500 {
				return true
			}
		}
		time.Sleep(1500 * time.Millisecond)
	}
	return false
}

/* ─────────── API ─────────── */

func runSnapshot(project string) map[string]any {
	run := getRun(project)
	run.mu.Lock()
	defer run.mu.Unlock()
	pipeMu.Lock()
	hist := pipeHistory[project]
	pipeMu.Unlock()
	return map[string]any{
		"running":     run.running,
		"phase":       run.phase,
		"ok":          run.ok,
		"rolledBack":  run.rolledBack,
		"trigger":     run.trigger,
		"holder":      run.holder, // who's currently deploying (lock indicator)
		"log":         run.log.String(),
		"autoDeploy":  autoDeployEnabled(project),
		"history":     hist,
		"startedAt":   run.startedAt,
		"finishedAt":  run.finishedAt,
	}
}

// GET /api/deploy-status/<project>
func handleDeployStatus(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/deploy-status/")
	if !isSafeName(name) {
		http.Error(w, "invalid project name", http.StatusBadRequest)
		return
	}
	writeJSONResp(w, runSnapshot(name))
}

// GET /api/deploy-stream/<project>?token=…  (SSE live build log)
func handleDeployStream(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/deploy-stream/")
	if !isSafeName(name) {
		http.Error(w, "invalid project name", http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	run := getRun(name)
	sent := 0
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	ctx := r.Context()

	emit := func() bool {
		run.mu.Lock()
		full := run.log.String()
		running := run.running
		phase := run.phase
		run.mu.Unlock()
		if len(full) > sent {
			chunk := full[sent:]
			sent = len(full)
			// SSE: one data line per log line
			for _, line := range strings.Split(strings.TrimRight(chunk, "\n"), "\n") {
				fmt.Fprintf(w, "data: %s\n\n", line)
			}
			flusher.Flush()
		}
		// send phase as a separate event
		fmt.Fprintf(w, "event: phase\ndata: %s\n\n", phase)
		flusher.Flush()
		return running
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			running := emit()
			if !running {
				fmt.Fprintf(w, "event: done\ndata: end\n\n")
				flusher.Flush()
				return
			}
		}
	}
}

// GET  /api/autodeploy/<project>          → {autoDeploy:bool}
// POST /api/autodeploy/<project> {on}     → set
func handleAutoDeploy(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/autodeploy/")
	if !isSafeName(name) {
		http.Error(w, "invalid project name", http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodPost {
		var req struct {
			On bool `json:"on"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		setAutoDeploy(name, req.On)
	}
	writeJSONResp(w, map[string]any{"autoDeploy": autoDeployEnabled(name)})
}
