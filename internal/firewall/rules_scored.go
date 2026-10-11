package firewall

import "regexp"

/*
Scored signature set (CRS-style anomaly scoring).

Instead of "first regex wins → block", every matching rule contributes points.
A request is blocked once the total crosses the threshold. That buys two things
a binary matcher can't have:

  - aggressive coverage without a false-positive flood: a single weak signal
    (a lone quote, one suspicious word) scores low and passes; several weak
    signals together add up and get blocked;
  - a tunable dial (paranoia level) instead of an on/off switch.

Severity weights follow CRS: critical 5, error 4, warning 3, notice 2.
Paranoia level: 1 = high-confidence only (default), 2 = adds broader rules,
3 = aggressive//noisy. Rules above the active level simply don't run.
*/

const (
	sevCritical = 5
	sevError    = 4
	sevWarning  = 3
	sevNotice   = 2
)

// scoredRule is one detection. Targets narrows where it applies ("" = all).
type scoredRule struct {
	id       string
	category string // user-facing category shown in the dashboard
	score    int
	paranoia int
	re       *regexp.Regexp
}

// scoredRules runs against NORMALIZED targets (see normalize.go), so patterns
// assume lowercase, decoded, comment-stripped, whitespace-collapsed input.
var scoredRules = []scoredRule{
	/* ── SQL injection ── */
	{"sqli-union", "SQL injection", sevCritical, 1, regexp.MustCompile(`\bunion\b[\s\S]{0,60}\bselect\b`)},
	{"sqli-select-from", "SQL injection", sevCritical, 1, regexp.MustCompile(`\bselect\b[\s\S]{0,80}\bfrom\b[\s\S]{0,80}\bwhere\b`)},
	{"sqli-tautology", "SQL injection", sevCritical, 1, regexp.MustCompile(`(\bor\b|\band\b)\s*['"\x60]?\s*\d+\s*['"\x60]?\s*=\s*['"\x60]?\s*\d+`)},
	{"sqli-stacked", "SQL injection", sevCritical, 1, regexp.MustCompile(`;\s*(drop|alter|truncate|create|insert|update|delete)\s+(table|database|from|into)\b`)},
	{"sqli-timing", "SQL injection", sevCritical, 1, regexp.MustCompile(`\b(sleep|benchmark|pg_sleep|waitfor\s+delay|dbms_pipe\.receive_message)\s*\(`)},
	{"sqli-meta", "SQL injection", sevCritical, 1, regexp.MustCompile(`\b(information_schema|sysobjects|pg_catalog|mysql\.user|load_file|into\s+outfile|into\s+dumpfile)\b`)},
	{"sqli-comment-tail", "SQL injection", sevWarning, 2, regexp.MustCompile(`['"\x60]\s*(--|#)\s*$`)},
	{"sqli-quote-op", "SQL injection", sevNotice, 2, regexp.MustCompile(`['"\x60]\s*(or|and)\s+['"\x60]?\w`)},

	/* ── Cross-site scripting ── */
	{"xss-script-tag", "Cross-site scripting", sevCritical, 1, regexp.MustCompile(`<\s*script\b|<\s*/\s*script\s*>`)},
	{"xss-js-uri", "Cross-site scripting", sevCritical, 1, regexp.MustCompile(`(javascript|vbscript|data)\s*:[\s\S]{0,40}(alert|eval|script|base64)`)},
	{"xss-event-handler", "Cross-site scripting", sevCritical, 1, regexp.MustCompile(`\bon(error|load|click|mouseover|focus|animationstart|toggle|beforeprint)\s*=`)},
	{"xss-dangerous-tag", "Cross-site scripting", sevError, 1, regexp.MustCompile(`<\s*(iframe|svg|object|embed|applet|meta|base|form|math|details|marquee)\b`)},
	{"xss-sink", "Cross-site scripting", sevError, 1, regexp.MustCompile(`\b(document\.(cookie|domain|write)|window\.(location|name)|innerhtml|outerhtml|srcdoc)\b`)},
	{"xss-eval", "Cross-site scripting", sevError, 1, regexp.MustCompile(`\b(eval|settimeout|setinterval|function|atob|fromcharcode)\s*\(`)},
	{"xss-angle-quote", "Cross-site scripting", sevNotice, 2, regexp.MustCompile(`<[a-z][\s\S]{0,20}=`)},

	/* ── Path traversal / local file include ── */
	{"lfi-dotdot", "Path traversal", sevCritical, 1, regexp.MustCompile(`(\.\./|\.\.\\){2,}|/\.\./`)},
	{"lfi-sensitive", "Path traversal", sevCritical, 1, regexp.MustCompile(`/(etc/(passwd|shadow|hosts)|proc/self/(environ|cmdline)|windows/win\.ini|boot\.ini)\b`)},
	{"lfi-wrapper", "Path traversal", sevCritical, 1, regexp.MustCompile(`\b(php|file|glob|expect|zip|phar|data)://`)},

	/* ── Remote command execution ── */
	{"rce-shell-chain", "Command injection", sevCritical, 1, regexp.MustCompile(`[;&|\x60]\s*(cat|ls|id|whoami|uname|wget|curl|nc|ncat|bash|sh|python|perl|powershell)\b`)},
	{"rce-substitution", "Command injection", sevCritical, 1, regexp.MustCompile(`\$\([\s\S]{1,60}\)|\x60[\s\S]{1,60}\x60|\$\{[\s\S]{1,60}\}`)},
	{"rce-reverse-shell", "Command injection", sevCritical, 1, regexp.MustCompile(`\b(/dev/tcp/|bash\s+-i|nc\s+-e|mkfifo|socat\s+)`)},
	{"rce-php", "Command injection", sevCritical, 1, regexp.MustCompile(`\b(system|exec|shell_exec|passthru|popen|proc_open|assert)\s*\(`)},

	/* ── Server-side request forgery ── */
	// Cloud metadata / loopback is unambiguous — must reach the threshold on
	// its own, so it's critical rather than error.
	{"ssrf-internal-host", "SSRF", sevCritical, 1, regexp.MustCompile(`https?://(127\.0\.0\.1|localhost|0\.0\.0\.0|\[::1\]|169\.254\.169\.254|metadata\.google\.internal)`)},
	{"ssrf-private-range", "SSRF", sevWarning, 2, regexp.MustCompile(`https?://(10\.\d{1,3}\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3}|172\.(1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3})`)},

	/* ── XML external entity ── */
	{"xxe-doctype", "XXE", sevCritical, 1, regexp.MustCompile(`<!(doctype|entity)\b[\s\S]{0,80}(system|public)\b`)},

	/* ── Server-side template injection ── */
	{"ssti-braces", "Template injection", sevCritical, 1, regexp.MustCompile(`\{\{[\s\S]{0,60}(config|self|request|__class__|__globals__|\.\.)[\s\S]{0,60}\}\}`)},
	{"ssti-expr", "Template injection", sevError, 2, regexp.MustCompile(`(\$\{[\s\S]{0,40}\}|#\{[\s\S]{0,40}\}|<%=[\s\S]{0,40}%>)`)},

	/* ── JNDI / Log4Shell ── */
	{"jndi-lookup", "JNDI injection", sevCritical, 1, regexp.MustCompile(`jndi\s*:\s*(ldaps?|rmi|dns|iiop|corba|nis|nds)\b`)},

	/* ── NoSQL injection ── */
	{"nosql-operator", "NoSQL injection", sevCritical, 1, regexp.MustCompile(`["']?\$(where|ne|gt|gte|lt|lte|regex|expr|function|accumulator)["']?\s*:`)},

	/* ── Insecure deserialization ── */
	{"deser-java", "Deserialization", sevCritical, 1, regexp.MustCompile(`(rt\x00\x05|aced0005|\bjava\.lang\.runtime\b|commonscollections)`)},
	{"deser-php", "Deserialization", sevError, 1, regexp.MustCompile(`\bo:\d+:"[a-z_\\]+":\d+:\{`)},

	/* ── Scanners, probes and leaked secrets ── */
	{"scan-known-path", "Scanner / probe", sevError, 1, regexp.MustCompile(`/(wp-login\.php|wp-admin|xmlrpc\.php|phpmyadmin|\.env|\.git/|\.svn/|\.aws/|\.ssh/|vendor/phpunit|actuator/|solr/|struts|cgi-bin/|shell\.php|adminer\.php)`)},
	{"scan-backup-file", "Scanner / probe", sevWarning, 1, regexp.MustCompile(`\.(bak|old|swp|sql|sql\.gz|tar\.gz|zip|log|ini|conf|pem|key)(\?|$)`)},

	/* ── Protocol / header anomalies ── */
	{"proto-crlf", "Protocol anomaly", sevCritical, 1, regexp.MustCompile(`(%0d%0a|\r\n)(set-cookie|location|content-length|transfer-encoding)\s*:`)},
	{"proto-null", "Protocol anomaly", sevError, 1, regexp.MustCompile(`%00|\x00`)},
}

// activeRules returns the rules enabled at a given paranoia level.
func activeRules(paranoia int) []scoredRule {
	if paranoia <= 0 {
		paranoia = 1
	}
	out := make([]scoredRule, 0, len(scoredRules))
	for _, r := range scoredRules {
		if r.paranoia <= paranoia {
			out = append(out, r)
		}
	}
	return out
}

// Match represents one rule hit during scoring.
type Match struct {
	RuleID   string `json:"rule_id"`
	Category string `json:"category"`
	Score    int    `json:"score"`
	Target   string `json:"target"` // where it hit: path, query, body, header:x
}

// scoreTargets runs the active ruleset over every normalized target and returns
// the total anomaly score plus what matched. Stops early once the score is far
// past the threshold — no point burning CPU on an obvious attack.
func scoreTargets(targets map[string]string, paranoia, stopAt int) (int, []Match) {
	rules := activeRules(paranoia)
	total := 0
	var hits []Match
	for name, raw := range targets {
		if raw == "" {
			continue
		}
		norm := normalize(raw)
		if norm == "" {
			continue
		}
		for _, r := range rules {
			if r.re.MatchString(norm) {
				total += r.score
				hits = append(hits, Match{RuleID: r.id, Category: r.category, Score: r.score, Target: name})
				if stopAt > 0 && total >= stopAt*3 {
					return total, hits
				}
			}
		}
	}
	return total, hits
}

// topCategory picks the category to report for a blocked request: the one
// contributing the highest score (ties broken by first seen).
func topCategory(hits []Match) string {
	best, bestScore := "", -1
	agg := map[string]int{}
	for _, h := range hits {
		agg[h.Category] += h.Score
		if agg[h.Category] > bestScore {
			best, bestScore = h.Category, agg[h.Category]
		}
	}
	return best
}
