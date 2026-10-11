import { motion } from 'framer-motion'
import {
  Wifi, Server, Activity, ShieldAlert, Rocket, Gauge, ExternalLink, Globe, Lock, Cpu, MemoryStick, HardDrive,
  GitBranch, Clock, ArrowUpRight, ShieldCheck, FileBadge, LogIn, Upload,
} from 'lucide-react'
import { fmtUptime, timeAgo } from '../../shared.jsx'
import { PageHeader, StatCard, Card, CardHead, Metric, CommandBox, Badge, fadeUp } from './widgets.jsx'

// deterministic pseudo-sparkline so cards aren't empty (cosmetic only)
const spark = (seed) => Array.from({ length: 16 }, (_, i) => 40 + Math.sin(i * 0.7 + seed) * 18 + (i * seed) % 11)

function connectedSince(uptime) {
  if (!uptime) return '—'
  const d = new Date(Date.now() - uptime * 1000)
  return d.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

export default function OverviewPage({ me, status, security, setTab }) {
  const tunnel = status?.tunnel
  const repo = status?.repo
  const tunnelOn = tunnel?.connected
  const repoOn = repo?.connected
  const totalReq = (tunnel?.requests || 0) + (repo?.requests || 0)
  const blocked = (security?.tunnel?.blocked || 0) + (security?.repo?.blocked || 0)
  const allowed = (security?.tunnel?.allowed || 0) + (security?.repo?.allowed || 0)
  const reqPerSec = tunnelOn && tunnel.uptime_secs ? Math.max(1, Math.round(tunnel.requests / tunnel.uptime_secs)) : 0

  const stats = [
    { icon: Wifi, label: 'Tunnel status', value: tunnelOn ? 'Online' : 'Offline', tone: tunnelOn ? 'green' : 'red', spark: spark(1), badge: <Badge tone={tunnelOn ? 'green' : 'gray'} live={tunnelOn} small>{tunnelOn ? 'LIVE' : 'IDLE'}</Badge> },
    { icon: Server, label: 'Repo server', value: repoOn ? 'Online' : 'Offline', tone: repoOn ? 'green' : 'red', spark: spark(2), badge: <Badge tone={repoOn ? 'green' : 'gray'} live={repoOn} small>{repoOn ? 'LIVE' : 'IDLE'}</Badge> },
    { icon: Activity, label: 'Total requests', value: totalReq.toLocaleString(), tone: 'blue', spark: spark(3) },
    { icon: ShieldAlert, label: 'Threats blocked', value: blocked.toLocaleString(), tone: blocked ? 'amber' : 'green', spark: spark(4), badge: <Badge tone={blocked ? 'amber' : 'green'} small>{blocked ? 'BLOCKED' : 'CLEAR'}</Badge> },
    { icon: ShieldCheck, label: 'Requests allowed', value: allowed.toLocaleString(), tone: 'green', spark: spark(5) },
    { icon: Gauge, label: 'Tunnel latency', value: tunnelOn && tunnel?.latency_ms ? `${tunnel.latency_ms} ms` : '—', tone: 'violet', spark: spark(6) },
  ]

  // Real activity only — built from what the account actually reports:
  // live firewall events plus tunnel/repo connection state. Nothing simulated.
  const fwEvents = [
    ...((security?.tunnel?.recent) || []),
    ...((security?.repo?.recent) || []),
  ]
  const activity = fwEvents.map((e, i) => ({
    key: `fw-${i}-${e.time || ''}-${e.ip || ''}`,
    icon: ShieldCheck,
    color: '#f59e0b',
    t: 'Firewall blocked request',
    d: `${e.category || e.reason || 'blocked'}${e.ip ? ' from ' + e.ip : ''}`,
    time: e.time,
  }))
  if (tunnelOn && tunnel?.connected_since) {
    activity.push({ key: 'tunnel-up', icon: Wifi, color: '#8b7cff', t: 'Tunnel started', d: `${tunnel.subdomain}.sixter.in`, time: tunnel.connected_since })
  }
  if (repoOn && repo?.connected_since) {
    activity.push({ key: 'repo-up', icon: Server, color: '#22c55e', t: 'Repo server online', d: `${repo.subdomain}.sixter.in`, time: repo.connected_since })
  }
  activity.sort((a, b) => new Date(b.time || 0) - new Date(a.time || 0))
  const recentActivity = activity.slice(0, 6)

  return (
    <>
      <PageHeader
        title="Overview"
        sub="Everything Sixter is doing for your account, in real time."
        actions={<Badge tone="green" live>All systems operational</Badge>}
      />

      <div className="dx-stats">
        {stats.map((s, i) => <StatCard key={s.label} i={i} {...s} />)}
      </div>

      {/* tunnel + repo detail cards */}
      <div className="dx-row c2 dx-mt-lg">
        <motion.div {...fadeUp(1)}>
          <Card className="dx-card-pad" style={{ height: '100%' }}>
            <CardHead icon={Wifi} title="Tunnel" action={<Badge tone={tunnelOn ? 'green' : 'gray'} live={tunnelOn}>{tunnelOn ? 'Connected' : 'Offline'}</Badge>} />
            {tunnelOn ? (
              <a href={`https://${tunnel.subdomain}.sixter.in`} target="_blank" rel="noreferrer" style={{ display: 'inline-flex', alignItems: 'center', gap: 7, color: 'var(--dx-primary)', fontSize: 14, fontWeight: 500, marginBottom: 16 }}>
                <Globe size={15} /> {tunnel.subdomain}.sixter.in <ExternalLink size={13} />
              </a>
            ) : <p style={{ color: 'var(--dx-text2)', fontSize: 13.5, marginBottom: 16 }}>Start a tunnel to expose a local port on your subdomain.</p>}

            <div className="dx-metrics">
              <Metric k="Latency" v={tunnelOn && tunnel?.latency_ms ? `${tunnel.latency_ms} ms` : '—'} />
              <Metric k="Requests" v={tunnelOn ? (tunnel.requests || 0).toLocaleString() : '—'} />
              <Metric k="Requests/sec" v={tunnelOn ? reqPerSec : '—'} />
              <Metric k="Connected" v={tunnelOn ? fmtUptime(tunnel.uptime_secs) : '—'} />
              <Metric k="Domains" v={tunnelOn ? (tunnel?.domains?.length || 1) : '—'} />
              <Metric k="SSL" v={tunnelOn ? 'Active' : '—'} dot={tunnelOn ? '#22c55e' : undefined} />
            </div>
            <div style={{ marginTop: 12, fontSize: 12, color: 'var(--dx-text3)' }}>
              {tunnelOn ? `Connected since ${tunnel?.connected_since ? new Date(tunnel.connected_since).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : connectedSince(tunnel?.uptime_secs)}` : 'Not connected'}
            </div>
            <div className="dx-mt"><CommandBox cmd="sixter tunnel -port 3000" title="start tunnel" /></div>
          </Card>
        </motion.div>

        <motion.div {...fadeUp(2)}>
          <Card className="dx-card-pad" style={{ height: '100%' }}>
            <CardHead icon={Server} title="Repo server" action={<Badge tone={repoOn ? 'green' : 'gray'} live={repoOn}>{repoOn ? 'Online' : 'Offline'}</Badge>} />
            {repoOn ? (
              <a href={`https://${repo.subdomain}.sixter.in`} target="_blank" rel="noreferrer" style={{ display: 'inline-flex', alignItems: 'center', gap: 7, color: 'var(--dx-primary)', fontSize: 14, fontWeight: 500, marginBottom: 16 }}>
                <GitBranch size={15} /> {repo.subdomain}.sixter.in <ExternalLink size={13} />
              </a>
            ) : <p style={{ color: 'var(--dx-text2)', fontSize: 13.5, marginBottom: 16 }}>Run the repo server on your VPS to deploy and manage projects.</p>}

            <div className="dx-metrics">
              <Metric k="Status" v={repoOn ? 'Online' : 'Offline'} dot={repoOn ? '#22c55e' : undefined} />
              <Metric k="Requests" v={repoOn ? (repo.requests || 0).toLocaleString() : '—'} />
              <Metric k="Latency" v={repoOn && repo?.latency_ms ? `${repo.latency_ms} ms` : '—'} />
              <Metric k="Connected" v={repoOn ? fmtUptime(repo.uptime_secs) : '—'} />
              <Metric k="Domains" v={repoOn ? (repo?.domains?.length || 1) : '—'} />
              <Metric k="Blocked" v={repoOn ? (security?.repo?.blocked || 0).toLocaleString() : '—'} />
            </div>
            <div style={{ marginTop: 12, fontSize: 12, color: 'var(--dx-text3)' }}>
              {repoOn
                ? <>Live CPU, memory and per-project stats are in your <a href={`https://${repo.subdomain}.sixter.in`} target="_blank" rel="noreferrer" style={{ color: 'var(--dx-primary)' }}>repo dashboard</a>.</>
                : 'Start the repo server to deploy and manage projects.'}
            </div>
            <div className="dx-mt">
              <CommandBox cmd={`sixter repo -server "${me.repo_subdomain}" -project myapp`} title="deploy project" />
            </div>
          </Card>
        </motion.div>
      </div>

      {/* activity timeline + protection banner */}
      <div className="dx-row c2-wide dx-mt-lg">
        <motion.div {...fadeUp(3)}>
          <Card className="dx-card-pad" style={{ height: '100%' }}>
            <CardHead icon={Clock} title="Activity" action={<button className="dx-link" onClick={() => setTab('security')}>View all <ArrowUpRight size={13} style={{ verticalAlign: -2 }} /></button>} />
            <div className="dx-timeline">
              {recentActivity.length === 0 && (
                <div style={{ color: 'var(--dx-text3)', fontSize: 13.5, padding: '8px 0' }}>
                  No activity yet — start a tunnel or deploy a project and events will appear here.
                </div>
              )}
              {recentActivity.map((a) => (
                <div className="dx-tl-item" key={a.key}>
                  <span className="dx-tl-ico" style={{ color: a.color }}><a.icon size={16} /></span>
                  <div className="dx-tl-body"><b>{a.t}</b><span>{a.d}</span></div>
                  <span className="dx-tl-time">{a.time ? timeAgo(a.time) : ''}</span>
                </div>
              ))}
            </div>
          </Card>
        </motion.div>

        <motion.div {...fadeUp(4)}>
          <Card className="dx-card-pad" style={{ height: '100%', background: 'linear-gradient(180deg, rgba(139,124,255,0.08), var(--dx-card))' }}>
            <CardHead icon={ShieldCheck} title="You're protected" />
            <p style={{ color: 'var(--dx-text2)', fontSize: 13.5, lineHeight: 1.7 }}>
              Every request flowing through your tunnels is inspected by our edge firewall
              <b style={{ color: 'var(--dx-text)' }}> before</b> it reaches your machine — SQL injection,
              XSS, path traversal, scanners and floods are blocked automatically.
            </p>
            <div className="dx-metrics dx-mt" style={{ gridTemplateColumns: '1fr 1fr' }}>
              <Metric k="Blocked" v={blocked.toLocaleString()} dot="#ef4444" />
              <Metric k="Banned IPs" v={((security?.tunnel?.banned_ips || 0) + (security?.repo?.banned_ips || 0))} dot="#f59e0b" />
            </div>
            <button className="dx-btn dx-btn-ghost dx-mt" style={{ width: '100%' }} onClick={() => setTab('security')}>
              <Lock size={15} /> Open security center
            </button>
          </Card>
        </motion.div>
      </div>
    </>
  )
}
