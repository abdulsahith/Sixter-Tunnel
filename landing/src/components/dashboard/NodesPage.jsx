import { useState, useEffect } from 'react'
import { motion } from 'framer-motion'
import {
  Server, Cpu, MemoryStick, Copy, Check, RefreshCw, Trash2, Circle, Boxes, Terminal,
} from 'lucide-react'
import { api, fmtUptime } from '../../shared.jsx'
import { PageHeader, StatCard, Card, CardHead, Badge, fadeUp } from './widgets.jsx'

/*
Fleet view. One permanent join token enrols every machine; each agent then
holds its own credentials. Agents dial OUT, so nothing here asks the user to
open a port or configure a VPC.
*/

const fmtBytes = (b) => {
  if (!b) return '—'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  while (b >= 1024 && i < u.length - 1) { b /= 1024; i++ }
  return b.toFixed(i > 2 ? 1 : 0) + ' ' + u[i]
}

const ago = (t) => {
  const s = Math.floor((Date.now() - new Date(t).getTime()) / 1000)
  if (s < 60) return s + 's ago'
  if (s < 3600) return Math.floor(s / 60) + 'm ago'
  if (s < 86400) return Math.floor(s / 3600) + 'h ago'
  return Math.floor(s / 86400) + 'd ago'
}

/* Fleet workloads — desired state. You say what should run; the gateway picks
   a node and its agent reconciles toward it on the next heartbeat. */
function FleetApps({ nodes }) {
  const [apps, setApps] = useState(null)
  const [form, setForm] = useState({ project: '', image: '', command: '', port: '', node_id: '', memory_mb: '', cpu_percent: '' })
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  const load = () => api('/api/fleet/apps').then((r) => setApps(r.apps || [])).catch(() => setApps([]))
  useEffect(() => { load(); const id = setInterval(load, 8000); return () => clearInterval(id) }, [])

  const set = (k) => (e) => setForm((f) => ({ ...f, [k]: e.target.value }))

  const deploy = async () => {
    setBusy(true); setErr('')
    try {
      await api('/api/fleet/apps', {
        method: 'POST',
        body: JSON.stringify({
          project: form.project.trim(),
          image: form.image.trim(),
          command: form.command.trim(),
          port: Number(form.port) || 0,
          node_id: form.node_id || '',
          memory_mb: Number(form.memory_mb) || 0,
          cpu_percent: Number(form.cpu_percent) || 0,
        }),
      })
      setForm({ project: '', image: '', command: '', port: '', node_id: '', memory_mb: '', cpu_percent: '' })
      load()
    } catch (e) { setErr(e.message) }
    setBusy(false)
  }

  const remove = async (a) => {
    if (!window.confirm(`Stop ${a.project}? The node will destroy its container.`)) return
    try { await api('/api/fleet/apps?id=' + encodeURIComponent(a.id), { method: 'DELETE' }); load() }
    catch (e) { setErr(e.message) }
  }

  const nodeName = (id) => nodes.find((n) => n.id === id)?.name || id
  const tone = { running: 'green', pending: 'amber', failed: 'red', stopped: 'gray' }

  return (
    <Card className="dx-card-pad" style={{ marginTop: 18 }}>
      <CardHead icon={Boxes} title="Fleet workloads" action={<Badge tone="gray">{apps?.length || 0}</Badge>} />
      <p style={{ fontSize: 13, color: 'var(--dx-text2)', lineHeight: 1.6, marginBottom: 14 }}>
        Describe what should run; Sixter places it on the node with the most free capacity and the
        agent starts it within about ten seconds. A base image is required — the node has to pull
        the filesystem from somewhere.
      </p>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))', gap: 10, marginBottom: 10 }}>
        <input className="dx-input" placeholder="app name" value={form.project} onChange={set('project')} />
        <input className="dx-input mono" placeholder="node:20-slim" value={form.image} onChange={set('image')} />
        <input className="dx-input mono" placeholder="npm start" value={form.command} onChange={set('command')} />
        <input className="dx-input" placeholder="port" value={form.port} onChange={set('port')} />
        <select className="dx-select" value={form.node_id} onChange={set('node_id')}>
          <option value="">Auto-place</option>
          {nodes.filter((n) => n.online).map((n) => <option key={n.id} value={n.id}>{n.name}</option>)}
        </select>
        <input className="dx-input" placeholder="memory MB" value={form.memory_mb} onChange={set('memory_mb')} />
      </div>
      <button className="dx-btn dx-btn-primary dx-btn-sm" onClick={deploy}
              disabled={busy || !form.project.trim() || !form.image.trim()}>
        {busy ? 'Placing…' : 'Deploy to fleet'}
      </button>
      {err && <div style={{ marginTop: 10, fontSize: 13, color: '#fb7185' }}>{err}</div>}

      {apps && apps.length > 0 && (
        <div style={{ marginTop: 16 }}>
          {apps.map((a) => (
            <div key={a.id} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 0', borderTop: '1px solid var(--dx-border)', flexWrap: 'wrap' }}>
              <b style={{ fontSize: 13.5 }}>{a.project}</b>
              <Badge tone={tone[a.state] || 'gray'} small>{a.state}</Badge>
              {/* Not a link: this is the shared pool address, and every replica
                  of the project answers on it. Presenting it per-row as a
                  clickable endpoint made replicas look like separate
                  deployments, which is the opposite of what scaling does. */}
              {a.subdomain && (
                <span className="dx-mono" style={{ fontSize: 11.5, color: 'var(--dx-text3)' }}>
                  serves {a.subdomain}.sixter.in
                </span>
              )}
              <span className="dx-mono" style={{ fontSize: 11.5, color: 'var(--dx-text3)' }}>{a.image}</span>
              <span style={{ fontSize: 12, color: 'var(--dx-text2)' }}>on {nodeName(a.node_id)}</span>
              <div style={{ flex: 1 }} />
              {a.message && <span style={{ fontSize: 11.5, color: '#fb7185', maxWidth: 340, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={a.message}>{a.message}</span>}
              <button className="dx-icon-btn" onClick={() => remove(a)} style={{ color: '#fb7185' }}><Trash2 size={15} /></button>
            </div>
          ))}
        </div>
      )}
      {apps && apps.length === 0 && (
        <div className="dx-empty" style={{ marginTop: 14 }}>Nothing scheduled yet.</div>
      )}
    </Card>
  )
}

export default function NodesPage() {
  const [data, setData] = useState(null)
  const [token, setToken] = useState('')
  const [copied, setCopied] = useState('')
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState('')

  const load = () => api('/api/nodes').then(setData).catch((e) => setErr(e.message))
  const loadToken = () => api('/api/nodes/token').then((r) => setToken(r.token)).catch(() => {})

  useEffect(() => {
    load(); loadToken()
    const id = setInterval(load, 8000) // nodes beat every 10s
    return () => clearInterval(id)
  }, [])

  const rotate = async () => {
    if (!window.confirm('Generate a new join token? Machines already enrolled keep working; new ones must use the new token.')) return
    setBusy('rotate')
    try { const r = await api('/api/nodes/token', { method: 'POST' }); setToken(r.token) }
    catch (e) { setErr(e.message) }
    setBusy('')
  }

  const remove = async (n) => {
    if (!window.confirm(`Remove ${n.name}? Its agent will re-enrol if it's still running with a join token.`)) return
    try { await api('/api/nodes?id=' + encodeURIComponent(n.id), { method: 'DELETE' }); load() }
    catch (e) { setErr(e.message) }
  }

  const copy = (what, text) => {
    try { navigator.clipboard.writeText(text) } catch { /* ignore */ }
    setCopied(what); setTimeout(() => setCopied(''), 1600)
  }

  const installCmd = `curl -fsSL https://sixter.in/install.sh | sh -s -- --join ${token || '<token>'}`
  const cloudInit = `#cloud-config\nruncmd:\n  - ${installCmd}`

  const f = data?.fleet
  const stats = [
    { icon: Server, label: 'Nodes online', value: f ? `${f.online} / ${f.total}` : '—', tone: f?.online ? 'green' : 'gray' },
    { icon: Cpu, label: 'Total cores', value: f?.cpus || '—', tone: 'violet' },
    { icon: MemoryStick, label: 'Total memory', value: fmtBytes(f?.memTotal), tone: 'blue' },
  ]

  return (
    <>
      <PageHeader
        title="Nodes"
        sub="Machines running the Sixter agent. Agents connect outward — no inbound ports, no VPC changes."
      />

      {err && <Card className="dx-card-pad" style={{ borderColor: 'rgba(239,68,68,.3)', color: '#f87171', marginBottom: 16 }}>{err}</Card>}

      <div className="dx-stats" style={{ marginBottom: 18 }}>
        {stats.map((s, i) => <StatCard key={s.label} i={i} {...s} />)}
      </div>

      {/* add a node */}
      <motion.div {...fadeUp(1)}>
        <Card className="dx-card-pad" style={{ marginBottom: 18, background: 'linear-gradient(180deg, rgba(139,124,255,.07), var(--dx-card))', borderColor: 'rgba(139,124,255,.25)' }}>
          <CardHead
            icon={Terminal}
            title="Add a machine"
            action={
              <button className="dx-btn dx-btn-ghost dx-btn-sm" onClick={rotate} disabled={busy === 'rotate'}>
                <RefreshCw size={13} /> {busy === 'rotate' ? 'Rotating…' : 'Rotate token'}
              </button>
            }
          />
          <p style={{ fontSize: 13.5, color: 'var(--dx-text2)', lineHeight: 1.6, marginBottom: 12 }}>
            Run this on any Linux machine — an EC2 instance, a GCP VM, or a server in your rack.
            The same command works everywhere.
          </p>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
            <code style={{
              flex: '1 1 380px', fontFamily: 'var(--dx-mono, monospace)', fontSize: 12.5,
              background: 'var(--dx-card-2)', border: '1px solid var(--dx-border)', borderRadius: 9,
              padding: '10px 13px', overflowX: 'auto', whiteSpace: 'nowrap',
            }}>{installCmd}</code>
            <button className="dx-btn dx-btn-primary dx-btn-sm" onClick={() => copy('cmd', installCmd)} disabled={!token}>
              {copied === 'cmd' ? <><Check size={14} /> Copied</> : <><Copy size={14} /> Copy</>}
            </button>
          </div>

          <div style={{ marginTop: 14 }}>
            <div style={{ fontSize: 11, textTransform: 'uppercase', letterSpacing: .5, color: 'var(--dx-text3)', marginBottom: 6 }}>
              Autoscaling — paste into instance start-up script
            </div>
            <div style={{ display: 'flex', gap: 8, alignItems: 'flex-start', flexWrap: 'wrap' }}>
              <pre style={{
                flex: '1 1 380px', margin: 0, fontFamily: 'var(--dx-mono, monospace)', fontSize: 12,
                background: 'var(--dx-card-2)', border: '1px solid var(--dx-border)', borderRadius: 9,
                padding: '10px 13px', overflowX: 'auto',
              }}>{cloudInit}</pre>
              <button className="dx-btn dx-btn-ghost dx-btn-sm" onClick={() => copy('ci', cloudInit)} disabled={!token}>
                {copied === 'ci' ? <><Check size={14} /> Copied</> : <><Copy size={14} /> Copy</>}
              </button>
            </div>
            <div style={{ fontSize: 12, color: 'var(--dx-text3)', marginTop: 8, lineHeight: 1.6 }}>
              AWS calls this <b>User data</b>, GCP <b>Startup script</b>, Azure <b>Custom data</b>.
              Machines your autoscaler creates will enrol themselves.
            </div>
          </div>
        </Card>
      </motion.div>

      {/* fleet */}
      <motion.div {...fadeUp(2)}>
        <Card className="dx-card-pad">
          <CardHead icon={Boxes} title="Fleet" action={<Badge tone="gray">{data?.nodes?.length || 0}</Badge>} />

          {!data && <div className="dx-empty">Loading…</div>}
          {data && data.nodes.length === 0 && (
            <div className="dx-empty">
              No machines yet — run the command above on a server and it will appear here within seconds.
            </div>
          )}

          {data && data.nodes.map((n) => (
            <div key={n.id} style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '13px 0', borderTop: '1px solid var(--dx-border)', flexWrap: 'wrap' }}>
              <span style={{
                width: 34, height: 34, borderRadius: 10, display: 'grid', placeItems: 'center', flexShrink: 0,
                background: n.online ? 'rgba(34,197,94,.14)' : 'var(--dx-card-2)',
                color: n.online ? '#4ade80' : 'var(--dx-text3)',
              }}><Server size={17} /></span>

              <div style={{ flex: '1 1 200px', minWidth: 0 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                  <b style={{ fontSize: 14 }}>{n.name}</b>
                  {n.online
                    ? <span style={{ fontSize: 11.5, color: '#4ade80', display: 'inline-flex', alignItems: 'center', gap: 4 }}><Circle size={7} fill="#22c55e" strokeWidth={0} /> online</span>
                    : <span style={{ fontSize: 11.5, color: 'var(--dx-text3)' }}>offline · {ago(n.lastSeen)}</span>}
                  <Badge tone={n.isolation === 'container' ? 'violet' : 'amber'} small>
                    {n.isolation === 'container' ? 'isolated' : 'unisolated'}
                  </Badge>
                </div>
                <div style={{ fontSize: 12, color: 'var(--dx-text3)', marginTop: 3 }}>
                  {n.os}/{n.arch} · {n.cpus} cores · {fmtBytes(n.memTotal)} · agent {n.agent}
                </div>
                {n.isolation !== 'container' && n.isolationReason && (
                  <div style={{ fontSize: 11.5, color: '#fbbf24', marginTop: 5, lineHeight: 1.5 }}>
                    {n.isolationReason}
                    {/^.*cgroup v2.*$/i.test(n.isolationReason) && (
                      <span style={{ color: 'var(--dx-text3)' }}>
                        {' '}— boot with <span className="dx-mono">systemd.unified_cgroup_hierarchy=1</span> and reboot.
                      </span>
                    )}
                  </div>
                )}
              </div>

              <div style={{ flex: '0 0 auto', display: 'flex', gap: 18, fontSize: 12.5 }}>
                <div><div style={{ color: 'var(--dx-text3)', fontSize: 11 }}>CPU</div><b>{n.online ? n.cpuPct + '%' : '—'}</b></div>
                <div><div style={{ color: 'var(--dx-text3)', fontSize: 11 }}>MEM</div><b>{n.online ? n.memPct + '%' : '—'}</b></div>
                <div><div style={{ color: 'var(--dx-text3)', fontSize: 11 }}>APPS</div><b>{n.instances}</b></div>
              </div>

              <button className="dx-icon-btn" title="Remove node" onClick={() => remove(n)} style={{ color: '#fb7185' }}>
                <Trash2 size={15} />
              </button>
            </div>
          ))}
        </Card>
      </motion.div>

      {/* fleet workloads */}
      <motion.div {...fadeUp(3)}>
        <FleetApps nodes={data?.nodes || []} />
      </motion.div>

      <div style={{ fontSize: 12.5, color: 'var(--dx-text3)', marginTop: 16, lineHeight: 1.7 }}>
        A node marked <b>unisolated</b> lacks cgroup v2 or overlayfs, so workloads there run as
        plain processes. Sixter reports this rather than implying isolation it can't provide.
      </div>
    </>
  )
}
