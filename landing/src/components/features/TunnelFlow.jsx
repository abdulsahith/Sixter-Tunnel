import { Reveal, SectionHeading } from '../common/ui.jsx'

/* Live request-flow diagram: visitor → Sixter edge → your machine, and back.
   Self-contained animated SVG (no deps), styled for the premium `.sx` theme. */
export default function TunnelFlow() {
  return (
    <section className="sx-section" style={{ paddingTop: 40 }}>
      <div className="sx-wrap">
        <SectionHeading
          eyebrow="Live"
          title="From localhost to the world"
          sub="Watch a request travel — visitor → Sixter edge → your machine, and all the way back."
        />
        <Reveal>
          <div className="sx-glass" style={{ marginTop: 48, padding: 'clamp(16px, 3vw, 36px)' }}>
            <svg viewBox="0 0 920 300" fill="none" fontFamily="'JetBrains Mono', Consolas, monospace" style={{ width: '100%', height: 'auto' }} aria-label="Request flow: visitor to Sixter edge to your machine">
              <defs>
                <filter id="fglow" x="-60%" y="-60%" width="220%" height="220%">
                  <feGaussianBlur stdDeviation="4" result="b" /><feMerge><feMergeNode in="b" /><feMergeNode in="SourceGraphic" /></feMerge>
                </filter>
              </defs>
              <path id="fp1" d="M168 132 C 270 70, 360 70, 430 122" stroke="#2a2a44" strokeWidth="2" />
              <path id="fp2" d="M490 122 C 560 70, 650 70, 752 132" stroke="#2a2a44" strokeWidth="2" />
              <path id="fp3" d="M752 168 C 650 230, 560 230, 490 178" stroke="#2a2a44" strokeWidth="2" />
              <path id="fp4" d="M430 178 C 360 230, 270 230, 168 168" stroke="#2a2a44" strokeWidth="2" />
              <text x="300" y="66" fill="#6b7090" fontSize="11" textAnchor="middle">https request</text>
              <text x="625" y="66" fill="#6b7090" fontSize="11" textAnchor="middle">encrypted tunnel</text>
              <text x="625" y="246" fill="#6b7090" fontSize="11" textAnchor="middle">response</text>
              <text x="300" y="246" fill="#6b7090" fontSize="11" textAnchor="middle">back to visitor</text>
              <circle r="5" fill="#57a0f5" filter="url(#fglow)"><animateMotion dur="2.4s" repeatCount="indefinite"><mpath href="#fp1" /></animateMotion></circle>
              <circle r="4" fill="#57a0f5" opacity="0.8" filter="url(#fglow)"><animateMotion dur="2.4s" begin="1.2s" repeatCount="indefinite"><mpath href="#fp1" /></animateMotion></circle>
              <circle r="5" fill="#8b7cff" filter="url(#fglow)"><animateMotion dur="2.4s" begin="0.6s" repeatCount="indefinite"><mpath href="#fp2" /></animateMotion></circle>
              <circle r="4" fill="#8b7cff" opacity="0.8" filter="url(#fglow)"><animateMotion dur="2.4s" begin="1.8s" repeatCount="indefinite"><mpath href="#fp2" /></animateMotion></circle>
              <circle r="5" fill="#34d399" filter="url(#fglow)"><animateMotion dur="2.4s" begin="0.3s" repeatCount="indefinite"><mpath href="#fp3" /></animateMotion></circle>
              <circle r="5" fill="#34d399" filter="url(#fglow)"><animateMotion dur="2.4s" begin="0.9s" repeatCount="indefinite"><mpath href="#fp4" /></animateMotion></circle>
              <circle r="4" fill="#34d399" opacity="0.8" filter="url(#fglow)"><animateMotion dur="2.4s" begin="1.5s" repeatCount="indefinite"><mpath href="#fp4" /></animateMotion></circle>
              <rect x="40" y="108" width="128" height="84" rx="16" fill="rgba(255,255,255,0.03)" stroke="rgba(255,255,255,0.1)" />
              <circle cx="62" cy="128" r="3.5" fill="#ff5f57" /><circle cx="74" cy="128" r="3.5" fill="#febc2e" /><circle cx="86" cy="128" r="3.5" fill="#28c840" />
              <text x="104" y="160" fill="#f5f5f7" fontSize="13" fontWeight="bold" textAnchor="middle">visitor</text>
              <text x="104" y="178" fill="#6b7090" fontSize="9.5" textAnchor="middle">anywhere on earth</text>
              <circle cx="460" cy="150" r="36" fill="rgba(139,124,255,0.10)" stroke="#8b7cff" strokeWidth="1.5" />
              <circle cx="460" cy="150" r="36" fill="none" stroke="#8b7cff" strokeWidth="1">
                <animate attributeName="r" values="36;66" dur="2.8s" repeatCount="indefinite" /><animate attributeName="opacity" values="0.6;0" dur="2.8s" repeatCount="indefinite" />
              </circle>
              <circle cx="460" cy="150" r="36" fill="none" stroke="#8b7cff" strokeWidth="1">
                <animate attributeName="r" values="36;66" dur="2.8s" begin="1.4s" repeatCount="indefinite" /><animate attributeName="opacity" values="0.6;0" dur="2.8s" begin="1.4s" repeatCount="indefinite" />
              </circle>
              <text x="460" y="147" fill="#f5f5f7" fontSize="13" fontWeight="bold" textAnchor="middle">SIXTER</text>
              <text x="460" y="164" fill="#a1a1aa" fontSize="9.5" textAnchor="middle">edge</text>
              <text x="460" y="280" fill="#a1a0ff" fontSize="12" textAnchor="middle">https://you.sixter.in</text>
              <rect x="752" y="108" width="128" height="84" rx="16" fill="rgba(255,255,255,0.03)" stroke="rgba(255,255,255,0.1)" />
              <text x="816" y="138" fill="#34d399" fontSize="11" textAnchor="middle">&#10095; sixter tunnel</text>
              <text x="816" y="158" fill="#a1a1aa" fontSize="11" textAnchor="middle">localhost:3000</text>
              <text x="816" y="178" fill="#6b7090" fontSize="9.5" textAnchor="middle">your machine</text>
            </svg>
          </div>
        </Reveal>
      </div>
    </section>
  )
}
