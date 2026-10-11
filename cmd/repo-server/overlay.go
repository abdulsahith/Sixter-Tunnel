package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"sixter.in/tunnel/internal/wg"
)

/*
The database host's side of the private overlay.

This machine usually runs Postgres, and every replica of a scaled application
has to reach it. It joins the same mesh the nodes do, gets a stable overlay
address, and then — the part that actually matters — makes Postgres listen on
that address and accept connections from the overlay subnet.

Automating the Postgres side is the difference between a feature and a
support ticket. An operator who has to hand-edit pg_hba.conf and
postgresql.conf will get one of them subtly wrong, and the failure ("no
pg_hba.conf entry for host") arrives at runtime, on someone else's machine,
hours later.

Everything here is idempotent and marked, so re-running changes nothing and an
operator can see exactly what Sixter added and delete it by hand if they want
their config back.
*/

const overlayMarker = "# managed by sixter — private overlay"
const lanMarker = "# managed by sixter — local network"

var (
	ovMu        sync.Mutex
	ovPrivate   wg.Key
	ovPublic    wg.Key
	ovIP        string // this machine's overlay address
	ovLastCfg   string
	ovReachable bool
	ovWarning   string
)

func ovKeyPath() string { return filepath.Join(installDir, "wg.key") }

// initOverlay establishes this machine's overlay identity at startup.
func initOverlay() {
	// Open the database to this machine's own network FIRST, so a same-LAN
	// fleet works whether or not the overlay ever comes up.
	openPostgresToLAN()

	if err := wg.Available(); err != nil {
		log.Printf("overlay: unavailable (%v) — scaled replicas on other machines won't reach this database", err)
		return
	}
	priv, pub, err := wg.LoadOrCreateKey(ovKeyPath())
	if err != nil {
		log.Printf("overlay: key error: %v", err)
		return
	}
	ovPrivate, ovPublic = priv, pub
	ovReachable = wg.Dialable()
	log.Printf("overlay: identity ready (key %s…, inbound %v)", string(pub)[:12], ovReachable)

	go overlayLoop()
}

// overlayLoop keeps this machine's mesh membership current.
//
// Polled rather than pushed for the same reason the agent polls: this server
// may sit behind NAT with no inbound path, so it has to ask.
func overlayLoop() {
	for {
		if err := overlaySync(); err != nil {
			log.Printf("overlay: sync failed: %v", err)
		}
		time.Sleep(30 * time.Second)
	}
}

func overlaySync() error {
	if ovPrivate == "" {
		return nil
	}
	body, _ := json.Marshal(map[string]any{
		"sub":        serverName,
		"public_key": string(ovPublic),
		"port":       wg.DefaultPort,
		"reachable":  ovReachable,
		"role":       "db",
		// This server usually runs alongside the gateway, so the address the
		// gateway observes is 127.0.0.1. Send our own so it has something
		// dialable to publish.
		"public_ips": wg.PublicIPs(),
	})
	req, err := http.NewRequest(http.MethodPost, gatewayBase()+"/api/mesh/register", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Token", gatewayToken())

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", strings.TrimSpace(string(raw)))
	}

	var res struct {
		OverlayIP string    `json:"overlay_ip"`
		Peers     []wg.Peer `json:"peers"`
		Reachable bool      `json:"reachable"`
		Warning   string    `json:"warning"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}

	cfg := wg.Config{
		PrivateKey: ovPrivate,
		OverlayIP:  res.OverlayIP,
		ListenPort: wg.DefaultPort,
		Peers:      res.Peers,
	}
	// Only reconfigure when the mesh actually changed: reapplying resets
	// handshakes, and doing that on a timer would drop live database
	// connections every 30 seconds.
	if fp := cfg.Fingerprint(); fp != ovLastCfg {
		if err := wg.Apply(cfg); err != nil {
			return err
		}
		ovLastCfg = fp
		log.Printf("overlay: %s up with %d peer(s)", res.OverlayIP, len(res.Peers))

		// The address only becomes useful once Postgres answers on it.
		if err := openPostgresToOverlay(res.OverlayIP); err != nil {
			log.Printf("overlay: postgres not reconfigured: %v", err)
		}
	}

	ovMu.Lock()
	ovIP, ovWarning = res.OverlayIP, res.Warning
	ovMu.Unlock()
	return nil
}

// overlayIP returns this machine's overlay address ("" if not on the mesh).
func overlayIP() string {
	ovMu.Lock()
	defer ovMu.Unlock()
	return ovIP
}

/* ─────────── Postgres ─────────── */

// openPostgresToOverlay makes Postgres reachable on the overlay address.
//
// Two files, both edited idempotently and marked:
//
//	postgresql.conf  listen_addresses must include the overlay IP
//	pg_hba.conf      a rule allowing the overlay subnet, with scram auth
//
// listen_addresses requires a RESTART, not a reload, so it's only touched when
// genuinely wrong — restarting a production database because a config file was
// rewritten with identical content would be its own outage.
func openPostgresToOverlay(ip string) error {
	if ip == "" {
		return nil
	}
	confDir, err := postgresConfDir()
	if err != nil {
		return err
	}

	changedListen, err := ensureListenAddress(filepath.Join(confDir, "postgresql.conf"), ip)
	if err != nil {
		return err
	}
	changedHBA, err := ensureHBARule(filepath.Join(confDir, "pg_hba.conf"), wg.OverlayCIDR, overlayMarker)
	if err != nil {
		return err
	}

	switch {
	case changedListen:
		// listen_addresses is not reloadable.
		out, err := runShell("/", "systemctl restart postgresql", 2*time.Minute)
		if err != nil {
			return fmt.Errorf("restart postgresql: %v: %s", err, strings.TrimSpace(out))
		}
		log.Printf("overlay: postgres now listening on %s (restarted)", ip)
	case changedHBA:
		out, err := runShell("/", "systemctl reload postgresql", time.Minute)
		if err != nil {
			return fmt.Errorf("reload postgresql: %v: %s", err, strings.TrimSpace(out))
		}
		log.Printf("overlay: postgres now accepts connections from the overlay")
	}
	return nil
}

// postgresConfDir locates the active cluster's configuration directory.
func postgresConfDir() (string, error) {
	// Debian/Ubuntu layout: /etc/postgresql/<version>/<cluster>/
	matches, _ := filepath.Glob("/etc/postgresql/*/*/postgresql.conf")
	if len(matches) > 0 {
		// Highest version wins — the one a fresh install would be running.
		best := matches[len(matches)-1]
		return filepath.Dir(best), nil
	}
	// RHEL/Alpine and hand-built clusters keep config in the data directory.
	for _, p := range []string{"/var/lib/pgsql/data", "/var/lib/postgresql/data"} {
		if _, err := os.Stat(filepath.Join(p, "postgresql.conf")); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("could not find postgresql.conf — set listen_addresses and pg_hba.conf manually")
}

var listenRe = regexp.MustCompile(`(?m)^\s*#?\s*listen_addresses\s*=.*$`)

// ensureListenAddress adds the overlay IP to listen_addresses.
//
// Appends rather than replaces: the existing value carries localhost and
// possibly a LAN address that other things depend on. Overwriting it would
// cut off every client that already worked — including this server's own apps
// connecting over 127.0.0.1.
func ensureListenAddress(path, ip string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	src := string(b)

	current := "localhost"
	if m := listenRe.FindString(src); m != "" {
		if i := strings.Index(m, "="); i >= 0 {
			v := strings.TrimSpace(m[i+1:])
			if j := strings.Index(v, "#"); j >= 0 {
				v = strings.TrimSpace(v[:j])
			}
			v = strings.Trim(v, `'"`)
			if !strings.HasPrefix(strings.TrimSpace(m), "#") && v != "" {
				current = v
			}
		}
	}
	if current == "*" || containsAddr(current, ip) {
		return false, nil // already listening where we need
	}

	updated := current + "," + ip
	line := fmt.Sprintf("listen_addresses = '%s'\t%s", updated, overlayMarker)
	if listenRe.MatchString(src) {
		src = listenRe.ReplaceAllString(src, line)
	} else {
		src += "\n" + line + "\n"
	}
	return true, writeConf(path, src)
}

func containsAddr(list, ip string) bool {
	for _, p := range strings.Split(list, ",") {
		if strings.TrimSpace(p) == ip {
			return true
		}
	}
	return false
}

// ensureHBARule allows the overlay subnet to authenticate.
//
// scram-sha-256 rather than md5 or trust: overlay traffic is already encrypted
// by WireGuard, but a weak auth method would still let anything that reached
// the socket in, and "trust" on a network interface is how databases get
// deleted by strangers.
func ensureHBARule(path, cidr, marker string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	src := string(b)
	if strings.Contains(src, marker) {
		return false, nil
	}
	rule := fmt.Sprintf("\n%s\nhost\tall\tall\t%s\tscram-sha-256\n", marker, cidr)
	return true, writeConf(path, src+rule)
}

// writeConf replaces a config file, keeping a one-time backup so an operator
// can always get back to what was there before Sixter touched it.
func writeConf(path, content string) error {
	if _, err := os.Stat(path + ".sixter-bak"); err != nil {
		if orig, rerr := os.ReadFile(path); rerr == nil {
			_ = os.WriteFile(path+".sixter-bak", orig, 0640)
		}
	}
	info, err := os.Stat(path)
	mode := os.FileMode(0640)
	if err == nil {
		mode = info.Mode().Perm()
	}
	tmp := path + ".sixter-tmp"
	if err := os.WriteFile(tmp, []byte(content), mode); err != nil {
		return err
	}
	// Preserve ownership: postgres must still be able to read its own config.
	if err := exec.Command("chown", "--reference="+path, tmp).Run(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("preserve config ownership: %w", err)
	}
	return os.Rename(tmp, path)
}

/* ─────────── status ─────────── */

// overlayStatus reports the mesh as the kernel actually sees it.
//
// A peer that is configured but has NEVER handshaken is the most common
// WireGuard failure — almost always a blocked UDP port — and it is invisible
// unless something reads `wg show`. Surfacing it converts "the database is
// unreachable" into "no handshake with db-host: check UDP 51820".
func overlayStatus() map[string]any {
	ovMu.Lock()
	ip, warn := ovIP, ovWarning
	ovMu.Unlock()

	out := map[string]any{
		"enabled":   ip != "",
		"overlayIP": ip,
		"reachable": ovReachable,
		"warning":   warn,
		"subnet":    wg.OverlayCIDR,
	}
	if err := wg.Available(); err != nil {
		out["unavailable"] = err.Error()
		return out
	}
	peers, err := wg.Status()
	if err != nil {
		return out
	}
	list := make([]map[string]any, 0, len(peers))
	for _, p := range peers {
		entry := map[string]any{
			"publicKey": p.PublicKey[:12] + "…", // enough to identify, not to copy
			"endpoint":  p.Endpoint,
			"rxBytes":   p.RxBytes,
			"txBytes":   p.TxBytes,
		}
		if p.LastHandshake.IsZero() {
			entry["handshake"] = "never"
			entry["problem"] = "no handshake yet — UDP " + itoaPort(wg.DefaultPort) +
				" is probably blocked between these machines"
		} else {
			entry["handshake"] = p.LastHandshake.UTC().Format(time.RFC3339)
			entry["staleSeconds"] = int(time.Since(p.LastHandshake).Seconds())
		}
		list = append(list, entry)
	}
	out["peers"] = list
	return out
}

// GET /api/overlay — console view of the private network.
func handleOverlay(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	writeJSONResp(w, overlayStatus())
}


/* ─────────── same-LAN access ─────────── */

// openPostgresToLAN makes the database reachable from this machine's own
// network, with no overlay involved.
//
// This is the case the overlay exists to REPLACE, not the case it covers: a
// customer whose machines all sit in one rack, one office, or one VPC. There a
// direct LAN address is already reachable, faster than an encrypted hop, and
// works on hosts that cannot run WireGuard at all.
//
// It runs independently of the mesh precisely because it must work when the
// mesh doesn't. Both rules can coexist — Postgres simply accepts connections
// from either network.
func openPostgresToLAN() {
	subnet := hostLANSubnet()
	if subnet == "" {
		return // no private address: nothing on a LAN could reach us anyway
	}
	ip := hostPrivateIPs()[0]

	confDir, err := postgresConfDir()
	if err != nil {
		return // no local Postgres — the customer's database is elsewhere
	}

	changedListen, err := ensureListenAddress(filepath.Join(confDir, "postgresql.conf"), ip)
	if err != nil {
		log.Printf("lan: postgres listen_addresses: %v", err)
		return
	}
	changedHBA, err := ensureHBARule(filepath.Join(confDir, "pg_hba.conf"), subnet, lanMarker)
	if err != nil {
		log.Printf("lan: postgres pg_hba: %v", err)
		return
	}

	switch {
	case changedListen:
		if out, err := runShell("/", "systemctl restart postgresql", 2*time.Minute); err != nil {
			log.Printf("lan: restart postgres: %v: %s", err, strings.TrimSpace(out))
			return
		}
		log.Printf("lan: postgres now listening on %s, accepting %s (restarted)", ip, subnet)
	case changedHBA:
		if out, err := runShell("/", "systemctl reload postgresql", time.Minute); err != nil {
			log.Printf("lan: reload postgres: %v: %s", err, strings.TrimSpace(out))
			return
		}
		log.Printf("lan: postgres now accepts connections from %s", subnet)
	}
}
