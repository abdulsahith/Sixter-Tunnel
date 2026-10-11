import { Reveal, SectionHeading } from '../common/ui.jsx'

const ATTACKS = [
  { y: 70, label: "' OR 1=1 --", delay: '0s' },
  { y: 160, label: '<script>', delay: '1.1s' },
  { y: 250, label: '../etc/passwd', delay: '2.2s' },
  { y: 115, label: '/wp-login.php', delay: '3.3s' },
]

/* WAF demo: malicious requests slam into the Sixter edge firewall and are
   stopped, while a clean request sails through to your app. Animated SVG. */
export default function FirewallDemo() {
  return (
    <section className="sx-section" style={{ paddingTop: 40 }}>
      <div className="sx-wrap">
        <SectionHeading
          eyebrow="Edge security"
          title="Attacks stop at our edge"
          sub="Your traffic runs through Sixter, so every request is inspected before it reaches your machine. SQL injection, XSS, traversal and scanners never get through."
        />
        <Reveal>
          <div className="sx-glass" style={{ marginTop: 48, padding: 'clamp(16px, 3vw, 36px)' }}>
            <svg viewBox="0 0 920 320" fill="none" fontFamily="'JetBrains Mono', Consolas, monospace" style={{ width: '100%', height: 'auto' }} aria-label="Firewall blocking malicious requests at the edge">
              <defs>
                <radialGradient id="shieldg" cx="50%" cy="45%" r="60%">
                  <stop offset="0%" stopColor="rgba(139,124,255,0.4)" /><stop offset="100%" stopColor="rgba(139,124,255,0)" />
                </radialGradient>
                <filter id="fwglow" x="-60%" y="-60%" width="220%" height="220%">
                  <feGaussianBlur stdDeviation="3.5" result="b" /><feMerge><feMergeNode in="b" /><feMergeNode in="SourceGraphic" /></feMerge>
                </filter>
              </defs>
              <ellipse cx="600" cy="160" rx="60" ry="150" fill="url(#shieldg)" />
              <line x1="600" y1="20" x2="600" y2="300" stroke="#8b7cff" strokeWidth="1.5" strokeDasharray="4 6" opacity="0.5" />
              <g filter="url(#fwglow)">
                <path d="M600 110 l30 12 v22 c0 22 -14 34 -30 42 c-16 -8 -30 -20 -30 -42 v-22 z" fill="rgba(139,124,255,0.12)" stroke="#8b7cff" strokeWidth="1.6" />
                <path d="M590 150 l7 7 14 -15" stroke="#34d399" strokeWidth="2.4" fill="none" />
              </g>
              <text x="600" y="220" fill="#a1a0ff" fontSize="11" textAnchor="middle">SIXTER firewall</text>
              <rect x="760" y="128" width="138" height="66" rx="14" fill="rgba(255,255,255,0.03)" stroke="rgba(255,255,255,0.1)" />
              <text x="829" y="156" fill="#34d399" fontSize="11" textAnchor="middle">your app</text>
              <text x="829" y="174" fill="#6b7090" fontSize="9.5" textAnchor="middle">protected</text>
              {ATTACKS.map((a, i) => (
                <g key={i}>
                  <g filter="url(#fwglow)">
                    <circle r="5" fill="#ef4444">
                      <animateMotion dur="3.6s" begin={a.delay} repeatCount="indefinite" keyPoints="0;0.62;0.62;0.78" keyTimes="0;0.45;0.62;1" calcMode="linear" path={`M40 ${a.y} L560 160 L560 160 L600 300`} />
                      <animate attributeName="fill" values="#ef4444;#ef4444;#5a2230" keyTimes="0;0.62;1" dur="3.6s" begin={a.delay} repeatCount="indefinite" />
                      <animate attributeName="opacity" values="1;1;0" keyTimes="0;0.7;1" dur="3.6s" begin={a.delay} repeatCount="indefinite" />
                    </circle>
                  </g>
                  <text x="40" y={a.y - 12} fill="#f87171" fontSize="10.5" textAnchor="middle">{a.label}
                    <animate attributeName="opacity" values="0;1;1;0" keyTimes="0;0.1;0.5;0.62" dur="3.6s" begin={a.delay} repeatCount="indefinite" />
                  </text>
                </g>
              ))}
              <text x="40" y="305" fill="#6b7090" fontSize="10.5" textAnchor="middle">attacks</text>
              <g filter="url(#fwglow)"><circle r="5" fill="#34d399"><animateMotion dur="3.6s" begin="0.5s" repeatCount="indefinite" path="M40 160 L900 160" /></circle></g>
              <text x="185" y="148" fill="#34d399" fontSize="10.5" textAnchor="middle">GET /api/users ✓
                <animate attributeName="opacity" values="0;1;1;0" keyTimes="0;0.1;0.4;0.55" dur="3.6s" begin="0.5s" repeatCount="indefinite" />
              </text>
            </svg>

            <div style={{ display: 'flex', gap: 24, justifyContent: 'center', flexWrap: 'wrap', marginTop: 18, fontSize: 13, color: 'var(--sx-dim)' }}>
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}><i style={{ width: 10, height: 10, borderRadius: '50%', background: '#34d399' }} /> clean request → forwarded</span>
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}><i style={{ width: 10, height: 10, borderRadius: '50%', background: '#ef4444' }} /> SQLi · XSS · traversal · scanners → blocked</span>
            </div>
          </div>
        </Reveal>
      </div>
    </section>
  )
}
