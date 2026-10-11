import { useState, useEffect, useRef } from 'react'
import QRCode from 'qrcode'
import { Ic, useRoute, api, Copy, Reveal } from './shared.jsx'
import { AdminDashboard } from './admin.jsx'

const IS_ADMIN_HOST = typeof window !== 'undefined' && window.location.hostname.startsWith('admin.')
import { Dashboard } from './console.jsx'
import { Docs } from './docs.jsx'
import PremiumLanding from './components/PremiumLanding.jsx'
import MemberProfile from './components/MemberProfile.jsx'

/* ═══════════ nav ═══════════ */

function Nav({ nav, me, onLogout }) {
  const goSection = (id) => {
    if (window.location.pathname !== '/') nav('/')
    setTimeout(() => document.getElementById(id)?.scrollIntoView({ behavior: 'smooth' }), 60)
  }
  return (
    <nav className="nav">
      <div className="nav-logo" onClick={() => nav('/')}>
        <img src="/sixter-logo.png" alt="Sixter" />
      </div>
      <div className="nav-links">
        <button className="nav-link" onClick={() => goSection('features')}>Features</button>
        <button className="nav-link" onClick={() => goSection('download')}>Download</button>
        <button className="nav-link" onClick={() => nav('/docs')}>Docs</button>
      </div>
      <div className="nav-right">
        {me ? (
          <>
            <button className="btn ghost" onClick={() => nav('/dashboard')}>Console</button>
            <button className="btn" onClick={onLogout}>Log out</button>
          </>
        ) : (
          <>
            <button className="btn ghost" onClick={() => nav('/login')}>Log in</button>
            <button className="btn primary" onClick={() => nav('/signup')}>Sign up</button>
          </>
        )}
      </div>
    </nav>
  )
}

/* ═══════════ globe ═══════════ */

// Glowing network globe — pure SVG, self-contained (no WebGL, no CDN, no content).
// Dark sphere + glowing lat/long grid + pulsing network nodes + flowing data arcs
// + atmosphere halo + orbit comets. On-theme for "Secure Tunnel Network" and
// renders identically everywhere. Everything just glows.
function Globe() {
  return (
    <div className="hero-globe">
      <div className="globe-stage">
        <svg className="globe-svg" viewBox="0 0 460 460" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
          <defs>
            <radialGradient id="sphereG" cx="40%" cy="34%" r="72%">
              <stop offset="0%" stopColor="#2a2350" />
              <stop offset="55%" stopColor="#161033" />
              <stop offset="100%" stopColor="#0a0818" />
            </radialGradient>
            <radialGradient id="atmG" cx="50%" cy="50%" r="50%">
              <stop offset="58%" stopColor="rgba(139,133,224,0)" />
              <stop offset="82%" stopColor="rgba(139,133,224,0.45)" />
              <stop offset="100%" stopColor="rgba(139,133,224,0)" />
            </radialGradient>
            <linearGradient id="arcG" x1="0" y1="0" x2="1" y2="1">
              <stop offset="0%" stopColor="#c4b9ff" />
              <stop offset="100%" stopColor="#57a0f5" />
            </linearGradient>
            <filter id="gridGlow" x="-30%" y="-30%" width="160%" height="160%">
              <feGaussianBlur stdDeviation="1.4" result="b" /><feMerge><feMergeNode in="b" /><feMergeNode in="SourceGraphic" /></feMerge>
            </filter>
            <filter id="oglow" x="-80%" y="-80%" width="260%" height="260%">
              <feGaussianBlur stdDeviation="4" result="b" /><feMerge><feMergeNode in="b" /><feMergeNode in="SourceGraphic" /></feMerge>
            </filter>
            <clipPath id="globeClip"><circle cx="230" cy="230" r="150" /></clipPath>
          </defs>

          {/* atmosphere halo */}
          <circle className="atm-pulse" cx="230" cy="230" r="182" fill="url(#atmG)" />

          {/* sphere body */}
          <circle cx="230" cy="230" r="150" fill="url(#sphereG)" stroke="rgba(170,164,242,0.55)" strokeWidth="1.3" />

          <g clipPath="url(#globeClip)">
            {/* glowing lat/long grid */}
            <g stroke="rgba(150,135,255,0.34)" strokeWidth="0.9" fill="none" filter="url(#gridGlow)">
              <line x1="80" y1="230" x2="380" y2="230" stroke="rgba(150,135,255,0.42)" />
              <ellipse cx="230" cy="230" rx="150" ry="48" />
              <ellipse cx="230" cy="230" rx="150" ry="100" />
              <path d="M86 178 A 150 150 0 0 1 374 178" />
              <path d="M86 282 A 150 150 0 0 0 374 282" />
              <path d="M104 140 A 150 150 0 0 1 356 140" />
              <path d="M104 320 A 150 150 0 0 0 356 320" />
              <line x1="230" y1="80" x2="230" y2="380" stroke="rgba(150,135,255,0.42)" />
              <ellipse cx="230" cy="230" rx="50" ry="150" />
              <ellipse cx="230" cy="230" rx="100" ry="150" />
            </g>

            {/* flowing data arcs between nodes */}
            <g fill="none" stroke="url(#arcG)" strokeWidth="1.6" filter="url(#gridGlow)" strokeLinecap="round">
              <path className="arc-flow" d="M95 195 Q 230 70 368 215" />
              <path className="arc-flow a2" d="M100 275 Q 230 395 360 260" />
              <path className="arc-flow a3" d="M120 150 Q 300 220 330 330" />
              <path className="arc-flow a4" d="M140 330 Q 230 170 352 165" />
            </g>

            {/* pulsing network nodes */}
            <g filter="url(#oglow)">
              <circle className="node" cx="180" cy="168" r="3.6" fill="#cabfff" />
              <circle className="node n2" cx="300" cy="175" r="3.6" fill="#aaa4f2" />
              <circle className="node n3" cx="168" cy="250" r="3.2" fill="#57a0f5" />
              <circle className="node n4" cx="300" cy="255" r="3.2" fill="#cabfff" />
              <circle className="node n2" cx="200" cy="290" r="3" fill="#aaa4f2" />
              <circle className="node n3" cx="330" cy="200" r="3" fill="#8b85e0" />
              <circle className="node n4" cx="322" cy="200" r="2.6" fill="#57a0f5" />
              <circle className="node" cx="240" cy="200" r="2.6" fill="#cabfff" />
            </g>
          </g>

          {/* orbiting rings + comet dots */}
          <ellipse className="orbit" cx="230" cy="230" rx="212" ry="80" stroke="rgba(170,164,242,0.34)" strokeWidth="1" strokeDasharray="2 10" transform="rotate(-20 230 230)" />
          <ellipse className="orbit" cx="230" cy="230" rx="218" ry="62" stroke="rgba(87,160,245,0.28)" strokeWidth="1" strokeDasharray="2 11" transform="rotate(16 230 230)" />
          <g transform="rotate(-20 230 230)">
            <circle r="4.5" fill="#aaa4f2" filter="url(#oglow)">
              <animateMotion dur="9s" repeatCount="indefinite" path="M18,230 a212,80 0 1,1 424,0 a212,80 0 1,1 -424,0" />
            </circle>
          </g>
          <g transform="rotate(16 230 230)">
            <circle r="4" fill="#57a0f5" filter="url(#oglow)">
              <animateMotion dur="12s" begin="2s" repeatCount="indefinite" path="M12,230 a218,62 0 1,1 436,0 a218,62 0 1,1 -436,0" />
            </circle>
          </g>
        </svg>
      </div>
    </div>
  )
}

/* ═══════════ tunnel flow diagram ═══════════ */

function TunnelFlow() {
  return (
    <div className="flow-wrap">
      <svg viewBox="0 0 920 300" fill="none" fontFamily="Consolas, Menlo, monospace">
        <defs>
          <filter id="fglow" x="-60%" y="-60%" width="220%" height="220%">
            <feGaussianBlur stdDeviation="4" result="b" /><feMerge><feMergeNode in="b" /><feMergeNode in="SourceGraphic" /></feMerge>
          </filter>
        </defs>
        <path id="fp1" d="M168 132 C 270 70, 360 70, 430 122" stroke="#232338" strokeWidth="2" />
        <path id="fp2" d="M490 122 C 560 70, 650 70, 752 132" stroke="#232338" strokeWidth="2" />
        <path id="fp3" d="M752 168 C 650 230, 560 230, 490 178" stroke="#232338" strokeWidth="2" />
        <path id="fp4" d="M430 178 C 360 230, 270 230, 168 168" stroke="#232338" strokeWidth="2" />
        <text x="300" y="66" fill="#615f80" fontSize="10" textAnchor="middle">https request</text>
        <text x="625" y="66" fill="#615f80" fontSize="10" textAnchor="middle">encrypted tunnel</text>
        <text x="625" y="246" fill="#615f80" fontSize="10" textAnchor="middle">response</text>
        <text x="300" y="246" fill="#615f80" fontSize="10" textAnchor="middle">back to visitor</text>
        <circle r="5" fill="#57a0f5" filter="url(#fglow)"><animateMotion dur="2.4s" repeatCount="indefinite"><mpath href="#fp1" /></animateMotion></circle>
        <circle r="4" fill="#57a0f5" opacity="0.8" filter="url(#fglow)"><animateMotion dur="2.4s" begin="1.2s" repeatCount="indefinite"><mpath href="#fp1" /></animateMotion></circle>
        <circle r="5" fill="#8b85e0" filter="url(#fglow)"><animateMotion dur="2.4s" begin="0.6s" repeatCount="indefinite"><mpath href="#fp2" /></animateMotion></circle>
        <circle r="4" fill="#8b85e0" opacity="0.8" filter="url(#fglow)"><animateMotion dur="2.4s" begin="1.8s" repeatCount="indefinite"><mpath href="#fp2" /></animateMotion></circle>
        <circle r="5" fill="#34c77b" filter="url(#fglow)"><animateMotion dur="2.4s" begin="0.3s" repeatCount="indefinite"><mpath href="#fp3" /></animateMotion></circle>
        <circle r="5" fill="#34c77b" filter="url(#fglow)"><animateMotion dur="2.4s" begin="0.9s" repeatCount="indefinite"><mpath href="#fp4" /></animateMotion></circle>
        <circle r="4" fill="#34c77b" opacity="0.8" filter="url(#fglow)"><animateMotion dur="2.4s" begin="1.5s" repeatCount="indefinite"><mpath href="#fp4" /></animateMotion></circle>
        <rect x="40" y="108" width="128" height="84" rx="14" fill="#11111f" stroke="#32324e" />
        <circle cx="62" cy="128" r="3.5" fill="#ff5f57" /><circle cx="74" cy="128" r="3.5" fill="#febc2e" /><circle cx="86" cy="128" r="3.5" fill="#28c840" />
        <text x="104" y="160" fill="#edebf8" fontSize="13" fontWeight="bold" textAnchor="middle">visitor</text>
        <text x="104" y="178" fill="#615f80" fontSize="9" textAnchor="middle">anywhere on earth</text>
        <circle cx="460" cy="150" r="36" fill="#171728" stroke="#8b85e0" strokeWidth="1.5" />
        <circle cx="460" cy="150" r="36" fill="none" stroke="#8b85e0" strokeWidth="1">
          <animate attributeName="r" values="36;66" dur="2.8s" repeatCount="indefinite" /><animate attributeName="opacity" values="0.6;0" dur="2.8s" repeatCount="indefinite" />
        </circle>
        <circle cx="460" cy="150" r="36" fill="none" stroke="#8b85e0" strokeWidth="1">
          <animate attributeName="r" values="36;66" dur="2.8s" begin="1.4s" repeatCount="indefinite" /><animate attributeName="opacity" values="0.6;0" dur="2.8s" begin="1.4s" repeatCount="indefinite" />
        </circle>
        <text x="460" y="147" fill="#edebf8" fontSize="13" fontWeight="bold" textAnchor="middle">SIXTER</text>
        <text x="460" y="164" fill="#9b98b8" fontSize="9" textAnchor="middle">edge</text>
        <text x="460" y="280" fill="#aaa4f2" fontSize="12" textAnchor="middle">https://you.sixter.in</text>
        <rect x="752" y="108" width="128" height="84" rx="14" fill="#11111f" stroke="#32324e" />
        <text x="816" y="138" fill="#34c77b" fontSize="11" textAnchor="middle">&#10095; sixter tunnel</text>
        <text x="816" y="158" fill="#9b98b8" fontSize="11" textAnchor="middle">localhost:3000</text>
        <text x="816" y="178" fill="#615f80" fontSize="9" textAnchor="middle">your machine</text>
      </svg>
    </div>
  )
}

/* ═══════════ firewall shield demo ═══════════ */

function FirewallDemo() {
  const attacks = [
    { y: 70, label: "' OR 1=1 --", delay: '0s' },
    { y: 160, label: '<script>', delay: '1.1s' },
    { y: 250, label: '../etc/passwd', delay: '2.2s' },
    { y: 115, label: '/wp-login.php', delay: '3.3s' },
  ]
  return (
    <div className="fw-demo">
      <svg viewBox="0 0 920 320" fill="none" fontFamily="Consolas, Menlo, monospace">
        <defs>
          <radialGradient id="shieldg" cx="50%" cy="45%" r="60%">
            <stop offset="0%" stopColor="rgba(139,133,224,0.35)" /><stop offset="100%" stopColor="rgba(139,133,224,0)" />
          </radialGradient>
          <filter id="fwglow" x="-60%" y="-60%" width="220%" height="220%">
            <feGaussianBlur stdDeviation="3.5" result="b" /><feMerge><feMergeNode in="b" /><feMergeNode in="SourceGraphic" /></feMerge>
          </filter>
        </defs>
        <ellipse cx="600" cy="160" rx="60" ry="150" fill="url(#shieldg)" />
        <line x1="600" y1="20" x2="600" y2="300" stroke="#8b85e0" strokeWidth="1.5" strokeDasharray="4 6" opacity="0.5" />
        <g filter="url(#fwglow)">
          <path d="M600 110 l30 12 v22 c0 22 -14 34 -30 42 c-16 -8 -30 -20 -30 -42 v-22 z" fill="#171728" stroke="#8b85e0" strokeWidth="1.6" />
          <path d="M590 150 l7 7 14 -15" stroke="#34c77b" strokeWidth="2.4" fill="none" />
        </g>
        <text x="600" y="220" fill="#aaa4f2" fontSize="11" textAnchor="middle">SIXTER firewall</text>
        <rect x="760" y="128" width="138" height="66" rx="13" fill="#11111f" stroke="#2f384a" />
        <text x="829" y="156" fill="#34c77b" fontSize="11" textAnchor="middle">your app</text>
        <text x="829" y="174" fill="#615f80" fontSize="9" textAnchor="middle">protected</text>
        {attacks.map((a, i) => (
          <g key={i}>
            <g filter="url(#fwglow)">
              <circle r="5" fill="#ff5d75">
                <animateMotion dur="3.6s" begin={a.delay} repeatCount="indefinite" keyPoints="0;0.62;0.62;0.78" keyTimes="0;0.45;0.62;1" calcMode="linear" path={`M40 ${a.y} L560 160 L560 160 L600 300`} />
                <animate attributeName="fill" values="#ff5d75;#ff5d75;#5a2230" keyTimes="0;0.62;1" dur="3.6s" begin={a.delay} repeatCount="indefinite" />
                <animate attributeName="opacity" values="1;1;0" keyTimes="0;0.7;1" dur="3.6s" begin={a.delay} repeatCount="indefinite" />
              </circle>
            </g>
            <text x="40" y={a.y - 12} fill="#ff7a8c" fontSize="10" textAnchor="middle">{a.label}
              <animate attributeName="opacity" values="0;1;1;0" keyTimes="0;0.1;0.5;0.62" dur="3.6s" begin={a.delay} repeatCount="indefinite" />
            </text>
          </g>
        ))}
        <text x="40" y="305" fill="#615f80" fontSize="10" textAnchor="middle">attacks</text>
        <g filter="url(#fwglow)"><circle r="5" fill="#34c77b"><animateMotion dur="3.6s" begin="0.5s" repeatCount="indefinite" path="M40 160 L900 160" /></circle></g>
        <text x="180" y="148" fill="#34c77b" fontSize="10" textAnchor="middle">GET /api/users ✓
          <animate attributeName="opacity" values="0;1;1;0" keyTimes="0;0.1;0.4;0.55" dur="3.6s" begin="0.5s" repeatCount="indefinite" />
        </text>
      </svg>
    </div>
  )
}

/* ═══════════ typing terminals ═══════════ */

function TypingTerm({ lines, title, loop }) {
  const [shown, setShown] = useState(0)
  useEffect(() => {
    if (shown >= lines.length) {
      if (!loop) return
      const r = setTimeout(() => setShown(0), 2600)
      return () => clearTimeout(r)
    }
    const id = setTimeout(() => setShown(shown + 1), shown === 0 ? 550 : 820)
    return () => clearTimeout(id)
  }, [shown, lines.length, loop])
  return (
    <div className="term-demo">
      <div className="term-demo-bar">
        <span className="tdot r" /><span className="tdot y" /><span className="tdot g" />
        <span className="term-demo-title">{title}</span>
      </div>
      <div className="term-demo-body">
        {lines.slice(0, shown).map((l, i) => (
          <div key={i}>
            {l.t === 'cmd' && (<><span className="tp">❯ </span><span className="tc">{l.s}</span></>)}
            {l.t === 'out' && <span className="to">{l.s}</span>}
            {l.t === 'th' && <span className="th">{l.s}</span>}
            {l.t === 'hi' && <span className="th">{l.s}</span>}
            {l.t === 'err' && <span style={{ color: '#ff5d75' }}>{l.s}</span>}
          </div>
        ))}
        {shown < lines.length && <span className="cursor" />}
      </div>
    </div>
  )
}

const TUNNEL_LINES = [
  { t: 'cmd', s: 'sixter login' },
  { t: 'out', s: '  ✓ Logged in as you@example.com' },
  { t: 'out', s: '  ✓ tunnel : https://you.sixter.in' },
  { t: 'cmd', s: 'sixter tunnel -port 3000' },
  { t: 'hi', s: 'Tunnel live → https://you.sixter.in' },
  { t: 'out', s: 'Forwarding all traffic to → http://127.0.0.1:3000' },
]

const SSH_LINES = [
  { t: 'cmd', s: 'ssh myapp@you.sixter.in -p 2222' },
  { t: 'out', s: 'Sixter SSH — project: myapp (jailed to folder)' },
  { t: 'cmd', s: 'ls' },
  { t: 'th', s: 'index.js   package.json   src   .env' },
  { t: 'cmd', s: 'cd ..' },
  { t: 'err', s: 'sixter: cannot leave project folder' },
]

/* ═══════════ landing ═══════════ */

const FEATURES = [
  { ic: 'zap', h: 'Tunnels in one command', p: 'Expose any local port on your own subdomain in under a second. HTTP and WebSocket, full duplex, zero configuration files.' },
  { ic: 'shieldCheck', h: 'Firewall at the edge', p: 'Every request is inspected before it reaches you. SQL injection, XSS, traversal, scanners and floods are blocked automatically.' },
  { ic: 'upload', h: 'Push projects anywhere', p: 'sixter repo ships your whole folder to your server in seconds — node_modules, build junk and hidden files excluded automatically.' },
  { ic: 'box', h: 'Auto-deploy on every upload', p: 'Push a project and Sixter detects the type, builds it, restarts the service and health-checks it — automatically. A broken release rolls itself back to the last working version.' },
  { ic: 'list', h: 'Versioned deploys + 1-click rollback', p: 'Every upload is snapshotted. Roll back to any earlier version instantly from the dashboard — your current state is saved first, then restored and redeployed live.' },
  { ic: 'shield', h: 'Per-site firewall & safety score', p: 'Every hosted site gets its own firewall view: a live safety score, attack feed, top offenders and one-click IP blocking — see exactly how protected each site is.' },
  { ic: 'globe', h: 'Custom domains + HTTPS', p: 'Bring your own domain. Sixter writes the nginx config, verifies your DNS record and provisions Let’s Encrypt certificates automatically.' },
  { ic: 'terminal', h: 'SSH & web terminal', p: 'A real shell into every deployed project — from the dashboard or over SSH, jailed to the project folder. Install deps, run migrations, ship.' },
  { ic: 'activity', h: 'See every request', p: 'A live, packet-level view of traffic hitting your server: source IPs, latency, status codes, payload sizes — like wireshark for HTTP.' },
  { ic: 'key', h: 'Device login for servers', p: 'SSH-only VPS? Run sixter login, approve from your phone. OAuth-style device flow — no browser needed on the machine.' },
  { ic: 'lock', h: 'Secure by default', p: 'PBKDF2-hashed passwords, one-time tokens stored only as hashes, per-IP rate limits and auto-banning — the boring security work is done.' },
]

const FLOW = [
  { ic: 'upload', t: 'You push your project', d: 'sixter repo ships your whole folder to your server in seconds.' },
  { ic: 'box', t: 'Sixter detects the type', d: 'Static, Node, Python or Go — recognised automatically, no config.' },
  { ic: 'zap', t: 'Build & run', d: 'Dependencies installed, built, kept alive under systemd, health-checked.' },
  { ic: 'server', t: 'nginx configured', d: 'A reverse proxy or static root is written and reloaded for you.' },
  { ic: 'lock', t: 'HTTPS provisioned', d: 'A Let’s Encrypt certificate is issued and auto-renews — green padlock.' },
  { ic: 'shield', t: 'Firewall & access control', d: 'WAF signatures, rate limiting and per-IP banning guard every request.' },
  { ic: 'globe', t: 'Live on your domain', d: 'Your site is online — versioned, with one-click rollback if a release breaks.' },
]

function DeployFlow() {
  return (
    <section className="section" id="workflow">
      <div className="section-inner">
        <span className="eyebrow">How a deploy flows</span>
        <h2>From your machine to a live, secured site</h2>
        <p className="sub">One push triggers the entire pipeline — automatically.</p>
        <Reveal>
          <div className="workflow">
            <div className="wf-line"><span className="wf-spark" /></div>
            {FLOW.map((s, i) => (
              <div className="wf-step" style={{ '--d': i * 0.45 + 's' }} key={i}>
                <div className="wf-dot"><Ic n={s.ic} size={18} /></div>
                <div className="wf-card">
                  <b>{s.t}</b>
                  <span>{s.d}</span>
                </div>
              </div>
            ))}
          </div>
        </Reveal>
      </div>
    </section>
  )
}

function Landing({ nav }) {
  return (
    <>
      <section className="hero-section">
        <div className="hero-aurora" aria-hidden="true" />
        <header className="hero">
          <div className="hero-left">
            <div className="hero-badge"><Ic n="zap" size={14} /> <b>Sixter 2.0</b> — firewall, SSH access, custom domains & auto-HTTPS</div>
            <h1>Secure Tunnel<br /><span className="grad">Network</span></h1>
            <p className="lead">High performance and high availability for apps and APIs. Expose localhost, deploy projects, bind your own domain — all from one tiny CLI, protected at our edge.</p>
            <div className="hero-ctas">
              <button className="btn primary big glow" onClick={() => nav('/signup')}>Get your subdomain <Ic n="arrow" size={16} /></button>
              <button className="btn big" onClick={() => document.getElementById('download')?.scrollIntoView({ behavior: 'smooth' })}>Download CLI</button>
            </div>
          </div>
          <Globe />
        </header>
      </section>

      <div className="stats-strip">
        <div className="stats-inner">
          <div className="stat"><b>&lt;1s</b><span>tunnel start time</span></div>
          <div className="stat"><b>2</b><span>free subdomains per account</span></div>
          <div className="stat"><b>WAF</b><span>firewall on every request</span></div>
          <div className="stat"><b>Auto</b><span>HTTPS via Let&apos;s Encrypt</span></div>
        </div>
      </div>

      <section className="section" style={{ paddingBottom: 20 }}>
        <div className="section-inner">
          <span className="eyebrow">Live</span>
          <h2>From localhost to the world</h2>
          <p className="sub">Watch a request travel: visitor → Sixter edge → your machine, and back.</p>
          <Reveal><TunnelFlow /></Reveal>
        </div>
      </section>

      <DeployFlow />

      <section className="section" id="features">
        <div className="section-inner">
          <span className="eyebrow">Platform</span>
          <h2>Everything between your laptop and production</h2>
          <p className="sub">One small binary. Eight superpowers.</p>
          <div className="feat-grid">
            {FEATURES.map((f, i) => (
              <Reveal key={f.h} delay={i * 80}>
                <div className="feat">
                  <div className="ico"><Ic n={f.ic} /></div>
                  <h3>{f.h}</h3>
                  <p>{f.p}</p>
                </div>
              </Reveal>
            ))}
          </div>
        </div>
      </section>

      <section className="section" style={{ paddingTop: 10 }}>
        <div className="section-inner split">
          <div>
            <span className="eyebrow" style={{ textAlign: 'left' }}>Workflow</span>
            <h2>Ship from your terminal</h2>
            <p className="blurb">No YAML, no dashboard clicking marathons. Log in once and your credentials follow you — every tunnel and deploy is a single command.</p>
            <ul className="check-list">
              <li><Ic n="check" size={17} /><span><b>sixter login</b> — browser approval from any device, even for SSH-only servers</span></li>
              <li><Ic n="check" size={17} /><span><b>sixter tunnel</b> — your local port becomes a public URL with your name on it</span></li>
              <li><Ic n="check" size={17} /><span><b>sixter repo</b> — push entire projects to your VPS and manage them from a dashboard</span></li>
            </ul>
          </div>
          <Reveal delay={120}><TypingTerm lines={TUNNEL_LINES} title="you@laptop ~ %" /></Reveal>
        </div>
      </section>

      <section className="section" style={{ paddingTop: 10 }}>
        <div className="section-inner">
          <span className="eyebrow">Firewall</span>
          <h2>Attacks stop at our edge</h2>
          <p className="sub">Your traffic runs through Sixter, so we inspect every request before it reaches your machine. Bad ones never get through.</p>
          <Reveal><FirewallDemo /></Reveal>
          <div className="fw-legend">
            <span><i style={{ background: '#34c77b' }} /> clean request → forwarded</span>
            <span><i style={{ background: '#ff5d75' }} /> SQLi · XSS · traversal · scanners → blocked</span>
          </div>
        </div>
      </section>

      <section className="section" style={{ paddingTop: 10 }}>
        <div className="section-inner split">
          <Reveal delay={120}><TypingTerm lines={SSH_LINES} title="ssh — myapp" loop /></Reveal>
          <div>
            <span className="eyebrow" style={{ textAlign: 'left' }}>Shell access</span>
            <h2>SSH into any project</h2>
            <p className="blurb">Every deployed project gets its own SSH login and a web terminal — a real shell, not a fake textbox. Jailed to the project folder, so you (and your team) can never wander out.</p>
            <ul className="check-list">
              <li><Ic n="check" size={17} /><span><b>ssh myapp@you.sixter.in -p 2222</b> — land straight inside the project</span></li>
              <li><Ic n="check" size={17} /><span>full interactive shell — <b>ls</b>, <b>npm install</b>, <b>vim</b>, everything works</span></li>
              <li><Ic n="check" size={17} /><span>folder jail — <b>cd ..</b> bounces you back, automatically</span></li>
            </ul>
          </div>
        </div>
      </section>

      <section className="section" style={{ paddingTop: 10 }}>
        <div className="section-inner">
          <span className="eyebrow">Quickstart</span>
          <h2>Live in three steps</h2>
          <p className="sub">From zero to public URL in under a minute.</p>
          <div className="steps">
            <div className="step"><div className="num">01</div><h3>Log in once</h3><p>Authorize from any device. You get two permanent subdomains, credentials saved locally.</p><code>sixter login</code></div>
            <div className="step"><div className="num">02</div><h3>Open a tunnel</h3><p>Your local app becomes a public URL instantly — WebSockets included.</p><code>sixter tunnel -port 3000</code></div>
            <div className="step"><div className="num">03</div><h3>Deploy for real</h3><p>One command sets up a managed server on your VPS. Push a project and it auto-builds, health-checks and gets HTTPS — with rollback if it fails.</p><code>sudo sixter repo-server -setup</code></div>
          </div>
        </div>
      </section>

      <section className="section" id="download" style={{ paddingTop: 10 }}>
        <div className="section-inner">
          <span className="eyebrow">Download</span>
          <h2>Get Sixter</h2>
          <p className="sub">One binary, every platform. Current release: 2.0</p>
          <div className="dl-grid">
            <Reveal>
              <div className="dl-card">
                <div className="os-ico"><Ic n="windows" size={30} /></div>
                <h3>Windows</h3>
                <p>sixter-setup.exe · amd64</p>
                <a href="./" download><button className="btn primary" style={{ width: '100%' }}>Download for Windows (.exe)</button></a>
              </div>
            </Reveal>
            <Reveal delay={110}>
              <div className="dl-card">
                <div className="os-ico"><Ic n="linux" size={30} /></div>
                <h3>Linux</h3>
                <p>sixter_1.0.0.deb · amd64</p>
                <a href="/sixter_2.0.0_amd64.deb" download><button className="btn green" style={{ width: '100%' }}>Download for Linux (.deb)</button></a>
                <div className="cmd-row" style={{ marginTop: 16 }}>
                  <code>curl -fsSL https://sixter.in/sixter_2.0.0_amd64.deb -o sixter.deb && sudo dpkg -i sixter.deb</code>
                  <Copy text="curl -fsSL https://sixter.in/sixter_2.0.0_amd64.deb -o sixter.deb && sudo dpkg -i sixter.deb" />
                </div>
              </div>
            </Reveal>
          </div>
        </div>
      </section>

      <section className="section" style={{ paddingTop: 10 }}>
        <div className="cta-banner">
          <h2>Claim your subdomain</h2>
          <p>yourname.sixter.in is waiting. Free, permanent, protected.</p>
          <button className="btn primary big" onClick={() => nav('/signup')}>Create account <Ic n="arrow" size={16} /></button>
        </div>
      </section>

      <footer className="footer">
        <div className="footer-inner">
          <div>
            <img src="/sixter-logo.png" alt="Sixter" />
            <p className="tagline">Secure tunnel network.<br />High performance and high availability for apps and APIs.</p>
          </div>
          <div><h5>Product</h5>
            <button onClick={() => document.getElementById('features')?.scrollIntoView({ behavior: 'smooth' })}>Features</button>
            <button onClick={() => document.getElementById('download')?.scrollIntoView({ behavior: 'smooth' })}>Download</button>
            <button onClick={() => nav('/docs')}>Documentation</button>
          </div>
          <div><h5>Account</h5>
            <button onClick={() => nav('/login')}>Log in</button>
            <button onClick={() => nav('/signup')}>Sign up</button>
            <button onClick={() => nav('/dashboard')}>Console</button>
          </div>
          <div><h5>Developers</h5>
            <button onClick={() => nav('/docs')}>CLI reference</button>
            <button onClick={() => nav('/docs')}>Firewall</button>
            <button onClick={() => nav('/docs')}>Self-hosting</button>
          </div>
        </div>
        <div className="footer-bottom">
          <span>© {new Date().getFullYear()} Sixter — sixter.in</span>
          <span>Built for developers who ship.</span>
        </div>
      </footer>
    </>
  )
}

/* ═══════════ auth pages ═══════════ */

/* Sign in with Google / GitHub. The buttons only render for providers the
   gateway actually has credentials for (GET /api/auth/providers). */
/* A ?next= target is only honoured when it points at this apex or one of its
   subdomains (a repo dashboard bouncing its user here to sign in). Anything
   else is ignored, so this can't be used as an open redirect. */
function returnURL() {
  const raw = new URLSearchParams(window.location.search).get('next')
  if (!raw || raw.startsWith('/')) return ''
  try {
    const u = new URL(raw)
    const apex = window.location.hostname
    if (u.protocol === 'https:' && (u.hostname === apex || u.hostname.endsWith('.' + apex))) {
      return u.toString()
    }
  } catch { /* not a URL — ignore */ }
  return ''
}

function OAuthButtons({ providers, label = 'Continue with' }) {
  if (!providers || (!providers.google && !providers.github)) return null
  const go = (p) => {
    const ret = returnURL()
    window.location.href = `/api/auth/${p}/start` + (ret ? `?next=${encodeURIComponent(ret)}` : '')
  }
  return (
    <>
      <div className="oauth-row">
        {providers.google && (
          <button type="button" className="oauth-btn" onClick={() => go('google')}>
            <svg width="18" height="18" viewBox="0 0 48 48" aria-hidden="true">
              <path fill="#4285F4" d="M45.1 24.5c0-1.6-.1-3.2-.4-4.7H24v8.9h11.8c-.5 2.7-2 5-4.4 6.6v5.5h7.1c4.1-3.8 6.6-9.4 6.6-16.3z" />
              <path fill="#34A853" d="M24 46c5.9 0 10.9-2 14.5-5.2l-7.1-5.5c-2 1.3-4.5 2.1-7.4 2.1-5.7 0-10.5-3.8-12.2-9H4.5v5.7C8.1 41.3 15.5 46 24 46z" />
              <path fill="#FBBC05" d="M11.8 28.4c-.4-1.3-.7-2.7-.7-4.4s.3-3.1.7-4.4v-5.7H4.5C2.9 17.1 2 20.4 2 24s.9 6.9 2.5 10.1l7.3-5.7z" />
              <path fill="#EA4335" d="M24 10.5c3.2 0 6.1 1.1 8.4 3.3l6.3-6.3C34.9 4 29.9 2 24 2 15.5 2 8.1 6.7 4.5 13.9l7.3 5.7c1.7-5.2 6.5-9.1 12.2-9.1z" />
            </svg>
            {label} Google
          </button>
        )}
        {providers.github && (
          <button type="button" className="oauth-btn github" onClick={() => go('github')}>
            <svg width="18" height="18" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
              <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27s1.36.09 2 .27c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8z" />
            </svg>
            {label} GitHub
          </button>
        )}
      </div>
      <div className="oauth-sep">or</div>
    </>
  )
}

function Login({ nav, onAuthed, next }) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [providers, setProviders] = useState(null)
  // 2FA step — may also be handed over by an OAuth callback (?mfa=<challenge>)
  const [challenge, setChallenge] = useState(() => new URLSearchParams(window.location.search).get('mfa') || '')
  const [code, setCode] = useState('')
  const [useBackup, setUseBackup] = useState(false)

  useEffect(() => {
    api('/api/auth/providers').then(setProviders).catch(() => setProviders({}))
    const q = new URLSearchParams(window.location.search)
    const oe = q.get('oauth_error')
    if (oe) {
      setErr(oe)
      // drop the error from the URL but keep ?next= so the bounce still works
      q.delete('oauth_error')
      const rest = q.toString()
      window.history.replaceState({}, '', '/login' + (rest ? '?' + rest : ''))
    }
  }, [])

  // Where to land once authenticated: an external *.sixter.in return URL wins
  // (a repo dashboard sent us here), otherwise normal client-side routing.
  const done = (u) => {
    const ret = returnURL()
    if (ret) { window.location.href = ret; return }
    onAuthed(u); nav(next || '/dashboard')
  }

  const submit = async (e) => {
    e.preventDefault(); setBusy(true); setErr('')
    try {
      const res = await api('/api/login', { method: 'POST', body: JSON.stringify({ email, password }) })
      if (res && res.mfa_required) {
        setChallenge(res.challenge)        // move to the code step
      } else {
        done(res)
      }
    } catch (e2) { setErr(e2.message) }
    setBusy(false)
  }

  const verify = async (e) => {
    e.preventDefault(); setBusy(true); setErr('')
    try {
      const body = useBackup ? { challenge, backup_code: code } : { challenge, code }
      const u = await api('/api/2fa/verify', { method: 'POST', body: JSON.stringify(body) })
      done(u)
    } catch (e2) { setErr(e2.message) }
    setBusy(false)
  }

  if (challenge) {
    return (
      <div className="auth-wrap">
        <form className="auth-card" onSubmit={verify}>
          <img className="mark" src="/sixter.png" alt="" />
          <h1>Two-factor authentication</h1>
          <p className="sub">{useBackup ? 'Enter one of your backup codes.' : 'Enter the 6-digit code from your authenticator app.'}</p>
          <label className="field-label">{useBackup ? 'Backup code' : 'Authentication code'}</label>
          <input className="input code-input" inputMode={useBackup ? 'text' : 'numeric'} autoComplete="one-time-code"
                 placeholder={useBackup ? 'xxxxx-xxxxx' : '123456'} value={code}
                 onChange={(e) => setCode(e.target.value)} required autoFocus />
          {err && <div className="err-box">{err}</div>}
          <button className="btn primary mt" style={{ width: '100%' }} disabled={busy}>{busy ? <span className="spin" /> : 'Verify'}</button>
          <div className="auth-foot">
            <button type="button" className="linkbtn" onClick={() => { setUseBackup(!useBackup); setCode(''); setErr('') }}>
              {useBackup ? 'Use authenticator app instead' : "Can't access your app? Use a backup code"}
            </button>
          </div>
        </form>
      </div>
    )
  }

  return (
    <div className="auth-wrap">
      <form className="auth-card" onSubmit={submit}>
        <img className="mark" src="/sixter.png" alt="" />
        <h1>Welcome back</h1>
        <p className="sub">Log in to your Sixter account.</p>
        <OAuthButtons providers={providers} label="Continue with" />
        <label className="field-label">Email</label>
        <input className="input" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required autoFocus />
        <label className="field-label">Password</label>
        <input className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required />
        {err && <div className="err-box">{err}</div>}
        <button className="btn primary mt" style={{ width: '100%' }} disabled={busy}>{busy ? <span className="spin" /> : 'Log in'}</button>
        <div className="auth-foot">New to Sixter? <button type="button" className="linkbtn" onClick={() => nav('/signup')}>Create an account</button></div>
      </form>
    </div>
  )
}

// Forced 2FA enrollment — shown after login/signup until the account has 2FA on.
function Enroll2FA({ me, onDone, logout }) {
  const [secret, setSecret] = useState('')
  const [uri, setUri] = useState('')
  const [qr, setQr] = useState('')
  const [code, setCode] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [backupCodes, setBackupCodes] = useState(null)

  useEffect(() => {
    api('/api/2fa/setup', { method: 'POST' })
      .then((d) => {
        setSecret(d.secret); setUri(d.otpauth_uri)
        QRCode.toDataURL(d.otpauth_uri, { margin: 1, width: 220, color: { dark: '#1a1438', light: '#ffffff' } })
          .then(setQr).catch(() => {})
      })
      .catch((e) => setErr(e.message))
  }, [])

  const enable = async (e) => {
    e.preventDefault(); setBusy(true); setErr('')
    try {
      const res = await api('/api/2fa/enable', { method: 'POST', body: JSON.stringify({ code }) })
      setBackupCodes(res.backup_codes)
    } catch (e2) { setErr(e2.message) }
    setBusy(false)
  }

  if (backupCodes) {
    return (
      <div className="auth-wrap">
        <div className="auth-card">
          <h1>Save your backup codes</h1>
          <p className="sub">Store these somewhere safe. Each works once if you lose your authenticator. They won't be shown again.</p>
          <div className="backup-grid">
            {backupCodes.map((c) => <code key={c}>{c}</code>)}
          </div>
          <button className="btn primary mt" style={{ width: '100%' }} onClick={onDone}>I've saved them — continue</button>
        </div>
      </div>
    )
  }

  return (
    <div className="auth-wrap">
      <form className="auth-card" onSubmit={enable}>
        <img className="mark" src="/sixter.png" alt="" />
        <h1>Secure your account</h1>
        <p className="sub">Sixter requires two-factor authentication. Scan this with Google Authenticator, Authy, or 1Password.</p>
        {qr && <div className="qr-box"><img src={qr} alt="2FA QR code" width="200" height="200" /></div>}
        <div className="hint">Can't scan? Enter this key manually:</div>
        <code className="secret-key">{secret || '…'}</code>
        <label className="field-label">Enter the 6-digit code to confirm</label>
        <input className="input code-input" inputMode="numeric" autoComplete="one-time-code" placeholder="123456"
               value={code} onChange={(e) => setCode(e.target.value)} required autoFocus />
        {err && <div className="err-box">{err}</div>}
        <button className="btn primary mt" style={{ width: '100%' }} disabled={busy}>{busy ? <span className="spin" /> : 'Enable 2FA'}</button>
        <div className="auth-foot"><button type="button" className="linkbtn" onClick={logout}>Log out</button></div>
      </form>
    </div>
  )
}

function Signup({ nav, onAuthed }) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [sub, setSub] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [providers, setProviders] = useState(null)
  useEffect(() => { api('/api/auth/providers').then(setProviders).catch(() => setProviders({})) }, [])
  const submit = async (e) => {
    e.preventDefault(); setBusy(true); setErr('')
    try {
      const u = await api('/api/signup', { method: 'POST', body: JSON.stringify({ email, password, subdomain: sub }) })
      onAuthed(u); nav('/dashboard')
    } catch (e2) { setErr(e2.message) }
    setBusy(false)
  }
  return (
    <div className="auth-wrap">
      <form className="auth-card" onSubmit={submit}>
        <img className="mark" src="/sixter.png" alt="" />
        <h1>Create your account</h1>
        <p className="sub">Pick a name — you get two permanent subdomains.</p>
        <OAuthButtons providers={providers} label="Sign up with" />
        <label className="field-label">Email</label>
        <input className="input" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required autoFocus />
        <label className="field-label">Password (min 8 characters)</label>
        <input className="input" type="password" minLength={8} value={password} onChange={(e) => setPassword(e.target.value)} required />
        <label className="field-label">Subdomain</label>
        <input className="input mono" placeholder="yourname" pattern="[a-z0-9-]{3,32}" value={sub} onChange={(e) => setSub(e.target.value.toLowerCase())} required />
        <div className="hint">{sub ? `you get: ${sub}.sixter.in + ${sub}-repo.sixter.in` : 'lowercase letters, digits, hyphens · 3–32 chars'}</div>
        {err && <div className="err-box">{err}</div>}
        <button className="btn primary mt" style={{ width: '100%' }} disabled={busy}>{busy ? <span className="spin" /> : 'Sign up free'}</button>
        <div className="auth-foot">Already registered? <button type="button" className="linkbtn" onClick={() => nav('/login')}>Log in</button></div>
      </form>
    </div>
  )
}

/* One-time onboarding for accounts created through Google/GitHub: they have no
   subdomain yet, so they choose one here before anything else loads. */
function ChooseSubdomain({ me, onDone, logout }) {
  const [sub, setSub] = useState('')
  const [state, setState] = useState(null) // {valid, available}
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  // debounced availability check
  useEffect(() => {
    const s = sub.trim()
    if (!s) { setState(null); return }
    const t = setTimeout(() => {
      api('/api/subdomain/check?sub=' + encodeURIComponent(s))
        .then((r) => setState(r))
        .catch(() => setState(null))
    }, 350)
    return () => clearTimeout(t)
  }, [sub])

  const claim = async (e) => {
    e.preventDefault(); setBusy(true); setErr('')
    try {
      const u = await api('/api/me/subdomain', { method: 'POST', body: JSON.stringify({ subdomain: sub.trim() }) })
      onDone(u)
    } catch (e2) { setErr(e2.message) }
    setBusy(false)
  }

  const ok = state && state.valid && state.available
  return (
    <div className="auth-wrap">
      <form className="auth-card" onSubmit={claim}>
        <img className="mark" src="/sixter.png" alt="" />
        <h1>Claim your subdomain</h1>
        <p className="sub">Welcome, {me.email}. Pick the name your tunnels and deployments will live on — this is permanent.</p>
        <label className="field-label">Subdomain</label>
        <input
          className="input mono" placeholder="yourname" autoFocus required
          pattern="[a-z0-9-]{3,32}" value={sub}
          onChange={(e) => { setSub(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '')); setErr('') }}
        />
        <div className="hint" style={{ color: state ? (ok ? 'var(--green, #34c77b)' : 'var(--red, #ff5d75)') : undefined }}>
          {!sub && 'lowercase letters, digits, hyphens · 3–32 chars'}
          {sub && !state && 'checking availability…'}
          {sub && state && !state.valid && (state.reason || 'not a valid subdomain')}
          {sub && state && state.valid && !state.available && `${sub}.sixter.in is already taken`}
          {sub && ok && `available — you get ${sub}.sixter.in and ${sub}-repo.sixter.in`}
        </div>
        {err && <div className="err-box">{err}</div>}
        <button className="btn primary mt" style={{ width: '100%' }} disabled={busy || !ok}>
          {busy ? <span className="spin" /> : 'Claim subdomain'}
        </button>
        <div className="auth-foot"><button type="button" className="linkbtn" onClick={logout}>Log out</button></div>
      </form>
    </div>
  )
}

function Activate({ nav, me }) {
  const params = new URLSearchParams(window.location.search)
  const [code, setCode] = useState(params.get('code') || '')
  const [err, setErr] = useState('')
  const [done, setDone] = useState(false)
  const [busy, setBusy] = useState(false)
  if (!me) {
    return <Login nav={nav} next={'/activate?code=' + encodeURIComponent(code)} onAuthed={() => { window.location.href = '/activate?code=' + encodeURIComponent(code) }} />
  }
  const submit = async (e) => {
    e.preventDefault(); setBusy(true); setErr('')
    try { await api('/api/device/approve', { method: 'POST', body: JSON.stringify({ code }) }); setDone(true) }
    catch (e2) { setErr(e2.message) }
    setBusy(false)
  }
  return (
    <div className="auth-wrap">
      <form className="auth-card" onSubmit={submit}>
        <img className="mark" src="/sixter.png" alt="" />
        <h1>Connect a device</h1>
        <p className="sub">Approve the login request from your terminal ({me.email}).</p>
        {done ? (
          <>
            <div className="ok-box">Device approved — go back to your terminal, the CLI finishes automatically.</div>
            <button type="button" className="btn mt" style={{ width: '100%' }} onClick={() => nav('/dashboard')}>Open console</button>
          </>
        ) : (
          <>
            <label className="field-label">Device code</label>
            <input className="input code-input" placeholder="XXXX-XXXX" maxLength={9} value={code} onChange={(e) => setCode(e.target.value)} required autoFocus />
            {err && <div className="err-box">{err}</div>}
            <button className="btn primary mt" style={{ width: '100%' }} disabled={busy}>{busy ? <span className="spin" /> : 'Approve device'}</button>
          </>
        )}
      </form>
    </div>
  )
}

/* ═══════════ app ═══════════ */

export default function App() {
  const [path, nav] = useRoute()
  const [me, setMe] = useState(null)
  const [checked, setChecked] = useState(false)

  useEffect(() => {
    api('/api/me').then(setMe).catch(() => {}).finally(() => setChecked(true))
  }, [])

  const logout = async () => {
    await api('/api/logout', { method: 'POST' }).catch(() => {})
    setMe(null); nav('/')
  }

  if (!checked) return null

  // admin.<apex> → control plane (reuses the same login + 2FA flow)
  if (IS_ADMIN_HOST) {
    if (!me) return <Login nav={nav} onAuthed={setMe} />
    if (me.totp_enabled === false) {
      return <Enroll2FA me={me} logout={logout} onDone={() => api('/api/me').then(setMe).catch(() => {})} />
    }
    if (!me.is_admin) {
      return (
        <div className="auth-wrap">
          <div className="auth-card">
            <h1>Not authorized</h1>
            <p className="sub">This is the Sixter admin console. Your account ({me.email}) doesn't have admin access.</p>
            <button className="btn mt" style={{ width: '100%' }} onClick={logout}>Log out</button>
          </div>
        </div>
      )
    }
    return <AdminDashboard me={me} logout={logout} />
  }

  let page
  // Routes that bring their own full chrome (no shared top Nav): the premium
  // landing (themed nav inside `.sx`) and the dashboard (its own TopBar).
  let chromeless = false
  // Already signed in and something sent us here to bounce back? Go straight there.
  if (path === '/login' && me && !me.needs_onboarding) {
    const ret = returnURL()
    if (ret) { window.location.href = ret; return null }
  }

  if (path === '/login') page = <Login nav={nav} onAuthed={setMe} />
  else if (path === '/signup') page = <Signup nav={nav} onAuthed={setMe} />
  else if (path === '/activate') page = <Activate nav={nav} me={me} />
  else if (path === '/docs') page = <Docs />
  else if (path === '/member') {
    page = me
      ? <MemberProfile nav={nav} />
      : <Login nav={nav} onAuthed={setMe} next={'/member' + window.location.search} />
  }
  else if (path === '/dashboard') {
    if (me) { page = <Dashboard me={me} nav={nav} onLogout={logout} />; chromeless = true }
    else page = <Login nav={nav} onAuthed={setMe} next="/dashboard" />
  }
  else { page = <PremiumLanding nav={nav} me={me} onLogout={logout} />; chromeless = true }

  // OAuth signups have no subdomain yet — that choice comes before everything.
  if (me && me.needs_onboarding) {
    return <ChooseSubdomain me={me} logout={logout} onDone={(u) => { setMe(u); nav('/dashboard') }} />
  }

  // 2FA is required for password accounts. Accounts that sign in through
  // Google/GitHub rely on that provider's own MFA, so they aren't forced here.
  if (me && me.totp_enabled === false && !(me.providers || []).length) {
    page = <Enroll2FA me={me} logout={logout} onDone={() => api('/api/me').then(setMe).catch(() => {})} />
    chromeless = false
  }

  if (chromeless) return page

  return (
    <>
      <Nav nav={nav} me={me} onLogout={logout} />
      {page}
    </>
  )
}
