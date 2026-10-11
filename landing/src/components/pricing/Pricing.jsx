import { Check } from 'lucide-react'
import { Button, GlassCard, Reveal, SectionHeading } from '../common/ui.jsx'

const PLANS = [
  {
    name: 'Hobby', price: '$0', per: '/forever', tag: 'For side projects',
    feats: ['2 permanent subdomains', 'Unlimited tunnels', 'Edge firewall (WAF)', 'Automatic HTTPS', 'Community support'],
    cta: 'Start free', highlight: false,
  },
  {
    name: 'Pro', price: '$19', per: '/month', tag: 'For developers shipping daily',
    feats: ['Everything in Hobby', 'Custom domains', 'Project deployments + rollback', 'SSH & web terminal', 'Live request analytics', 'Priority support'],
    cta: 'Start Pro trial', highlight: true,
  },
  {
    name: 'Team', price: 'Custom', per: '', tag: 'For companies at scale',
    feats: ['Everything in Pro', 'Unlimited team members', 'SSO & audit logs', 'Dedicated edge capacity', 'SLA & onboarding', '24/7 support'],
    cta: 'Contact sales', highlight: false,
  },
]

export default function Pricing({ nav }) {
  return (
    <section className="sx-section" id="pricing">
      <div className="sx-wrap">
        <SectionHeading
          eyebrow="Pricing"
          title="Simple pricing that scales with you"
          sub="Start free, upgrade when you ship for real. No hidden edge fees, no per-seat surprises."
        />
        <div className="sx-grid-3" style={{ marginTop: 56, alignItems: 'stretch' }}>
          {PLANS.map((p, i) => (
            <Reveal key={p.name} delay={i * 0.1}>
              <GlassCard
                hover
                style={{
                  padding: 32, height: '100%', display: 'flex', flexDirection: 'column',
                  border: p.highlight ? '1px solid rgba(139,124,255,0.5)' : undefined,
                  boxShadow: p.highlight ? '0 30px 100px rgba(139,124,255,0.32)' : undefined,
                  position: 'relative', overflow: 'hidden',
                }}
              >
                {p.highlight && (
                  <span style={{ position: 'absolute', top: 18, right: 18, fontSize: 11.5, fontWeight: 700, letterSpacing: '0.08em', textTransform: 'uppercase', color: '#fff', background: 'linear-gradient(120deg,#8b7cff,#a855f7)', padding: '5px 12px', borderRadius: 999 }}>
                    Popular
                  </span>
                )}
                <div style={{ fontSize: 14, color: 'var(--sx-dim)', fontWeight: 600 }}>{p.name}</div>
                <div style={{ display: 'flex', alignItems: 'baseline', gap: 4, margin: '12px 0 4px' }}>
                  <span style={{ fontSize: 44, fontWeight: 800, letterSpacing: '-0.03em' }}>{p.price}</span>
                  <span style={{ color: 'var(--sx-dim)', fontSize: 15 }}>{p.per}</span>
                </div>
                <div style={{ color: 'var(--sx-faint)', fontSize: 13.5, marginBottom: 24 }}>{p.tag}</div>
                <ul style={{ listStyle: 'none', display: 'flex', flexDirection: 'column', gap: 13, margin: 0, padding: 0, flex: 1 }}>
                  {p.feats.map((f) => (
                    <li key={f} style={{ display: 'flex', gap: 10, alignItems: 'flex-start', fontSize: 14.5, color: 'var(--sx-text)' }}>
                      <Check size={18} style={{ color: '#8b7cff', flexShrink: 0, marginTop: 1 }} /> {f}
                    </li>
                  ))}
                </ul>
                <Button
                  variant={p.highlight ? 'primary' : 'ghost'}
                  style={{ width: '100%', marginTop: 28 }}
                  onClick={() => nav(p.name === 'Team' ? '/signup' : '/signup')}
                >
                  {p.cta}
                </Button>
              </GlassCard>
            </Reveal>
          ))}
        </div>
      </div>
    </section>
  )
}
