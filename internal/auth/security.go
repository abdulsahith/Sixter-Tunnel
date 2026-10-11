package auth

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

/*
Baseline HTTP hardening for the apex (account APIs + landing SPA).

This wraps ONLY the gateway's own mux — never proxied user tunnels, whose
responses must be passed through untouched (a CSP of ours would break other
people's apps).

Covers the OWASP "secure headers" baseline, no-store on anything carrying
personal data, and per-IP throttling of the endpoints worth brute-forcing
(login, signup, 2FA, invite redemption, profile lookups).
*/

// Secure wraps h with security headers, cache policy and abuse throttling.
func Secure(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()

		// Never sniff, never frame, never leak the full URL in referrers.
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		hd.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(), payment=(), usb=(), interest-cohort=()")
		hd.Set("Cross-Origin-Opener-Policy", "same-origin")
		hd.Set("X-Permitted-Cross-Domain-Policies", "none")

		// HSTS only over TLS, so a plain-HTTP dev run isn't pinned.
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			hd.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		// The SPA is bundled: no third-party scripts, no inline <script>.
		// 'unsafe-inline' is styles only (React inline styles + framer-motion);
		// style injection is far less severe than script injection.
		hd.Set("Content-Security-Policy", strings.Join([]string{
			"default-src 'self'",
			"base-uri 'self'",
			"object-src 'none'",
			"frame-ancestors 'none'",
			"form-action 'self'",
			"script-src 'self'",
			"style-src 'self' 'unsafe-inline'",
			"img-src 'self' data: blob: https:",
			"font-src 'self' data:",
			"connect-src 'self'",
			"upgrade-insecure-requests",
		}, "; "))

		// API responses carry account data — keep them out of every cache.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			hd.Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
			hd.Set("Pragma", "no-cache")
		}

		if limit, window, ok := throttleFor(r.URL.Path); ok {
			if !authLimiter.allow(clientIP(r)+"|"+bucketFor(r.URL.Path), limit, window) {
				hd.Set("Retry-After", "60")
				jsonError(w, http.StatusTooManyRequests, "too many attempts — wait a minute and try again")
				return
			}
		}

		h.ServeHTTP(w, r)
	})
}

// throttleFor returns the per-IP budget for abuse-prone endpoints.
// Anything not listed is unthrottled here (tunnels have their own limiter, and
// the CLI device-flow poll must stay fast).
func throttleFor(path string) (limit int, window time.Duration, ok bool) {
	switch {
	// credential endpoints — brute force / password spraying
	case path == "/api/login", path == "/api/signup",
		path == "/api/2fa/verify", path == "/api/2fa/enable":
		return 10, time.Minute, true
	// identity + invite endpoints — enumeration scanning
	case path == "/api/member/profile", path == "/api/workspaces/join",
		path == "/api/enroll/redeem", path == "/api/subdomain/check",
		path == "/api/node/enroll":
		return 40, time.Minute, true
	// heartbeats are frequent by design (every 10s per node) — a fleet of 200
	// nodes behind one NAT would trip a tighter budget, so keep this generous.
	case path == "/api/node/heartbeat":
		return 2000, time.Minute, true
	// OAuth entry points — callback spam
	case strings.HasPrefix(path, "/api/auth/") && strings.HasSuffix(path, "/start"):
		return 20, time.Minute, true
	}
	return 0, 0, false
}

// bucketFor groups related paths so one endpoint can't exhaust another's budget.
func bucketFor(path string) string {
	if strings.HasPrefix(path, "/api/auth/") {
		return "/api/auth"
	}
	return path
}

// clientIP resolves the caller's address, trusting X-Forwarded-For only for its
// first entry (the value our own nginx/edge prepends).
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return ip
	}
	return r.RemoteAddr
}

/* ─────────── sliding-window limiter ─────────── */

type slidingLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
	last time.Time
}

var authLimiter = &slidingLimiter{hits: map[string][]time.Time{}}

func (l *slidingLimiter) allow(key string, limit int, window time.Duration) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	// periodic sweep so the map can't grow without bound
	if now.Sub(l.last) > 5*time.Minute {
		for k, ts := range l.hits {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) > 10*time.Minute {
				delete(l.hits, k)
			}
		}
		l.last = now
	}

	cutoff := now.Add(-window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}
