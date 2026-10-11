package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"sixter.in/tunnel/internal/amm"
)

/*
AMM ↔ Sixter integration.

Per project, the user chooses how the application is executed:

	systemd (default) — today's model: a host process, no isolation
	AMM               — namespaces + cgroups + overlayfs, real limits

The toggle is deliberately per-project and opt-in. Flipping the whole platform
to a new runtime in one step would be reckless; this way a user can move one
project, watch it, and roll back by toggling off.

When AMM is enabled for a project:
  - deploy starts the app through the supervisor instead of writing a
    systemd unit (and removes any unit left from the systemd era);
  - restart/stop go through the supervisor;
  - CPU/memory come from real cgroup counters instead of being unavailable;
  - the app still binds its normal port (host networking), so the existing
    nginx/tunnel routing keeps working untouched.
*/

/* ─────────── per-project runtime preference ─────────── */

type ammConfig struct {
	Enabled    bool `json:"enabled"`
	MemoryMB   int  `json:"memoryMB"`   // 0 = unlimited
	CPUPercent int  `json:"cpuPercent"` // 0 = proportional share (no hard quota)
	MaxPIDs    int  `json:"maxPIDs"`

	// Image is an optional OCI base image ("node:20-slim", "python:3.12-slim").
	// Empty = use this host's system directories, which works but pins the
	// container to this machine. Setting an image makes the container portable
	// and reproducible — required before it can ever run on another node.
	Image string `json:"image"`
}

var (
	ammCfgMu sync.RWMutex
	ammCfgs  = map[string]ammConfig{} // project → config
)

func ammCfgFile() string { return filepath.Join(dataDir, ".sixter-amm.json") }

func loadAMMConfigs() {
	b, err := os.ReadFile(ammCfgFile())
	if err != nil {
		return
	}
	var m map[string]ammConfig
	if json.Unmarshal(b, &m) == nil && m != nil {
		ammCfgMu.Lock()
		ammCfgs = m
		ammCfgMu.Unlock()
	}
}

func saveAMMConfigsLocked() {
	b, _ := json.MarshalIndent(ammCfgs, "", "  ")
	_ = atomicWriteFile(ammCfgFile(), b, 0644)
}

func ammConfigFor(project string) ammConfig {
	ammCfgMu.RLock()
	defer ammCfgMu.RUnlock()
	return ammCfgs[project]
}

// ammEnabled reports whether this project should run under AMM. It returns
// false when the host can't actually provide containers, so a user can't
// enable a tier that would silently degrade to an unisolated process.
func ammEnabled(project string) bool {
	if ammSupervisor == nil || !ammSupervisor.ContainerReady() {
		return false
	}
	return ammConfigFor(project).Enabled
}

func setAMMConfig(project string, cfg ammConfig) {
	ammCfgMu.Lock()
	ammCfgs[project] = cfg
	saveAMMConfigsLocked()
	ammCfgMu.Unlock()
}

/* ─────────── lifecycle through AMM ─────────── */

// ammStart launches (or relaunches) a project under AMM. Called by the deploy
// pipeline in place of writing a systemd unit.
func ammStart(project, workdir, runCmd string, port int) error {
	if ammSupervisor == nil {
		return fmt.Errorf("AMM supervisor not ready")
	}
	cfg := ammConfigFor(project)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Replace any previous instance — a redeploy is a fresh container, which
	// is the whole point: no accumulated state from the last release.
	_ = ammSupervisor.Destroy(ctx, project)

	env := map[string]string{}
	for k, v := range readEnvFile(project) {
		env[k] = v
	}
	env["NODE_ENV"] = "production"

	// With an image, the container's filesystem is the IMAGE — not this host.
	// The deploy plan's command was written for this host, so it references
	// things the image doesn't have: a .venv whose interpreter symlink dangles,
	// a Go binary built here, node_modules for the wrong ABI. Rewrite it into
	// the container form, which recreates its environment from the source tree.
	if cfg.Image != "" {
		plan := detectProject(workdir, port)
		if c := containerCommand(plan, plan.WorkDir, cfg.Image); c != "" {
			if c != runCmd {
				log.Printf("AMM: %s adapted for image %s: %s", project, cfg.Image, c)
			}
			runCmd = c
		}
	}

	// Pulling a base image can take a while on first use.
	if cfg.Image != "" {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel2()
		ctx = ctx2
	}

	_, err := ammSupervisor.Run(ctx, amm.Spec{
		ID:      project,
		Project: project,
		RootFS:  workdir,
		Image:   cfg.Image,
		Command: runCmd,
		Env:     env,
		Port:    port,

		MemoryMB:   cfg.MemoryMB,
		CPUPercent: cfg.CPUPercent,
		MaxPIDs:    cfg.MaxPIDs,

		// Host networking so the app binds its port exactly as before and the
		// existing nginx/tunnel routing needs no changes. Private netns +
		// veth is the next phase.
		HostNetwork: true,

		Tier: amm.TierContainer,
	})
	return err
}

func ammStop(project string) error {
	if ammSupervisor == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return ammSupervisor.Stop(ctx, project, 10*time.Second)
}

func ammDestroy(project string) error {
	if ammSupervisor == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return ammSupervisor.Destroy(ctx, project)
}

// ammRestart relaunches a project already configured for AMM, by re-reading
// its deploy plan. Used by the dashboard's Restart button.
func ammRestart(project string) error {
	projPath := filepath.Join(dataDir, project)
	if _, err := os.Stat(projPath); err != nil {
		return fmt.Errorf("project not found")
	}
	// detectProject resolves the real project root (handles a single wrapper
	// directory) and returns the run command + port.
	plan := detectProject(projPath, projectPort(project))
	workdir := plan.WorkDir
	if workdir == "" {
		workdir = projPath
	}

	runCmd, port := plan.RunCmd, plan.Port
	if !plan.NeedsProc || runCmd == "" {
		// A frontend has no process on this host — nginx or the repo server
		// serves its files. In a container there is no such server, so give it
		// one; otherwise a "running" replica would answer nothing at all.
		if port == 0 {
			port = staticPortFor(project)
		}
		p2 := plan
		p2.Port = port
		runCmd = staticServeCmd(p2, workdir, port)
		if runCmd == "" {
			return fmt.Errorf("this project has nothing to run")
		}
	}
	return ammStart(project, workdir, runCmd, port)
}

// ammLogTail returns the last n lines the container wrote (its stdout/stderr,
// including any amm-init setup failure). This is what turns "exit status 1"
// into an actionable message.
func ammLogTail(project string, n int) string {
	if ammSupervisor == nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(installDir, "amm", project, "output.log"))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// projectPort returns the port this project was configured with, so an AMM
// restart binds the same port the nginx/tunnel route already points at.
func projectPort(project string) int {
	domainsMu.Lock()
	defer domainsMu.Unlock()
	if cfg, ok := domains[project]; ok && cfg != nil && cfg.Port > 0 {
		return cfg.Port
	}
	return 0 // detectProject picks a default
}

// readEnvFile parses a project's saved production env vars (KEY=VALUE lines)
// so an AMM container gets the same environment a systemd unit would have.
func readEnvFile(project string) map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(envFilePath(project))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.IndexByte(line, '='); i > 0 {
			out[strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+1:])
		}
	}
	return out
}

/* ─────────── HTTP API ─────────── */

// GET    /api/amm/<project>            → current config + live status
// POST   /api/amm/<project> {enabled, memoryMB, cpuPercent}
// POST   /api/amm/<project>/restart
func handleAMM(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/amm/")
	parts := strings.SplitN(rest, "/", 2)
	project := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	if !isSafeName(project) {
		http.Error(w, "invalid project", http.StatusBadRequest)
		return
	}

	switch {
	case action == "" && r.Method == http.MethodGet:
		writeJSONResp(w, ammProjectStatus(project))

	case action == "" && r.Method == http.MethodPost:
		var body ammConfig
		_ = json.NewDecoder(r.Body).Decode(&body)

		// Refuse to enable a tier the host can't actually deliver, rather than
		// accepting the toggle and quietly running unisolated.
		if body.Enabled && (ammSupervisor == nil || !ammSupervisor.ContainerReady()) {
			reason := "AMM supervisor not ready"
			if ammSupervisor != nil {
				reason = ammSupervisor.Degraded
			}
			http.Error(w, "this host can't run containers: "+reason, http.StatusBadRequest)
			return
		}

		was := ammConfigFor(project).Enabled
		setAMMConfig(project, body)

		actor := "owner"
		if p := principal(r); p != nil && p.Email != "" {
			actor = p.Email
		}

		switch {
		case body.Enabled && !was:
			// Moving to AMM: drop the systemd unit first so the two runtimes
			// never both hold the port.
			removeService(project)
			if err := ammRestart(project); err != nil {
				logActivity(actor, "AMM enable failed: "+err.Error(), project, 500)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			logActivity(actor, "Enabled AMM runtime", project, 200)

		case !body.Enabled && was:
			// Moving back to systemd: stop the container and redeploy the
			// classic way, so toggling off is a real rollback.
			_ = ammDestroy(project)
			logActivity(actor, "Disabled AMM runtime", project, 200)
			go runPipeline(project, "amm-disable", actor)

		case body.Enabled && was:
			// limits changed → recreate with the new cgroup values
			if err := ammRestart(project); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			logActivity(actor, "Updated AMM limits", project, 200)
		}
		writeJSONResp(w, ammProjectStatus(project))

	case action == "restart" && r.Method == http.MethodPost:
		if !ammEnabled(project) {
			http.Error(w, "AMM is not enabled for this project", http.StatusBadRequest)
			return
		}
		if err := ammRestart(project); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSONResp(w, ammProjectStatus(project))

	default:
		http.NotFound(w, r)
	}
}

// ammProjectStatus is the payload the project page renders.
func ammProjectStatus(project string) map[string]any {
	cfg := ammConfigFor(project)
	cpus := runtime.NumCPU()
	_, memPct, memUsed, memTotal, _ := systemMetrics()
	// Size the project so the developer isn't guessing. A limit chosen for the
	// RUNNING app is the classic footgun: the build peaks several times higher
	// and gets OOM-killed with nothing in the log but "Killed".
	plan := detectProject(filepath.Join(dataDir, project), projectPort(project))
	rec := recommendResources(plan, plan.WorkDir, cfg.Image)

	out := map[string]any{
		"project":    project,
		"enabled":    cfg.Enabled,
		"memoryMB":   cfg.MemoryMB,
		"cpuPercent": cfg.CPUPercent,
		"maxPIDs":    cfg.MaxPIDs,
		"image":      cfg.Image,
		"portable":   cfg.Image != "", // can this container run on another node?

		"recommendedMemoryMB":   rec.MemoryMB,
		"recommendedCPUPercent": rec.CPUPercent,
		"sizingWhy":             rec.Why,
		"suggestedImage":        recommendImage(plan, plan.WorkDir),
		// A limit below the recommendation is the single most common cause of
		// an unexplained kill, so say it before it happens.
		"limitTooLow": cfg.MemoryMB > 0 && cfg.MemoryMB < rec.MemoryMB,
		"hostReady":  ammSupervisor != nil && ammSupervisor.ContainerReady(),

		// Host capacity, so the limit fields mean something concrete:
		// "100 = one core, this machine has N".
		"hostCPUs":        cpus,
		"hostCPUMaxPct":   cpus * 100,
		"hostMemTotal":    formatSize(memTotal),
		"hostMemUsed":     formatSize(memUsed),
		"hostMemPct":      memPct,
		"hostMemTotalMB":  memTotal / (1024 * 1024),
	}
	if ammSupervisor != nil {
		out["hostReason"] = ammSupervisor.Degraded
		if inst, ok := ammSupervisor.Get(project); ok {
			out["state"] = string(inst.State)
			out["pid"] = inst.PID
			out["tier"] = string(inst.Tier)
			out["startedAt"] = inst.StartedAt
			if inst.Err != "" {
				out["error"] = inst.Err
			}
			// "exit status 1" alone is useless. Attach the container's own
			// output so a failure explains itself in the UI.
			if tail := ammLogTail(project, 40); tail != "" {
				out["log"] = tail
			}
			// Real numbers from cgroup counters — not estimates.
			if st, err := ammSupervisor.Stats(project); err == nil {
				out["stats"] = map[string]any{
					"cpuPercent":       st.CPUPercent,
					"memoryBytes":      st.MemoryBytes,
					"memoryHuman":      formatSize(st.MemoryBytes),
					"memoryMax":        st.MemoryMax,
					"pids":             st.PIDs,
					"uptimeSecs":       int64(st.Uptime.Seconds()),
					"throttledPeriods": st.ThrottledPeriods,
					"throttledSecs":    st.ThrottledTime.Seconds(),
				}
			}
		} else {
			out["state"] = "stopped"
		}
	}
	return out
}
