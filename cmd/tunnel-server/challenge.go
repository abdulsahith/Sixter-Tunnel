package main

// Lightweight "checking your browser" challenge — a poor-man's Cloudflare
// interstitial. While the edge is under attack (GlobalGuard), un-trusted clients
// get an HTML page that sets a signed cookie via JS and reloads. Real browsers
// pass once and are trusted for ~2h; dumb flood bots that don't run JS never get
// the cookie and stay filtered.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"
)

const challengeCookie = "sixter_pass"

// random per-process secret — a restart simply re-challenges everyone (cheap).
var challengeSecret = func() []byte {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return b
}()

// passToken is the expected cookie value for an IP within an hourly window.
func passToken(ip string, window int64) string {
	mac := hmac.New(sha256.New, challengeSecret)
	fmt.Fprintf(mac, "%s|%d", ip, window)
	return hex.EncodeToString(mac.Sum(nil))[:32]
}

// hasValidPass reports whether the request carries a valid challenge cookie.
func hasValidPass(r *http.Request, ip string) bool {
	c, err := r.Cookie(challengeCookie)
	if err != nil || c.Value == "" {
		return false
	}
	win := time.Now().Unix() / 3600
	return c.Value == passToken(ip, win) || c.Value == passToken(ip, win-1)
}

// serveChallenge returns the interstitial that sets the cookie + reloads.
func serveChallenge(w http.ResponseWriter, ip string) {
	token := passToken(ip, time.Now().Unix()/3600)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Sixter-Challenge", "1")
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprintf(w, challengeHTML, token)
}

const challengeHTML = `<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Checking your browser…</title>
<style>
body{background:#0a0a14;color:#cdc8f0;font-family:system-ui,'Segoe UI',sans-serif;display:flex;min-height:100vh;align-items:center;justify-content:center;margin:0}
.box{text-align:center;max-width:340px;padding:24px}
.spin{width:42px;height:42px;border:3px solid #2a2350;border-top-color:#8b85e0;border-radius:50%%;animation:s .9s linear infinite;margin:0 auto 20px}
@keyframes s{to{transform:rotate(360deg)}}
h1{font-size:18px;font-weight:600;margin:0 0 8px}
p{color:#7c77a6;font-size:13px;line-height:1.5;margin:0}
.b{color:#8b85e0;font-weight:600}
</style></head>
<body><div class="box">
<div class="spin"></div>
<h1>Checking your browser…</h1>
<p>This site is protected by <span class="b">Sixter</span>. We're verifying your connection — you'll be redirected automatically.</p>
<script>document.cookie="sixter_pass=%s; path=/; max-age=7200; SameSite=Lax";setTimeout(function(){location.reload()},800);</script>
</div></body></html>`
