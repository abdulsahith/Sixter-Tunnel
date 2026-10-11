import { motion } from 'framer-motion'
import { AreaChart, Area, ResponsiveContainer, Tooltip, XAxis } from 'recharts'
import { ShieldCheck, ShieldX, ShieldAlert, Ban, Globe2, MapPin } from 'lucide-react'
import { timeAgo } from '../../shared.jsx'
import { PageHeader, StatCard, Card, CardHead, Donut, BarRow, Badge, fadeUp } from './widgets.jsx'

const THREAT_COLORS = {
  'SQL injection': '#ef4444', 'Cross-site scripting': '#f59e0b', 'Path traversal': '#5a8bff',
  'Command injection': '#fb7185', 'Scanner / probe': '#8b7cff', 'Rate limit': '#22c55e', 'Auto-ban': '#ef4444', 'Custom rule': '#a78bfa',
}
function merge(a = {}, b = {}) {
  const out = { ...a }
  for (const [k, v] of Object.entries(b)) out[k] = (out[k] || 0) + v
  return out
}

export default function SecurityPage({ security }) {
  const t = security?.tunnel, r = security?.repo
  const allowed = (t?.allowed || 0) + (r?.allowed || 0)
  const blocked = (t?.blocked || 0) + (r?.blocked || 0)
  const banned = (t?.banned_ips || 0) + (r?.banned_ips || 0)
  const cats = Object.entries(merge(t?.by_category, r?.by_category)).sort((a, b) => b[1] - a[1])
  const maxCat = Math.max(1, ...cats.map(([, v]) => v))
  const events = [...(t?.recent || []), ...(r?.recent || [])].sort((a, b) => new Date(b.time) - new Date(a.time))
  const recent = events.slice(0, 8)
  const total = allowed + blocked
  const score = Math.max(72, 100 - cats.length * 3 - (blocked > 500 ? 6 : 0))
  const grade = score >= 95 ? 'A+' : score >= 90 ? 'A' : score >= 80 ? 'B' : 'C'

  // REAL blocked-per-hour timeline, bucketed from actual firewall events
  const timeline = Array.from({ length: 24 }, (_, i) => ({ h: `${i}:00`, blocked: 0 }))
  events.forEach((e) => { const d = new Date(e.time); if (!isNaN(d)) timeline[d.getHours()].blocked++ })

  // REAL top attacker IPs, counted from the blocked events
  const ipCounts = {}
  events.forEach((e) => { if (e.ip) ipCounts[e.ip] = (ipCounts[e.ip] || 0) + 1 })
  const topIPs = Object.entries(ipCounts).sort((a, b) => b[1] - a[1]).slice(0, 5)
  const maxIP = Math.max(1, ...topIPs.map(([, v]) => v))

  return (
    <>
      <PageHeader title="Security" sub="Full transparency — every request we inspected and every attack we blocked." actions={<Badge tone="green" live>Firewall active</Badge>} />

      <div className="dx-stats">
        <StatCard i={0} icon={ShieldX} label="Blocked requests" value={blocked.toLocaleString()} tone="red" badge={<Badge tone="red" small>THREATS</Badge>} />
        <StatCard i={1} icon={ShieldCheck} label="Allowed requests" value={allowed.toLocaleString()} tone="green" />
        <StatCard i={2} icon={Ban} label="IPs banned now" value={banned.toLocaleString()} tone="amber" />
      </div>

      {/* score + donut + categories */}
      <div className="dx-row c2-wide dx-mt-lg">
        <motion.div {...fadeUp(1)}>
          <Card className="dx-card-pad" style={{ height: '100%' }}>
            <CardHead icon={ShieldCheck} title="Security score" />
            <div className="dx-score-wrap">
              <Donut
                size={148} thickness={13}
                segments={[
                  { value: allowed || 1, color: '#22c55e' },
                  { value: blocked, color: '#ef4444' },
                ]}
                center={
                  <div>
                    <div className="dx-score-num" style={{ color: score >= 90 ? '#22c55e' : '#f59e0b' }}>{score}</div>
                    <div style={{ fontSize: 11.5, color: 'var(--dx-text2)' }}>Grade {grade}</div>
                  </div>
                }
              />
              <div style={{ flex: 1, minWidth: 180 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13.5, marginBottom: 10 }}>
                  <span style={{ width: 9, height: 9, borderRadius: '50%', background: '#22c55e' }} /> Allowed
                  <b style={{ marginLeft: 'auto' }}>{allowed.toLocaleString()}</b>
                </div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13.5, marginBottom: 10 }}>
                  <span style={{ width: 9, height: 9, borderRadius: '50%', background: '#ef4444' }} /> Blocked
                  <b style={{ marginLeft: 'auto' }}>{blocked.toLocaleString()}</b>
                </div>
                <div style={{ fontSize: 12.5, color: 'var(--dx-text2)', marginTop: 14, lineHeight: 1.6 }}>
                  {total ? `${((blocked / total) * 100).toFixed(1)}% of traffic was malicious and stopped at the edge.` : 'No traffic seen yet — your firewall is armed and waiting.'}
                </div>
              </div>
            </div>
          </Card>
        </motion.div>

        <motion.div {...fadeUp(2)}>
          <Card className="dx-card-pad" style={{ height: '100%' }}>
            <CardHead icon={ShieldAlert} title="Threat categories" />
            {cats.length === 0
              ? <div className="dx-empty">No attacks blocked yet. That's a good thing.</div>
              : cats.map(([name, v]) => <BarRow key={name} label={name} value={v} max={maxCat} color={THREAT_COLORS[name] || '#8b7cff'} />)}
          </Card>
        </motion.div>
      </div>

      {/* attack timeline */}
      <motion.div {...fadeUp(3)} className="dx-mt-lg">
        <Card className="dx-card-pad">
          <CardHead icon={Globe2} title="Blocked attacks — by hour" action={<span style={{ fontSize: 12.5, color: 'var(--dx-text2)' }}><span style={{ color: '#ef4444' }}>●</span> Blocked</span>} />
          <div style={{ height: 210 }}>
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={timeline} margin={{ top: 6, right: 6, left: 0, bottom: 0 }}>
                <defs>
                  <linearGradient id="dxBlock" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stopColor="#ef4444" stopOpacity={0.4} /><stop offset="100%" stopColor="#ef4444" stopOpacity={0} /></linearGradient>
                </defs>
                <XAxis dataKey="h" stroke="#6b6b76" fontSize={11} tickLine={false} axisLine={false} interval={3} />
                <Tooltip contentStyle={{ background: '#16161b', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 12, color: '#fff', fontSize: 12 }} cursor={{ stroke: 'rgba(139,124,255,0.3)' }} />
                <Area type="monotone" dataKey="blocked" stroke="#ef4444" strokeWidth={2} fill="url(#dxBlock)" />
              </AreaChart>
            </ResponsiveContainer>
          </div>
        </Card>
      </motion.div>

      {/* top countries + blocked table */}
      <div className="dx-row c2-wide dx-mt-lg">
        <motion.div {...fadeUp(4)}>
          <Card className="dx-card-pad" style={{ height: '100%' }}>
            <CardHead icon={MapPin} title="Top attacker IPs" />
            {topIPs.length === 0 ? (
              <div className="dx-empty">No attackers yet — nothing has been blocked.</div>
            ) : topIPs.map(([ip, v]) => (
              <div key={ip} style={{ display: 'grid', gridTemplateColumns: '1fr 44px', alignItems: 'center', gap: 12, padding: '8px 0' }}>
                <div>
                  <div className="dx-mono" style={{ fontSize: 13, marginBottom: 5 }}>{ip}</div>
                  <div className="dx-bar" style={{ height: 6 }}><span style={{ width: `${(v / maxIP) * 100}%`, background: 'linear-gradient(90deg,#ef4444,#f59e0b)' }} /></div>
                </div>
                <span style={{ fontSize: 13, fontWeight: 600, textAlign: 'right' }}>{v}</span>
              </div>
            ))}
          </Card>
        </motion.div>

        <motion.div {...fadeUp(5)}>
          <Card className="dx-card-pad" style={{ height: '100%' }}>
            <CardHead icon={ShieldX} title="Latest blocked requests" />
            {recent.length === 0 ? (
              <div className="dx-empty">No attacks blocked yet.</div>
            ) : (
              <div style={{ overflowX: 'auto' }}>
                <table className="dx-table">
                  <thead><tr><th>Category</th><th>Request</th><th>IP</th><th>When</th></tr></thead>
                  <tbody>
                    {recent.map((e, i) => (
                      <tr key={i}>
                        <td><span style={{ display: 'inline-flex', alignItems: 'center', gap: 7 }}><span style={{ width: 8, height: 8, borderRadius: '50%', background: THREAT_COLORS[e.category] || '#8b7cff' }} />{e.category}</span></td>
                        <td className="dx-mono" style={{ color: 'var(--dx-text2)', maxWidth: 200, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={e.path}>{e.method} {e.path}</td>
                        <td className="dx-mono">{e.ip}</td>
                        <td style={{ color: 'var(--dx-text2)', whiteSpace: 'nowrap' }}>{timeAgo(e.time)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </Card>
        </motion.div>
      </div>
    </>
  )
}
