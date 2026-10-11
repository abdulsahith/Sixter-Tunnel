import { ArrowRight } from 'lucide-react'
import { Button, Reveal } from './ui.jsx'

/* lucide dropped brand marks, so we ship minimal inline brand SVGs. */
const Github = (p) => (
  <svg width={p.size} height={p.size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
    <path d="M12 2C6.48 2 2 6.58 2 12.26c0 4.5 2.87 8.32 6.84 9.67.5.1.68-.22.68-.49v-1.7c-2.78.62-3.37-1.36-3.37-1.36-.46-1.18-1.11-1.5-1.11-1.5-.91-.63.07-.62.07-.62 1 .07 1.53 1.06 1.53 1.06.89 1.56 2.34 1.11 2.91.85.09-.66.35-1.11.63-1.36-2.22-.26-4.56-1.14-4.56-5.07 0-1.12.39-2.03 1.03-2.75-.1-.26-.45-1.3.1-2.71 0 0 .84-.27 2.75 1.05a9.4 9.4 0 0 1 5 0c1.91-1.32 2.75-1.05 2.75-1.05.55 1.41.2 2.45.1 2.71.64.72 1.03 1.63 1.03 2.75 0 3.94-2.34 4.81-4.57 5.06.36.32.68.94.68 1.9v2.82c0 .27.18.6.69.49A10.02 10.02 0 0 0 22 12.26C22 6.58 17.52 2 12 2z" />
  </svg>
)
const Twitter = (p) => (
  <svg width={p.size} height={p.size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
    <path d="M18.24 2H21.5l-7.27 8.31L22.75 22h-6.6l-5.17-6.76L4.97 22H1.7l7.77-8.88L1.25 2h6.77l4.67 6.18L18.24 2zm-1.16 18h1.83L7.01 3.9H5.05L17.08 20z" />
  </svg>
)
const Linkedin = (p) => (
  <svg width={p.size} height={p.size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
    <path d="M20.45 20.45h-3.56v-5.57c0-1.33-.02-3.04-1.85-3.04-1.86 0-2.14 1.45-2.14 2.94v5.67H9.34V9h3.42v1.56h.05c.48-.9 1.64-1.85 3.37-1.85 3.6 0 4.27 2.37 4.27 5.46v6.28zM5.34 7.43a2.07 2.07 0 1 1 0-4.14 2.07 2.07 0 0 1 0 4.14zM7.12 20.45H3.56V9h3.56v11.45zM22.22 0H1.77C.79 0 0 .77 0 1.73v20.54C0 23.22.79 24 1.77 24h20.45c.98 0 1.78-.78 1.78-1.73V1.73C24 .77 23.2 0 22.22 0z" />
  </svg>
)

const COLS = [
  { h: 'Product', links: [['Features', 'features'], ['How it works', 'how'], ['Download', 'cli']] },
  { h: 'Developers', links: [['Docs', 'docs'], ['CLI reference', 'docs'], ['Self-hosting', 'docs']] },
  { h: 'Account', links: [['Log in', 'login'], ['Sign up', 'signup'], ['Console', 'dashboard']] },
]

export default function Footer({ nav }) {
  const go = (target) => {
    if (['features', 'how', 'cli'].includes(target)) {
      if (window.location.pathname !== '/') nav('/')
      setTimeout(() => document.getElementById(target)?.scrollIntoView({ behavior: 'smooth' }), 60)
    } else {
      nav('/' + target)
    }
  }

  return (
    <>
      {/* final CTA banner */}
      <section className="sx-section" style={{ paddingTop: 40 }}>
        <div className="sx-wrap">
          <Reveal>
            <div className="sx-glass" style={{
              padding: 'clamp(36px, 6vw, 72px)', textAlign: 'center', position: 'relative', overflow: 'hidden',
              background: 'radial-gradient(800px 300px at 50% 0%, rgba(139,124,255,0.22), rgba(255,255,255,0.04))',
            }}>
              <h2 style={{ fontSize: 'clamp(28px,4vw,44px)', fontWeight: 800, letterSpacing: '-0.03em', marginBottom: 14 }}>
                Claim your <span className="sx-grad-text">subdomain</span>
              </h2>
              <p className="sx-sub" style={{ margin: '0 auto 30px' }}>
                yourname.sixter.in is waiting. Free, permanent and protected at the edge.
              </p>
              <Button size="lg" onClick={() => nav('/signup')}>Create your account <ArrowRight size={18} /></Button>
            </div>
          </Reveal>
        </div>
      </section>

      {/* footer */}
      <footer style={{ borderTop: '1px solid rgba(255,255,255,0.06)', paddingTop: 64, paddingBottom: 40 }}>
        <div className="sx-wrap">
          <div style={{ display: 'grid', gridTemplateColumns: '1.6fr 1fr 1fr 1fr', gap: 32 }} className="sx-footer-grid">
            <div>
              <img src="/sixter-logo.png" alt="Sixter" style={{ height: 28, marginBottom: 16 }} />
              <p style={{ color: 'var(--sx-dim)', fontSize: 14, lineHeight: 1.7, maxWidth: 280 }}>
                Secure tunnel network. High performance and high availability for apps and APIs — from one tiny CLI.
              </p>
              <div style={{ display: 'flex', gap: 12, marginTop: 20 }}>
                {[Github, Twitter, Linkedin].map((Ic, i) => (
                  <a key={i} href="#" aria-label="social" style={{
                    width: 38, height: 38, borderRadius: 10, display: 'grid', placeItems: 'center',
                    background: 'rgba(255,255,255,0.04)', border: '1px solid rgba(255,255,255,0.08)', color: 'var(--sx-dim)',
                  }}>
                    <Ic size={17} />
                  </a>
                ))}
              </div>
            </div>
            {COLS.map((c) => (
              <div key={c.h}>
                <h5 style={{ fontSize: 13, fontWeight: 700, letterSpacing: '0.06em', textTransform: 'uppercase', color: 'var(--sx-faint)', marginBottom: 16 }}>{c.h}</h5>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 11 }}>
                  {c.links.map(([label, target]) => (
                    <button key={label} onClick={() => go(target)} style={{ background: 'none', border: 0, color: 'var(--sx-dim)', fontSize: 14, textAlign: 'left', cursor: 'pointer', fontFamily: 'inherit', padding: 0 }}>
                      {label}
                    </button>
                  ))}
                </div>
              </div>
            ))}
          </div>
          <div style={{ display: 'flex', justifyContent: 'space-between', flexWrap: 'wrap', gap: 12, marginTop: 48, paddingTop: 24, borderTop: '1px solid rgba(255,255,255,0.06)', color: 'var(--sx-faint)', fontSize: 13 }}>
            <span>© {new Date().getFullYear()} Sixter — sixter.in</span>
            <span>Built for developers who ship.</span>
          </div>
        </div>
      </footer>
    </>
  )
}
