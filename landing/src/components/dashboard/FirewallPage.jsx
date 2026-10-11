import { useState, useEffect } from 'react'
import { motion } from 'framer-motion'
import { Shield, ShieldAlert, Gauge, Ban, Bot, Globe, Plus, Trash2, Check } from 'lucide-react'
import { api } from '../../shared.jsx'
import { PageHeader, Card, CardHead, Switch, Badge, fadeUp } from './widgets.jsx'

const DEFAULT_FW = { enabled: true, waf: true, rate_limit_per_min: 600, auto_ban: true, rules: [] }

// Match fields & actions. Backend-supported ones persist; others are flagged "soon"
// so the premium UI is complete without ever breaking a save.
const FIELDS = [
  { v: 'ip', label: 'IP / CIDR' }, { v: 'country', label: 'Country' },
  { v: 'path', label: 'Path' }, { v: 'method', label: 'Method' }, { v: 'ua', label: 'User agent' },
  { v: 'asn', label: 'ASN', soon: true }, { v: 'header', label: 'Header', soon: true },
  { v: 'port', label: 'Port', soon: true }, { v: 'protocol', label: 'Protocol', soon: true },
]
const ACTIONS = [
  { v: 'block', label: 'Block' }, { v: 'allow', label: 'Allow' },
  { v: 'challenge', label: 'Challenge', soon: true }, { v: 'ratelimit', label: 'Rate limit', soon: true },
]

export default function FirewallPage({ me }) {
  const [which, setWhich] = useState('tunnel')
  const [fw, setFw] = useState(null)
  const [busy, setBusy] = useState(false)
  const [saved, setSaved] = useState(false)
  const [newRule, setNewRule] = useState({ field: 'ip', value: '', action: 'block', note: '' })

  const label = which === 'tunnel' ? `${me.tunnel_subdomain}.sixter.in` : `${me.repo_subdomain}.sixter.in`

  useEffect(() => {
    setFw(null)
    api('/api/me/firewall?sub=' + which)
      .then((r) => setFw(r.settings || { ...DEFAULT_FW }))
      .catch(() => setFw({ ...DEFAULT_FW }))
  }, [which])

  const save = async (next) => {
    setBusy(true); setSaved(false)
    try {
      await api('/api/me/firewall?sub=' + which, { method: 'POST', body: JSON.stringify(next) })
      setFw(next); setSaved(true); setTimeout(() => setSaved(false), 1800)
    } catch { /* ignore — matches original behaviour */ }
    setBusy(false)
  }

  const toggle = (k) => save({ ...fw, [k]: !fw[k] })
  const toggleRate = () => save({ ...fw, rate_limit_per_min: fw.rate_limit_per_min > 0 ? 0 : 600 })
  const addRule = () => {
    if (!newRule.value.trim()) return
    const r = { ...newRule, id: Date.now().toString(36), enabled: true }
    save({ ...fw, rules: [...(fw.rules || []), r] })
    setNewRule({ field: 'ip', value: '', action: 'block', note: '' })
  }
  const delRule = (id) => save({ ...fw, rules: fw.rules.filter((r) => r.id !== id) })

  const SETTINGS = fw && [
    { key: 'enabled', icon: Shield, title: 'Firewall', desc: 'Master switch — inspect every request at the edge.', on: fw.enabled, onClick: () => toggle('enabled') },
    { key: 'waf', icon: ShieldAlert, title: 'Attack detection', desc: 'Block SQLi, XSS, traversal and scanner signatures.', on: fw.waf, onClick: () => toggle('waf') },
    { key: 'rate', icon: Gauge, title: 'Rate limiting', desc: 'Throttle abusive IPs by requests per minute.', on: fw.rate_limit_per_min > 0, onClick: toggleRate },
    { key: 'auto_ban', icon: Ban, title: 'Auto ban', desc: 'Temporarily ban IPs that keep attacking.', on: fw.auto_ban, onClick: () => toggle('auto_ban') },
    { key: 'bot', icon: Bot, title: 'Bot protection', desc: 'Block scrapers, scanners & automation tools (curl, scrapy, headless browsers). Verified search engines stay allowed.', on: fw.bot_protection, onClick: () => toggle('bot_protection') },
    { key: 'geo', icon: Globe, title: 'Geo blocking', desc: 'Allow or block traffic by country.', soon: true },
  ]

  return (
    <>
      <PageHeader
        title="Firewall"
        sub="Tune protection for each subdomain. Changes apply instantly at the edge."
        actions={
          <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
            {saved && <Badge tone="green"><Check size={13} /> Saved</Badge>}
            <div style={{ display: 'flex', gap: 4, padding: 4, borderRadius: 11, background: 'var(--dx-card)', border: '1px solid var(--dx-border)' }}>
              {['tunnel', 'repo'].map((w) => (
                <button key={w} onClick={() => setWhich(w)} style={{
                  fontFamily: 'inherit', fontSize: 13, fontWeight: 600, padding: '6px 14px', borderRadius: 8, cursor: 'pointer', border: 0, textTransform: 'capitalize',
                  background: which === w ? 'rgba(139,124,255,0.16)' : 'transparent', color: which === w ? '#fff' : 'var(--dx-text2)',
                }}>{w}</button>
              ))}
            </div>
          </div>
        }
      />
      <div style={{ fontSize: 13, color: 'var(--dx-text2)', marginTop: -16, marginBottom: 24 }}>
        Editing <span className="dx-mono" style={{ color: 'var(--dx-primary)' }}>{label}</span>
      </div>

      {!fw ? (
        <div className="dx-fw-grid">{[0, 1, 2, 3, 4, 5].map((i) => <div key={i} className="dx-fw-card dx-shimmer" style={{ height: 120 }} />)}</div>
      ) : (
        <>
          <div className="dx-fw-grid">
            {SETTINGS.map((s, i) => (
              <motion.div key={s.key} className={`dx-fw-card ${s.on ? 'on' : ''}`} {...fadeUp(i)}>
                <div className="dx-fw-top">
                  <span style={{ width: 38, height: 38, borderRadius: 11, display: 'grid', placeItems: 'center', background: s.on ? 'rgba(139,124,255,0.16)' : 'rgba(255,255,255,0.05)', border: '1px solid var(--dx-border)', color: s.on ? 'var(--dx-primary)' : 'var(--dx-text2)' }}>
                    <s.icon size={19} />
                  </span>
                  {s.soon
                    ? <Badge tone="gray" small>Coming soon</Badge>
                    : <Switch on={s.on} onClick={s.onClick} disabled={busy} />}
                </div>
                <div style={{ fontSize: 15, fontWeight: 600, marginBottom: 4 }}>{s.title}</div>
                <div style={{ fontSize: 12.5, color: 'var(--dx-text2)', lineHeight: 1.5 }}>{s.desc}</div>
                {s.key === 'rate' && s.on && (
                  <div style={{ display: 'flex', alignItems: 'center', gap: 9, marginTop: 12 }}>
                    <input
                      className="dx-input mono" type="number" min="0" style={{ width: 90, padding: '7px 10px' }}
                      value={fw.rate_limit_per_min}
                      onChange={(e) => setFw({ ...fw, rate_limit_per_min: parseInt(e.target.value || '0', 10) })}
                      onBlur={() => save(fw)}
                    />
                    <span style={{ fontSize: 12, color: 'var(--dx-text2)' }}>req / min / IP</span>
                  </div>
                )}
              </motion.div>
            ))}
          </div>

          {/* rule builder */}
          <motion.div {...fadeUp(2)} className="dx-mt-lg">
            <Card className="dx-card-pad">
              <CardHead icon={Shield} title="Rule builder" action={<span style={{ fontSize: 12.5, color: 'var(--dx-text3)' }}>First match wins</span>} />
              <div style={{ display: 'grid', gridTemplateColumns: '150px 170px 1fr auto', gap: 12, alignItems: 'center' }} className="dx-rule-builder">
                <div>
                  <label style={{ fontSize: 11.5, color: 'var(--dx-text3)', display: 'block', marginBottom: 6 }}>ACTION</label>
                  <select className="dx-select" style={{ width: '100%' }} value={newRule.action} onChange={(e) => setNewRule({ ...newRule, action: e.target.value })}>
                    {ACTIONS.map((a) => <option key={a.v} value={a.v} disabled={a.soon}>{a.label}{a.soon ? ' (soon)' : ''}</option>)}
                  </select>
                </div>
                <div>
                  <label style={{ fontSize: 11.5, color: 'var(--dx-text3)', display: 'block', marginBottom: 6 }}>MATCH</label>
                  <select className="dx-select" style={{ width: '100%' }} value={newRule.field} onChange={(e) => setNewRule({ ...newRule, field: e.target.value })}>
                    {FIELDS.map((f) => <option key={f.v} value={f.v} disabled={f.soon}>{f.label}{f.soon ? ' (soon)' : ''}</option>)}
                  </select>
                </div>
                <div>
                  <label style={{ fontSize: 11.5, color: 'var(--dx-text3)', display: 'block', marginBottom: 6 }}>VALUE</label>
                  <input
                    className="dx-input mono" style={{ width: '100%' }}
                    placeholder={newRule.field === 'ip' ? '1.2.3.4 or 10.0.0.0/8' : newRule.field === 'country' ? 'US, RU, CN' : 'value'}
                    value={newRule.value}
                    onChange={(e) => setNewRule({ ...newRule, value: e.target.value })}
                    onKeyDown={(e) => e.key === 'Enter' && addRule()}
                  />
                </div>
                <button className="dx-btn dx-btn-primary" style={{ marginTop: 22 }} onClick={addRule}><Plus size={15} /> Add rule</button>
              </div>

              {/* preview */}
              <div style={{ marginTop: 16, padding: '12px 15px', borderRadius: 11, background: '#0c0c10', border: '1px solid var(--dx-border)', fontSize: 13, color: 'var(--dx-text2)' }} className="dx-mono">
                <span style={{ color: newRule.action === 'allow' ? '#22c55e' : '#ef4444', fontWeight: 600 }}>{newRule.action.toUpperCase()}</span>
                {' '}when <span style={{ color: 'var(--dx-secondary)' }}>{newRule.field}</span> matches{' '}
                <span style={{ color: '#e8e8f0' }}>{newRule.value || '…'}</span>
              </div>
            </Card>
          </motion.div>

          {/* rules table */}
          <motion.div {...fadeUp(3)} className="dx-mt-lg">
            <Card className="dx-card-pad">
              <CardHead icon={Shield} title={`Rules (${(fw.rules || []).length})`} />
              {(fw.rules || []).length === 0 ? (
                <div className="dx-empty">No custom rules yet. Build one above to allow or block specific traffic.</div>
              ) : (
                <div style={{ overflowX: 'auto' }}>
                  <table className="dx-table">
                    <thead><tr><th>Action</th><th>Field</th><th>Value</th><th>Note</th><th></th></tr></thead>
                    <tbody>
                      {fw.rules.map((r) => (
                        <tr key={r.id}>
                          <td><Badge tone={r.action === 'allow' ? 'green' : 'red'} small>{r.action}</Badge></td>
                          <td style={{ color: 'var(--dx-text2)' }}>{r.field}</td>
                          <td className="dx-mono">{r.value}</td>
                          <td style={{ color: 'var(--dx-text3)' }}>{r.note || '—'}</td>
                          <td style={{ textAlign: 'right' }}>
                            <button className="dx-icon-btn" style={{ width: 32, height: 32 }} onClick={() => delRule(r.id)} title="Delete rule"><Trash2 size={15} /></button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </Card>
          </motion.div>
        </>
      )}
    </>
  )
}
