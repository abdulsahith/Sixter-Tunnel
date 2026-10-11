package main

// Per-site firewall view for the repo dashboard. A deployed site's traffic goes
// browser → nginx → app, so the repo-server isn't inline — but it already tails
// each project's nginx access log (capture.go). Here we scan those captured
// requests against the WAF signatures to show how safe a site is: a safety
// score, attack attempts by category, rate-limited hits, and the top offenders,
// with a one-click IP block (nftables).

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"time"

	"sixter.in/tunnel/internal/firewall"
)

type secOffender struct {
	IP    string `json:"ip"`
	Count int    `json:"count"`
	Proxy bool   `json:"proxy"` // behind a known CDN/proxy (Cloudflare) — don't ban
}

// Cloudflare (and common CDN) IPv4 ranges. Requests proxied through these arrive
// with the CDN's IP as the source — banning them would block ALL real visitors.
var proxyNets = func() []*net.IPNet {
	cidrs := []string{
		"104.16.0.0/13", "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
		"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
		"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
		"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15",
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			out = append(out, n)
		}
	}
	return out
}()

// isProxyIP reports whether an IP belongs to a known CDN/proxy (Cloudflare).
func isProxyIP(ip string) bool {
	p := net.ParseIP(ip)
	if p == nil {
		return false
	}
	for _, n := range proxyNets {
		if n.Contains(p) {
			return true
		}
	}
	return false
}

// securitySnapshot analyses a project's captured requests.
func securitySnapshot(project string) map[string]any {
	c := captureForProject(project)
	c.mu.Lock()
	pkts := make([]pktEntry, len(c.packets))
	copy(pkts, c.packets)
	c.mu.Unlock()

	total := len(pkts)
	byCat := map[string]int{}
	attackByIP := map[string]int{}
	rateLimited, clientErr, serverErr := 0, 0, 0
	events := []map[string]any{}

	for i := len(pkts) - 1; i >= 0; i-- { // newest first
		p := pkts[i]
		switch {
		case p.Status == 429:
			rateLimited++
		case p.Status >= 500:
			serverErr++
		case p.Status >= 400:
			clientErr++
		}
		if cat, hit := firewall.Classify(p.Path, "", ""); hit {
			byCat[cat]++
			attackByIP[p.Src]++
			if len(events) < 40 {
				events = append(events, map[string]any{
					"time": p.Time, "ip": p.Src, "method": p.Method,
					"path": p.Path, "category": cat, "status": p.Status,
				})
			}
		}
	}

	attacks := 0
	for _, v := range byCat {
		attacks += v
	}

	offs := []secOffender{}
	for ip, n := range attackByIP {
		offs = append(offs, secOffender{IP: ip, Count: n, Proxy: isProxyIP(ip)})
	}
	sort.Slice(offs, func(i, j int) bool { return offs[i].Count > offs[j].Count })
	if len(offs) > 10 {
		offs = offs[:10]
	}

	// safety score: based on what FRACTION of traffic is malicious. A handful of
	// scanner probes (normal background noise on any public site) barely moves it;
	// only a sustained flood drops it. ratio is a percentage 0–100.
	ratio := 0
	if total > 0 {
		ratio = attacks * 100 / total
	}
	score := 100 - ratio
	if total > 0 {
		score -= (rateLimited * 100 / total) / 2
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	status := "Protected"
	switch {
	case total == 0:
		status = "No traffic yet"
	case ratio >= 50:
		status = "Under attack"
	case ratio >= 20:
		status = "Elevated"
	}

	return map[string]any{
		"score":        score,
		"status":       status,
		"total":        total,
		"attacks":      attacks,
		"byCategory":   byCat,
		"rateLimited":  rateLimited,
		"clientErrors": clientErr,
		"serverErrors": serverErr,
		"events":       events,
		"offenders":    offs,
	}
}

// banIP adds an IP to the nftables flood_ban set (needs the hardening bundle).
func banIP(ip string) bool {
	if net.ParseIP(ip) == nil || runtime.GOOS != "linux" {
		return false
	}
	cmd := fmt.Sprintf("nft add element inet sixter flood_ban { %s timeout 1h }", ip)
	if isRoot() {
		_, err := runShell("/", cmd, 10*time.Second)
		return err == nil
	}
	if helperInstalled() {
		_, err := sudoHelper("banip", ip)
		return err == nil
	}
	return false
}

// GET  /api/security/<project>             → safety snapshot
// POST /api/security/<project>/ban {ip}    → block an IP at the firewall
func handleSecurity(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/security/")

	if strings.HasSuffix(rest, "/ban") {
		name := strings.TrimSuffix(rest, "/ban")
		if !isSafeName(name) {
			http.Error(w, "invalid project name", http.StatusBadRequest)
			return
		}
		var body struct {
			IP string `json:"ip"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		ip := strings.TrimSpace(body.IP)
		if isProxyIP(ip) {
			writeJSONResp(w, map[string]any{"ok": false,
				"error": "that's a Cloudflare/CDN IP — blocking it would cut off ALL real visitors to this site. The real visitor IP is in the CF-Connecting-IP header (enable real-IP in nginx)."})
			return
		}

		// Tunnel-mode apps are fronted by the Sixter gateway — the attacker never
		// touches this box, so a local nftables ban is useless. Ban at the gateway
		// (covers every tunnel domain on this server's subdomain). Public-mode apps
		// receive traffic directly, so the local nftables ban is correct there.
		domainsMu.Lock()
		isTunnel := false
		if d, ok := domains[name]; ok && d != nil {
			isTunnel = d.Mode == "tunnel"
		}
		domainsMu.Unlock()

		if isTunnel {
			if err := banAtGateway(ip, 60); err != nil {
				writeJSONResp(w, map[string]any{"ok": false, "error": "gateway ban failed: " + err.Error()})
			} else {
				writeJSONResp(w, map[string]any{"ok": true, "note": "IP blocked at the Sixter gateway for 1 hour"})
			}
			return
		}

		if banIP(ip) {
			writeJSONResp(w, map[string]any{"ok": true, "note": "IP blocked for 1 hour"})
		} else {
			writeJSONResp(w, map[string]any{"ok": false, "error": "couldn't block — is the hardening bundle (nftables) installed?"})
		}
		return
	}

	if !isSafeName(rest) {
		http.Error(w, "invalid project name", http.StatusBadRequest)
		return
	}
	writeJSONResp(w, securitySnapshot(rest))
}
