import { useState, useMemo } from 'react'
import {
  Search, LayoutGrid, List, Star, Globe, Lock, Clock, HardDrive, Activity,
  Layers, ChevronDown, ChevronRight, Rocket, TerminalSquare, ScrollText, Settings2,
} from 'lucide-react'

/*
Projects page — replaces the endless project list that used to live in the
sidebar. Search, filter, sort, grid/list, and folders that group by track /
environment. Card-based, matching the main Sixter console.
*/

const FOLDERS = [
  { id: 'production', label: 'Production', match: (p) => !!p.domain && p.dnsOk },
  { id: 'development', label: 'Development', match: (p) => !p.domain },
  { id: 'pending', label: 'Awaiting review', match: (p) => !!p.pending },
  { id: 'all', label: 'All projects', match: () => true },
]

const fmtWhen = (t) => {
  if (!t) return '—'
  const s = Math.floor((Date.now() - new Date(t).getTime()) / 1000)
  if (s < 60) return s + 's ago'
  if (s < 3600) return Math.floor(s / 60) + 'm ago'
  if (s < 86400) return Math.floor(s / 3600) + 'h ago'
  return Math.floor(s / 86400) + 'd ago'
}

function StatusDot({ live, pending }) {
  const c = pending ? '#fbbf24' : live ? '#22c55e' : 'var(--dx-text3)'
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 12, color: c }}>
      <span style={{ width: 7, height: 7, borderRadius: '50%', background: c, boxShadow: live ? `0 0 8px ${c}` : 'none' }} />
      {pending ? 'Pending review' : live ? 'Live' : 'Idle'}
    </span>
  )
}

function ProjectCard({ p, open, pinned, togglePin, act }) {
  return (
    <div className="dx-pcard" onClick={() => open(p.name)}>
      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 13 }}>
        <span className="dx-pico"><Layers size={19} /></span>
        <div style={{ minWidth: 0, flex: 1 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <b style={{ fontSize: 15, letterSpacing: '-.01em', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{p.name}</b>
            {p.track && <span className="badge">{p.track}</span>}
          </div>
          {p.domain ? (
            <a
              href={`https://${p.domain}`} target="_blank" rel="noreferrer"
              onClick={(e) => e.stopPropagation()}
              style={{ fontSize: 12.5, color: 'var(--dx-primary)', display: 'inline-flex', alignItems: 'center', gap: 5, marginTop: 3 }}
            >
              {p.https ? <Lock size={11} /> : <Globe size={11} />} {p.domain}
            </a>
          ) : (
            <div style={{ fontSize: 12.5, color: 'var(--dx-text3)', marginTop: 3 }}>no domain attached</div>
          )}
        </div>
        <button
          className="dx-icon-btn"
          title={pinned ? 'Unpin' : 'Pin to top'}
          onClick={(e) => { e.stopPropagation(); togglePin(p.name) }}
          style={{ color: pinned ? '#fbbf24' : 'var(--dx-text3)' }}
        >
          <Star size={15} fill={pinned ? '#fbbf24' : 'none'} />
        </button>
      </div>

      <div style={{ marginTop: 12 }}><StatusDot live={p.dnsOk} pending={p.pending} /></div>

      <div className="dx-pmeta">
        <div><div className="k">Files</div><div className="v">{(p.files || 0).toLocaleString()}</div></div>
        <div><div className="k">Storage</div><div className="v">{p.size || '—'}</div></div>
        <div><div className="k">Mode</div><div className="v">{p.mode === 'tunnel' ? 'Tunnel' : p.domain ? 'Public' : '—'}</div></div>
        <div><div className="k">SSL</div><div className="v">{p.https ? 'Active' : '—'}</div></div>
      </div>

      <div className="dx-pacts" onClick={(e) => e.stopPropagation()}>
        <button className="btn" style={{ padding: '6px 11px', fontSize: 12 }} onClick={() => act(p, 'deploy')}><Rocket size={13} /> Deploy</button>
        <button className="btn" style={{ padding: '6px 11px', fontSize: 12 }} onClick={() => act(p, 'logs')}><ScrollText size={13} /> Logs</button>
        <button className="btn" style={{ padding: '6px 11px', fontSize: 12 }} onClick={() => act(p, 'terminal')}><TerminalSquare size={13} /> Terminal</button>
        <button className="btn" style={{ padding: '6px 11px', fontSize: 12 }} onClick={() => act(p, 'settings')}><Settings2 size={13} /></button>
      </div>
    </div>
  )
}

function ProjectRow({ p, open, pinned, togglePin }) {
  return (
    <div className="dx-prow" onClick={() => open(p.name)}>
      <span className="dx-pico" style={{ width: 32, height: 32, borderRadius: 10 }}><Layers size={15} /></span>
      <div style={{ minWidth: 0, flex: '1 1 200px' }}>
        <b style={{ fontSize: 14 }}>{p.name}</b>
        <div style={{ fontSize: 12, color: 'var(--dx-text3)' }}>{p.domain || 'no domain'}</div>
      </div>
      <div style={{ flex: '0 0 120px' }}><StatusDot live={p.dnsOk} pending={p.pending} /></div>
      <div style={{ flex: '0 0 90px', fontSize: 12.5, color: 'var(--dx-text2)' }}><HardDrive size={11} /> {p.size || '—'}</div>
      <div style={{ flex: '0 0 90px', fontSize: 12.5, color: 'var(--dx-text2)' }}><Activity size={11} /> {(p.files || 0).toLocaleString()}</div>
      <button className="dx-icon-btn" onClick={(e) => { e.stopPropagation(); togglePin(p.name) }} style={{ color: pinned ? '#fbbf24' : 'var(--dx-text3)' }}>
        <Star size={14} fill={pinned ? '#fbbf24' : 'none'} />
      </button>
    </div>
  )
}

export default function ProjectsPage({ projects = [], openProject, loading }) {
  const [q, setQ] = useState('')
  const [view, setView] = useState(() => localStorage.getItem('sx_projects_view') || 'grid')
  const [sort, setSort] = useState('name')
  const [openFolders, setOpenFolders] = useState({ production: true, development: true, pending: true, all: true })
  const [pins, setPins] = useState(() => {
    try { return JSON.parse(localStorage.getItem('sx_pinned') || '[]') } catch { return [] }
  })

  const setViewP = (v) => { setView(v); localStorage.setItem('sx_projects_view', v) }
  const togglePin = (name) => {
    setPins((cur) => {
      const next = cur.includes(name) ? cur.filter((n) => n !== name) : [...cur, name]
      localStorage.setItem('sx_pinned', JSON.stringify(next))
      return next
    })
  }

  const filtered = useMemo(() => {
    const term = q.trim().toLowerCase()
    let list = projects.filter((p) =>
      !term || p.name.toLowerCase().includes(term) || (p.domain || '').toLowerCase().includes(term))
    list = [...list].sort((a, b) => {
      const ap = pins.includes(a.name), bp = pins.includes(b.name)
      if (ap !== bp) return ap ? -1 : 1
      if (sort === 'size') return (b.sizeBytes || 0) - (a.sizeBytes || 0)
      if (sort === 'files') return (b.files || 0) - (a.files || 0)
      return a.name.localeCompare(b.name)
    })
    return list
  }, [projects, q, sort, pins])

  const act = (p, what) => openProject(p.name, what)

  if (loading) {
    return (
      <div className="dx-pgrid">
        {[0, 1, 2, 3, 4, 5].map((i) => <div key={i} className="dx-skel" style={{ height: 190 }} />)}
      </div>
    )
  }

  const body = (list) =>
    view === 'grid' ? (
      <div className="dx-pgrid">
        {list.map((p) => (
          <ProjectCard key={p.name} p={p} open={openProject} pinned={pins.includes(p.name)} togglePin={togglePin} act={act} />
        ))}
      </div>
    ) : (
      <div>
        {list.map((p) => (
          <ProjectRow key={p.name} p={p} open={openProject} pinned={pins.includes(p.name)} togglePin={togglePin} />
        ))}
      </div>
    )

  return (
    <>
      <div className="dx-page-head">
        <div>
          <h1 className="dx-page-title">Projects</h1>
          <p className="dx-page-sub">Everything deployed on this server. {projects.length} total.</p>
        </div>
      </div>

      <div className="dx-toolbar">
        <div style={{ position: 'relative', flex: '1 1 260px' }}>
          <Search size={15} style={{ position: 'absolute', left: 12, top: '50%', transform: 'translateY(-50%)', color: 'var(--dx-text3)' }} />
          <input
            className="input" placeholder="Search projects or domains…" value={q}
            onChange={(e) => setQ(e.target.value)} style={{ width: '100%', paddingLeft: 36 }}
          />
        </div>
        <select className="input" value={sort} onChange={(e) => setSort(e.target.value)} style={{ flex: '0 0 auto' }}>
          <option value="name">Sort: name</option>
          <option value="size">Sort: size</option>
          <option value="files">Sort: files</option>
        </select>
        <div className="dx-seg">
          <button className={view === 'grid' ? 'on' : ''} onClick={() => setViewP('grid')}><LayoutGrid size={13} /> Grid</button>
          <button className={view === 'list' ? 'on' : ''} onClick={() => setViewP('list')}><List size={13} /> List</button>
        </div>
      </div>

      {projects.length === 0 && (
        <div className="empty">
          No projects yet.<br />
          Push one with <span className="mono">sixter repo -server &lt;name&gt; -project myapp</span>
        </div>
      )}

      {q.trim() ? (
        <>
          <div className="dx-folder-head" style={{ cursor: 'default' }}>{filtered.length} result{filtered.length === 1 ? '' : 's'}</div>
          {body(filtered)}
        </>
      ) : (
        FOLDERS.map((f) => {
          const list = filtered.filter(f.match)
          if (f.id !== 'all' && list.length === 0) return null
          if (f.id === 'all' && filtered.length === 0) return null
          const isOpen = openFolders[f.id] !== false
          return (
            <div className="dx-folder" key={f.id}>
              <div className="dx-folder-head" onClick={() => setOpenFolders((s) => ({ ...s, [f.id]: !isOpen }))}>
                {isOpen ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                {f.label}
                <span style={{ color: 'var(--dx-text3)', fontWeight: 500 }}>{list.length}</span>
              </div>
              {isOpen && body(list)}
            </div>
          )
        })
      )}
    </>
  )
}
