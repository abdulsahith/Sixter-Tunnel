package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"sixter.in/tunnel/internal/config"
)

/*
Tunnel Mode — host a project through the Sixter gateway when the server has no
public IP. Two halves:

  1. attach/detach: tell the gateway "route <domain> to my tunnel" via its
     /api/tunnel/domain API, authenticated with this account's API token.
  2. tunnelProxy: when a request for an attached domain arrives over the tunnel,
     reverse-proxy it to the project's local port.
*/

// gatewayBase returns the gateway HTTPS API base, e.g. "https://sixter.in".
// Read from ~/.sixter/config.yaml (written by `sixter login`); falls back to
// deriving from the ws Server url, then to https://sixter.in.
// gatewayOverride, when set (via -gateway), forces the gateway base URL —
// handy for local testing where the gateway is http://localhost:8008.
var gatewayOverride string

func gatewayBase() string {
	if gatewayOverride != "" {
		return strings.TrimRight(gatewayOverride, "/")
	}
	c, err := config.Load()
	if err == nil {
		if c.API != "" {
			return strings.TrimRight(c.API, "/")
		}
		if c.Server != "" {
			s := c.Server
			s = strings.Replace(s, "wss://", "https://", 1)
			s = strings.Replace(s, "ws://", "http://", 1)
			// strip any :port — the public API is on 443
			if u, e := url.Parse(s); e == nil && u.Host != "" {
				host := u.Hostname()
				return "https://" + host
			}
		}
	}
	return "https://sixter.in"
}

// apexDomain returns the bare apex (e.g. "sixter.in") for user-facing DNS hints.
func apexDomain() string {
	b := gatewayBase()
	b = strings.TrimPrefix(b, "https://")
	b = strings.TrimPrefix(b, "http://")
	return strings.TrimSuffix(b, "/")
}

// gatewayToken returns the account API token used to prove tunnel ownership.
func gatewayToken() string {
	if c, err := config.Load(); err == nil && c.Token != "" {
		return c.Token
	}
	return uploadToken
}

// attachDomain points a custom domain at any subdomain this account controls —
// used to move a project's domain onto its SERVICE address so the gateway can
// balance it across replicas instead of pinning it to this machine.
func attachDomain(domain, sub string) error {
	_, err := callGatewaySub(http.MethodPost, domain, sub)
	return err
}

func callGateway(method, domain string) ([]byte, error) {
	return callGatewaySub(method, domain, serverName)
}

func callGatewaySub(method, domain, sub string) ([]byte, error) {
	body, _ := json.Marshal(map[string]string{"domain": domain, "sub": sub})
	req, err := http.NewRequest(method, gatewayBase()+"/api/tunnel/domain", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Token", gatewayToken())

	cl := &http.Client{Timeout: 15 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return out, nil
}

// attachTunnelDomain registers domain→this tunnel at the gateway, returns tunnel id.
func attachTunnelDomain(domain string) (string, error) {
	out, err := callGateway(http.MethodPost, domain)
	if err != nil {
		return "", err
	}
	var r struct {
		TunnelID string `json:"tunnel_id"`
	}
	_ = json.Unmarshal(out, &r)
	return r.TunnelID, nil
}

// detachTunnelDomain removes the gateway route for a domain.
func detachTunnelDomain(domain string) error {
	_, err := callGateway(http.MethodDelete, domain)
	return err
}

// banAtGateway blocks an attacker IP at the Sixter gateway for this server's
// subdomain. This is the only ban that works for tunnel-mode apps — the
// attacker hits the gateway, not this box, so a local nftables ban is blind.
func banAtGateway(ip string, minutes int) error {
	body, _ := json.Marshal(map[string]any{"sub": serverName, "ip": ip, "minutes": minutes})
	req, err := http.NewRequest(http.MethodPost, gatewayBase()+"/api/tunnel/ban", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Token", gatewayToken())
	cl := &http.Client{Timeout: 15 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

/* ─────────── local host-based reverse proxy ─────────── */

var (
	tunnelRoutesMu sync.RWMutex
	tunnelRoutes   = map[string]http.Handler{} // host → handler (reverse proxy OR static file server)
	tunnelHosts    = map[string]bool{}          // host set for fast lookup
)

// rebuildTunnelRoutes regenerates the host handlers from the current set of
// tunnel-mode domains. Backend apps reverse-proxy to their port; static sites
// are served straight from disk. Safe to call after any domain change.
func rebuildTunnelRoutes() {
	domainsMu.Lock()
	type entry struct {
		host     string
		port     int
		serveDir string
	}
	var list []entry
	for _, cfg := range domains {
		if cfg != nil && cfg.Mode == "tunnel" && cfg.Domain != "" {
			if cfg.ServeDir != "" || cfg.Port > 0 {
				list = append(list, entry{strings.ToLower(cfg.Domain), cfg.Port, cfg.ServeDir})
			}
		}
	}
	domainsMu.Unlock()

	routes := make(map[string]http.Handler, len(list))
	hosts := make(map[string]bool, len(list))
	for _, e := range list {
		if e.serveDir != "" {
			// static site: serve files directly, no process needed
			routes[e.host] = http.FileServer(http.Dir(e.serveDir))
		} else {
			target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", e.port))
			rp := httputil.NewSingleHostReverseProxy(target)
			orig := rp.Director
			host := e.host
			rp.Director = func(r *http.Request) {
				orig(r)
				r.Host = host // app sees its real domain
			}
			rp.FlushInterval = 100 * time.Millisecond // streaming / SSE friendly
			routes[e.host] = rp
		}
		hosts[e.host] = true
	}

	tunnelRoutesMu.Lock()
	tunnelRoutes = routes
	tunnelHosts = hosts
	tunnelRoutesMu.Unlock()
}

// modeLabel returns a human label for pipeline logs.
func modeLabel(isTunnel bool) string {
	if isTunnel {
		return "tunnel"
	}
	return "public"
}

// isTunnelHost reports whether a request Host belongs to an attached tunnel domain.
func isTunnelHost(host string) bool {
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	host = strings.ToLower(host)
	tunnelRoutesMu.RLock()
	ok := tunnelHosts[host]
	tunnelRoutesMu.RUnlock()
	return ok
}

// tunnelProxy serves a request if its Host is an attached tunnel-mode domain.
// Returns true if it handled the request.
func tunnelProxy(w http.ResponseWriter, r *http.Request) bool {
	host := r.Host
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	host = strings.ToLower(host)

	tunnelRoutesMu.RLock()
	h := tunnelRoutes[host]
	tunnelRoutesMu.RUnlock()
	if h == nil {
		return false
	}
	h.ServeHTTP(w, r)
	return true
}
