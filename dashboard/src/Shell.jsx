import { useState, useEffect, useMemo, useRef } from 'react'
import {
  Home, Activity, ArrowRightLeft, Database, History, Boxes, GitPullRequest,
  Folder, Users, UserCircle, BookOpen, Search, PanelLeftClose, PanelLeft,
  Cpu, MemoryStick, Circle, CornerDownLeft, Layers,
} from 'lucide-react'

/*
Repo-server shell — navigation only.

The old sidebar mixed navigation with every project, customer and server in one
stack. Projects now live on their own page; the sidebar is a short, grouped,
scrollable nav with server health pinned at the bottom. Matches the main Sixter
console so the two feel like one product.
*/

export const NAV_GROUPS = [
  {
    group: 'Observe',
    items: [
      { id: 'home', icon: Home, label: 'Overview' },
      { id: 'traffic', icon: Activity, label: 'Traffic' },
      { id: 'packets', icon: ArrowRightLeft, label: 'Packet capture' },
      { id: 'databases', icon: Database, label: 'Databases' },
      { id: 'activity', icon: History, label: 'Activity' },
    ],
  },
  {
    group: 'Deployments',
    items: [{ id: 'projects', icon: Folder, label: 'Projects', countKey: 'projects' }],
  },
  {
    group: 'Workspace',
    items: [
      { id: 'prs', icon: GitPullRequest, label: 'Pull requests', countKey: 'prs' },
      { id: 'workspaces', icon: Boxes, label: 'Workspaces' },
    ],
  },
  {
    group: 'Settings',
    items: [
      { id: 'team', icon: Users, label: 'Team' },
      { id: 'profile', icon: UserCircle, label: 'Profile' },
      { id: 'docs', icon: BookOpen, label: 'Documentation', external: 'https://sixter.in/docs' },
    ],
  },
]

/* ─────────── sidebar ─────────── */

export function Sidebar({ page, go, counts = {}, perf, serverName, collapsed, onToggle, me }) {
  const memPct = perf?.memPct ?? null
  const cpuPct = perf?.cpuPct ?? null

  return (
    <aside className="dx-side">
      <div className="dx-side-head" style={{ paddingBottom: 8 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <button className="dx-logo" onClick={() => go('home')} title="Overview">
            <img src="/sixter.png" alt="Sixter" />
            <b style={{ fontSize: 15, letterSpacing: '-0.02em' }}>Sixter</b>
          </button>
          <button
            className="dx-icon-btn"
            style={{ marginLeft: 'auto' }}
            onClick={onToggle}
            title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          >
            {collapsed ? <PanelLeft size={16} /> : <PanelLeftClose size={16} />}
          </button>
        </div>
      </div>

      <nav className="dx-side-scroll">
        {NAV_GROUPS.map((g) => (
          <div key={g.group}>
            <div className="dx-navgroup">{g.group}</div>
            {g.items.map((it) => (
              <button
                key={it.id}
                className={`dx-navitem ${page === it.id ? 'on' : ''}`}
                title={it.label}
                onClick={() => (it.external ? window.open(it.external, '_blank') : go(it.id))}
              >
                <span className="ico"><it.icon size={16.5} /></span>
                <span className="lbl">{it.label}</span>
                {!collapsed && it.countKey && counts[it.countKey] > 0 && (
                  <span className="count">{counts[it.countKey]}</span>
                )}
              </button>
            ))}
          </div>
        ))}
      </nav>

      {/* server health */}
      <div className="dx-srv">
        <div className="dx-srv-body">
          <div style={{ display: 'flex', alignItems: 'center', gap: 7, marginBottom: 10 }}>
            <span style={{ width: 7, height: 7, borderRadius: '50%', background: '#22c55e', boxShadow: '0 0 8px #22c55e' }} />
            <b style={{ fontSize: 12.5 }}>{serverName || 'this server'}</b>
          </div>
          <div className="dx-srv-row"><span><Cpu size={11} style={{ verticalAlign: -1 }} /> CPU</span><b>{cpuPct != null ? cpuPct + '%' : '—'}</b></div>
          <div className="dx-meter"><span style={{ width: `${Math.max(cpuPct ?? 0, cpuPct ? 2 : 0)}%` }} /></div>
          <div className="dx-srv-row" style={{ marginTop: 10 }}><span><MemoryStick size={11} style={{ verticalAlign: -1 }} /> Memory</span><b>{perf?.memUsed && perf?.memTotal ? `${perf.memUsed} / ${perf.memTotal}` : (perf?.heap || '—')}</b></div>
          <div className="dx-meter"><span style={{ width: `${Math.max(memPct ?? 0, memPct ? 2 : 0)}%` }} /></div>
          <div style={{ fontSize: 11, color: 'var(--dx-text3)', marginTop: 11 }}>
            up {perf?.uptime || '—'}{perf?.loadAvg ? ` · load ${perf.loadAvg}` : ''}
          </div>
        </div>
        {collapsed && <Circle size={9} style={{ color: '#22c55e', margin: '0 auto', display: 'block' }} />}
      </div>
    </aside>
  )
}

/* ─────────── command palette (Cmd/Ctrl + K) ─────────── */

export function CommandPalette({ open, onClose, projects = [], go, openProject, recent = [] }) {
  const [q, setQ] = useState('')
  const [sel, setSel] = useState(0)
  const inputRef = useRef(null)

  useEffect(() => { if (open) { setQ(''); setSel(0); setTimeout(() => inputRef.current?.focus(), 20) } }, [open])

  const results = useMemo(() => {
    const term = q.trim().toLowerCase()
    const navItems = NAV_GROUPS.flatMap((g) =>
      g.items.map((i) => ({ kind: 'Go to', label: i.label, icon: i.icon, run: () => (i.external ? window.open(i.external, '_blank') : go(i.id)) }))
    )
    const projItems = projects.map((p) => ({
      kind: 'Project', label: p.name, sub: p.domain, icon: Layers, run: () => openProject(p.name),
    }))
    const recentItems = !term
      ? recent.map((n) => ({ kind: 'Recent', label: n, icon: History, run: () => openProject(n) }))
      : []
    const all = [...recentItems, ...projItems, ...navItems]
    if (!term) return all.slice(0, 12)
    return all.filter((r) => r.label.toLowerCase().includes(term) || (r.sub || '').toLowerCase().includes(term)).slice(0, 14)
  }, [q, projects, recent]) // eslint-disable-line

  if (!open) return null

  const run = (i) => { const r = results[i]; if (r) { r.run(); onClose() } }
  const onKey = (e) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); setSel((s) => Math.min(s + 1, results.length - 1)) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setSel((s) => Math.max(s - 1, 0)) }
    else if (e.key === 'Enter') { e.preventDefault(); run(sel) }
    else if (e.key === 'Escape') onClose()
  }

  let lastKind = ''
  return (
    <div className="dx-cmd-back" onClick={onClose}>
      <div className="dx-cmd-box" onClick={(e) => e.stopPropagation()}>
        <input
          ref={inputRef} className="dx-cmd-in" placeholder="Search projects, pages and actions…"
          value={q} onChange={(e) => { setQ(e.target.value); setSel(0) }} onKeyDown={onKey}
        />
        <div className="dx-cmd-list">
          {results.length === 0 && <div className="dx-cmd-sec">No matches</div>}
          {results.map((r, i) => {
            const head = r.kind !== lastKind ? r.kind : null
            lastKind = r.kind
            return (
              <div key={r.kind + r.label + i}>
                {head && <div className="dx-cmd-sec">{head}</div>}
                <div
                  className={`dx-cmd-item ${i === sel ? 'sel' : ''}`}
                  onMouseEnter={() => setSel(i)} onClick={() => run(i)}
                >
                  <r.icon size={15} />
                  <span>{r.label}</span>
                  {r.sub && <span style={{ color: 'var(--dx-text3)', fontSize: 12 }}>{r.sub}</span>}
                  {i === sel && <CornerDownLeft size={13} style={{ marginLeft: 'auto', opacity: .6 }} />}
                </div>
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}

/* ─────────── topbar ─────────── */

export function TopBar({ crumb, onSearch, right }) {
  const mac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)
  return (
    <div className="dx-top">
      <div style={{ display: 'flex', alignItems: 'center', gap: 9, fontSize: 13.5, minWidth: 0 }}>
        <span style={{ color: 'var(--dx-text3)' }}>Sixter</span>
        <span style={{ color: 'var(--dx-text3)' }}>/</span>
        <b style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{crumb}</b>
      </div>
      <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 12 }}>
        <button className="dx-search" onClick={onSearch}>
          <Search size={14} /> Search…
          <span className="dx-kbd">{mac ? '⌘' : 'Ctrl'} K</span>
        </button>
        {right}
      </div>
    </div>
  )
}

/* useCommandPalette wires the Cmd/Ctrl+K shortcut. */
export function useCommandPalette() {
  const [open, setOpen] = useState(false)
  useEffect(() => {
    const h = (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setOpen((o) => !o) }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [])
  return [open, setOpen]
}
