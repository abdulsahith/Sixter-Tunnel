import { useState, useEffect } from 'react'
import { motion } from 'framer-motion'
import {
  Users, Wifi, Activity, ShieldCheck, Server, Clock, ShieldAlert, LogOut, AlertTriangle, UserCog,
} from 'lucide-react'
import { api, fmtUptime, timeAgo } from './shared.jsx'
import { PageHeader, StatCard, Card, CardHead, Badge, Metric, fadeUp } from './components/dashboard/widgets.jsx'

/* Platform admin dashboard — served on admin.<apex>. The control plane:
   user management, system health, and platform-wide security.
   Rebuilt on the premium `.dx` design system (matches the user console). */

function AdminOverview() {
  const [o, setO] = useState(null)
  const [err, setErr] = useState('')
  useEffect(() => {
    const load = () => api('/api/admin/overview').then(setO).catch((e) => setErr(e.message))
    load()
    const id = setInterval(load, 5000)
    return () => clearInterval(id)
  }, [])
  if (err) return <Card className="dx-card-pad" style={{ borderColor: 'rgba(239,68,68,0.3)', color: '#f87171' }}>{err}</Card>
  if (!o) return <div className="dx-empty">Loading…</div>
  const sys = o.system || {}
  const guard = o.guard || {}
  const adoption = o.users ? Math.round((o.twoFactor / o.users) * 100) : 0

  return (
    <>
      {guard.underAttack && (
        <motion.div {...fadeUp(0)} className="dx-card dx-card-pad" style={{ borderColor: 'rgba(239,68,68,0.4)', background: 'rgba(239,68,68,0.08)', marginBottom: 18, display: 'flex', alignItems: 'center', gap: 12 }}>
          <AlertTriangle size={20} style={{ color: '#f87171', flexShrink: 0 }} />
          <div style={{ fontSize: 13.5 }}>
            <b style={{ color: '#f87171' }}>Edge under attack</b> — challenge mode active. {guard.reqPerSec} req/s, {guard.bannedIPs} IP(s) banned.
          </div>
        </motion.div>
      )}

      <div className="dx-stats">
        <StatCard i={0} icon={Users} label="Total users" value={o.users} tone="violet" badge={<Badge tone="violet" small>{o.admins} admin{o.admins === 1 ? '' : 's'}</Badge>} />
        <StatCard i={1} icon={Wifi} label="Live tunnels" value={o.tunnelsLive} tone="green" badge={<Badge tone={o.tunnelsLive ? 'green' : 'gray'} live={!!o.tunnelsLive} small>{o.tunnelsLive ? 'LIVE' : 'IDLE'}</Badge>} />
        <StatCard i={2} icon={Activity} label="Total requests" value={(o.totalRequests || 0).toLocaleString()} tone="blue" />
        <StatCard i={3} icon={ShieldCheck} label="2FA adoption" value={adoption + '%'} tone={adoption >= 80 ? 'green' : 'amber'} badge={<Badge tone={adoption >= 80 ? 'green' : 'amber'} small>{o.twoFactor}/{o.users}</Badge>} />
      </div>

      <div className="dx-row c2-wide dx-mt-lg">
        <motion.div {...fadeUp(1)}>
          <Card className="dx-card-pad" style={{ height: '100%' }}>
            <CardHead icon={Server} title="System health" action={<Badge tone={guard.underAttack ? 'red' : 'green'} live={!guard.underAttack}>{guard.underAttack ? 'Under attack' : 'Normal'}</Badge>} />
            <div className="dx-metrics">
              <Metric k="Uptime" v={fmtUptime(sys.uptimeSecs)} />
              <Metric k="Load (1m)" v={sys.loadAvg || '—'} />
              <Metric k="Memory" v={`${sys.memMB} MB`} />
              <Metric k="CPU cores" v={sys.cpus} />
              <Metric k="Goroutines" v={sys.goroutines} />
              <Metric k="Edge req/s" v={guard.reqPerSec ?? 0} />
              <Metric k="Banned IPs" v={guard.bannedIPs ?? 0} dot={(guard.bannedIPs ?? 0) ? '#f59e0b' : undefined} />
              <Metric k="DDoS guard" v={guard.underAttack ? 'Attack' : 'Normal'} dot={guard.underAttack ? '#ef4444' : '#22c55e'} />
            </div>
          </Card>
        </motion.div>

        <motion.div {...fadeUp(2)}>
          <Card className="dx-card-pad" style={{ height: '100%' }}>
            <CardHead icon={Clock} title="Recent signups" />
            {(o.recentSignups || []).length === 0 ? (
              <div className="dx-empty">No accounts yet.</div>
            ) : (
              <div style={{ display: 'flex', flexDirection: 'column' }}>
                {(o.recentSignups || []).map((s) => (
                  <div key={s.email} style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '10px 0', borderTop: '1px solid var(--dx-border)' }}>
                    <span style={{ width: 32, height: 32, borderRadius: 9, display: 'grid', placeItems: 'center', fontWeight: 700, fontSize: 13, color: '#fff', background: 'linear-gradient(135deg,#8b7cff,#5a8bff)', flexShrink: 0 }}>{s.email[0].toUpperCase()}</span>
                    <span style={{ flex: 1, minWidth: 0, fontSize: 13.5, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{s.email}</span>
                    <span style={{ fontSize: 12.5, color: 'var(--dx-text3)', whiteSpace: 'nowrap' }}>{timeAgo(s.createdAt)}</span>
                  </div>
                ))}
              </div>
            )}
          </Card>
        </motion.div>
      </div>
    </>
  )
}

function AdminUsers({ meEmail, ask, notify }) {
  const [rows, setRows] = useState(null)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState('')
  const load = () => api('/api/admin/users').then((d) => setRows(d.users || [])).catch((e) => setErr(e.message))
  useEffect(() => { load() }, [])

  const act = async (email, fn, confirmOpts) => {
    if (confirmOpts && !(await ask(confirmOpts))) return
    setBusy(email)
    try { await fn(); await load(); notify('Done') } catch (e) { notify(e.message, 'err') }
    setBusy('')
  }
  const revoke = (u) => act(u.email, () => api(`/api/admin/users/${encodeURIComponent(u.email)}/revoke`, { method: 'POST' }),
    { title: 'Force log out?', body: `Sign ${u.email} out of all devices immediately.`, confirmLabel: 'Force logout', danger: true })
  const toggleAdmin = (u) => act(u.email, () => api(`/api/admin/users/${encodeURIComponent(u.email)}/admin`, { method: 'POST', body: JSON.stringify({ on: !u.isAdmin }) }),
    { title: u.isAdmin ? 'Revoke admin?' : 'Grant admin?', body: u.email, confirmLabel: u.isAdmin ? 'Revoke admin' : 'Make admin', danger: u.isAdmin })
  const disconnect = (u) => act(u.email, () => api(`/api/admin/users/${encodeURIComponent(u.email)}/disconnect`, { method: 'POST' }),
    { title: 'Disconnect their tunnel?', body: `Kills ${u.email}'s live tunnel. Their CLI can reconnect — use Suspend to stop that.`, confirmLabel: 'Disconnect', danger: true })
  const suspend = (u) => act(u.email, () => api(`/api/admin/users/${encodeURIComponent(u.email)}/suspend`, { method: 'POST', body: JSON.stringify({ on: !u.suspended }) }),
    u.suspended
      ? { title: 'Restore this account?', body: `${u.email} will be able to log in and reconnect their tunnel.`, confirmLabel: 'Restore' }
      : { title: 'Suspend this account?', body: `Disconnects ${u.email}'s tunnels and blocks login, tunnel auth and their API token. Reversible.`, confirmLabel: 'Suspend', danger: true })
  const del = (u) => act(u.email, () => api(`/api/admin/users/${encodeURIComponent(u.email)}`, { method: 'DELETE' }),
    { title: 'Delete this account?', body: `Permanently delete ${u.email}, their subdomains and all data. This cannot be undone.`, confirmLabel: 'Delete', danger: true })

  if (err) return <Card className="dx-card-pad" style={{ borderColor: 'rgba(239,68,68,0.3)', color: '#f87171' }}>{err}</Card>
  if (!rows) return <div className="dx-empty">Loading…</div>
  return (
    <motion.div {...fadeUp(0)}>
      <Card className="dx-card-pad">
        <CardHead icon={UserCog} title="All users" action={<Badge tone="violet">{rows.length}</Badge>} />
        <div style={{ overflowX: 'auto' }}>
          <table className="dx-table">
            <thead><tr><th>Email</th><th>Subdomain</th><th>Status</th><th>2FA</th><th>Joined</th><th style={{ textAlign: 'right' }}>Actions</th></tr></thead>
            <tbody>
              {rows.map((u) => (
                <tr key={u.email}>
                  <td>
                    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7, flexWrap: 'wrap' }}>
                      {u.email}
                      {u.isAdmin && <Badge tone="violet" small>admin</Badge>}
                      {u.suspended && <Badge tone="red" small>suspended</Badge>}
                      {u.email === meEmail && <Badge tone="gray" small>you</Badge>}
                    </span>
                  </td>
                  <td className="dx-mono">{u.tunnelSub}</td>
                  <td>
                    {u.tunnelLive
                      ? <Badge tone="green" live small>{fmtUptime(u.uptimeSecs)} · {(u.requests || 0).toLocaleString()} req</Badge>
                      : <Badge tone="gray" small>offline</Badge>}
                  </td>
                  <td>{u.twoFactor ? <ShieldCheck size={16} style={{ color: '#22c55e' }} /> : <span style={{ color: 'var(--dx-text3)' }}>—</span>}</td>
                  <td style={{ color: 'var(--dx-text2)', whiteSpace: 'nowrap' }}>{timeAgo(u.createdAt)}</td>
                  <td>
                    <div style={{ display: 'flex', gap: 7, justifyContent: 'flex-end', flexWrap: 'wrap' }}>
                      <button className="dx-btn dx-btn-ghost dx-btn-sm" disabled={busy === u.email} onClick={() => revoke(u)}>Force logout</button>
                      <button className="dx-btn dx-btn-ghost dx-btn-sm" disabled={busy === u.email || !u.tunnelLive} onClick={() => disconnect(u)}>Disconnect</button>
                      <button className="dx-btn dx-btn-ghost dx-btn-sm" disabled={busy === u.email || u.email === meEmail} onClick={() => suspend(u)} style={{ color: u.suspended ? '#4ade80' : '#fbbf24' }}>{u.suspended ? 'Restore' : 'Suspend'}</button>
                      <button className="dx-btn dx-btn-ghost dx-btn-sm" disabled={busy === u.email} onClick={() => toggleAdmin(u)}>{u.isAdmin ? 'Revoke admin' : 'Make admin'}</button>
                      <button className="dx-btn dx-btn-danger dx-btn-sm" disabled={busy === u.email || u.email === meEmail} onClick={() => del(u)}>Delete</button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>
    </motion.div>
  )
}

function AdminSecurity() {
  const [subs, setSubs] = useState(null)
  const [err, setErr] = useState('')
  useEffect(() => {
    const load = () => api('/api/admin/security').then((d) => setSubs(d.subdomains || [])).catch((e) => setErr(e.message))
    load()
    const id = setInterval(load, 6000)
    return () => clearInterval(id)
  }, [])
  if (err) return <Card className="dx-card-pad" style={{ borderColor: 'rgba(239,68,68,0.3)', color: '#f87171' }}>{err}</Card>
  if (!subs) return <div className="dx-empty">Loading…</div>
  // NB: the firewall returns allowed/blocked/recent — NOT totalAllowed/events.
  const active = subs.filter((s) => s.stats && ((s.stats.blocked || 0) > 0 || (s.stats.allowed || 0) > 0))
  return (
    <motion.div {...fadeUp(0)}>
      <Card className="dx-card-pad">
        <CardHead icon={ShieldAlert} title="Firewall — platform wide" action={<Badge tone="green" live>Edge active</Badge>} />
        <p style={{ fontSize: 13.5, color: 'var(--dx-text2)', lineHeight: 1.6, marginBottom: 18 }}>
          Requests inspected at the Sixter edge across every tunnel. Attacks are blocked before they ever reach a user's machine.
        </p>
        <div style={{ overflowX: 'auto' }}>
          <table className="dx-table">
            <thead><tr><th>Subdomain</th><th>Owner</th><th>Tunnel ID</th><th>Allowed</th><th>Blocked</th><th>Banned IPs</th><th>Events</th></tr></thead>
            <tbody>
              {active.length === 0 && <tr><td colSpan="7"><div className="dx-empty">No traffic recorded yet.</div></td></tr>}
              {active.map((s) => (
                <tr key={s.subdomain}>
                  <td className="dx-mono">
                    {s.subdomain}
                    {s.kind && <Badge tone={s.kind === 'repo' ? 'blue' : 'violet'} small>{s.kind}</Badge>}
                  </td>
                  <td style={{ color: 'var(--dx-text2)' }}>
                    {s.email}{s.suspended && <Badge tone="red" small>SUSPENDED</Badge>}
                  </td>
                  <td className="dx-mono" style={{ fontSize: 11, color: 'var(--dx-text3)' }}>{(s.tunnelId || '—').slice(0, 8)}</td>
                  <td style={{ color: '#4ade80' }}>{(s.stats.allowed || 0).toLocaleString()}</td>
                  <td style={{ color: s.stats.blocked ? '#f87171' : 'var(--dx-text2)' }}>{(s.stats.blocked || 0).toLocaleString()}</td>
                  <td style={{ color: 'var(--dx-text2)' }}>{s.stats.banned_ips || 0}</td>
                  <td style={{ color: 'var(--dx-text2)' }}>{(s.stats.recent || []).length} logged</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>
    </motion.div>
  )
}

/* Live attack feed across every tunnel, with one-click edge bans. */
function AdminThreats({ ask, notify }) {
  const [d, setD] = useState(null)
  const [err, setErr] = useState('')
  const [ip, setIp] = useState('')
  const load = () => api('/api/admin/threats?limit=200').then(setD).catch((e) => setErr(e.message))
  useEffect(() => { load(); const id = setInterval(load, 6000); return () => clearInterval(id) }, [])

  const ban = async (addr, hours = 0) => {
    if (!(await ask({ title: 'Ban ' + addr + ' at the edge?', body: 'Blocks this IP across every tunnel on the platform.', confirmLabel: 'Ban IP', danger: true }))) return
    try { await api('/api/admin/bans', { method: 'POST', body: JSON.stringify({ ip: addr, hours }) }); setIp(''); notify('Banned ' + addr); load() }
    catch (e) { notify(e.message, 'err') }
  }
  const unban = async (addr) => {
    try { await api('/api/admin/bans?ip=' + encodeURIComponent(addr), { method: 'DELETE' }); notify('Lifted ' + addr); load() }
    catch (e) { notify(e.message, 'err') }
  }

  if (err) return <Card className="dx-card-pad" style={{ borderColor: 'rgba(239,68,68,0.3)', color: '#f87171' }}>{err}</Card>
  if (!d) return <div className="dx-empty">Loading…</div>
  const cats = Object.entries(d.byCategory || {}).sort((a, b) => b[1] - a[1])

  return (
    <motion.div {...fadeUp(0)} style={{ display: 'grid', gap: 16 }}>
      <Card className="dx-card-pad">
        <CardHead icon={ShieldAlert} title="Ban an IP platform-wide" />
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
          <input className="dx-input" placeholder="203.0.113.9" value={ip} onChange={(e) => setIp(e.target.value)} style={{ flex: '1 1 220px' }} />
          <button className="dx-btn dx-btn-danger dx-btn-sm" onClick={() => ban(ip.trim(), 0)} disabled={!ip.trim()}>Ban permanently</button>
          <button className="dx-btn dx-btn-ghost dx-btn-sm" onClick={() => ban(ip.trim(), 24)} disabled={!ip.trim()}>Ban 24h</button>
        </div>
        {(d.banned || []).length > 0 && (
          <div style={{ marginTop: 14 }}>
            <div style={{ fontSize: 11, textTransform: 'uppercase', letterSpacing: .5, color: 'var(--dx-text3)', marginBottom: 6 }}>Active edge bans</div>
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
              {d.banned.map((b) => (
                <span key={b.ip} className="dx-mono" style={{ fontSize: 12, background: 'var(--dx-card-2)', border: '1px solid var(--dx-border)', borderRadius: 8, padding: '5px 9px', display: 'inline-flex', gap: 8, alignItems: 'center' }}>
                  {b.ip}
                  <button className="dx-btn dx-btn-ghost dx-btn-sm" style={{ padding: '1px 7px', fontSize: 11 }} onClick={() => unban(b.ip)}>lift</button>
                </span>
              ))}
            </div>
          </div>
        )}
      </Card>

      <div className="dx-row c2">
        <Card className="dx-card-pad">
          <CardHead icon={AlertTriangle} title="Top attacker IPs" />
          {(d.topIPs || []).length === 0 && <div className="dx-empty">No attacks recorded.</div>}
          {(d.topIPs || []).map((t) => (
            <div key={t.ip} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '7px 0', borderTop: '1px solid var(--dx-border)' }}>
              <span className="dx-mono" style={{ fontSize: 13 }}>{t.ip}</span>
              <Badge tone="red" small>{t.count}</Badge>
              <div style={{ flex: 1 }} />
              <button className="dx-btn dx-btn-danger dx-btn-sm" onClick={() => ban(t.ip, 0)}>Ban</button>
            </div>
          ))}
        </Card>
        <Card className="dx-card-pad">
          <CardHead icon={ShieldCheck} title="Attack categories" />
          {cats.length === 0 && <div className="dx-empty">Nothing blocked yet.</div>}
          {cats.map(([c, n]) => (
            <div key={c} style={{ display: 'flex', justifyContent: 'space-between', padding: '7px 0', borderTop: '1px solid var(--dx-border)', fontSize: 13.5 }}>
              <span>{c}</span><b style={{ color: '#f87171' }}>{n}</b>
            </div>
          ))}
        </Card>
      </div>

      <Card className="dx-card-pad">
        <CardHead icon={Activity} title="Live threat feed" action={<Badge tone="violet">{(d.threats || []).length}</Badge>} />
        <div style={{ overflowX: 'auto' }}>
          <table className="dx-table">
            <thead><tr><th>When</th><th>Attacker IP</th><th>Target</th><th>Owner</th><th>Attack</th><th>Path</th><th /></tr></thead>
            <tbody>
              {(d.threats || []).length === 0 && <tr><td colSpan="7"><div className="dx-empty">No attacks blocked yet.</div></td></tr>}
              {(d.threats || []).map((t, i) => (
                <tr key={i}>
                  <td style={{ color: 'var(--dx-text3)', fontSize: 12 }}>{timeAgo(t.time)}</td>
                  <td className="dx-mono">{t.ip}</td>
                  <td className="dx-mono" style={{ fontSize: 12 }}>{t.subdomain}</td>
                  <td style={{ color: 'var(--dx-text2)', fontSize: 12.5 }}>{t.owner}</td>
                  <td><Badge tone="red" small>{t.category || t.reason}</Badge></td>
                  <td className="dx-mono" style={{ fontSize: 11.5, maxWidth: 260, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{t.method} {t.path}</td>
                  <td style={{ textAlign: 'right' }}><button className="dx-btn dx-btn-ghost dx-btn-sm" onClick={() => ban(t.ip, 0)}>Ban</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>
    </motion.div>
  )
}

/* Who actually uses the platform: companies vs individuals. */
function AdminBusiness() {
  const [b, setB] = useState(null)
  const [err, setErr] = useState('')
  useEffect(() => { api('/api/admin/business').then(setB).catch((e) => setErr(e.message)) }, [])
  if (err) return <Card className="dx-card-pad" style={{ borderColor: 'rgba(239,68,68,0.3)', color: '#f87171' }}>{err}</Card>
  if (!b) return <div className="dx-empty">Loading…</div>
  const stats = [
    { icon: Users, label: 'Total accounts', value: b.totalAccounts, tone: 'violet' },
    { icon: Server, label: 'Companies', value: b.companies, tone: 'blue' },
    { icon: UserCog, label: 'Individuals', value: b.individuals, tone: 'green' },
    { icon: AlertTriangle, label: 'Suspended', value: b.suspended, tone: b.suspended ? 'red' : 'gray' },
  ]
  return (
    <motion.div {...fadeUp(0)} style={{ display: 'grid', gap: 16 }}>
      <div className="dx-stats">{stats.map((s, i) => <StatCard key={s.label} i={i} {...s} />)}</div>
      <Card className="dx-card-pad">
        <CardHead icon={Server} title="Companies using Sixter" action={<Badge tone="blue">{(b.companyList || []).length}</Badge>} />
        <div style={{ overflowX: 'auto' }}>
          <table className="dx-table">
            <thead><tr><th>Company</th><th>Contact</th><th>Website</th><th>Location</th><th>Since</th></tr></thead>
            <tbody>
              {(b.companyList || []).length === 0 && <tr><td colSpan="5"><div className="dx-empty">No company accounts yet — users set this on their profile page.</div></td></tr>}
              {(b.companyList || []).map((c) => (
                <tr key={c.email}>
                  <td><b>{c.company || '—'}</b></td>
                  <td style={{ color: 'var(--dx-text2)' }}>{c.name ? c.name + ' · ' : ''}{c.email}</td>
                  <td>{c.website ? <a href={c.website} target="_blank" rel="noreferrer" style={{ color: 'var(--dx-primary)' }}>{c.website.replace(/^https?:\/\//, '')}</a> : '—'}</td>
                  <td style={{ color: 'var(--dx-text2)' }}>{c.location || '—'}</td>
                  <td style={{ color: 'var(--dx-text3)', fontSize: 12 }}>{new Date(c.since).toLocaleDateString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div style={{ fontSize: 12, color: 'var(--dx-text3)', marginTop: 12 }}>
          {b.noProfile} account{b.noProfile === 1 ? '' : 's'} haven't filled in a profile yet, so they're not counted either way.
        </div>
      </Card>
      <Card className="dx-card-pad">
        <CardHead icon={Activity} title="Busiest live tunnels" />
        {(b.topTunnels || []).length === 0 && <div className="dx-empty">No tunnels connected.</div>}
        {(b.topTunnels || []).map((t) => (
          <div key={t.subdomain} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '8px 0', borderTop: '1px solid var(--dx-border)' }}>
            <span className="dx-mono" style={{ fontSize: 13 }}>{t.subdomain}</span>
            <span style={{ color: 'var(--dx-text2)', fontSize: 12.5 }}>{t.email}</span>
            <div style={{ flex: 1 }} />
            <b>{(t.requests || 0).toLocaleString()}</b><span style={{ color: 'var(--dx-text3)', fontSize: 12 }}>reqs</span>
          </div>
        ))}
      </Card>
    </motion.div>
  )
}

/* Every privileged action, for accountability. */
function AdminAudit() {
  const [e, setE] = useState(null)
  useEffect(() => { api('/api/admin/audit').then((d) => setE(d.events || [])).catch(() => setE([])) }, [])
  if (!e) return <div className="dx-empty">Loading…</div>
  return (
    <motion.div {...fadeUp(0)}>
      <Card className="dx-card-pad">
        <CardHead icon={Clock} title="Admin audit log" action={<Badge tone="violet">{e.length}</Badge>} />
        <p style={{ fontSize: 13, color: 'var(--dx-text2)', marginBottom: 14 }}>
          Every privileged action since the last restart. Also written to the server journal, which survives restarts.
        </p>
        <div style={{ overflowX: 'auto' }}>
          <table className="dx-table">
            <thead><tr><th>When</th><th>Admin</th><th>Action</th><th>Target</th><th>Detail</th></tr></thead>
            <tbody>
              {e.length === 0 && <tr><td colSpan="5"><div className="dx-empty">No admin actions recorded yet.</div></td></tr>}
              {e.map((x, i) => (
                <tr key={i}>
                  <td style={{ color: 'var(--dx-text3)', fontSize: 12 }}>{timeAgo(x.time)}</td>
                  <td style={{ color: 'var(--dx-text2)' }}>{x.admin}</td>
                  <td><b>{x.action}</b></td>
                  <td className="dx-mono" style={{ fontSize: 12 }}>{x.target}</td>
                  <td style={{ color: 'var(--dx-text3)', fontSize: 12 }}>{x.detail || '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>
    </motion.div>
  )
}

export function AdminDashboard({ me, logout }) {
  const [tab, setTab] = useState('overview')
  const TABS = [
    ['overview', 'Overview'], ['users', 'Users'], ['threats', 'Threats'],
    ['security', 'Security'], ['business', 'Business'], ['audit', 'Audit'],
  ]

  // professional confirm dialog + toast (replaces native confirm/alert)
  const [confirmReq, setConfirmReq] = useState(null)
  const ask = (opts) => new Promise((resolve) => setConfirmReq({ ...opts, resolve }))
  const resolveConfirm = (val) => setConfirmReq((c) => { c && c.resolve(val); return null })
  const [toast, setToast] = useState(null)
  const notify = (text, kind = 'ok') => { setToast({ text, kind }); setTimeout(() => setToast(null), 3800) }

  return (
    <div className="dx">
      <div className="dx-main" style={{ minHeight: '100vh' }}>
        {/* topbar */}
        <div className="dx-top">
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <img src="/sixter.png" alt="" style={{ height: 24 }} />
            <b style={{ fontSize: 15 }}>Sixter</b>
            <Badge tone="violet" small>ADMIN</Badge>
          </div>
          <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 14 }}>
            <span style={{ color: 'var(--dx-text2)', fontSize: 13 }} className="dx-hide-sm">{me.email}</span>
            <button className="dx-btn dx-btn-ghost dx-btn-sm" onClick={logout}><LogOut size={15} /> Log out</button>
          </div>
        </div>

        <main className="dx-content">
          <PageHeader title="Admin control plane" sub="Platform-wide users, system health and edge security." />

          {/* tab bar */}
          <div style={{ display: 'flex', gap: 4, padding: 4, borderRadius: 12, background: 'var(--dx-card)', border: '1px solid var(--dx-border)', width: 'fit-content', marginBottom: 28, flexWrap: 'wrap' }}>
            {TABS.map(([id, label]) => (
              <button key={id} onClick={() => setTab(id)} style={{
                fontFamily: 'inherit', fontSize: 13.5, fontWeight: 600, padding: '7px 16px', borderRadius: 9, cursor: 'pointer', border: 0,
                background: tab === id ? 'rgba(139,124,255,0.16)' : 'transparent', color: tab === id ? '#fff' : 'var(--dx-text2)',
              }}>{label}</button>
            ))}
          </div>

          {tab === 'overview' && <AdminOverview />}
          {tab === 'users' && <AdminUsers meEmail={me.email} ask={ask} notify={notify} />}
          {tab === 'threats' && <AdminThreats ask={ask} notify={notify} />}
          {tab === 'security' && <AdminSecurity />}
          {tab === 'business' && <AdminBusiness />}
          {tab === 'audit' && <AdminAudit />}
        </main>
      </div>

      {confirmReq && (
        <div className="modal-backdrop" onClick={() => resolveConfirm(false)}>
          <div className="modal" onClick={(e) => e.stopPropagation()}>
            <h3>{confirmReq.title}</h3>
            <p>{confirmReq.body}</p>
            <div className="row-flex mt" style={{ justifyContent: 'flex-end', gap: 10 }}>
              <button className="btn" onClick={() => resolveConfirm(false)}>Cancel</button>
              <button className={'btn ' + (confirmReq.danger ? 'danger' : 'primary')} autoFocus onClick={() => resolveConfirm(true)}>
                {confirmReq.confirmLabel || 'Confirm'}
              </button>
            </div>
          </div>
        </div>
      )}
      {toast && <div className={'toast ' + toast.kind}>{toast.text}</div>}
    </div>
  )
}
