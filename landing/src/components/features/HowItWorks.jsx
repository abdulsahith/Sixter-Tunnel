import { motion } from 'framer-motion'
import { Upload, Cpu, Globe2 } from 'lucide-react'
import { Reveal, SectionHeading } from '../common/ui.jsx'

const STEPS = [
  { n: '01', Icon: Upload, t: 'Push your project', d: 'One command ships your whole folder to your server — build junk excluded, secrets preserved.', code: 'sixter repo -project myapp' },
  { n: '02', Icon: Cpu, t: 'Sixter builds & secures it', d: 'The type is auto-detected, dependencies installed, the service kept alive under systemd, HTTPS issued and the edge firewall armed.', code: 'detect → build → health-check' },
  { n: '03', Icon: Globe2, t: 'Live on your domain', d: 'Your site is online on your own domain — versioned, monitored, with one-click rollback if a release breaks.', code: 'https://app.sixter.in ✓' },
]

export default function HowItWorks() {
  return (
    <section className="sx-section" id="how">
      <div className="sx-wrap">
        <SectionHeading
          eyebrow="How Sixter works"
          title="From your machine to a live, secured site"
          sub="One push triggers the entire pipeline — automatically. No YAML, no dashboards to babysit."
        />
        <div className="sx-grid-3" style={{ marginTop: 56, position: 'relative' }}>
          {/* connecting line */}
          <div className="hidden md:block" style={{
            position: 'absolute', top: 44, left: '16%', right: '16%', height: 1,
            background: 'linear-gradient(90deg, transparent, rgba(139,124,255,0.4), transparent)',
          }} />
          {STEPS.map((s, i) => (
            <Reveal key={s.n} delay={i * 0.12}>
              <div style={{ textAlign: 'center', padding: '0 8px' }}>
                <motion.div
                  whileHover={{ scale: 1.06 }}
                  style={{
                    width: 72, height: 72, borderRadius: 20, margin: '0 auto 22px', display: 'grid', placeItems: 'center',
                    background: 'rgba(8,11,26,0.9)', border: '1px solid rgba(139,124,255,0.35)',
                    boxShadow: '0 12px 40px rgba(139,124,255,0.25)', color: '#c4b9ff', position: 'relative', zIndex: 1,
                  }}
                >
                  <s.Icon size={30} />
                </motion.div>
                <div className="sx-grad-text" style={{ fontSize: 14, fontWeight: 700, letterSpacing: '0.1em', marginBottom: 8 }}>{s.n}</div>
                <h3 style={{ fontSize: 21, fontWeight: 700, marginBottom: 10, letterSpacing: '-0.02em' }}>{s.t}</h3>
                <p style={{ color: 'var(--sx-dim)', fontSize: 15, lineHeight: 1.65, maxWidth: 320, margin: '0 auto 16px' }}>{s.d}</p>
                <code style={{ fontFamily: 'JetBrains Mono, monospace', fontSize: 13, color: '#a1a0ff', background: 'rgba(139,124,255,0.08)', border: '1px solid rgba(139,124,255,0.18)', padding: '7px 14px', borderRadius: 10, display: 'inline-block' }}>{s.code}</code>
              </div>
            </Reveal>
          ))}
        </div>
      </div>
    </section>
  )
}
