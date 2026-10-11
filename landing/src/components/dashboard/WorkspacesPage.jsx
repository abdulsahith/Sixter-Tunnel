import { useState, useEffect } from 'react'
import { motion } from 'framer-motion'
import {
  Boxes, Plus, Trash2, UserPlus, Crown, Server, Layout, Rocket, Eye, FlaskConical,
  Copy, Check, Link as LinkIcon, RefreshCw, ExternalLink,
} from 'lucide-react'
import { api } from '../../shared.jsx'
import { PageHeader, Card, Badge, fadeUp } from './widgets.jsx'

const ROLES = [
  { v: 'lead', label: 'Project lead', icon: Crown, tone: 'violet' },
  { v: 'backend', label: 'Backend', icon: Server, tone: 'green' },
  { v: 'frontend', label: 'Frontend', icon: Layout, tone: 'blue' },
  { v: 'deployment', label: 'Deployment', icon: Rocket, tone: 'amber' },
  { v: 'tester', label: 'Tester', icon: FlaskConical, tone: 'blue' },
  { v: 'viewer', label: 'Viewer', icon: Eye, tone: 'gray' },
]
const VIEWER = ROLES[ROLES.length - 1]
const roleLabel = (v) => (ROLES.find((r) => r.v === v) || VIEWER).label
const roleTone = (v) => (ROLES.find((r) => r.v === v) || VIEWER).tone

export default function WorkspacesPage({ me }) {
  const [list, setList] = useState(null)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [copied, setCopied] = useState('')      // wsId currently showing "Copied"
  const [rotating, setRotating] = useState('')
  const [links, setLinks] = useState({})        // wsId → join token
  // per-workspace add-member form state
  const [form, setForm] = useState({}) // wsId → {email, role}

  // Load workspaces, then make sure every one has its permanent join link
  // resolved (the endpoint mints it on first read, so old workspaces get one).
  const load = async () => {
    let ws = []
    try { const r = await api('/api/workspaces'); ws = r.workspaces || [] } catch { /* ignore */ }
    setList(ws)
    const found = {}
    await Promise.all(ws.map(async (w) => {
      if (w.join_token) { found[w.id] = w.join_token; return }
      try { const r = await api('/api/workspaces/' + w.id + '/join'); found[w.id] = r.token } catch { /* ignore */ }
    }))
    setLinks(found)
  }
  useEffect(() => { load() }, [])

  const rotate = async (wsId) => {
    if (!window.confirm('Generate a new link? The current link stops working — people already on the team keep their access.')) return
    setRotating(wsId); setErr('')
    try {
      const r = await api('/api/workspaces/' + wsId + '/join', { method: 'POST' })
      setLinks((s) => ({ ...s, [wsId]: r.token }))
    } catch (e) { setErr(e.message || 'could not rotate the link') }
    setRotating('')
  }

  const create = async () => {
    if (!name.trim()) return
    setBusy(true); setErr('')
    try { await api('/api/workspaces', { method: 'POST', body: JSON.stringify({ name: name.trim() }) }); setName(''); load() }
    catch (e) { setErr(e.message || 'could not create') }
    setBusy(false)
  }
  const delWs = async (id) => { try { await api('/api/workspaces/' + id, { method: 'DELETE' }); load() } catch (e) { setErr(e.message) } }

  const f = (wsId) => form[wsId] || { email: '', role: 'backend' }
  const setF = (wsId, patch) => setForm((s) => ({ ...s, [wsId]: { ...f(wsId), ...patch } }))

  const addMember = async (wsId) => {
    const cur = f(wsId)
    if (!cur.email.trim()) return
    setErr('')
    try {
      await api('/api/workspaces/' + wsId + '/members', { method: 'POST', body: JSON.stringify({ email: cur.email.trim(), role: cur.role }) })
      setF(wsId, { email: '' }); load()
    } catch (e) { setErr(e.message || 'could not add member') }
  }
  const removeMember = async (wsId, email) => {
    try { await api('/api/workspaces/' + wsId + '/members?email=' + encodeURIComponent(email), { method: 'DELETE' }); load() }
    catch (e) { setErr(e.message) }
  }

  const copyCmd = (wsId, token) => {
    try { navigator.clipboard.writeText('sixter project init ' + token) } catch { /* ignore */ }
    setCopied(wsId); setTimeout(() => setCopied(''), 1600)
  }

  return (
    <>
      <PageHeader title="Workspaces" sub="Group an app's work and allocate its team. Each workspace has one permanent join link — share it with everyone; each member is identified by their own Sixter login." />

      {/* create */}
      <Card style={{ marginBottom: 18 }}>
        <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'center' }}>
          <Boxes size={18} style={{ color: 'var(--dx-primary)' }} />
          <input className="dx-input" placeholder="New workspace (e.g. E-commerce)" value={name}
            onChange={(e) => setName(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && create()} style={{ flex: '1 1 260px' }} />
          <button className="dx-btn dx-btn-primary" onClick={create} disabled={busy || !name.trim()}><Plus size={15} /> Create workspace</button>
        </div>
        {err && <div style={{ marginTop: 10, fontSize: 13, color: '#fb7185' }}>{err}</div>}
      </Card>

      {list === null && <div style={{ color: 'var(--dx-text3)', padding: 12 }}>Loading…</div>}
      {list && list.length === 0 && <div style={{ color: 'var(--dx-text3)', padding: 12 }}>No workspaces yet — create one above.</div>}

      {list && list.map((wsp, i) => (
        <motion.div key={wsp.id} {...fadeUp(i)}>
          <Card style={{ marginBottom: 16 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 12 }}>
              <span style={{ width: 34, height: 34, borderRadius: 10, display: 'grid', placeItems: 'center', background: 'rgba(139,124,255,.14)', color: 'var(--dx-primary)' }}><Boxes size={18} /></span>
              <b style={{ fontSize: 16 }}>{wsp.name}</b>
              <Badge tone="gray" small>{(wsp.members || []).length} members</Badge>
              <div style={{ flex: 1 }} />
              <button className="dx-btn dx-btn-danger dx-btn-sm" onClick={() => delWs(wsp.id)}><Trash2 size={14} /> Delete</button>
            </div>

            {/* one permanent join link for the whole team */}
            <div style={{
              background: 'linear-gradient(180deg, rgba(139,124,255,.08), var(--dx-card-2))',
              border: '1px solid rgba(139,124,255,.28)', borderRadius: 12, padding: 14, marginBottom: 14,
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
                <LinkIcon size={15} style={{ color: 'var(--dx-primary)' }} />
                <b style={{ fontSize: 13.5 }}>Workspace join link</b>
                <Badge tone="green" small>PERMANENT</Badge>
                <div style={{ flex: 1 }} />
                <button className="dx-btn dx-btn-ghost dx-btn-sm" onClick={() => rotate(wsp.id)} disabled={rotating === wsp.id} title="Generate a new link if this one leaks">
                  <RefreshCw size={13} /> {rotating === wsp.id ? 'Rotating…' : 'Rotate'}
                </button>
              </div>
              <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                <code style={{
                  flex: '1 1 320px', fontFamily: 'var(--dx-mono, monospace)', fontSize: 12.5,
                  background: 'var(--dx-card)', border: '1px solid var(--dx-border)', borderRadius: 8,
                  padding: '9px 12px', overflowX: 'auto', whiteSpace: 'nowrap',
                }}>
                  sixter project init {links[wsp.id] || '…'}
                </code>
                <button className="dx-btn dx-btn-primary dx-btn-sm" onClick={() => copyCmd(wsp.id, links[wsp.id])} disabled={!links[wsp.id]}>
                  {copied === wsp.id ? <><Check size={14} /> Copied</> : <><Copy size={14} /> Copy</>}
                </button>
              </div>
              <div style={{ fontSize: 11.5, color: 'var(--dx-text3)', marginTop: 8, lineHeight: 1.6 }}>
                Everyone on the team uses this same command — it never expires. Each person is identified by
                their own Sixter login, so add them below first and they'll get the role you gave them.
              </div>
            </div>

            {/* members */}
            <div style={{ fontSize: 11, textTransform: 'uppercase', letterSpacing: .5, color: 'var(--dx-text3)', marginBottom: 2 }}>Team</div>
            {(wsp.members || []).length === 0 && (
              <div style={{ fontSize: 13, color: 'var(--dx-text3)', padding: '8px 0' }}>No teammates yet — add one below, then share the link.</div>
            )}
            {(wsp.members || []).map((m) => (
              <div key={m.email} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '9px 0', borderTop: '1px solid var(--dx-border)' }}>
                <span style={{
                  width: 30, height: 30, borderRadius: 9, flexShrink: 0, display: 'grid', placeItems: 'center',
                  background: 'linear-gradient(140deg,#8b7cff,#5a8bff)', color: '#fff', fontSize: 13, fontWeight: 700,
                }}>{m.email[0].toUpperCase()}</span>
                <a
                  href={`/member?email=${encodeURIComponent(m.email)}`}
                  title={`View ${m.email}'s profile`}
                  style={{ fontSize: 13.5, wordBreak: 'break-all', color: 'var(--dx-primary)', textDecoration: 'none', display: 'inline-flex', alignItems: 'center', gap: 5 }}
                >
                  {m.email} <ExternalLink size={12} />
                </a>
                <Badge tone={roleTone(m.role)} small>{roleLabel(m.role)}</Badge>
                <div style={{ flex: 1 }} />
                <button className="dx-icon-btn" title="Remove" onClick={() => removeMember(wsp.id, m.email)} style={{ color: '#fb7185' }}><Trash2 size={15} /></button>
              </div>
            ))}

            {/* add member */}
            <div style={{ display: 'flex', gap: 8, marginTop: 12, flexWrap: 'wrap', alignItems: 'center' }}>
              <UserPlus size={15} style={{ color: 'var(--dx-text3)' }} />
              <input className="dx-input" placeholder="teammate@company.com" value={f(wsp.id).email}
                onChange={(e) => setF(wsp.id, { email: e.target.value })} style={{ flex: '1 1 220px' }} />
              <select className="dx-select" value={f(wsp.id).role} onChange={(e) => setF(wsp.id, { role: e.target.value })} style={{ flex: '0 0 150px' }}>
                {ROLES.map((r) => <option key={r.v} value={r.v}>{r.label}</option>)}
              </select>
              <button className="dx-btn dx-btn-primary dx-btn-sm" onClick={() => addMember(wsp.id)} disabled={!f(wsp.id).email.trim()}>Add to team</button>
            </div>
          </Card>
        </motion.div>
      ))}
    </>
  )
}
