package main

import (
	"net/http"
	"strings"
)

/*
Security-header baseline for the repo dashboard.

Only applied to the dashboard's own host — never to a project hosted through
tunnel mode, whose responses belong to the user's app and must pass through
untouched (our CSP would break their site).

The dashboard fetches an SSO grant from the gateway (a different origin), so
connect-src has to allow the apex as well as self.
*/

func applySecurityHeaders(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(), payment=(), usb=()")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("X-Permitted-Cross-Domain-Policies", "none")

	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	}

	h.Set("Content-Security-Policy", strings.Join([]string{
		"default-src 'self'",
		"base-uri 'self'",
		"object-src 'none'",
		"frame-ancestors 'none'",
		"form-action 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob: https:",
		"font-src 'self' data:",
		// same-origin APIs, websockets (terminal + deploy stream), gateway SSO grant
		"connect-src 'self' ws: wss: " + gatewayOrigin(),
	}, "; "))

	// dashboard APIs carry project data, tokens and logs — never cache them
	if strings.HasPrefix(r.URL.Path, "/api/") {
		h.Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
		h.Set("Pragma", "no-cache")
	}
}

// gatewayOrigin returns the gateway's origin, for CSP connect-src.
func gatewayOrigin() string {
	if base := strings.TrimSuffix(gatewayBase(), "/"); base != "" {
		return base
	}
	return "https://sixter.in"
}
