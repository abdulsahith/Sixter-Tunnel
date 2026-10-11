import { AreaChart, Area, ResponsiveContainer, Tooltip, XAxis } from 'recharts'
import { Activity, Wifi, ShieldCheck, Server } from 'lucide-react'
import { Reveal, SectionHeading } from '../common/ui.jsx'

const TRAFFIC = [
  { t: 'Mon', v: 2400 }, { t: 'Tue', v: 3100 }, { t: 'Wed', v: 2800 },
  { t: 'Thu', v: 4200 }, { t: 'Fri', v: 5300 }, { t: 'Sat', v: 4100 }, { t: 'Sun', v: 6200 },
]

const DEPLOYS = [
  { name: 'api-server', status: 'green', label: 'Running', domain: 'api.sixter.in', when: '2 min ago' },
  { name: 'frontend', status: 'green', label: 'Running', domain: 'app.sixter.in', when: '5 min ago' },
  { name: 'repo-server', status: 'amber', label: 'Deploying', domain: 'repo.sixter.in', when: '1 min ago' },
]

const STATS = [
  { Icon: Server, k: 'Total projects', v: '12' },
  { Icon: Wifi, k: 'Active tunnels', v: '4' },
  { Icon: Activity, k: 'Requests today', v: '128k' },
  { Icon: ShieldCheck, k: 'Threats blocked', v: '3,402' },
]

export default function DashboardPreview() {
  return (
    <section className="sx-section">
      <div className="sx-wrap">
        <SectionHeading
          eyebrow="Live dashboard"
          title="A real control panel for everything you ship"
          sub="Projects, deployments, traffic and security — monitored in real time, all in one place."
        />

        <Reveal>
          <div className="sx-window" style={{ marginTop: 52 }}>
            <div className="sx-window-bar">
              <span className="sx-dot" style={{ background: '#ff5f57' }} />
              <span className="sx-dot" style={{ background: '#febc2e' }} />
              <span className="sx-dot" style={{ background: '#28c840' }} />
              <span style={{ marginLeft: 10, fontSize: 13, color: 'var(--sx-faint)', fontFamily: 'JetBrains Mono, monospace' }}>console.sixter.in</span>
            </div>

            <div style={{ padding: 'clamp(16px, 3vw, 28px)' }}>
              {/* stat row */}
              <div className="sx-grid-4" style={{ marginBottom: 22 }}>
                {STATS.map((s) => (
                  <div key={s.k} style={{ padding: 18, borderRadius: 16, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.07)' }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8, color: '#a1a0ff', marginBottom: 10 }}>
                      <s.Icon size={17} /><span style={{ fontSize: 12.5, color: 'var(--sx-dim)' }}>{s.k}</span>
                    </div>
                    <div style={{ fontSize: 26, fontWeight: 700 }}>{s.v}</div>
                  </div>
                ))}
              </div>

              <div className="sx-two-col" style={{ gap: 22, alignItems: 'stretch' }}>
                {/* traffic chart */}
                <div style={{ padding: 20, borderRadius: 16, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.07)' }}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 14 }}>
                    <span style={{ fontWeight: 600, fontSize: 15 }}>Traffic — last 7 days</span>
                    <span className="sx-status green">Healthy</span>
                  </div>
                  <div style={{ height: 180 }}>
                    <ResponsiveContainer width="100%" height="100%">
                      <AreaChart data={TRAFFIC} margin={{ top: 6, right: 6, left: 0, bottom: 0 }}>
                        <defs>
                          <linearGradient id="sxArea" x1="0" y1="0" x2="0" y2="1">
                            <stop offset="0%" stopColor="#8b7cff" stopOpacity={0.55} />
                            <stop offset="100%" stopColor="#8b7cff" stopOpacity={0} />
                          </linearGradient>
                        </defs>
                        <XAxis dataKey="t" stroke="#6b7090" fontSize={12} tickLine={false} axisLine={false} />
                        <Tooltip
                          contentStyle={{ background: '#0b1020', border: '1px solid rgba(139,124,255,0.3)', borderRadius: 12, color: '#fff' }}
                          labelStyle={{ color: '#a1a0ff' }} cursor={{ stroke: 'rgba(139,124,255,0.3)' }}
                        />
                        <Area type="monotone" dataKey="v" stroke="#a855f7" strokeWidth={2.5} fill="url(#sxArea)" />
                      </AreaChart>
                    </ResponsiveContainer>
                  </div>
                </div>

                {/* deployment table */}
                <div style={{ padding: 20, borderRadius: 16, background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.07)' }}>
                  <div style={{ fontWeight: 600, fontSize: 15, marginBottom: 14 }}>Recent deployments</div>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                    <div style={{ display: 'grid', gridTemplateColumns: '1.1fr 1fr 1.3fr 0.9fr', fontSize: 11.5, color: 'var(--sx-faint)', textTransform: 'uppercase', letterSpacing: '0.06em', padding: '0 0 10px' }}>
                      <span>Project</span><span>Status</span><span>Domain</span><span style={{ textAlign: 'right' }}>Last deploy</span>
                    </div>
                    {DEPLOYS.map((d) => (
                      <div key={d.name} style={{ display: 'grid', gridTemplateColumns: '1.1fr 1fr 1.3fr 0.9fr', alignItems: 'center', padding: '11px 0', borderTop: '1px solid rgba(255,255,255,0.06)', fontSize: 13.5 }}>
                        <span style={{ fontFamily: 'JetBrains Mono, monospace', fontWeight: 500 }}>{d.name}</span>
                        <span className={`sx-status ${d.status}`}>{d.label}</span>
                        <span style={{ color: '#a1a0ff', fontFamily: 'JetBrains Mono, monospace', fontSize: 12.5 }}>{d.domain}</span>
                        <span style={{ color: 'var(--sx-dim)', textAlign: 'right', fontSize: 12.5 }}>{d.when}</span>
                      </div>
                    ))}
                  </div>
                </div>
              </div>
            </div>
          </div>
        </Reveal>
      </div>
    </section>
  )
}
