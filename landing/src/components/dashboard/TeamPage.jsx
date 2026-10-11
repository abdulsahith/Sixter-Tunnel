import { useState, useEffect } from 'react'
import { motion } from 'framer-motion'
import { Users, UserPlus, Trash2, Crown, Server, Layout, Eye, Check, Mail } from 'lucide-react'
import { api } from '../../shared.jsx'
import { PageHeader, Card, CardHead, Badge, fadeUp } from './widgets.jsx'

const ROLES = [
  { v: 'lead', label: 'Team lead', icon: Crown, tone: 'violet', desc: 'Full control — every project, deploys, domains, firewall and member management.' },
  { v: 'backend', label: 'Backend', icon: Server, tone: 'green', desc: 'Backend + shared projects: deploy, env vars, databases and terminal.' },
  { v: 'frontend', label: 'Frontend', icon: Layout, tone: 'blue', desc: 'Frontend + shared projects: deploy, env vars and logs.' },
  { v: 'viewer', label: 'Viewer', icon: Eye, tone: 'gray', desc: 'Read-only — status, logs and traffic across the whole team.' },
]
const roleMeta = (v) => ROLES.find((r) => r.v === v) || ROLES[3]

const rowStyle = {
  display: 'flex', alignItems: 'center', gap: 12, padding: '12px 2px',
  borderTop: '1px solid var(--dx-border)',
}

export default function TeamPage({ me }) {
  const [members, setMembers] = useState(null)
  const [owner, setOwner] = useState('')
  const [email, setEmail] = useState('')
  const [role, setRole] = useState('backend')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [saved, setSaved] = useState(false)

  const load = () => api('/api/team/members')
    .then((r) => { setMembers(r.members || []); setOwner(r.owner || '') })
    .catch(() => setMembers([]))
  useEffect(() => { load() }, [])

  const flash = () => { setSaved(true); setTimeout(() => setSaved(false), 1600) }

  const add = async () => {
    setErr(''); setBusy(true)
    try {
      await api('/api/team/members', { method: 'POST', body: JSON.stringify({ email: email.trim(), role }) })
      setEmail(''); flash(); load()
    } catch (e) { setErr(e.message || 'could not add member') }
    setBusy(false)
  }
  const changeRole = async (m, r) => {
    try { await api('/api/team/members', { method: 'POST', body: JSON.stringify({ email: m.email, role: r }) }); flash(); load() }
    catch (e) { setErr(e.message || 'could not update role') }
  }
  const remove = async (m) => {
    try { await api('/api/team/members?email=' + encodeURIComponent(m.email), { method: 'DELETE' }); load() }
    catch (e) { setErr(e.message || 'could not remove') }
  }

  return (
    <>
      <PageHeader
        title="Team"
        sub="Invite teammates and set their role. They sign in with their own Sixter account — no extra passwords."
        actions={saved && <Badge tone="green"><Check size={13} /> Saved</Badge>}
      />

      {/* role legend */}
      <div className="dx-fw-grid" style={{ marginBottom: 22 }}>
        {ROLES.map((r, i) => (
          <motion.div key={r.v} className="dx-fw-card" {...fadeUp(i)}>
            <div className="dx-fw-top">
              <span style={{ width: 38, height: 38, borderRadius: 11, display: 'grid', placeItems: 'center', background: 'rgba(139,124,255,0.12)', border: '1px solid var(--dx-border)', color: 'var(--dx-primary)' }}>
                <r.icon size={19} />
              </span>
              <Badge tone={r.tone} small>{r.label}</Badge>
            </div>
            <div style={{ fontSize: 12.5, color: 'var(--dx-text2)', lineHeight: 1.5, marginTop: 8 }}>{r.desc}</div>
          </motion.div>
        ))}
      </div>

      {/* add member */}
      <Card style={{ marginBottom: 18 }}>
        <CardHead icon={UserPlus} title="Add a member" />
        <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'center', marginTop: 8 }}>
          <div style={{ position: 'relative', flex: '1 1 260px' }}>
            <Mail size={15} style={{ position: 'absolute', left: 12, top: '50%', transform: 'translateY(-50%)', color: 'var(--dx-text3)' }} />
            <input
              className="dx-input" placeholder="teammate@company.com" value={email}
              onChange={(e) => setEmail(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && add()}
              style={{ width: '100%', paddingLeft: 36 }}
            />
          </div>
          <select className="dx-select" value={role} onChange={(e) => setRole(e.target.value)} style={{ flex: '0 0 170px' }}>
            {ROLES.map((r) => <option key={r.v} value={r.v}>{r.label}</option>)}
          </select>
          <button className="dx-btn dx-btn-primary" onClick={add} disabled={busy || !email.trim()}>
            {busy ? '…' : 'Add member'}
          </button>
        </div>
        {err && <div style={{ marginTop: 10, fontSize: 13, color: '#fb7185' }}>{err}</div>}
      </Card>

      {/* members */}
      <Card>
        <CardHead icon={Users} title="Members" />

        {/* owner (you) */}
        <div style={{ ...rowStyle, borderTop: 'none' }}>
          <span className="dx-avatar">{(owner || me.email)[0].toUpperCase()}</span>
          <div style={{ flex: 1, minWidth: 0 }}>
            <b style={{ fontSize: 14 }}>{owner || me.email}</b>
            <div style={{ fontSize: 12, color: 'var(--dx-text3)' }}>You — team owner</div>
          </div>
          <Badge tone="violet" small><Crown size={12} /> Owner</Badge>
        </div>

        {members === null && <div style={{ ...rowStyle, color: 'var(--dx-text3)' }}>Loading…</div>}
        {members && members.length === 0 && (
          <div style={{ ...rowStyle, fontSize: 13, color: 'var(--dx-text3)' }}>No teammates yet — add one above.</div>
        )}
        {members && members.map((m) => (
          <div key={m.email} style={rowStyle}>
            <span className="dx-avatar" style={{ background: 'rgba(255,255,255,0.07)', color: 'var(--dx-text2)' }}>{m.email[0].toUpperCase()}</span>
            <div style={{ flex: 1, minWidth: 0 }}>
              <b style={{ fontSize: 14, wordBreak: 'break-all' }}>{m.email}</b>
              <div style={{ fontSize: 12 }}>
                {m.registered
                  ? <span style={{ color: '#34d399' }}>● active account</span>
                  : <span style={{ color: '#fbbf24' }}>● invited — must sign up to gain access</span>}
              </div>
            </div>
            <select className="dx-select" value={m.role} onChange={(e) => changeRole(m, e.target.value)} style={{ flex: '0 0 150px', padding: '6px 10px' }}>
              {ROLES.map((r) => <option key={r.v} value={r.v}>{r.label}</option>)}
            </select>
            <button className="dx-icon-btn" title="Remove member" onClick={() => remove(m)} style={{ color: '#fb7185' }}>
              <Trash2 size={16} />
            </button>
          </div>
        ))}
      </Card>

      <div style={{ fontSize: 12.5, color: 'var(--dx-text3)', marginTop: 16, lineHeight: 1.6 }}>
        Roles take effect on your repo dashboard once member sign-in (SSO) is enabled. Today this builds your team;
        enforcement arrives with the next phase — nothing here changes how your repo-server works right now.
      </div>
    </>
  )
}
