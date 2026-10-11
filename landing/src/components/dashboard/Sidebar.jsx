import { LayoutDashboard, ShieldCheck, Shield, Rocket, BookOpen, ChevronsUpDown, Wifi, Activity, ShieldAlert, Users, Boxes, UserCircle, Server } from 'lucide-react'
import { Badge } from './widgets.jsx'

const NAV = [
  {
    group: 'Platform',
    items: [{ id: 'overview', icon: LayoutDashboard, label: 'Overview' }],
  },
  {
    group: 'Security',
    items: [
      { id: 'security', icon: ShieldCheck, label: 'Security' },
      { id: 'firewall', icon: Shield, label: 'Firewall' },
    ],
  },
  {
    group: 'Workspace',
    items: [
      { id: 'workspaces', icon: Boxes, label: 'Workspaces' },
      { id: 'nodes', icon: Server, label: 'Nodes' },
      { id: 'team', icon: Users, label: 'Team' },
    ],
  },
  {
    group: 'Account',
    items: [
      { id: 'profile', icon: UserCircle, label: 'Profile' },
      { id: 'setup', icon: Rocket, label: 'Get started' },
    ],
  },
]

export default function Sidebar({ me, tab, setTab, nav, tunnelOn, repoOn, status, security, open, onClose }) {
  const tunnels = (tunnelOn ? 1 : 0) + (repoOn ? 1 : 0)
  // real metrics from the data the dashboard already polls
  const requests = (status?.tunnel?.requests || 0) + (status?.repo?.requests || 0)
  const blocked = (security?.tunnel?.blocked || 0) + (security?.repo?.blocked || 0)
  const fmt = (n) => (n >= 1000 ? (n / 1000).toFixed(n >= 10000 ? 0 : 1) + 'k' : String(n))
  return (
    <>
      {open && <div className="dx-overlay" onClick={onClose} />}
      <aside className={`dx-side ${open ? 'open' : ''}`}>
        <div className="dx-side-head">
          <button className="dx-logo" onClick={() => nav('/')}>
            <img src="/sixter-logo.png" alt="Sixter" />
          </button>

          <button className="dx-ws">
            <span className="dx-ws-avatar">{me.email[0].toUpperCase()}</span>
            <span className="dx-ws-text">
              <b>{me.tunnel_subdomain}</b>
              <span>Personal workspace</span>
            </span>
            <ChevronsUpDown size={15} style={{ color: 'var(--dx-text3)', flexShrink: 0 }} />
          </button>
        </div>

        <nav className="dx-nav">
          {NAV.map((g) => (
            <div key={g.group}>
              <div className="dx-nav-group">{g.group}</div>
              {g.items.map((it) => (
                <button
                  key={it.id}
                  className={`dx-nav-item ${tab === it.id ? 'on' : ''}`}
                  onClick={() => { setTab(it.id); onClose?.() }}
                >
                  <it.icon size={17} /> {it.label}
                  {it.id === 'security' && <Badge tone="violet" small>WAF</Badge>}
                </button>
              ))}
            </div>
          ))}
          <div className="dx-nav-group">Resources</div>
          <button className="dx-nav-item" onClick={() => nav('/docs')}><BookOpen size={17} /> Documentation</button>
        </nav>

        <div className="dx-side-foot">
          <div className="dx-usage">
            <div className="dx-usage-row">
              <span style={{ display: 'flex', alignItems: 'center', gap: 7, color: 'var(--dx-text2)' }}><Wifi size={14} /> Active tunnels</span>
              <b>{tunnels} / 2</b>
            </div>
            <div className="dx-bar"><span style={{ width: `${(tunnels / 2) * 100}%` }} /></div>
            <div className="dx-usage-row" style={{ marginTop: 14 }}>
              <span style={{ display: 'flex', alignItems: 'center', gap: 7, color: 'var(--dx-text2)' }}><Activity size={14} /> Requests served</span>
              <b>{fmt(requests)}</b>
            </div>
            <div className="dx-usage-row" style={{ marginTop: 10, marginBottom: 0 }}>
              <span style={{ display: 'flex', alignItems: 'center', gap: 7, color: 'var(--dx-text2)' }}><ShieldAlert size={14} /> Threats blocked</span>
              <b style={{ color: blocked ? '#fbbf24' : undefined }}>{fmt(blocked)}</b>
            </div>
          </div>

          <div
            className="dx-profile"
            role="button"
            title="Open your profile"
            style={{ cursor: 'pointer' }}
            onClick={() => { setTab('profile'); onClose?.() }}
          >
            <span className="dx-avatar">{me.email[0].toUpperCase()}</span>
            <span className="dx-profile-text">
              <b>{me.email.split('@')[0]}</b>
              <span>{me.email}</span>
            </span>
            <Badge tone="violet" small>{me.is_admin ? 'Admin' : 'Free'}</Badge>
          </div>
        </div>
      </aside>
    </>
  )
}
