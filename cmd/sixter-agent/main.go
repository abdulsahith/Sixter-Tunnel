// Command sixter-agent runs on every machine in a Sixter fleet.
//
// It does three things:
//
//  1. enrols the machine with the gateway using a fleet join token, exchanging
//     it for per-node credentials (stored locally, mode 0600);
//  2. heartbeats every 10s with capacity and health, keeping the node visible
//     in the console;
//  3. hosts the AMM runtime so workloads can be scheduled here.
//
// It only ever makes OUTBOUND connections. There is nothing to expose, no
// inbound port, and it works behind NAT — which is why the same command works
// unchanged on EC2, GCE, Azure and bare metal.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"sixter.in/tunnel/internal/amm"
	"sixter.in/tunnel/internal/wg"
)

const agentVersion = "1.0"

var (
	gateway   string
	joinToken string
	nodeName  string
	stateDir  = "/var/lib/sixter/agent"
	credFile  = "/etc/sixter/node.json"

	supervisor *amm.Supervisor
	httpc      = &http.Client{Timeout: 20 * time.Second}
)

// creds are the per-node credentials issued at enrolment.
type creds struct {
	NodeID string `json:"node_id"`
	Secret string `json:"secret"`
	Name   string `json:"name"`
}

func main() {
	// The AMM container init re-execs this binary; it must be handled before
	// any other setup, exactly as in the repo-server.
	if amm.IsInit() {
		amm.RunInit() // never returns
	}

	log.SetFlags(log.LstdFlags)
	gateway = strings.TrimRight(env("SIXTER_GATEWAY", "https://sixter.in"), "/")
	joinToken = env("SIXTER_JOIN_TOKEN", "")
	nodeName = env("SIXTER_NODE_NAME", hostname())
	if d := env("SIXTER_STATE_DIR", ""); d != "" {
		stateDir = d
	}

	log.Printf("Sixter agent %s starting — node %q → %s", agentVersion, nodeName, gateway)

	supervisor = amm.NewSupervisor(filepath.Join(stateDir, "amm"))
	if supervisor.ContainerReady() {
		log.Printf("runtime: containers available (namespaces + cgroup v2 + overlayfs)")
	} else {
		log.Printf("runtime: DEGRADED — %s", supervisor.Degraded)
		log.Printf("runtime: workloads would run unisolated on this node")
	}

	initOverlay() // private overlay identity, before the first heartbeat

	c, err := loadOrEnroll()
	if err != nil {
		log.Fatalf("enrolment failed: %v", err)
	}
	log.Printf("enrolled as %s (%s)", c.Name, c.NodeID)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	run(ctx, c)
	log.Printf("agent stopped")
}

/* ─────────── enrolment ─────────── */

// loadOrEnroll reuses existing credentials when present so a restart doesn't
// create a duplicate node, and only spends the join token on first run.
func loadOrEnroll() (*creds, error) {
	if b, err := os.ReadFile(credFile); err == nil {
		var c creds
		if json.Unmarshal(b, &c) == nil && c.NodeID != "" && c.Secret != "" {
			return &c, nil
		}
	}
	if joinToken == "" {
		return nil, fmt.Errorf("no credentials at %s and SIXTER_JOIN_TOKEN is not set", credFile)
	}
	return enroll()
}

func enroll() (*creds, error) {
	isolation, reason := "process", supervisor.Degraded
	if supervisor.ContainerReady() {
		isolation, reason = "container", ""
	}
	_, _, _, memTotal, _ := hostMetrics()

	body, _ := json.Marshal(map[string]interface{}{
		"token": joinToken, "name": nodeName,
		"os": runtime.GOOS, "arch": runtime.GOARCH,
		"cpus": runtime.NumCPU(), "memTotal": memTotal,
		"agent": agentVersion, "isolation": isolation, "isolation_reason": reason,
		// Where this machine can be reached from. Needed to answer the question
		// every stateful deploy hits: can this node reach the database? Without
		// it the platform can only guess, and a wrong guess means an app that
		// starts fine and fails on its first query.
		"private_ips": privateIPs(),
	})

	// The gateway may be briefly unreachable while a fleet boots — retry with
	// backoff rather than dying and leaving the node unenrolled.
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		resp, err := httpc.Post(gateway+"/api/node/enroll", "application/json", bytes.NewReader(body))
		if err != nil {
			lastErr = err
		} else {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var c creds
				if err := json.Unmarshal(raw, &c); err != nil {
					return nil, err
				}
				if err := saveCreds(&c); err != nil {
					log.Printf("warning: could not persist credentials: %v", err)
				}
				return &c, nil
			}
			// A rejected token will never succeed — fail immediately.
			if resp.StatusCode == http.StatusForbidden {
				return nil, fmt.Errorf("%s", strings.TrimSpace(string(raw)))
			}
			lastErr = fmt.Errorf("enroll returned %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		}
		wait := time.Duration(attempt+1) * 3 * time.Second
		log.Printf("enrolment attempt %d failed (%v) — retrying in %s", attempt+1, lastErr, wait)
		time.Sleep(wait)
	}
	return nil, lastErr
}

func saveCreds(c *creds) error {
	if err := os.MkdirAll(filepath.Dir(credFile), 0700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(credFile, b, 0600) // contains this node's secret
}

/* ─────────── heartbeat loop ─────────── */

func run(ctx context.Context, c *creds) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()

	beat(c) // report immediately so the console shows the node at once
	misses, reEnrolls := 0, 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := beat(c); err != nil {
				misses++
				// Credentials rejected → usually the node was removed in the
				// console, so re-enrol. But a gateway-side fault can reject
				// every beat, and re-enrolling each time would hammer it in a
				// hot loop — so back off and say so plainly.
				if strings.Contains(err.Error(), "unknown node") && joinToken != "" {
					reEnrolls++
					if reEnrolls > 3 {
						if reEnrolls%30 == 0 {
							log.Printf("re-enrolment keeps failing (%d attempts) — the gateway is rejecting this node; "+
								"check `journalctl -u sixter-tunnel` for errors", reEnrolls)
						}
						continue // keep beating slowly; don't spin
					}
					log.Printf("node no longer registered — re-enrolling (attempt %d)", reEnrolls)
					_ = os.Remove(credFile)
					if nc, nerr := enroll(); nerr == nil {
						*c = *nc
						misses, reEnrolls = 0, 0
						log.Printf("re-enrolled as %s", nc.NodeID)
						continue
					} else {
						log.Printf("re-enrolment failed: %v", nerr)
					}
				}
				if misses == 1 || misses%30 == 0 {
					log.Printf("heartbeat failed (%d in a row): %v", misses, err)
				}
			} else if misses > 0 {
				log.Printf("heartbeat recovered after %d failures", misses)
				misses, reEnrolls = 0, 0
			}
		}
	}
}

// assignment mirrors the gateway's desired-state record.
type assignment struct {
	ID         string            `json:"id"`
	Project    string            `json:"project"`
	Image      string            `json:"image"`
	Command    string            `json:"command"`
	Port       int               `json:"port"`
	Env        map[string]string `json:"env"`
	// Probe lists host:port endpoints this workload depends on (its database,
	// usually). They are checked BEFORE the app starts: an app launched
	// against an unreachable database starts fine and then fails every
	// request, which looks like a broken deploy rather than a network problem.
	Probe      []string          `json:"probe,omitempty"`
	MemoryMB   int               `json:"memory_mb"`
	CPUPercent int               `json:"cpu_percent"`
	State      string            `json:"state"`

	// Routing: this node has no inbound port, so the app is published by
	// opening a reverse tunnel to the gateway for Subdomain, authenticated
	// with RouteToken. The gateway's existing subdomain routing then carries
	// public traffic through it — no new proxy path was needed.
	Subdomain  string `json:"subdomain"`
	RouteToken string `json:"route_token"`

	// Source: where to fetch the application's code. The image supplies the
	// runtime (node/python/go); this supplies the app itself.
	SourceURL   string `json:"source_url"`
	SourceToken string `json:"source_token"`
}

// fetchSource downloads and extracts a project's tarball into dir. Called
// before the container starts, so /app contains the user's code rather than
// being empty — this is what lets a node run YOUR app, not just a public image.
func fetchSource(a assignment, dir string) error {
	if a.SourceURL == "" {
		return nil
	}
	req, err := http.NewRequest(http.MethodGet, a.SourceURL, nil)
	if err != nil {
		return err
	}
	if a.SourceToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.SourceToken)
	}
	req.Header.Set("User-Agent", "sixter-agent/"+agentVersion)

	cl := &http.Client{Timeout: 5 * time.Minute}
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("fetch source: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("fetch source: %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	// A 200 that isn't a tarball is an interstitial — typically the gateway's
	// "tunnel offline" page while the repo server restarts mid-deploy. Fail
	// with a reason instead of handing HTML to gunzip ("invalid header").
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "text/html") {
		return fmt.Errorf("fetch source: got an HTML page instead of the tarball " +
			"(repo server offline or restarting?) — will retry")
	}

	// Replace wholesale: a redeploy must not leave files from the old release.
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return amm.ExtractTarGz(resp.Body, dir)
}

// report is what we tell the gateway about work it gave us.
type report struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Message string `json:"message"`
}

// pending holds status to send on the next beat.
var pendingReports []report

func beat(c *creds) error {
	cpuPct, memPct, _, _, load := hostMetrics()

	reports := pendingReports
	pendingReports = nil

	body, _ := json.Marshal(map[string]interface{}{
		"node_id": c.NodeID, "secret": c.Secret,
		"cpuPct": cpuPct, "memPct": memPct,
		"instances": len(supervisor.List()), "load": load,
		"report": reports,
		// Overlay identity. Only the PUBLIC key ever leaves this machine — the
		// gateway coordinates addresses and cannot decrypt peer traffic.
		"wg_public_key": string(wgPublic),
		"wg_port":       wg.DefaultPort,
		"wg_reachable":  wgReachable,
	})
	resp, err := httpc.Post(gateway+"/api/node/heartbeat", "application/json", bytes.NewReader(body))
	if err != nil {
		pendingReports = append(reports, pendingReports...) // retry next beat
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		pendingReports = append(reports, pendingReports...)
		return fmt.Errorf("%s", strings.TrimSpace(string(raw)))
	}

	var res struct {
		Assignments []assignment `json:"assignments"`
		Overlay     *struct {
			OverlayIP string    `json:"overlay_ip"`
			PodCIDR   string    `json:"pod_cidr"`
			Peers     []wg.Peer `json:"peers"`
			Reachable bool      `json:"reachable"`
			Warning   string    `json:"warning"`
		} `json:"overlay"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	if res.Overlay != nil {
		applyOverlay(res.Overlay.OverlayIP, res.Overlay.Peers, res.Overlay.Warning)
		setPodNetwork(res.Overlay.PodCIDR)
	}
	reconcile(res.Assignments)
	return nil
}

/* ─────────── reconciliation ─────────── */

// reconcile drives actual state toward desired state.
//
// The gateway can't push (we dial out, possibly from behind NAT), so every
// heartbeat carries the full desired list and we diff it against what's
// actually running:
//
//	in desired, not running   → start
//	running, not in desired   → stop and destroy
//	spec changed              → restart with the new spec
//
// Being a full-state diff rather than an event stream means a missed message,
// an agent restart or a gateway restart all self-heal on the next beat.
func reconcile(desired []assignment) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	want := map[string]assignment{}
	for _, a := range desired {
		want[a.Project] = a
	}

	// stop anything no longer wanted, including its published route
	for _, inst := range supervisor.List() {
		if _, ok := want[inst.ID]; !ok {
			log.Printf("reconcile: %s is no longer assigned — stopping", inst.ID)
			stopRoute(inst.ID)
			releasePodAddress(inst.ID)
			_ = supervisor.Destroy(ctx, inst.ID)
			delete(applied, inst.ID)
		}
	}

	for _, a := range desired {
		cur, running := supervisor.Get(a.Project)
		if running && cur.State == amm.StateRunning && !specChanged(a) {
			// Stable for a minute → forget past crashes so an old bad deploy
			// doesn't slow down restarts forever.
			if crashes[a.Project] > 0 && time.Since(cur.StartedAt) > time.Minute {
				delete(crashes, a.Project)
				delete(nextTry, a.Project)
			}
			continue // already correct
		}
		if running {
			// The app died on its own. Capture WHY before Destroy removes the
			// overlay — output.log lives inside it, so this is the only moment
			// the crash reason exists. Report it so the dashboard's node card
			// shows the actual error instead of an eternal 502.
			if cur.State == amm.StateFailed || cur.State == amm.StateStopped {
				msg := fmt.Sprintf("app exited (code %d)", cur.ExitCode)
				if cur.ExitCode == 137 { // 128+SIGKILL: the OOM killer, in practice
					msg += " — killed for exceeding available memory; raise the " +
						"project's memory limit or add swap on this node"
				}
				if tail := tailFile(cur.LogPath, 700); tail != "" {
					msg += ": " + tail
				}
				log.Printf("reconcile: %s %s", a.Project, msg)
				pendingReports = append(pendingReports, report{a.ID, "failed", msg})

				// Back off a crash loop. Restarting every beat re-downloads the
				// source and re-runs the build just to crash again — and each
				// Destroy erases the log the user needs to debug it.
				crashes[a.Project]++
				wait := time.Duration(10*(1<<min(crashes[a.Project], 5))) * time.Second
				nextTry[a.Project] = time.Now().Add(wait)
			}
			_ = supervisor.Destroy(ctx, a.Project)
		}
		if t, ok := nextTry[a.Project]; ok && time.Now().Before(t) && !specChanged(a) {
			continue // still backing off; a changed spec (new deploy) retries now
		}

		// An image workload NEEDS the container tier. The process fallback
		// would ignore the image and run the command on the bare host — where
		// the runtime (go, node, python) usually doesn't exist — then die
		// instantly while looking "running". Refuse loudly instead.
		if a.Image != "" && !supervisor.ContainerReady() {
			msg := "this node cannot run containers: " + supervisor.Degraded +
				" — image workloads need container isolation (fix the node, then redeploy)"
			if !refusalSent[a.Project] {
				log.Printf("reconcile: %s refused: %s", a.Project, msg)
				pendingReports = append(pendingReports, report{a.ID, "failed", msg})
				refusalSent[a.Project] = true
			}
			continue
		}
		delete(refusalSent, a.Project)

		// Verify dependencies are reachable from THIS machine before starting.
		// The repo server rewrote loopback addresses to something a LAN peer
		// could use, but only this node can prove whether the path exists.
		if bad := unreachable(a.Probe); len(bad) > 0 {
			msg := "cannot reach " + strings.Join(bad, ", ") +
				" from this machine — the database is not on this node's network. " +
				"Put it on a reachable address or connect this node over the private overlay."
			if !refusalSent[a.Project] {
				log.Printf("reconcile: %s blocked: %s", a.Project, msg)
				pendingReports = append(pendingReports, report{a.ID, "failed", msg})
				refusalSent[a.Project] = true
			}
			continue
		}

		appDir := filepath.Join(stateDir, "apps", a.Project)

		// Fetch the application's code before starting. Without this the
		// container has only the base image and /app is empty.
		if a.SourceURL != "" {
			log.Printf("reconcile: fetching source for %s", a.Project)
			if err := fetchSource(a, appDir); err != nil {
				log.Printf("reconcile: %s source failed: %v", a.Project, err)
				pendingReports = append(pendingReports, report{a.ID, "failed", err.Error()})
				continue
			}
		} else {
			_ = os.MkdirAll(appDir, 0755)
		}

		podIP, podNet, podOK := podAddressFor(a.Project)
		log.Printf("reconcile: starting %s (image %s)", a.Project, a.Image)
		_, err := supervisor.Run(ctx, amm.Spec{
			ID:          a.Project,
			Project:     a.Project,
			RootFS:      appDir,
			Image:       a.Image,
			Command:     a.Command,
			Env:         a.Env,
			Port:        a.Port,
			MemoryMB:    a.MemoryMB,
			CPUPercent:  a.CPUPercent,
			// A private network namespace when the pod network is up: the
			// container gets its own address, so replicas no longer collide on
			// a port and a rolling deploy becomes possible. Falls back to host
			// networking when the overlay isn't available, which keeps single
			// -replica deploys working on machines without WireGuard.
			HostNetwork: !podOK,
			PodIP:       podIP,
			PodCIDR:     podNet,
			Tier:        amm.TierContainer,
		})
		if err != nil {
			log.Printf("reconcile: %s failed: %v", a.Project, err)
			pendingReports = append(pendingReports, report{a.ID, "failed", err.Error()})
			continue
		}
		applied[a.Project] = specKey(a)
		startRoute(a) // publish it
		pendingReports = append(pendingReports, report{a.ID, "running", ""})
	}
}

/* ─────────── publishing (reverse tunnel per workload) ─────────── */

// routes tracks the sixter-client process publishing each workload.
var routes = map[string]*exec.Cmd{}

// startRoute publishes a running container to the internet.
//
// We spawn the existing sixter-client binary rather than reimplementing the
// tunnel protocol: it already handles framing, reconnection and WebSocket
// upgrades, and it ships with the agent. Reimplementing that protocol here
// would be a second copy to keep in sync for no benefit.
func startRoute(a assignment) {
	if a.Subdomain == "" || a.RouteToken == "" || a.Port == 0 {
		return // nothing to publish (e.g. a worker with no HTTP port)
	}
	stopRoute(a.Project)

	bin, err := exec.LookPath("sixter-client")
	if err != nil {
		log.Printf("route: sixter-client not found — %s will run but stay unreachable", a.Project)
		return
	}

	ws := strings.Replace(strings.Replace(gateway, "https://", "wss://", 1), "http://", "ws://", 1)
	// With pod networking the app listens on the CONTAINER's address, not the
	// host's — publishing 127.0.0.1 would find nothing there.
	target := "127.0.0.1"
	if ip, _, ok := podAddressFor(a.Project); ok {
		target = ip
	}
	cmd := exec.Command(bin,
		"-subdomain", a.Subdomain,
		"-host", target,
		"-port", strconv.Itoa(a.Port),
		"-token", a.RouteToken,
		"-server", ws+"/connect",
	)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		log.Printf("route: could not publish %s: %v", a.Project, err)
		return
	}
	routes[a.Project] = cmd
	log.Printf("route: %s published at %s", a.Project, a.Subdomain)

	// Reap it, and forget it, so a crashed tunnel is re-established on the
	// next reconcile rather than silently staying down.
	go func(project string, c *exec.Cmd) {
		_ = c.Wait()
		if routes[project] == c {
			delete(routes, project)
			delete(applied, project) // force a restart on the next beat
		}
	}(a.Project, cmd)
}

func stopRoute(project string) {
	if c, ok := routes[project]; ok && c.Process != nil {
		_ = c.Process.Kill()
		delete(routes, project)
	}
}

// applied remembers the spec we last started, so a changed assignment
// (new image, new limits) triggers a restart instead of being ignored.
var applied = map[string]string{}

// Crash-loop bookkeeping: consecutive failures and the earliest next restart.
var (
	crashes = map[string]int{}
	nextTry = map[string]time.Time{}

	// refusalSent dedupes the "can't run containers" report so the gateway
	// isn't told the same thing every heartbeat.
	refusalSent = map[string]bool{}
)

// tailFile returns the last maxBytes of a file, cleaned up for a one-line
// status message. Empty string when the file is missing or unreadable.
func tailFile(path string, maxBytes int64) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	off := int64(0)
	if st.Size() > maxBytes {
		off = st.Size() - maxBytes
	}
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && len(buf) == 0 {
		return ""
	}
	s := strings.TrimSpace(string(buf))
	s = strings.ReplaceAll(s, "\r", "")
	if s == "" {
		return ""
	}
	// Keep the LAST few lines — that's where the actual error is.
	lines := strings.Split(s, "\n")
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	return strings.Join(lines, " | ")
}

func specKey(a assignment) string {
	return fmt.Sprintf("%s|%s|%d|%d|%d|%v|%s|%s",
		a.Image, a.Command, a.Port, a.MemoryMB, a.CPUPercent, a.Env, a.SourceURL, a.Subdomain)
}

func specChanged(a assignment) bool {
	prev, ok := applied[a.Project]
	return !ok || prev != specKey(a)
}

// unreachable returns the endpoints this machine cannot open a TCP connection
// to. A short timeout keeps a wrong address from stalling the whole reconcile.
func unreachable(endpoints []string) []string {
	var bad []string
	for _, ep := range endpoints {
		if strings.TrimSpace(ep) == "" {
			continue
		}
		c, err := net.DialTimeout("tcp", ep, 4*time.Second)
		if err != nil {
			bad = append(bad, ep)
			continue
		}
		_ = c.Close()
	}
	return bad
}

// privateIPs lists this machine's own RFC1918 addresses.
//
// These are what a database on the same LAN would accept a connection from, so
// they're what the platform needs to build a pg_hba rule or decide that a
// direct connection isn't possible at all.
func privateIPs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		ip := ipnet.IP.To4()
		if ip == nil || !ip.IsPrivate() {
			continue // IPv6 and public addresses aren't LAN evidence
		}
		out = append(out, ip.String())
	}
	sort.Strings(out)
	return out
}

/* ─────────── private overlay ─────────── */

var (
	wgPrivate    wg.Key
	wgPublic     wg.Key
	wgReachable  bool   // can peers dial US, or must we always dial them?
	wgLastConfig string // fingerprint of the last applied mesh
	wgWarned     string
)

// initOverlay creates this machine's overlay identity, once, at startup.
//
// A machine that cannot run the overlay still runs workloads — it simply
// cannot reach private services on other machines. Degrading is correct here:
// refusing to start would take down stateless apps over a database feature
// they may not use at all.
func initOverlay() {
	if err := wg.Available(); err != nil {
		log.Printf("overlay: unavailable on this host (%v) — private services on other machines will be unreachable", err)
		return
	}
	priv, pub, err := wg.LoadOrCreateKey(filepath.Join(filepath.Dir(credFile), "wg.key"))
	if err != nil {
		log.Printf("overlay: key error: %v", err)
		return
	}
	wgPrivate, wgPublic = priv, pub

	// Whether our UDP port is usable decides if peers can dial US or whether
	// we must always dial them. Claiming reachability we don't have would
	// advertise an endpoint that never answers, leaving every peer waiting on
	// a handshake that cannot complete — so this defaults to false.
	wgReachable = wg.Dialable()
	log.Printf("overlay: identity ready (key %s…, inbound %v)", string(pub)[:12], wgReachable)
}

/* ─────────── pod addressing ─────────── */

var (
	podMu    sync.Mutex
	podCIDR  string
	podIPs   = map[string]string{} // project → its container address
	podNext  = 2                   // .0 is the network, .1 the host bridge
	podReady bool
)

// setPodNetwork records this machine's pod subnet and prepares the bridge.
//
// Every container gets an address here rather than sharing the host's network.
// That is what allows two replicas of one app on one machine (no port
// collision) and, more usefully, a rolling deploy: the new container can be
// started and proven healthy while the old one still serves, because they no
// longer compete for the same port.
func setPodNetwork(cidr string) {
	if cidr == "" {
		return
	}
	podMu.Lock()
	changed := cidr != podCIDR
	podCIDR = cidr
	podMu.Unlock()
	if !changed {
		return
	}
	if err := amm.EnsureBridge(cidr); err != nil {
		log.Printf("pod network: unavailable (%v) — containers will share the host network", err)
		podMu.Lock()
		podReady = false
		podMu.Unlock()
		return
	}
	podMu.Lock()
	podReady = true
	podMu.Unlock()
	log.Printf("pod network: %s ready", cidr)
}

// podAddressFor returns a stable address for a project's container.
//
// Stable across restarts so a redeployed container keeps its address and any
// peer that cached it stays correct; addresses are only reused after the
// project is removed entirely.
func podAddressFor(project string) (ip, cidr string, ok bool) {
	podMu.Lock()
	defer podMu.Unlock()
	if !podReady || podCIDR == "" {
		return "", "", false
	}
	if existing, has := podIPs[project]; has {
		return existing, podCIDR, true
	}
	for podNext <= 254 {
		candidate, err := wg.PodIP(podCIDR, podNext)
		podNext++
		if err != nil {
			break
		}
		taken := false
		for _, v := range podIPs {
			if v == candidate {
				taken = true
				break
			}
		}
		if !taken {
			podIPs[project] = candidate
			return candidate, podCIDR, true
		}
	}
	log.Printf("pod network: no free address left in %s", podCIDR)
	return "", "", false
}

func releasePodAddress(project string) {
	podMu.Lock()
	delete(podIPs, project)
	podMu.Unlock()
}

// applyOverlay reconciles the local WireGuard interface toward the mesh.
func applyOverlay(overlayIP string, peers []wg.Peer, warning string) {
	if wgPrivate == "" || overlayIP == "" {
		return
	}
	cfg := wg.Config{
		PrivateKey: wgPrivate,
		OverlayIP:  overlayIP,
		ListenPort: wg.DefaultPort,
		Peers:      peers,
	}
	// Only touch the interface when the mesh actually changed. Reapplying on
	// every 10-second heartbeat would reset handshakes and drop live database
	// connections continuously — a self-inflicted outage on a timer.
	if fp := cfg.Fingerprint(); fp != wgLastConfig {
		if err := wg.Apply(cfg); err != nil {
			log.Printf("overlay: apply failed: %v", err)
			return
		}
		wgLastConfig = fp
		log.Printf("overlay: %s up with %d peer(s)", overlayIP, len(peers))
	}
	if warning != "" && warning != wgWarned {
		log.Printf("overlay: %s", warning)
		wgWarned = warning
	}
}

/* ─────────── host metrics ─────────── */

// hostMetrics returns cpu%, mem%, memUsed, memTotal, load1 (Linux only).
// CPU is sampled between calls, so the first reading is 0 by design.
var lastCPU struct{ idle, total uint64 }

func hostMetrics() (cpuPct, memPct int, memUsed, memTotal int64, load string) {
	if runtime.GOOS != "linux" {
		return 0, 0, 0, 0, ""
	}

	if b, err := os.ReadFile("/proc/stat"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if !strings.HasPrefix(line, "cpu ") {
				continue
			}
			var total, idle uint64
			for i, f := range strings.Fields(line)[1:] {
				v, _ := strconv.ParseUint(f, 10, 64)
				total += v
				if i == 3 || i == 4 { // idle + iowait
					idle += v
				}
			}
			if lastCPU.total > 0 && total > lastCPU.total {
				dT, dI := total-lastCPU.total, idle-lastCPU.idle
				if dT > 0 {
					cpuPct = int(float64(dT-dI) / float64(dT) * 100)
				}
			}
			lastCPU.idle, lastCPU.total = idle, total
			break
		}
	}

	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		var totalKB, availKB int64
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			kb, _ := strconv.ParseInt(f[1], 10, 64)
			switch f[0] {
			case "MemTotal:":
				totalKB = kb
			case "MemAvailable:":
				availKB = kb
			}
		}
		if totalKB > 0 {
			memTotal = totalKB * 1024
			memUsed = (totalKB - availKB) * 1024
			memPct = int(float64(totalKB-availKB) / float64(totalKB) * 100)
		}
	}

	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			load = f[0]
		}
	}
	return
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "sixter-node"
	}
	return h
}
