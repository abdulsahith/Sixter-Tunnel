import { motion } from 'framer-motion'
import { ArrowRight, Terminal, Zap } from 'lucide-react'
import { Button } from '../common/ui.jsx'
import Globe from './Globe.jsx'

const fade = (delay) => ({
  initial: { opacity: 0, y: 24 },
  animate: { opacity: 1, y: 0 },
  transition: { duration: 0.7, delay, ease: [0.22, 1, 0.36, 1] },
})

export default function Hero({ nav }) {
  return (
    <section className="sx-section" style={{ paddingTop: 90, overflow: 'hidden' }}>
      <div className="sx-wrap sx-hero-grid">
        <div>
          <motion.div {...fade(0)}>
            <span className="sx-eyebrow"><Zap size={14} /> Sixter 2.0 — global edge network</span>
          </motion.div>

          <motion.h1
            {...fade(0.08)}
            style={{ fontSize: 'clamp(40px, 5.6vw, 70px)', fontWeight: 800, lineHeight: 1.04, letterSpacing: '-0.04em', margin: '24px 0 0' }}
          >
            Secure Tunnel<br />
            <span className="sx-grad-text">Network</span>
          </motion.h1>

          <motion.p {...fade(0.16)} className="sx-sub" style={{ marginTop: 22, fontSize: 19, maxWidth: 520 }}>
            Secure tunnels, instant project deployment and custom domains with automatic
            HTTPS — protected at our global edge. High performance and high availability
            for apps and APIs, from one tiny CLI.
          </motion.p>

          <motion.div {...fade(0.24)} style={{ display: 'flex', gap: 14, marginTop: 34, flexWrap: 'wrap' }}>
            <Button size="lg" onClick={() => nav('/signup')}>
              Get your subdomain <ArrowRight size={18} />
            </Button>
            <Button size="lg" variant="ghost" onClick={() => document.getElementById('cli')?.scrollIntoView({ behavior: 'smooth' })}>
              <Terminal size={18} /> Install CLI
            </Button>
          </motion.div>

          <motion.div {...fade(0.32)} style={{ display: 'flex', gap: 28, marginTop: 40, flexWrap: 'wrap' }}>
            {[['<1s', 'tunnel start'], ['99.99%', 'edge uptime'], ['Auto', 'HTTPS + WAF']].map(([b, s]) => (
              <div key={s}>
                <div style={{ fontSize: 26, fontWeight: 700 }} className="sx-grad-text">{b}</div>
                <div style={{ fontSize: 13.5, color: 'var(--sx-dim)' }}>{s}</div>
              </div>
            ))}
          </motion.div>
        </div>

        <motion.div
          initial={{ opacity: 0, scale: 0.9 }}
          animate={{ opacity: 1, scale: 1 }}
          transition={{ duration: 1.1, ease: [0.22, 1, 0.36, 1] }}
        >
          <Globe />
        </motion.div>
      </div>
    </section>
  )
}
