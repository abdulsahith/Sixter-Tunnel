package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

/*
Service addressing — one domain, many machines.

Scaling must ADD CAPACITY to the address a project already serves, not create a
second address. That's the Kubernetes Service model: a stable name in front of
N interchangeable endpoints.

	           yourdomain.com          ← the address never changes
	                 │
	         gateway replica pool      ← least-connections balancing
	          ┌──────┴──────┐
	     server 1        node 2        ← both publish the SAME service subdomain
	    (this box)      (fleet)

For that to work, THIS server must also be a pool member — otherwise scaling
would move traffic off it rather than sharing with it. So when a project is
scaled we publish its local port through a tunnel on the service subdomain,
exactly as a fleet node does, and point the project's domain at that service
address instead of at this server directly.
*/

type serviceCfg struct {
	Subdomain  string `json:"subdomain"`   // stable service address
	RouteToken string `json:"route_token"` // credential for the reverse tunnel
	Port       int    `json:"port"`        // local port this server's copy listens on
}

var (
	svcMu   sync.RWMutex
	svcCfgs = map[string]serviceCfg{}    // project → service
	svcCmds = map[string]*exec.Cmd{}     // project → local publisher process
)

func svcFile() string { return filepath.Join(dataDir, ".sixter-services.json") }

func loadServices() {
	b, err := os.ReadFile(svcFile())
	if err != nil {
		return
	}
	var m map[string]serviceCfg
	if json.Unmarshal(b, &m) == nil && m != nil {
		svcMu.Lock()
		svcCfgs = m
		svcMu.Unlock()
		// Re-publish every scaled project so a restart doesn't silently drop
		// this server out of its own load-balancing pool.
		for project := range m {
			if err := publishService(project); err != nil {
				log.Printf("service: %s not re-published: %v", project, err)
			}
		}
	}
}

func saveServicesLocked() {
	b, _ := json.MarshalIndent(svcCfgs, "", "  ")
	_ = atomicWriteFile(svcFile(), b, 0600) // contains route tokens
}

func serviceFor(project string) (serviceCfg, bool) {
	svcMu.RLock()
	defer svcMu.RUnlock()
	c, ok := svcCfgs[project]
	return c, ok
}

// publishService makes THIS server a member of the project's pool by opening a
// reverse tunnel for the service subdomain — the same mechanism fleet nodes
// use, so the gateway sees interchangeable endpoints.
func publishService(project string) error {
	cfg, ok := serviceFor(project)
	if !ok || cfg.Subdomain == "" || cfg.RouteToken == "" || cfg.Port == 0 {
		return fmt.Errorf("service for %s is not fully configured", project)
	}

	// Only a tunnel-mode domain can be balanced. A public domain is terminated
	// by nginx on this box, and nodes dial out with no inbound address, so
	// joining a pool for it would advertise capacity that traffic never reaches.
	if _, mode := projectDomainMode(project); mode != "tunnel" {
		return fmt.Errorf("%s is not in tunnel mode", project)
	}
	unpublishService(project)

	bin, err := exec.LookPath("sixter-client")
	if err != nil {
		return fmt.Errorf("sixter-client not found — this server cannot join the pool")
	}
	ws := strings.Replace(strings.Replace(gatewayBase(), "https://", "wss://", 1), "http://", "ws://", 1)
	// Publish this server's port, NOT the app's. The request then re-enters the
	// normal tunnel-mode path — capture middleware, WAF, request logging — and
	// is proxied to the app from there. Pointing straight at the app port would
	// silently make balanced traffic invisible to the firewall.
	cmd := exec.Command(bin,
		"-subdomain", cfg.Subdomain,
		"-port", strconv.Itoa(localPort),
		"-token", cfg.RouteToken,
		"-server", ws+"/connect",
	)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start the pool tunnel: %w", err)
	}

	svcMu.Lock()
	svcCmds[project] = cmd
	svcMu.Unlock()
	log.Printf("service: %s joined pool %s (via :%d)", project, cfg.Subdomain, localPort)

	go func(p string, c *exec.Cmd) {
		_ = c.Wait()
		svcMu.Lock()
		if svcCmds[p] == c {
			delete(svcCmds, p)
		}
		svcMu.Unlock()
	}(project, cmd)
	return nil
}

func unpublishService(project string) {
	svcMu.Lock()
	if c, ok := svcCmds[project]; ok && c.Process != nil {
		_ = c.Process.Kill()
		delete(svcCmds, project)
	}
	svcMu.Unlock()
}

// POST /api/service/<project> {subdomain, routeToken, port}
//
//	Called by the dashboard right after the first replica is scheduled. It
//	registers the service, publishes this server into the pool, and re-points
//	the project's custom domain at the service address so existing traffic is
//	balanced instead of being pinned to this machine.
//
// DELETE → stop publishing and hand the domain back to this server alone.
func handleService(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	project := strings.TrimPrefix(r.URL.Path, "/api/service/")
	if !isSafeName(project) {
		http.Error(w, "invalid project", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		cfg, _ := serviceFor(project)
		svcMu.RLock()
		publishing := svcCmds[project] != nil
		svcMu.RUnlock()
		dom, mode := projectDomainMode(project)
		writeJSONResp(w, map[string]any{
			"project": project, "subdomain": cfg.Subdomain,
			"port": cfg.Port, "publishing": publishing,
			"domain": dom, "mode": mode,
		})

	case http.MethodPost:
		var body serviceCfg
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Subdomain == "" || body.RouteToken == "" {
			http.Error(w, "subdomain and route_token required", http.StatusBadRequest)
			return
		}
		if body.Port == 0 {
			body.Port = projectPort(project)
		}
		if body.Port == 0 {
			http.Error(w, "this project has no port — nothing to load balance", http.StatusBadRequest)
			return
		}

		svcMu.Lock()
		svcCfgs[project] = body
		saveServicesLocked()
		svcMu.Unlock()

		pubErr := publishService(project)

		// Point the project's domain at the service address — but ONLY if this
		// server actually joined the pool. Re-pointing the domain at a pool
		// we're not in would hand ALL traffic to the other machines; if they're
		// broken, that's a self-inflicted total outage of a working domain.
		routed, warn := "", ""
		d, mode := projectDomainMode(project)
		switch {
		case pubErr != nil:
			warn = "this server could not join the pool (" + pubErr.Error() +
				") — the domain stays on this server until that's fixed"
			log.Printf("service: %s: %s", project, warn)
		case d != "" && mode != "tunnel":
			// Unreachable in practice (publishService already requires tunnel
			// mode), kept for defence in depth.
			warn = "domain " + d + " is in public mode, so it stays on this server. " +
				"Switch it to tunnel mode to balance across machines."
		case d != "":
			if err := attachDomain(d, body.Subdomain); err != nil {
				warn = "could not re-point " + d + ": " + err.Error()
				log.Printf("service: %s", warn)
			} else {
				routed = d
				log.Printf("service: %s now load balanced across the pool", d)
			}
		}
		writeJSONResp(w, map[string]any{
			"ok": true, "subdomain": body.Subdomain, "domainRouted": routed, "warning": warn,
		})

	case http.MethodDelete:
		unpublishService(project)
		svcMu.Lock()
		delete(svcCfgs, project)
		saveServicesLocked()
		svcMu.Unlock()
		// hand the domain back to this server alone
		if d := projectDomain(project); d != "" {
			_ = attachDomain(d, serverName)
		}
		writeJSONResp(w, map[string]any{"ok": true})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// projectDomain returns the custom domain attached to a project ("" if none).
func projectDomain(project string) string {
	d, _ := projectDomainMode(project)
	return d
}

// projectDomainMode also reports the hosting mode, which decides whether
// balancing across machines is even possible: a "public" domain terminates on
// this server's nginx, so it can never reach a NAT'd node. Only a "tunnel"
// domain goes through the gateway, where the replica pool lives.
func projectDomainMode(project string) (string, string) {
	domainsMu.Lock()
	defer domainsMu.Unlock()
	if cfg, ok := domains[project]; ok && cfg != nil {
		return cfg.Domain, cfg.Mode
	}
	return "", ""
}
