package firewall

import (
	"strings"
	"time"
)

/*
Full-request inspection.

The original Inspect() only ever saw path, query and user-agent — so anything
posted in a request BODY (the usual home of real SQLi/XSS/RCE payloads) or
hidden in a header passed straight through unexamined. InspectFull closes that
gap and runs the scored ruleset over every target.

Kept as a separate entry point so the existing Inspect() call sites keep
working unchanged; the proxy passes a Request when it has one.
*/

// Request is everything the WAF can look at for one HTTP request.
type Request struct {
	IP      string
	Method  string
	Path    string
	Query   string
	UA      string
	Country string

	// Body is the buffered prefix of the request body (empty if not read).
	// The proxy caps this — see Settings.MaxBodyScanKB.
	Body string
	// Truncated reports that Body is only a prefix of the real payload.
	Truncated bool

	// Headers worth inspecting: referer, cookie, x-forwarded-for, content-type…
	Headers map[string]string
}

// InspectFull is the scored hot path. It runs the legacy checks first (bans,
// user rules, rate limits, bot policy) via Inspect, then adds anomaly scoring
// across path/query/body/headers.
func (e *Engine) InspectFull(r Request) Decision {
	// Stage 1 — bans, custom rules, rate limiting, bot policy. This also
	// applies the legacy signature list, so nothing gets weaker.
	if d := e.Inspect(r.IP, r.Method, r.Path, r.Query, r.UA, r.Country); d.Blocked {
		return d
	}

	e.mu.Lock()
	s := e.settings
	e.mu.Unlock()

	if !s.Enabled || !s.WAF {
		return allow
	}

	paranoia, threshold, _ := s.scoringConfig()

	targets := map[string]string{
		"path":  r.Path,
		"query": r.Query,
		"ua":    r.UA,
	}
	if s.InspectBody && r.Body != "" {
		targets["body"] = r.Body
	}
	for k, v := range r.Headers {
		if v == "" {
			continue
		}
		switch strings.ToLower(k) {
		// headers an attacker controls and apps commonly trust
		case "referer", "referrer", "cookie", "x-forwarded-for", "x-forwarded-host",
			"x-real-ip", "content-type", "origin", "x-api-version", "accept-language":
			targets["header:"+strings.ToLower(k)] = v
		}
	}

	score, hits := scoreTargets(targets, paranoia, threshold)
	if score < threshold || len(hits) == 0 {
		return allow
	}

	category := topCategory(hits)
	reason := buildReason(hits, score, threshold)

	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()

	// Detection-only: record the finding, let the request through. This is how
	// a new ruleset is rolled out on a live platform without breaking anyone.
	if s.DetectionOnly {
		// NB: Inspect() already counted this request as allowed — don't
		// double-count. Only record the finding.
		e.byCategory[category+" (detected)"]++
		e.events = append(e.events, Event{
			Time: now, IP: r.IP, Method: r.Method, Path: r.Path,
			Reason: "would block — " + reason, Category: category, Status: 200,
		})
		if len(e.events) > 500 {
			e.events = e.events[len(e.events)-500:]
		}
		return allow
	}

	e.strike(r.IP, now)
	return e.block(r.IP, r.Method, r.Path, reason, category, 403, now)
}

// buildReason summarises which rules fired, for the event feed.
func buildReason(hits []Match, score, threshold int) string {
	var b strings.Builder
	b.WriteString("anomaly score ")
	b.WriteString(itoa(score))
	b.WriteByte('/')
	b.WriteString(itoa(threshold))
	b.WriteString(" — ")
	shown := 0
	for _, h := range hits {
		if shown >= 3 {
			b.WriteString(", +")
			b.WriteString(itoa(len(hits) - shown))
			b.WriteString(" more")
			break
		}
		if shown > 0 {
			b.WriteString(", ")
		}
		b.WriteString(h.RuleID)
		b.WriteString(" in ")
		b.WriteString(h.Target)
		shown++
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
