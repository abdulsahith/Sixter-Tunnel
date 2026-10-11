import { Zap, ShieldCheck, Upload, Boxes, RotateCcw, Globe2, TerminalSquare, Activity, KeyRound } from 'lucide-react'
import { GlassCard, Reveal, SectionHeading } from '../common/ui.jsx'

const FEATURES = [
  { Icon: Zap, h: 'Tunnels in one command', p: 'Expose any local port on your own subdomain in under a second. HTTP and WebSocket, full duplex, zero config files.' },
  { Icon: ShieldCheck, h: 'Firewall at the edge', p: 'Every request is inspected before it reaches you. SQL injection, XSS, traversal, scanners and floods blocked automatically.' },
  { Icon: Upload, h: 'Push projects anywhere', p: 'sixter repo ships your whole folder to your server in seconds — node_modules and build junk excluded automatically.' },
  { Icon: Boxes, h: 'Auto-deploy on every push', p: 'Sixter detects the project type, builds it, restarts the service and health-checks it. Broken releases roll back themselves.' },
  { Icon: RotateCcw, h: 'Versioned deploys + 1-click rollback', p: 'Every upload is snapshotted. Roll back to any earlier version instantly from the dashboard — live.' },
  { Icon: Globe2, h: 'Custom domains + HTTPS', p: 'Bring your own domain. Sixter writes the nginx config, verifies DNS and provisions Let’s Encrypt certificates for you.' },
  { Icon: TerminalSquare, h: 'SSH & web terminal', p: 'A real shell into every deployed project — from the dashboard or over SSH, jailed safely to the project folder.' },
  { Icon: Activity, h: 'See every request', p: 'A live, packet-level view of traffic hitting your server: source IPs, latency, status codes, payload sizes.' },
  { Icon: KeyRound, h: 'Device login for servers', p: 'SSH-only VPS? Run sixter login, approve from your phone. OAuth-style device flow — no browser needed.' },
]

export default function Features() {
  return (
    <section className="sx-section" id="features">
      <div className="sx-wrap">
        <SectionHeading
          eyebrow="Platform"
          title="Everything between your laptop and production"
          sub="One small binary. Nine superpowers — tunnels, deploys, domains, firewall and a real shell."
        />
        <div className="sx-grid-3" style={{ marginTop: 56 }}>
          {FEATURES.map((f, i) => (
            <Reveal key={f.h} delay={(i % 3) * 0.08}>
              <GlassCard style={{ padding: 28, height: '100%' }}>
                <div style={{
                  width: 48, height: 48, borderRadius: 14, display: 'grid', placeItems: 'center',
                  background: 'linear-gradient(135deg, rgba(139,124,255,0.22), rgba(168,85,247,0.12))',
                  border: '1px solid rgba(139,124,255,0.3)', color: '#c4b9ff', marginBottom: 18,
                }}>
                  <f.Icon size={22} />
                </div>
                <h3 style={{ fontSize: 18.5, fontWeight: 700, marginBottom: 9, letterSpacing: '-0.02em' }}>{f.h}</h3>
                <p style={{ color: 'var(--sx-dim)', fontSize: 14.5, lineHeight: 1.65 }}>{f.p}</p>
              </GlassCard>
            </Reveal>
          ))}
        </div>
      </div>
    </section>
  )
}
