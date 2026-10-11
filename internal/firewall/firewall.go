// Package firewall is Sixter's application-layer (L7) firewall. Every request
// flowing through the tunnel proxy is inspected here BEFORE it reaches a user's
// machine, so the platform — not the user — absorbs attacks.
//
// It provides:
//   - attack-signature detection (SQLi, XSS, path traversal, scanner probes)
//   - per-subdomain user rules (block/allow by IP/CIDR, path, method, country, UA)
//   - per-IP rate limiting with fail2ban-style auto-banning
//   - a transparent event log + live counters surfaced in the user console
//
// Everything is in-memory and lock-guarded; rules are loaded from the account
// store and can be hot-swapped via SetRules.
package firewall

import (
	"net"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

/* ─────────── rules (user-configurable, persisted per account) ─────────── */

// Action is what to do when a rule matches.
type Action string

const (
	ActionAllow Action = "allow" // whitelist — skip remaining checks
	ActionBlock Action = "block" // reject with 403
)

// Field is the request attribute a rule matches against.
type Field string

const (
	FieldIP      Field = "ip"      // CIDR or exact IP
	FieldPath    Field = "path"    // substring/prefix match
	FieldMethod  Field = "method"  // exact (GET, POST…)
	FieldUA      Field = "ua"      // user-agent substring
	FieldCountry Field = "country" // 2-letter code (best-effort)
)

// Rule is one user-defined firewall rule. Rules are evaluated in order;
// the first match wins.
type Rule struct {
	ID      string `json:"id"`
	Field   Field  `json:"field"`
	Value   string `json:"value"`
	Action  Action `json:"action"`
	Note    string `json:"note,omitempty"`
	Enabled bool   `json:"enabled"`
}

// Settings is the full firewall configuration for one subdomain.
type Settings struct {
	Enabled         bool   `json:"enabled"`            // master switch
	WAF             bool   `json:"waf"`                // attack-signature detection
	BotProtection   bool   `json:"bot_protection"`     // block scrapers / automation tools / scanners
	RateLimitPerMin int    `json:"rate_limit_per_min"` // 0 = unlimited
	AutoBan         bool   `json:"auto_ban"`           // ban IPs that trip too many blocks
	Rules           []Rule `json:"rules"`

	// ── anomaly scoring (CRS-style) ───────────────────────────────────────
	// DetectionOnly logs and scores attacks without blocking them, so a new
	// ruleset can be observed against real traffic before it's enforced.
	DetectionOnly bool `json:"detection_only"`
	// Paranoia 1 = high-confidence rules only (default), 2 = broader, 3 = noisy.
	Paranoia int `json:"paranoia"`
	// AnomalyThreshold is the score at which a request is blocked. CRS uses 5,
	// i.e. one critical rule is enough; several weak signals also add up.
	AnomalyThreshold int `json:"anomaly_threshold"`
	// InspectBody enables request-body scanning (bounded by MaxBodyScanKB).
	InspectBody   bool `json:"inspect_body"`
	MaxBodyScanKB int  `json:"max_body_scan_kb"`
}

// scoringConfig fills in defaults for accounts saved before these fields existed.
func (s Settings) scoringConfig() (paranoia, threshold, bodyKB int) {
	paranoia, threshold, bodyKB = s.Paranoia, s.AnomalyThreshold, s.MaxBodyScanKB
	if paranoia <= 0 {
		paranoia = 1
	}
	if threshold <= 0 {
		threshold = 5
	}
	if bodyKB <= 0 {
		bodyKB = 128
	}
	return
}

// DefaultSettings returns a secure-by-default config for new subdomains.
// BotProtection is opt-in (off by default) since it blocks non-browser clients
// like curl/python — fine for sites, not always wanted for APIs.
func DefaultSettings() Settings {
	return Settings{
		Enabled:         true,
		WAF:             true,
		BotProtection:   false,
		RateLimitPerMin: 600,
		AutoBan:         true,
		Rules:           []Rule{},

		// New scoring engine ships in DETECTION-ONLY so a live platform can't
		// be broken by a rule change. Flip DetectionOnly off to enforce.
		DetectionOnly:    true,
		Paranoia:         1,
		AnomalyThreshold: 5,
		InspectBody:      true,
		MaxBodyScanKB:    128,
	}
}

/* ─────────── attack signatures (the WAF core) ─────────── */

type signature struct {
	name string
	kind string // category shown to the user
	re   *regexp.Regexp
}

var signatures = []signature{
	{"sqli-union", "SQL injection", regexp.MustCompile(`(?i)(\bunion\b.{0,40}\bselect\b|\bselect\b.{0,40}\bfrom\b.{0,40}\bwhere\b|\bor\b\s+1\s*=\s*1|';\s*drop\s+table|\bsleep\s*\(|benchmark\s*\()`)},
	{"xss-script", "Cross-site scripting", regexp.MustCompile(`(?i)(<script\b|javascript:|onerror\s*=|onload\s*=|<img[^>]+src[^>]+=[^>]*onerror|document\.cookie|\beval\s*\()`)},
	{"path-traversal", "Path traversal", regexp.MustCompile(`(\.\./|\.\.\\|/etc/passwd|/etc/shadow|c:\\windows|\bboot\.ini\b|%2e%2e%2f)`)},
	{"cmd-injection", "Command injection", regexp.MustCompile("(?i)(;\\s*(cat|ls|wget|curl|nc|bash|sh)\\s|\\$\\(.*\\)|`.*`|\\|\\s*(nc|bash|sh)\\b)")},
	{"scanner-probe", "Scanner / probe", regexp.MustCompile(`(?i)(/wp-login\.php|/wp-admin|/\.env\b|/\.git/|/phpmyadmin|/xmlrpc\.php|/vendor/phpunit|/actuator/|/\.aws/|/config\.json\b)`)},
}

/* ─────────── bot protection ─────────── */

// goodBots are verified crawlers + link-unfurlers we always allow (so SEO and
// social previews keep working even with bot protection on).
var goodBots = regexp.MustCompile(`(?i)\b(googlebot|google-inspectiontool|bingbot|slurp|duckduckbot|baiduspider|yandexbot|applebot|facebookexternalhit|facebot|twitterbot|linkedinbot|slackbot|discordbot|telegrambot|whatsapp|pinterest|chrome-lighthouse|uptimerobot|pingdom)\b`)

// badBots are scrapers, scanners and automation tools we block when bot
// protection is enabled. Deliberately excludes generic mobile/app HTTP stacks
// (okhttp, java, axios) to avoid breaking legitimate API/mobile clients.
var badBots = regexp.MustCompile(`(?i)(curl/|wget|python-requests|python-urllib|libwww-perl|httpx|aiohttp|scrapy|node-fetch|go-http-client|mechanize|httrack|phantomjs|headlesschrome|selenium|puppeteer|playwright|masscan|zgrab|nmap|nikto|sqlmap|wpscan|dirbuster|gobuster|feroxbuster|semrushbot|ahrefsbot|mj12bot|dotbot|bytespider|petalbot|dataforseobot|censys|crawler4j|scrapbot|harvest|extractor)`)

// ClassifyBot inspects the User-Agent. Returns ("good", true) for verified
// crawlers, ("bad", true) for scrapers/scanners/automation (and empty UAs),
// and ("", false) for normal browser traffic.
func ClassifyBot(ua string) (kind string, isBot bool) {
	u := strings.TrimSpace(ua)
	// Sixter's own CLI/tooling (uploads, pushes, the tunnel client) is never a
	// bot, even though it's an automated HTTP client. It identifies itself with
	// a "Sixter" user-agent so bot protection never blocks a legitimate deploy.
	if strings.Contains(strings.ToLower(u), "sixter") {
		return "", false
	}
	if u != "" && goodBots.MatchString(u) {
		return "good", true
	}
	if u == "" || badBots.MatchString(u) {
		return "bad", true
	}
	return "", false
}

/* ─────────── decision + event ─────────── */

// Decision is the firewall's verdict for one request.
type Decision struct {
	Blocked  bool
	Reason   string // e.g. "SQL injection", "rate limit", "rule: block ip"
	Category string // grouping for stats
	Status   int    // HTTP status to return (403, 429)
}

var allow = Decision{Blocked: false}

// Event is a recorded firewall action (the transparent log).
type Event struct {
	Time     time.Time `json:"time"`
	IP       string    `json:"ip"`
	Method   string    `json:"method"`
	Path     string    `json:"path"`
	Reason   string    `json:"reason"`
	Category string    `json:"category"`
	Status   int       `json:"status"`
}

/* ─────────── per-IP rate limiter + auto-ban ─────────── */

type ipState struct {
	hits     []time.Time
	strikes  int
	bannedTo time.Time
}

/* ─────────── engine (one per subdomain) ─────────── */

// Engine is the firewall for a single subdomain.
type Engine struct {
	mu       sync.Mutex
	settings Settings
	cidrs    []parsedRule // pre-parsed IP rules
	ips      map[string]*ipState

	// stats
	totalAllowed int64
	totalBlocked int64
	byCategory   map[string]int64
	events       []Event // ring buffer (most-recent-last)
}

type parsedRule struct {
	rule Rule
	ipn  *net.IPNet
	ip   net.IP
}

// New builds an engine with the given settings.
func New(s Settings) *Engine {
	e := &Engine{
		ips:        map[string]*ipState{},
		byCategory: map[string]int64{},
	}
	e.SetRules(s)
	return e
}

// SetRules hot-swaps the configuration.
func (e *Engine) SetRules(s Settings) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.settings = s
	e.cidrs = e.cidrs[:0]
	for _, r := range s.Rules {
		if r.Field != FieldIP || !r.Enabled {
			continue
		}
		pr := parsedRule{rule: r}
		if strings.Contains(r.Value, "/") {
			if _, ipn, err := net.ParseCIDR(r.Value); err == nil {
				pr.ipn = ipn
			}
		} else {
			pr.ip = net.ParseIP(r.Value)
		}
		e.cidrs = append(e.cidrs, pr)
	}
}

// Settings returns a copy of the current config.
func (e *Engine) Settings() Settings {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.settings
}

// BanIP manually bans an IP for the given duration. Inspect blocks it at the
// edge until the ban expires (in-memory; survives until gateway restart).
func (e *Engine) BanIP(ip string, d time.Duration) {
	if net.ParseIP(ip) == nil || d <= 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.ips[ip]
	if st == nil {
		st = &ipState{}
		e.ips[ip] = st
	}
	st.bannedTo = time.Now().Add(d)
}

// buildHaystack assembles the text the WAF scans: the raw request plus its
// URL-decoded forms (up to two passes) so percent-encoded attacks — e.g.
// ..%2f..%2fetc%2fpasswd, or double-encoded %252e — are still caught. The
// result is lower-cased so signatures match regardless of case.
func buildHaystack(path, query, ua string) string {
	var b strings.Builder
	b.WriteString(path)
	b.WriteByte('?')
	b.WriteString(query)
	b.WriteByte(' ')
	b.WriteString(ua)

	cur := path + "?" + query
	for i := 0; i < 2; i++ {
		dec, err := url.QueryUnescape(cur)
		if err != nil || dec == cur {
			break
		}
		b.WriteByte(' ')
		b.WriteString(dec)
		cur = dec
	}
	return strings.ToLower(b.String())
}

// Classify runs the WAF attack signatures against a request and returns the
// matched category ("" if clean). Exported for log-based detection elsewhere
// (e.g. the repo-server scanning each site's nginx access log).
func Classify(path, query, ua string) (category string, matched bool) {
	hay := buildHaystack(path, query, ua)
	for _, sig := range signatures {
		if sig.re.MatchString(hay) {
			return sig.kind, true
		}
	}
	return "", false
}

// Inspect is the hot path: called for every proxied request.
func (e *Engine) Inspect(ip, method, path, query, ua, country string) Decision {
	e.mu.Lock()
	defer e.mu.Unlock()

	s := e.settings
	if !s.Enabled {
		e.totalAllowed++
		return allow
	}

	now := time.Now()

	// 0. existing ban?
	if st := e.ips[ip]; st != nil && now.Before(st.bannedTo) {
		return e.block(ip, method, path, "IP temporarily banned", "Auto-ban", 403, now)
	}

	// 1. user rules (first match wins; allow short-circuits)
	for _, pr := range e.cidrs {
		if e.ipMatches(pr, ip) {
			if pr.rule.Action == ActionAllow {
				e.totalAllowed++
				return allow
			}
			return e.block(ip, method, path, "rule: block "+pr.rule.Value, "Custom rule", 403, now)
		}
	}
	for _, r := range s.Rules {
		if !r.Enabled || r.Field == FieldIP {
			continue
		}
		if e.nonIPRuleMatches(r, method, path, ua, country) {
			if r.Action == ActionAllow {
				e.totalAllowed++
				return allow
			}
			return e.block(ip, method, path, "rule: "+string(r.Field)+" "+r.Value, "Custom rule", 403, now)
		}
	}

	// 2. WAF attack signatures (matched against the NORMALIZED request)
	if s.WAF {
		hay := buildHaystack(path, query, ua)
		for _, sig := range signatures {
			if sig.re.MatchString(hay) {
				e.strike(ip, now)
				return e.block(ip, method, path, sig.name, sig.kind, 403, now)
			}
		}
	}

	// 2b. bot protection — block scrapers, scanners and automation tools
	// (verified search engines + social unfurlers are always allowed).
	if s.BotProtection {
		if kind, isBot := ClassifyBot(ua); isBot && kind == "bad" {
			e.strike(ip, now)
			return e.block(ip, method, path, "automated client blocked", "Bot", 403, now)
		}
	}

	// 3. per-IP rate limit
	if s.RateLimitPerMin > 0 {
		st := e.ips[ip]
		if st == nil {
			st = &ipState{}
			e.ips[ip] = st
		}
		cutoff := now.Add(-time.Minute)
		kept := st.hits[:0]
		for _, t := range st.hits {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		st.hits = append(kept, now)
		if len(st.hits) > s.RateLimitPerMin {
			e.strikeState(st, now)
			return e.block(ip, method, path, "rate limit exceeded", "Rate limit", 429, now)
		}
	}

	e.totalAllowed++
	return allow
}

func (e *Engine) ipMatches(pr parsedRule, ip string) bool {
	pip := net.ParseIP(ip)
	if pip == nil {
		return false
	}
	if pr.ipn != nil {
		return pr.ipn.Contains(pip)
	}
	return pr.ip != nil && pr.ip.Equal(pip)
}

func (e *Engine) nonIPRuleMatches(r Rule, method, path, ua, country string) bool {
	v := strings.ToLower(r.Value)
	switch r.Field {
	case FieldPath:
		return strings.Contains(strings.ToLower(path), v)
	case FieldMethod:
		return strings.EqualFold(method, r.Value)
	case FieldUA:
		return strings.Contains(strings.ToLower(ua), v)
	case FieldCountry:
		return strings.EqualFold(country, r.Value)
	}
	return false
}

// strike increments an IP's strike count and bans it past the threshold.
func (e *Engine) strike(ip string, now time.Time) {
	st := e.ips[ip]
	if st == nil {
		st = &ipState{}
		e.ips[ip] = st
	}
	e.strikeState(st, now)
}

func (e *Engine) strikeState(st *ipState, now time.Time) {
	if !e.settings.AutoBan {
		return
	}
	st.strikes++
	if st.strikes >= 5 {
		st.bannedTo = now.Add(15 * time.Minute)
		st.strikes = 0
	}
}

func (e *Engine) block(ip, method, path, reason, category string, status int, now time.Time) Decision {
	e.totalBlocked++
	e.byCategory[category]++
	e.events = append(e.events, Event{
		Time: now, IP: ip, Method: method, Path: path,
		Reason: reason, Category: category, Status: status,
	})
	if len(e.events) > 500 {
		e.events = e.events[len(e.events)-500:]
	}
	return Decision{Blocked: true, Reason: reason, Category: category, Status: status}
}

type Stats struct {
	Allowed    int64            `json:"allowed"`
	Blocked    int64            `json:"blocked"`
	ByCategory map[string]int64 `json:"by_category"`
	BannedIPs  int              `json:"banned_ips"`
	Recent     []Event          `json:"recent"`
}

// Stats returns current counters + recent events (newest first).
func (e *Engine) Stats(limit int) Stats {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now()
	banned := 0
	for _, st := range e.ips {
		if now.Before(st.bannedTo) {
			banned++
		}
	}

	cat := make(map[string]int64, len(e.byCategory))
	for k, v := range e.byCategory {
		cat[k] = v
	}

	n := len(e.events)
	if limit <= 0 || limit > n {
		limit = n
	}
	recent := make([]Event, 0, limit)
	for i := n - 1; i >= 0 && len(recent) < limit; i-- {
		recent = append(recent, e.events[i])
	}

	return Stats{
		Allowed:    e.totalAllowed,
		Blocked:    e.totalBlocked,
		ByCategory: cat,
		BannedIPs:  banned,
		Recent:     recent,
	}
}
