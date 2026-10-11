import { Star } from 'lucide-react'
import { GlassCard, Reveal, SectionHeading } from '../common/ui.jsx'

const QUOTES = [
  { q: 'We replaced a tangle of ngrok, nginx and CI scripts with a single Sixter command. Deploys that took 20 minutes now take 20 seconds.', a: 'Priya Nair', r: 'Staff Engineer, Northwind' },
  { q: 'The edge firewall caught a credential-stuffing wave before it ever reached our origin. The live request feed is genuinely better than our old APM.', a: 'Marcus Feld', r: 'CTO, Vortex Labs' },
  { q: 'One-click rollback saved a Friday launch. Push, break, roll back, fix — all from the dashboard without SSHing anywhere. This is how shipping should feel.', a: 'Dani Okoro', r: 'Founder, Beacon' },
  { q: 'Custom domains with auto-HTTPS just worked. I pointed DNS and Sixter handled nginx and Let’s Encrypt. No certbot cron jobs ever again.', a: 'Sven Larsson', r: 'Indie hacker' },
  { q: 'The SSH jail per project means I can hand a contractor terminal access to exactly one app and nothing else. Security and convenience, finally together.', a: 'Aisha Rahman', r: 'Platform Lead, Quanta' },
  { q: 'It feels like Vercel and Cloudflare had a baby that also runs on my own VPS. Premium product, hobby-friendly price.', a: 'Tom Becker', r: 'Backend Dev, Stackline' },
]

export default function Testimonials() {
  return (
    <section className="sx-section">
      <div className="sx-wrap">
        <SectionHeading
          eyebrow="Loved by builders"
          title="Trusted by teams who ship fast"
          sub="From solo hackers to platform teams — here's what developers say about running on Sixter."
        />
        <div className="sx-grid-3" style={{ marginTop: 56, alignItems: 'stretch' }}>
          {QUOTES.map((t, i) => (
            <Reveal key={t.a} delay={(i % 3) * 0.09}>
              <GlassCard style={{ padding: 26, height: '100%', display: 'flex', flexDirection: 'column' }}>
                <div style={{ display: 'flex', gap: 3, marginBottom: 14 }}>
                  {[0, 1, 2, 3, 4].map((s) => <Star key={s} size={15} style={{ color: '#fbbf24', fill: '#fbbf24' }} />)}
                </div>
                <p style={{ fontSize: 15, lineHeight: 1.7, color: 'var(--sx-text)', flex: 1 }}>“{t.q}”</p>
                <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginTop: 20 }}>
                  <div style={{ width: 40, height: 40, borderRadius: '50%', display: 'grid', placeItems: 'center', fontWeight: 700, color: '#fff', background: 'linear-gradient(135deg,#8b7cff,#a855f7)' }}>
                    {t.a[0]}
                  </div>
                  <div>
                    <div style={{ fontWeight: 600, fontSize: 14 }}>{t.a}</div>
                    <div style={{ color: 'var(--sx-faint)', fontSize: 12.5 }}>{t.r}</div>
                  </div>
                </div>
              </GlassCard>
            </Reveal>
          ))}
        </div>
      </div>
    </section>
  )
}
