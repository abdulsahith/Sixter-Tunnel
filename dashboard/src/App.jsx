import React, { useState, useEffect, useRef, useCallback } from 'react'
import { Terminal as XTermJS } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import {
  Home, Activity, ArrowRightLeft, Database, History, Menu, Check, Circle, ChevronRight,
  Play, Pause, Maximize2, Minimize2, ArrowLeft, Lock, Zap, Clock, Settings2, RotateCcw,
  X, Plus, AlertTriangle, Folder, TerminalSquare, GitPullRequest, Boxes, GitMerge, FileDiff,
  Cpu, MemoryStick, HardDrive, Rocket, Maximize, Minimize, Server,
} from 'lucide-react'
import { api, getToken, setToken, clearToken, getSSO, wsURL, fmtBytes, fmtDuration, trySSO, clearSSO } from './api.js'
import { Sidebar, TopBar, CommandPalette, useCommandPalette, NAV_GROUPS } from './Shell.jsx'
import ProjectsPage from './ProjectsPage.jsx'

/* Inline icon helper — keeps Lucide glyphs vertically centred inside text/badges. */
function Ico({ as: C, size = 15, style, ...rest }) {
  return <C size={size} style={{ verticalAlign: '-0.15em', flexShrink: 0, ...style }} {...rest} />
}

/* ════════════════ tiny SVG charts (no deps) ════════════════ */

// Catmull-Rom → bezier: smooth wave instead of straight strikes
function smoothPath(pts) {
  if (pts.length < 2) return ''
  let d = `M${pts[0][0]},${pts[0][1]}`
  for (let i = 0; i < pts.length - 1; i++) {
    const p0 = pts[Math.max(0, i - 1)]
    const p1 = pts[i]
    const p2 = pts[i + 1]
    const p3 = pts[Math.min(pts.length - 1, i + 2)]
    const c1x = p1[0] + (p2[0] - p0[0]) / 6
    const c1y = p1[1] + (p2[1] - p0[1]) / 6
    const c2x = p2[0] - (p3[0] - p1[0]) / 6
    const c2y = p2[1] - (p3[1] - p1[1]) / 6
    d += ` C${c1x.toFixed(1)},${c1y.toFixed(1)} ${c2x.toFixed(1)},${c2y.toFixed(1)} ${p2[0].toFixed(1)},${p2[1].toFixed(1)}`
  }
  return d
}

function AreaChart({ data, height = 120, color = '#8b7cff', label }) {
  const w = 600
  const h = height
  if (!data || data.length < 2) {
    return <div className="empty" style={{ padding: 24 }}>Collecting data…</div>
  }
  const max = Math.max(...data.map((d) => d.v), 1)
  const pts = data.map((d, i) => [
    (i / (data.length - 1)) * w,
    h - 6 - (d.v / max) * (h - 16),
  ])
  const line = smoothPath(pts)
  const area = line + ` L${w},${h} L0,${h} Z`
  return (
    <div className="chart-box">
      <svg viewBox={`0 0 ${w} ${h}`} width="100%" height={h} preserveAspectRatio="none">
        <defs>
          <linearGradient id={'g' + color} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={color} stopOpacity="0.35" />
            <stop offset="100%" stopColor={color} stopOpacity="0.02" />
          </linearGradient>
        </defs>
        <path d={area} fill={`url(#g${color})`} />
        <path d={line} fill="none" stroke={color} strokeWidth="2" />
      </svg>
      {label && (
        <div className="chart-legend">
          <span><i style={{ background: color }} />{label}</span>
          <span>peak {max.toLocaleString()}</span>
        </div>
      )}
    </div>
  )
}

function Sparkline({ data, color = '#8b7cff', width = 110, height = 30 }) {
  if (!data || data.length < 2) return <svg width={width} height={height} />
  const max = Math.max(...data, 1)
  const pts = data.map((v, i) => [
    (i / (data.length - 1)) * width,
    height - 3 - (v / max) * (height - 6),
  ])
  const d = smoothPath(pts)
  const area = d + ` L${width},${height} L0,${height} Z`
  return (
    <svg width={width} height={height}>
      <path d={area} fill={color} opacity="0.12" />
      <path d={d} fill="none" stroke={color} strokeWidth="1.6" />
    </svg>
  )
}

function BarList({ items, color = '#8b7cff' }) {
  if (!items || items.length === 0) return <div className="empty" style={{ padding: 18 }}>No data yet</div>
  const max = Math.max(...items.map((i) => i.count), 1)
  return (
    <div>
      {items.map((it) => (
        <div className="bar-row" key={it.key}>
          <span className="lbl" title={it.key}>{it.key}</span>
          <div className="bar-track">
            <div className="bar-fill" style={{ width: `${(it.count / max) * 100}%`, background: color }} />
          </div>
          <span className="val">{it.count.toLocaleString()}</span>
        </div>
      ))}
    </div>
  )
}

/* ════════════════ polling hook ════════════════ */

function usePoll(fn, ms, deps = []) {
  useEffect(() => {
    let alive = true
    const tick = async () => { if (alive) await fn().catch(() => {}) }
    tick()
    const id = setInterval(tick, ms)
    return () => { alive = false; clearInterval(id) }
  }, deps) // eslint-disable-line
}

/* ════════════════ token gate ════════════════ */

function Gate({ onOk }) {
  const [err, setErr] = useState('')
  const [ssoBusy, setSsoBusy] = useState(false)

  // Escape hatch: the server token still works, but it's a recovery path
  // (gateway unreachable), not the everyday way in.
  const [showToken, setShowToken] = useState(false)
  const [val, setVal] = useState('')
  const [busy, setBusy] = useState(false)

  const apex = () => window.location.hostname.split('.').slice(1).join('.') || window.location.hostname

  // Sign in with the Sixter account. If there's no gateway session yet we send
  // the user to sixter.in to log in (password, Google or GitHub) and come back.
  const ssoLogin = async () => {
    setSsoBusy(true)
    setErr('')
    const r = await trySSO()
    if (!r) {
      // No gateway session — go log in there and come straight back.
      sessionStorage.setItem('sixter_returning', '1')
      const next = encodeURIComponent(window.location.origin + window.location.pathname)
      window.location.href = `https://${apex()}/login?next=${next}`
      return
    }
    try {
      await api('/api/performance')
      onOk()
    } catch {
      clearSSO()
      setErr("Signed in as " + r.email + ", but that account isn't the owner or a workspace member on this server.")
    }
    setSsoBusy(false)
  }

  // Returning from the gateway login: finish the handshake automatically so
  // the user isn't asked to click "sign in" a second time. Only runs when we
  // were the ones who sent them there — never a blind auto-login.
  useEffect(() => {
    if (sessionStorage.getItem('sixter_returning') !== '1') return
    sessionStorage.removeItem('sixter_returning')
    let alive = true
    setSsoBusy(true)
    ;(async () => {
      const r = await trySSO()
      if (!alive) return
      if (r) {
        try { await api('/api/performance'); onOk(); return }
        catch {
          clearSSO()
          setErr("Signed in as " + r.email + ", but that account isn't the owner or a workspace member on this server.")
        }
      } else {
        setErr('Sign-in did not complete — please try again.')
      }
      setSsoBusy(false)
    })()
    return () => { alive = false }
  }, []) // eslint-disable-line

  const submitToken = async (e) => {
    e.preventDefault()
    setBusy(true)
    setErr('')
    setToken(val.trim())
    try {
      await api('/api/performance')
      onOk()
    } catch {
      clearToken()
      setErr('Invalid token — check the -token value you started the server with')
    }
    setBusy(false)
  }

  return (
    <div className="gate">
      <div className="gate-card">
        <img className="gate-mark" src="/sixter.png" alt="Sixter" />
        <h1>Sixter Dashboard</h1>
        <p>Sign in with your Sixter account. Owners get full control; team members get the access their workspace role allows.</p>

        <button type="button" className="btn primary" style={{ width: '100%', justifyContent: 'center' }} onClick={ssoLogin} disabled={ssoBusy}>
          {ssoBusy ? <span className="spin" /> : 'Sign in with Sixter'}
        </button>

        {err && <div className="err">{err}</div>}

        {!showToken ? (
          <div style={{ textAlign: 'center', marginTop: 18 }}>
            <button type="button" className="linkbtn" onClick={() => { setShowToken(true); setErr('') }}>
              Use server token instead
            </button>
          </div>
        ) : (
          <form onSubmit={submitToken} style={{ marginTop: 18, paddingTop: 18, borderTop: '1px solid var(--border-2)' }}>
            <p style={{ fontSize: 12.5, marginBottom: 12 }}>
              Recovery access for the owner — use this when sixter.in is unreachable.
            </p>
            <input
              className="input mono"
              type="password"
              placeholder="server token"
              value={val}
              onChange={(e) => setVal(e.target.value)}
              autoFocus
            />
            <button className="btn mt" style={{ width: '100%', justifyContent: 'center' }} disabled={busy || !val.trim()}>
              {busy ? <span className="spin" /> : 'Unlock with token'}
            </button>
          </form>
        )}
      </div>
    </div>
  )
}

/* ════════════════ overview page ════════════════ */

/* Scale — run this project on other machines.

   The node has no copy of your code, so we hand it a scoped download URL for
   this project's tarball plus a base image for the runtime. The agent fetches
   the source, starts a container, and publishes it through the gateway. */
function ScalePanel({ project }) {
  const apex = window.location.hostname.split('.').slice(1).join('.') || 'sixter.in'
  const [info, setInfo] = useState(null)   // from repo-server /api/scale
  const [nodes, setNodes] = useState([])   // from gateway /api/nodes
  const [apps, setApps] = useState([])     // from gateway /api/fleet/apps
  const [svc, setSvc] = useState(null)     // from repo-server /api/service
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState('')
  const [image, setImage] = useState('')
  const [cmd, setCmd] = useState('')
  const [port, setPort] = useState('')

  // Gateway calls are cross-origin from the repo dashboard; the browser's
  // sixter.in session cookie authorises them (same-site CORS).
  const gw = async (path, opts = {}) => {
    const r = await fetch(`https://${apex}${path}`, {
      credentials: 'include',
      headers: opts.body ? { 'Content-Type': 'application/json' } : undefined,
      ...opts,
    })
    if (!r.ok) throw new Error((await r.text()) || `HTTP ${r.status}`)
    return r.json()
  }

  const load = async () => {
    try {
      const i = await api('/api/scale/' + encodeURIComponent(project))
      setInfo(i)
      setImage((v) => v || i.image || i.suggestedImage || '')
      setCmd((v) => v || i.buildRunCmd || i.command || '')
      setPort((v) => v || String(i.port || ''))
    } catch (e) { setErr(e.message) }
    try { const n = await gw('/api/nodes'); setNodes(n.nodes || []) } catch { /* not logged in on gateway */ }
    try { const a = await gw('/api/fleet/apps'); setApps((a.apps || []).filter((x) => x.project === project)) } catch { /* ignore */ }
    try { setSvc(await api('/api/service/' + encodeURIComponent(project))) } catch { /* ignore */ }
  }
  useEffect(() => { load(); const id = setInterval(load, 8000); return () => clearInterval(id) }, [project])

  const placedOn = (nodeId) => apps.find((a) => a.node_id === nodeId)

  const scaleTo = async (nodeId) => {
    setBusy(nodeId); setErr('')
    try {
      // mint (or reuse) the scoped source token for this project
      const i = await api('/api/scale/' + encodeURIComponent(project), { method: 'POST' })
      setInfo(i)
      const r = await gw('/api/fleet/apps', {
        method: 'POST',
        body: JSON.stringify({
          project, node_id: nodeId,
          image: image || i.suggestedImage,
          command: cmd || i.buildRunCmd || i.command,
          port: Number(port) || i.port,
          memory_mb: i.memoryMB || 0, cpu_percent: i.cpuPercent || 0,
          source_url: i.sourceURL, source_token: i.sourceToken,
          // Without this the replica starts with no DB_URL and no API keys.
          env: i.env || {},
          // Checked on the node BEFORE the app starts, so an unreachable
          // database is a clear error instead of an app that 500s forever.
          probe: i.probe || [],
        }),
      })

      // Join THIS server to the same pool and move the project's domain onto
      // the service address. Without this the domain still resolves to this
      // machine alone, so the new replica would sit there taking no traffic.
      const app = r.app || {}
      if (app.subdomain && app.route_token) {
        const s = await api('/api/service/' + encodeURIComponent(project), {
          method: 'POST',
          body: JSON.stringify({
            subdomain: app.subdomain,
            route_token: app.route_token,
            port: Number(port) || i.port || 0,
          }),
        })
        if (s?.warning) setErr(s.warning)
      }
      load()
    } catch (e) { setErr(e.message) }
    setBusy('')
  }

  const removeFrom = async (a) => {
    if (!window.confirm(`Stop ${project} on that node?`)) return
    setBusy(a.node_id); setErr('')
    try {
      await gw('/api/fleet/apps?id=' + encodeURIComponent(a.id), { method: 'DELETE' })
      // Last replica gone → dissolve the service so the domain points back at
      // this server alone instead of at a pool with nobody else in it.
      if (apps.length <= 1) {
        try { await api('/api/service/' + encodeURIComponent(project), { method: 'DELETE' }); setSvc(null) }
        catch { /* old server build without /api/service — harmless */ }
      }
      load()
    } catch (e) { setErr(e.message) }
    setBusy('')
  }

  const stateColor = { running: '#4ade80', pending: '#fbbf24', failed: '#f87171', stopped: 'var(--dx-text3)' }

  return (
    <div className="card mt">
      <div className="card-title">SCALE ACROSS MACHINES</div>
      <p style={{ fontSize: 13, color: 'var(--dx-text2)', lineHeight: 1.6, marginBottom: 14 }}>
        Scaling adds capacity to the address this project already serves — it does not create a
        second one. Every machine runs the same code behind the same domain, and the gateway sends
        each request to whichever copy is least busy.
      </p>

      {/* The service address. This is the ONLY URL that matters: replicas are
          interchangeable members of one pool, not separate deployments with
          their own links. */}
      {(() => {
        const running = apps.filter((a) => a.state === 'running').length
        const total = running + 1 // this server is always a member
        const addr = svc?.domain || (info && apps[0]?.subdomain ? `${apps[0].subdomain}.${apex}` : '')
        if (!addr) return null
        return (
          <div style={{
            borderRadius: 12, padding: '13px 15px', marginBottom: 16,
            border: '1px solid rgba(139,124,255,.3)',
            background: 'linear-gradient(180deg, rgba(139,124,255,.08), transparent)',
          }}>
            <div style={{ fontSize: 11.5, letterSpacing: .4, color: 'var(--dx-text3)', marginBottom: 5 }}>
              SERVICE ADDRESS
            </div>
            <a className="mono" href={`https://${addr}`} target="_blank" rel="noreferrer"
               style={{ fontSize: 14.5, color: 'var(--dx-primary)' }}>
              {addr} ↗
            </a>
            <div style={{ fontSize: 12.5, color: 'var(--dx-text2)', marginTop: 6 }}>
              Load balanced across <b>{total}</b> machine{total === 1 ? '' : 's'}
              {svc?.publishing === false && total > 1 && (
                <span style={{ color: '#fbbf24' }}> · this server is not in the pool yet</span>
              )}
            </div>
          </div>
        )
      })()}

      {/* runtime image */}
      <div style={{ display: 'flex', gap: 10, alignItems: 'flex-end', flexWrap: 'wrap', marginBottom: 16 }}>
        <label style={{ flex: '1 1 240px' }}>
          <div style={{ fontSize: 12.5, color: 'var(--dx-text2)', marginBottom: 6 }}>
            Runtime image for other machines
          </div>
          <input className="input mono" value={image} onChange={(e) => setImage(e.target.value)}
                 placeholder={info?.suggestedImage || 'node:20-slim'} style={{ width: '100%' }} />
        </label>
        {info?.suggestedImage && (
          <span className="badge" style={{ marginBottom: 9 }}>
            detected {info.kind || 'app'} → {info.suggestedImage}
          </span>
        )}
      </div>

      {/* start command — nodes get SOURCE, so a prebuilt-binary command must
          be changed to one that builds first */}
      <div style={{ display: 'flex', gap: 10, alignItems: 'flex-end', flexWrap: 'wrap', marginBottom: 8 }}>
        <label style={{ flex: '2 1 320px' }}>
          <div style={{ fontSize: 12.5, color: 'var(--dx-text2)', marginBottom: 6 }}>Start command on other machines</div>
          <input className="input mono" value={cmd} onChange={(e) => setCmd(e.target.value)}
                 placeholder={info?.command || './app'} style={{ width: '100%' }} />
        </label>
        <label style={{ flex: '0 1 130px' }}>
          <div style={{ fontSize: 12.5, color: 'var(--dx-text2)', marginBottom: 6 }}>Port</div>
          <input className="input" value={port} onChange={(e) => setPort(e.target.value)}
                 placeholder={String(info?.port || 3000)} style={{ width: '100%' }} />
        </label>
      </div>
      <div style={{ fontSize: 12, color: 'var(--dx-text3)', marginBottom: 16, lineHeight: 1.6 }}>
        Nodes receive your <b>source</b>, not the build output from this server, so the command above
        rebuilds from scratch: it was adapted automatically for your stack (a Go binary is recompiled,
        a Python venv is replaced with the image's interpreter, node_modules are reinstalled).
        The port must match what your app listens on.
      </div>

      {info?.fleetBlocker && (
        <div style={{
          borderRadius: 10, padding: '11px 13px', marginBottom: 16,
          border: '1px solid rgba(251,113,133,.35)', background: 'rgba(251,113,133,.07)',
        }}>
          <div style={{ fontSize: 12.5, color: 'var(--dx-text2)', lineHeight: 1.6 }}>
            <b style={{ color: '#fb7185' }}>Scaling won\u2019t work yet.</b> {info.fleetBlocker}
          </div>
        </div>
      )}

      {/* A replica on another machine cannot reach this one's loopback. */}
      {info?.rewrittenVars?.length > 0 && (
        <div style={{
          borderRadius: 10, padding: '11px 13px', marginBottom: 16,
          border: '1px solid rgba(251,191,36,.35)', background: 'rgba(251,191,36,.07)',
        }}>
          <div style={{ fontSize: 12.5, color: 'var(--dx-text2)', lineHeight: 1.6 }}>
            <b>Rewritten for other machines:</b>{' '}
            <span className="mono">{info.rewrittenVars.join(', ')}</span> —{' '}
            <span className="mono">127.0.0.1</span> means "this node" on a replica, where nothing
            is listening, so these now point at <span className="mono">{info.dbHost}</span>.
            {info.probe?.length > 0 && (
              <> Each node checks it can reach <span className="mono">{info.probe.join(', ')}</span>{' '}
              before starting — if it can't, it says so instead of running an app that fails every
              request.</>
            )}
          </div>
        </div>
      )}

      {/* Sizing: nodes build from source, so the peak is the BUILD, not the app. */}
      {info?.recommendedMemoryMB > 0 && (
        <div style={{
          borderRadius: 10, padding: '11px 13px', marginBottom: 16,
          border: '1px solid var(--dx-border)', background: 'var(--dx-card-2)',
        }}>
          <div style={{ fontSize: 12.5, color: 'var(--dx-text2)', lineHeight: 1.6 }}>
            Each machine needs about <b style={{ color: 'var(--dx-text)' }}>{info.recommendedMemoryMB} MB</b> free
            to build and run this project. {info.sizingWhy}
          </div>
          {nodes.some((n) => n.memTotal > 0 && n.memTotal / (1024 * 1024) - 400 < info.recommendedMemoryMB) && (
            <div style={{ fontSize: 12.5, color: '#fbbf24', marginTop: 7, lineHeight: 1.6 }}>
              At least one node is smaller than that. Add swap on it, use a bigger machine, or the
              build will be killed part-way through.
            </div>
          )}
        </div>
      )}

      {/* node boxes */}
      {nodes.length === 0 && (
        <div className="empty">
          No other machines yet.<br />
          Add one from <b>{apex}</b> → Nodes, then it appears here.
        </div>
      )}

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(230px, 1fr))', gap: 14 }}>
        {/* this server, always first */}
        <div style={{
          borderRadius: 14, padding: 14,
          border: '1px solid rgba(139,124,255,.35)',
          background: 'linear-gradient(180deg, rgba(139,124,255,.10), var(--dx-card))',
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <Server size={16} style={{ color: 'var(--dx-primary)' }} />
            <b style={{ fontSize: 13.5 }}>This server</b>
          </div>
          <div style={{ fontSize: 11.5, color: 'var(--dx-text3)', marginTop: 5 }}>primary · AMM container</div>
          <div style={{ marginTop: 12, display: 'flex', alignItems: 'center', gap: 6, fontSize: 12 }}>
            <span style={{ width: 7, height: 7, borderRadius: '50%', background: '#4ade80', boxShadow: '0 0 7px #4ade80' }} />
            <span style={{ color: '#4ade80' }}>running</span>
          </div>
        </div>

        {nodes.map((n) => {
          const a = placedOn(n.id)
          const live = !!a && a.state === 'running'
          return (
            <div key={n.id} style={{
              borderRadius: 14, padding: 14,
              border: `1px solid ${live ? 'rgba(34,197,94,.35)' : 'var(--dx-border)'}`,
              background: live ? 'linear-gradient(180deg, rgba(34,197,94,.08), var(--dx-card))' : 'var(--dx-card)',
              opacity: n.online ? 1 : .55,
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <Server size={16} style={{ color: live ? '#4ade80' : 'var(--dx-text3)' }} />
                <b style={{ fontSize: 13.5, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{n.name}</b>
              </div>
              <div style={{ fontSize: 11.5, color: 'var(--dx-text3)', marginTop: 5 }}>
                {n.cpus} cores · CPU {n.cpuPct}% · MEM {n.memPct}%
              </div>
              {n.isolation !== 'container' && (
                <div style={{ fontSize: 11, color: '#fbbf24', marginTop: 5 }}>unisolated node</div>
              )}

              <div style={{ marginTop: 12, display: 'flex', alignItems: 'center', gap: 6, fontSize: 12 }}>
                <span style={{
                  width: 7, height: 7, borderRadius: '50%',
                  background: a ? stateColor[a.state] : 'var(--dx-text3)',
                  boxShadow: live ? '0 0 7px #4ade80' : 'none',
                }} />
                <span style={{ color: a ? stateColor[a.state] : 'var(--dx-text3)' }}>
                  {a ? a.state : (n.online ? 'not deployed' : 'offline')}
                </span>
              </div>
              {a?.message && (
                <div style={{ fontSize: 11, color: '#f87171', marginTop: 6, wordBreak: 'break-word' }}>{a.message}</div>
              )}

              <div style={{ marginTop: 12 }}>
                {a ? (
                  <button className="btn" style={{ width: '100%', fontSize: 12, color: '#fb7185' }}
                          disabled={busy === n.id} onClick={() => removeFrom(a)}>
                    {busy === n.id ? '…' : 'Remove from node'}
                  </button>
                ) : (
                  <button className="btn primary" style={{ width: '100%', fontSize: 12 }}
                          disabled={busy === n.id || !n.online} onClick={() => scaleTo(n.id)}>
                    {busy === n.id ? 'Deploying…' : 'Run here'}
                  </button>
                )}
              </div>
            </div>
          )
        })}
      </div>

      {err && <div className="dns-status warn mt"><div className="dns-tick"><AlertTriangle size={14} /></div><div>{err}</div></div>}

      <div style={{ fontSize: 12, color: 'var(--dx-text3)', marginTop: 14, lineHeight: 1.6 }}>
        Each node downloads the code with a token scoped to this project only — never your account
        or server token. Redeploy from a node card to pick up your latest push.
      </div>
    </div>
  )
}

/* Release command — the migration step.

   Runs once on this server, after the build and before anything serves
   traffic, with your production env loaded. Putting migrations in the app's
   startup instead breaks the moment you scale: N replicas booting together
   means N concurrent migrations on the same tables. */
/* Panel — isolates a section so one failure can't take the page down.

   React unmounts the WHOLE tree on an uncaught render error, which is why a
   single bad component turns the dashboard into a blank screen with no clue
   what happened. A boundary converts that into a contained message naming the
   panel and the error, and leaves the rest of the app usable. */
class Panel extends React.Component {
  constructor(p) { super(p); this.state = { err: null } }
  static getDerivedStateFromError(err) { return { err } }
  componentDidCatch(err, info) {
    console.error('panel "' + this.props.name + '" crashed:', err, info)
  }
  render() {
    if (this.state.err) {
      return (
        <div className="card mt" style={{ borderColor: 'rgba(251,113,133,.35)' }}>
          <div className="card-title" style={{ color: '#fb7185' }}>
            {this.props.name} failed to render
          </div>
          <div className="mono" style={{ fontSize: 12.5, color: 'var(--text-dim)', lineHeight: 1.6 }}>
            {String(this.state.err && this.state.err.message || this.state.err)}
          </div>
          <div style={{ fontSize: 12, color: 'var(--text-faint)', marginTop: 8 }}>
            The rest of the page still works. Full details are in the browser console.
          </div>
        </div>
      )
    }
    return this.props.children
  }
}

function ReleasePanel({ project }) {
  const [cmd, setCmd] = useState('')
  const [suggested, setSuggested] = useState('')
  const [saved, setSaved] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  useEffect(() => {
    api('/api/release/' + encodeURIComponent(project))
      .then((r) => { setCmd(r.command || ''); setSaved(r.command || ''); setSuggested(r.suggested || '') })
      .catch(() => {})
  }, [project])

  const save = async () => {
    setBusy(true); setErr('')
    try {
      const r = await api('/api/release/' + encodeURIComponent(project), {
        method: 'POST', body: JSON.stringify({ command: cmd }),
      })
      setSaved(r.command || '')
    } catch (e) { setErr(e.message) }
    setBusy(false)
  }

  return (
    <div className="card mt">
      <div className="card-title">RELEASE COMMAND</div>
      <p style={{ fontSize: 13, color: 'var(--text-dim)', lineHeight: 1.5 }}>
        Runs <b>once</b> on every deploy — after the build, before your app starts — with the
        environment variables below already loaded. This is where database migrations belong.
      </p>

      <div className="row-flex" style={{ marginTop: 10, alignItems: 'center' }}>
        <input className="input mono" style={{ flex: 1 }} value={cmd}
               placeholder={suggested || 'e.g. go run ./migrate'}
               onChange={(e) => setCmd(e.target.value)} />
        <button className="btn primary" disabled={busy || cmd === saved} onClick={save}>
          {busy ? '…' : 'Save'}
        </button>
      </div>

      {suggested && cmd !== suggested && (
        <div style={{ fontSize: 12.5, color: 'var(--text-dim)', marginTop: 9 }}>
          Detected a migration step in this project:{' '}
          <span className="mono">{suggested}</span>{' '}
          <button className="btn" style={{ fontSize: 12, marginLeft: 6 }} onClick={() => setCmd(suggested)}>
            Use it
          </button>
        </div>
      )}

      <div style={{ fontSize: 12, color: 'var(--text-faint)', marginTop: 10, lineHeight: 1.6 }}>
        If it fails, the deploy stops and your current version keeps running — an app started
        against a schema that didn’t migrate returns errors on every request instead of one clear
        failure here. Leave empty if this project has no database.
      </div>
      {err && <div style={{ fontSize: 12.5, color: '#fb7185', marginTop: 8 }}>{err}</div>}
    </div>
  )
}

/* AMM — per-project runtime control.

   Off  → the project runs as a host process (systemd), no isolation.
   On   → AMM runs it in a container: namespaces + cgroup limits + overlay
          rootfs, with real measured CPU/memory instead of estimates. */
function AMMPanel({ project }) {
  const [d, setD] = useState(null)
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState('')
  const [mem, setMem] = useState(0)
  const [cpu, setCpu] = useState(0)
  const [img, setImg] = useState('')

  const load = () => api('/api/amm/' + encodeURIComponent(project))
    .then((r) => { setD(r); setMem(r.memoryMB || 0); setCpu(r.cpuPercent || 0); setImg(r.image || '') })
    .catch((e) => setErr(e.message))

  useEffect(() => { load() }, [project])
  useEffect(() => {
    if (!d?.enabled) return
    const id = setInterval(load, 5000) // live cgroup stats
    return () => clearInterval(id)
  }, [d?.enabled, project]) // eslint-disable-line

  const save = async (enabled) => {
    setBusy('save'); setErr('')
    try {
      const r = await api('/api/amm/' + encodeURIComponent(project), {
        method: 'POST',
        body: JSON.stringify({
          enabled, memoryMB: Number(mem) || 0, cpuPercent: Number(cpu) || 0, image: img.trim(),
        }),
      })
      setD(r)
    } catch (e) { setErr(e.message) }
    setBusy('')
  }
  const restart = async () => {
    setBusy('restart'); setErr('')
    try { setD(await api('/api/amm/' + encodeURIComponent(project) + '/restart', { method: 'POST' })) }
    catch (e) { setErr(e.message) }
    setBusy('')
  }

  if (!d) return <div className="tab-panel"><div className="card mt">Loading…</div></div>

  const s = d.stats
  const on = !!d.enabled
  const memPct = s && s.memoryMax > 0 ? Math.round((s.memoryBytes / s.memoryMax) * 100) : null

  return (
    <div className="tab-panel">
      <div className="card mt">
        <div style={{ display: 'flex', alignItems: 'flex-start', gap: 14, flexWrap: 'wrap' }}>
          <span style={{ width: 42, height: 42, borderRadius: 12, display: 'grid', placeItems: 'center', flexShrink: 0, background: on ? 'rgba(139,124,255,.18)' : 'var(--dx-card-2)', color: on ? 'var(--dx-primary)' : 'var(--dx-text3)' }}>
            <Boxes size={21} />
          </span>
          <div style={{ flex: '1 1 280px', minWidth: 0 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 9, flexWrap: 'wrap' }}>
              <b style={{ fontSize: 16 }}>AMM runtime</b>
              {on
                ? <span className="badge" style={{ color: '#4ade80', borderColor: 'rgba(34,197,94,.3)' }}>● Managing this project</span>
                : <span className="badge">Off — running as a host process</span>}
            </div>
            <div style={{ fontSize: 13, color: 'var(--dx-text2)', marginTop: 5, lineHeight: 1.6 }}>
              AMM runs your app in an isolated container — its own process tree, filesystem view
              and enforced CPU/memory limits — instead of an unisolated host service.
              Each deploy starts a fresh container, so nothing carries over from the last release.
            </div>
          </div>
          <button
            className={'btn ' + (on ? '' : 'primary')}
            disabled={busy !== '' || (!on && !d.hostReady)}
            onClick={() => save(!on)}
          >
            {busy === 'save' ? '…' : on ? 'Turn off' : 'Activate AMM'}
          </button>
        </div>

        {!d.hostReady && (
          <div className="dns-status warn mt">
            <div className="dns-tick"><AlertTriangle size={14} /></div>
            <div>
              This host can’t run containers, so AMM is unavailable:
              <div className="mono" style={{ fontSize: 12, marginTop: 4 }}>{d.hostReason || 'unknown reason'}</div>
              <div style={{ fontSize: 12, marginTop: 6 }}>
                Needs Linux, root, cgroup v2 and overlayfs. Projects keep running as host processes.
              </div>
            </div>
          </div>
        )}
        {err && <div className="dns-status warn mt"><div className="dns-tick"><AlertTriangle size={14} /></div><div>{err}</div></div>}
      </div>

      {/* base image — what makes the container portable */}
      <div className="card mt">
        <div className="card-title">BASE IMAGE</div>
        <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end' }}>
          <label style={{ flex: '1 1 260px' }}>
            <div style={{ fontSize: 12.5, color: 'var(--dx-text2)', marginBottom: 6 }}>
              OCI image (leave empty to use this server’s system files)
            </div>
            <input className="input mono" value={img} onChange={(e) => setImg(e.target.value)}
                   placeholder="node:20-slim" style={{ width: '100%' }} />
          </label>
          <button className="btn" disabled={!on || busy !== ''} onClick={() => save(true)}>
            {busy === 'save' ? 'Pulling…' : 'Apply image'}
          </button>
        </div>
        <div style={{ marginTop: 12 }}>
          {d.portable ? (
            <span className="badge" style={{ color: '#4ade80', borderColor: 'rgba(34,197,94,.3)' }}>
              ● Portable — this container can run on any Sixter node
            </span>
          ) : (
            <span className="badge" style={{ color: '#fbbf24', borderColor: 'rgba(251,191,36,.3)' }}>
              ● Pinned to this server — set an image to make it movable
            </span>
          )}
        </div>
        <div style={{ fontSize: 12, color: 'var(--dx-text3)', marginTop: 10, lineHeight: 1.6 }}>
          Without an image the container borrows this machine’s <span className="mono">/bin</span>,{' '}
          <span className="mono">/usr</span> and <span className="mono">/lib</span> — it runs, but it
          can’t be moved to another machine and it behaves differently on a different OS.
          An image is a fixed, downloadable filesystem, so the same container runs identically
          anywhere. Common choices:{' '}
          <span className="mono">node:20-slim</span>, <span className="mono">python:3.12-slim</span>,{' '}
          <span className="mono">golang:1.24</span>, <span className="mono">alpine:3.20</span>.
          The first pull takes a moment; after that it’s cached and shared by every container using it.
        </div>
      </div>

      {/* limits */}
      <div className="card mt">
        <div className="card-title">RESOURCE LIMITS</div>

        {/* Sizing guidance. A limit chosen for the RUNNING app is the classic
            footgun: the build peaks several times higher and gets OOM-killed
            with nothing in the log but "Killed". */}
        {d.recommendedMemoryMB > 0 && (
          <div style={{
            borderRadius: 10, padding: '11px 13px', marginBottom: 14,
            border: `1px solid ${d.limitTooLow ? 'rgba(251,191,36,.35)' : 'var(--dx-border)'}`,
            background: d.limitTooLow ? 'rgba(251,191,36,.07)' : 'var(--dx-card-2)',
          }}>
            <div style={{ fontSize: 12.5, color: 'var(--dx-text2)', lineHeight: 1.6 }}>
              {d.limitTooLow && (
                <b style={{ color: '#fbbf24' }}>
                  Your limit of {mem} MB is below what this project needs — it will be killed mid-start.{' '}
                </b>
              )}
              Recommended: <b style={{ color: 'var(--dx-text)' }}>{d.recommendedMemoryMB} MB</b>
              {' · '}CPU <b style={{ color: 'var(--dx-text)' }}>{d.recommendedCPUPercent}%</b>
              {d.sizingWhy && <><br />{d.sizingWhy}</>}
            </div>
            <button className="btn" style={{ marginTop: 9, fontSize: 12 }}
                    onClick={() => { setMem(d.recommendedMemoryMB); setCpu(d.recommendedCPUPercent) }}>
              Use recommended
            </button>
          </div>
        )}

        <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end' }}>
          <label style={{ flex: '1 1 180px' }}>
            <div style={{ fontSize: 12.5, color: 'var(--dx-text2)', marginBottom: 6 }}>Memory limit (MB)</div>
            <input className="input" type="number" min="0" value={mem} onChange={(e) => setMem(e.target.value)} style={{ width: '100%' }} placeholder="0 = unlimited" />
          </label>
          <label style={{ flex: '1 1 180px' }}>
            <div style={{ fontSize: 12.5, color: 'var(--dx-text2)', marginBottom: 6 }}>CPU limit (% of one core)</div>
            <input className="input" type="number" min="0" value={cpu} onChange={(e) => setCpu(e.target.value)} style={{ width: '100%' }} placeholder="0 = fair share" />
          </label>
          <button className="btn" disabled={!on || busy !== ''} onClick={() => save(true)}>Apply</button>
          <button className="btn" disabled={!on || busy !== ''} onClick={restart}>
            <RotateCcw size={14} /> {busy === 'restart' ? '…' : 'Restart'}
          </button>
        </div>
        <div style={{ fontSize: 12, color: 'var(--dx-text3)', marginTop: 10, lineHeight: 1.6 }}>
          {d.hostCPUs > 0 && (
            <>
              This machine has <b style={{ color: 'var(--dx-text2)' }}>{d.hostCPUs} core{d.hostCPUs === 1 ? '' : 's'}</b>
              {' '}— 100 = one core, {d.hostCPUMaxPct} = all of them.
              {d.hostMemTotal && <> Memory: {d.hostMemUsed} of {d.hostMemTotal} in use.</>}
              <br />
            </>
          )}
          Leave CPU at 0 unless you need a hard cap. A hard quota stalls the app until the next
          scheduling window once it’s spent, which shows up as sudden latency spikes; 0 means the
          app competes fairly and is only slowed when the host is genuinely busy.
        </div>
      </div>

      {/* live, measured stats */}
      {on && (
        <div className="card mt">
          <div className="card-title">LIVE — MEASURED FROM CGROUPS</div>
          {!s && <div style={{ fontSize: 13, color: 'var(--dx-text3)' }}>Container is {d.state || 'stopped'}.</div>}
          {s && (
            <>
              <div className="grid grid-3">
                <div><div className="metric">{s.cpuPercent != null ? s.cpuPercent.toFixed(1) + '%' : '—'}</div><div className="metric-sub">CPU (avg since start)</div></div>
                <div><div className="metric">{s.memoryHuman || '—'}</div><div className="metric-sub">memory{memPct != null ? ` · ${memPct}% of limit` : ''}</div></div>
                <div><div className="metric">{s.pids ?? '—'}</div><div className="metric-sub">processes</div></div>
              </div>
              <div className="kv-row" style={{ marginTop: 14 }}><span>State</span><b>{d.state} · pid {d.pid}</b></div>
              <div className="kv-row"><span>Uptime</span><b>{fmtDuration ? Math.round(s.uptimeSecs) + 's' : s.uptimeSecs + 's'}</b></div>
              {s.throttledPeriods > 0 && (
                <div className="dns-status warn mt">
                  <div className="dns-tick"><AlertTriangle size={14} /></div>
                  <div>
                    <b>CPU throttling detected</b> — {s.throttledPeriods} periods, {s.throttledSecs.toFixed(1)}s stalled.
                    Your CPU limit is too low for this workload; raise it or set 0 for fair share.
                  </div>
                </div>
              )}
            </>
          )}
          {d.error && <div className="dns-status warn mt"><div className="dns-tick"><AlertTriangle size={14} /></div><div>{d.error}</div></div>}
        </div>
      )}

      {on && <Panel name="Scale"><ScalePanel project={project} /></Panel>}

      {/* container output — the actual reason a start failed */}
      {on && d.log && (
        <div className="card mt">
          <div className="card-title">CONTAINER OUTPUT</div>
          <pre className="code-block" style={{ maxHeight: 260, overflow: 'auto', margin: 0 }}>{d.log}</pre>
        </div>
      )}
    </div>
  )
}

/* Warp-style terminal: slides in from the right, drag-to-resize, fullscreen,
   blurred backdrop. Width persists so it feels like the user's own tool. */
function TerminalDrawer({ name, onClose }) {
  const [w, setW] = useState(() => Number(localStorage.getItem('sx_term_w')) || 620)
  const [full, setFull] = useState(false)
  const drag = useRef(null)

  useEffect(() => {
    const move = (e) => {
      if (!drag.current) return
      const next = Math.min(Math.max(window.innerWidth - e.clientX, 380), window.innerWidth - 120)
      setW(next)
    }
    const up = () => {
      if (drag.current) { drag.current = null; localStorage.setItem('sx_term_w', String(w)) }
      document.body.style.userSelect = ''
    }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
    return () => { window.removeEventListener('mousemove', move); window.removeEventListener('mouseup', up) }
  }, [w])

  useEffect(() => {
    const esc = (e) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', esc)
    return () => window.removeEventListener('keydown', esc)
  }, [onClose])

  return (
    <>
      <div className="dx-term-back" onClick={onClose} />
      <div className="dx-term-panel" style={{ width: full ? '100vw' : w }}>
        {!full && (
          <div
            className="dx-term-grip"
            onMouseDown={() => { drag.current = true; document.body.style.userSelect = 'none' }}
            title="Drag to resize"
          />
        )}
        <div className="dx-term-head">
          <TerminalSquare size={15} style={{ color: 'var(--dx-primary)' }} />
          <b style={{ fontSize: 13.5 }}>{name}</b>
          <span style={{ fontSize: 11.5, color: 'var(--dx-text3)' }}>terminal</span>
          <div style={{ flex: 1 }} />
          <button className="dx-icon-btn" onClick={() => setFull((f) => !f)} title={full ? 'Exit fullscreen' : 'Fullscreen'}>
            {full ? <Minimize size={15} /> : <Maximize size={15} />}
          </button>
          <button className="dx-icon-btn" onClick={onClose} title="Close (Esc)"><X size={16} /></button>
        </div>
        <div style={{ flex: 1, minHeight: 0, padding: 12 }}>
          <Terminal key={name} project={name} />
        </div>
      </div>
    </>
  )
}

/* Hero metric tile — the premium stat card from the main Sixter console. */
function Hero({ icon: Icon, label, value, sub, tone = 'violet' }) {
  const c = { violet: '#8b7cff', blue: '#5a8bff', green: '#22c55e', amber: '#f59e0b', red: '#ef4444' }[tone] || '#8b7cff'
  return (
    <div className="dx-stat">
      <div className="dx-stat-top">
        <span className="dx-stat-ico" style={{ background: `${c}22`, border: `1px solid ${c}33`, color: c }}>
          <Icon size={18} />
        </span>
      </div>
      <div className="dx-stat-label">{label}</div>
      <div className="dx-stat-value">{value}</div>
      {sub && <div style={{ fontSize: 11.5, color: 'var(--dx-text3)', marginTop: 4 }}>{sub}</div>}
    </div>
  )
}

function Overview({ perf, traffic, projects, openProject }) {
  const reqSeries = (traffic?.minutes || []).map((m) => ({ v: m.requests }))
  const totalReq = (traffic?.minutes || []).reduce((a, m) => a + m.requests, 0)
  const totalBytes = (traffic?.minutes || []).reduce((a, m) => a + m.bytes, 0)
  const live = projects.filter((p) => p.dnsOk).length
  const pending = projects.filter((p) => p.pending).length
  const storage = projects.reduce((a, p) => a + (p.sizeBytes || 0), 0)

  return (
    <>
      <div className="dx-page-head">
        <div>
          <h1 className="dx-page-title">Overview</h1>
          <p className="dx-page-sub">Everything running on this server — last 60 minutes.</p>
        </div>
      </div>

      <div className="dx-stats" style={{ marginBottom: 18 }}>
        <Hero icon={Cpu} label="Goroutines" value={perf?.goroutines ?? '—'} sub={perf ? `${perf.os}/${perf.arch}` : ''} tone="violet" />
        <Hero icon={MemoryStick} label="Memory" value={perf?.heap || '—'} sub="heap in use" tone="blue" />
        <Hero icon={HardDrive} label="Storage" value={fmtBytes(storage)} sub={`${projects.length} projects`} tone="green" />
        <Hero icon={Activity} label="Requests" value={totalReq.toLocaleString()} sub="last hour" tone="violet" />
        <Hero icon={ArrowRightLeft} label="Bandwidth" value={fmtBytes(totalBytes)} sub="last hour" tone="blue" />
        <Hero icon={Rocket} label="Live services" value={live} sub={pending ? `${pending} awaiting review` : 'all deployed'} tone={pending ? 'amber' : 'green'} />
      </div>

      <div className="grid grid-3">
        <div className="card">
          <div className="card-title">PERFORMANCE</div>
          <div className="metric-row">
            <div>
              <div className="metric">{perf?.uptime || '—'}</div>
              <div className="metric-sub">uptime</div>
            </div>
            <div>
              <div className="metric">{perf?.goroutines ?? '—'}</div>
              <div className="metric-sub">goroutines</div>
            </div>
          </div>
          <div className="mt">
            <div className="kv"><span>Heap</span><span className="mono">{perf?.heap || '—'}</span></div>
            <div className="kv"><span>Platform</span><span className="mono">{perf ? `${perf.os}/${perf.arch}` : '—'}</span></div>
          </div>
        </div>

        <div className="card">
          <div className="card-title">WEB TRAFFIC <span className="badge blue">1h</span></div>
          <div className="metric-row">
            <div>
              <div className="metric">{totalReq.toLocaleString()}</div>
              <div className="metric-sub">requests</div>
            </div>
            <div>
              <div className="metric">{fmtBytes(totalBytes)}</div>
              <div className="metric-sub">transferred</div>
            </div>
          </div>
          <div className="mt">
            <Sparkline data={reqSeries.map((d) => d.v)} width={260} height={42} color="#8b7cff" />
          </div>
        </div>

        <div className="card">
          <div className="card-title">PROJECTS</div>
          <div className="metric-row">
            <div>
              <div className="metric">{projects.length}</div>
              <div className="metric-sub">deployed</div>
            </div>
            <div>
              <div className="metric">{projects.filter((p) => p.dnsOk).length}</div>
              <div className="metric-sub">domains live</div>
            </div>
          </div>
          <div className="mt">
            <div className="kv"><span>Total size</span><span className="mono">{fmtBytes(projects.reduce((a, p) => a + (p.sizeBytes || 0), 0))}</span></div>
            <div className="kv"><span>Total files</span><span className="mono">{projects.reduce((a, p) => a + (p.files || 0), 0)}</span></div>
          </div>
        </div>
      </div>

      <div className="card mt">
        <div className="card-title">REQUESTS PER MINUTE</div>
        <AreaChart data={reqSeries} label="requests/min" color="#8b7cff" />
      </div>

      <div className="mt">
        <div className="card-title" style={{ marginTop: 10 }}>YOUR PROJECTS</div>
        {projects.length === 0 ? (
          <div className="empty">
            No projects yet — upload one with<br />
            <code style={{ fontFamily: 'var(--mono)', fontSize: 12 }}>
              sixter repo -server &lt;name&gt; -project myapp
            </code>
          </div>
        ) : (
          projects.map((p) => (
            <div className="proj-row" key={p.name} onClick={() => openProject(p.name)}>
              <div className={`proj-check ${p.dnsOk ? 'ok' : 'off'}`}>{p.dnsOk ? <Check size={15} /> : <Circle size={12} />}</div>
              <div>
                <div className="proj-name">{p.name}</div>
                <div className="proj-meta">
                  {p.files} files · {p.size}
                  {p.domain ? ` · ${p.domain}` : ' · no domain'}
                </div>
              </div>
              <div className="proj-spark">
                {p.domain && (
                  <span className={`badge ${p.dnsOk ? 'green' : 'amber'}`}>
                    {p.dnsOk ? 'DNS active' : 'DNS pending'}
                  </span>
                )}
              </div>
              <div className="proj-arrow"><ChevronRight size={18} /></div>
            </div>
          ))
        )}
      </div>
    </>
  )
}

/* ════════════════ traffic page ════════════════ */

function TrafficPage({ traffic }) {
  const reqSeries = (traffic?.minutes || []).map((m) => ({ v: m.requests }))
  const byteSeries = (traffic?.minutes || []).map((m) => ({ v: m.bytes }))
  return (
    <>
      <div className="page-title">Web traffic</div>
      <div className="page-sub">HTTP analytics for this server — rolling 60 minutes</div>

      <div className="card">
        <div className="card-title">REQUESTS PER MINUTE</div>
        <AreaChart data={reqSeries} label="requests/min" color="#8b7cff" height={150} />
      </div>

      <div className="card mt">
        <div className="card-title">BYTES PER MINUTE</div>
        <AreaChart data={byteSeries} label="bytes/min" color="#5a8bff" height={150} />
      </div>

      <div className="grid grid-2 mt">
        <div className="card">
          <div className="card-title">TOP PATHS</div>
          <BarList items={traffic?.topPaths || []} color="#8b7cff" />
        </div>
        <div className="card">
          <div className="card-title">TOP CLIENT IPs</div>
          <BarList items={traffic?.topIPs || []} color="#22c55e" />
        </div>
      </div>
    </>
  )
}

/* ════════════════ packets page (wireshark-style) ════════════════ */

function PacketsPage() {
  const [packets, setPackets] = useState([])
  const [filter, setFilter] = useState('')
  const [paused, setPaused] = useState(false)

  usePoll(async () => {
    if (paused) return
    const data = await api('/api/packets?limit=300')
    setPackets(data.packets || [])
  }, 2000, [paused])

  const f = filter.toLowerCase()
  const shown = f
    ? packets.filter((p) =>
        [p.src, p.method, p.host, p.path, String(p.status)].join(' ').toLowerCase().includes(f)
      )
    : packets

  return (
    <>
      <div className="page-title">Packet capture</div>
      <div className="page-sub">Live HTTP request capture — every request hitting this server</div>

      <div className="row-flex" style={{ marginBottom: 14 }}>
        <input
          className="input mono"
          style={{ maxWidth: 360 }}
          placeholder="filter: ip, path, method, status…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <button className="btn" onClick={() => setPaused(!paused)}>
          {paused ? <><Play size={14} /> Resume</> : <><Pause size={14} /> Pause</>}
        </button>
        <span className="badge dim">{shown.length} captured</span>
      </div>

      <div className="tbl-wrap scroll-y">
        <table className="tbl">
          <thead>
            <tr>
              <th>#</th><th>Time</th><th>Source</th><th>Method</th>
              <th>Host</th><th>Path</th><th>Status</th><th>Size</th><th>Duration</th>
            </tr>
          </thead>
          <tbody>
            {shown.map((p) => {
              const cls = 's' + Math.floor((p.status || 0) / 100)
              return (
                <tr key={p.id}>
                  <td className="dim">{p.id}</td>
                  <td>{p.time}</td>
                  <td>{p.src}</td>
                  <td>{p.method}</td>
                  <td>{p.host}</td>
                  <td title={p.path}>{p.path}</td>
                  <td className={cls}>{p.status}</td>
                  <td>{fmtBytes(p.respBytes)}</td>
                  <td>{fmtDuration(p.durMs)}</td>
                </tr>
              )
            })}
            {shown.length === 0 && (
              <tr><td colSpan="9" style={{ textAlign: 'center', padding: 30, color: 'var(--text-faint)' }}>
                No packets captured yet
              </td></tr>
            )}
          </tbody>
        </table>
      </div>
    </>
  )
}

/* ════════════════ terminal ════════════════ */

function Terminal({ project }) {
  const [connected, setConnected] = useState(false)
  const [full, setFull] = useState(false)
  const wsRef = useRef(null)
  const boxRef = useRef(null)
  const termRef = useRef(null)
  const cleanupRef = useRef(null)

  // refit the terminal whenever we toggle fullscreen
  useEffect(() => {
    const t = termRef.current
    if (!t) return
    const id = setTimeout(() => {
      t._fit?.fit()
      if (wsRef.current?.readyState === 1) {
        wsRef.current.send(JSON.stringify({ type: 'resize', cols: t.cols, rows: t.rows }))
      }
    }, 60)
    return () => clearTimeout(id)
  }, [full])

  // Esc exits fullscreen
  useEffect(() => {
    if (!full) return
    const onKey = (e) => { if (e.key === 'Escape') setFull(false) }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [full])

  const connect = useCallback(() => {
    if (wsRef.current) return

    let term = termRef.current
    if (!term) {
      term = new XTermJS({
        cursorBlink: true,
        fontSize: 13,
        fontFamily: '"Cascadia Code", "JetBrains Mono", Consolas, monospace',
        scrollback: 4000,
        theme: {
          background: '#0a0c10',
          foreground: '#d6e0ea',
          cursor: '#34c77b',
          cursorAccent: '#0a0c10',
          selectionBackground: 'rgba(139, 133, 224, 0.35)',
        },
      })
      const fit = new FitAddon()
      term.loadAddon(fit)
      term.open(boxRef.current)
      term._fit = fit
      termRef.current = term
    }
    term._fit.fit()

    const ws = new WebSocket(wsURL('/api/terminal/' + project))
    ws.binaryType = 'arraybuffer'

    ws.onopen = () => {
      setConnected(true)
      ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
      term.focus()
    }
    ws.onmessage = (e) => {
      term.write(typeof e.data === 'string' ? e.data : new Uint8Array(e.data))
    }
    ws.onclose = () => {
      setConnected(false)
      wsRef.current = null
      term.write('\r\n\x1b[31m[disconnected — click Connect to reopen]\x1b[0m\r\n')
    }

    const dataSub = term.onData((d) => {
      if (ws.readyState === 1) ws.send(new TextEncoder().encode(d))
    })
    const onWinResize = () => {
      term._fit.fit()
      if (ws.readyState === 1) ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
    }
    window.addEventListener('resize', onWinResize)

    cleanupRef.current = () => {
      dataSub.dispose()
      window.removeEventListener('resize', onWinResize)
    }
    wsRef.current = ws
  }, [project])

  useEffect(() => {
    return () => {
      cleanupRef.current?.()
      if (wsRef.current) wsRef.current.close()
      termRef.current?.dispose()
      termRef.current = null
    }
  }, [])

  return (
    <div className={'term' + (full ? ' term-full' : '')}>
      <div className="term-bar">
        <span className="term-dot r" /><span className="term-dot y" /><span className="term-dot g" />
        <span className="term-title">{project} — sixter shell</span>
        <div style={{ marginLeft: 'auto', display: 'flex', gap: 8, alignItems: 'center' }}>
          {connected && <span className="badge green"><span className="live-ball" /> live</span>}
          {!connected && <button className="btn" onClick={connect}>Connect</button>}
          <button className="btn term-btn" title={full ? 'Exit fullscreen (Esc)' : 'Fullscreen'}
                  onClick={() => setFull(!full)}>
            {full ? <><Minimize2 size={14} /> Exit</> : <><Maximize2 size={14} /> Fullscreen</>}
          </button>
        </div>
      </div>
      <div
        className="term-body term-xterm"
        ref={boxRef}
        onClick={() => { if (!wsRef.current) connect(); else termRef.current?.focus() }}
      />
    </div>
  )
}

/* ════════════════ project page ════════════════ */

function ProjectPage({ name, projects, refresh, goHome, me }) {
  // capability check from the signed-in member's role (owner/token → all caps)
  const can = (c) => !me || (me.caps || []).includes(c)
  const proj = projects.find((p) => p.name === name)
  // initialise straight from this project (the parent passes key={name} so the
  // component fully remounts per project — no state can leak between projects)
  const dnsFromProj = (p) => (p && p.domain
    ? { dnsOk: p.dnsOk, https: p.https, note: p.note, serverIP: p.serverIP, domainIP: p.domainIP, nginxPath: p.nginxPath }
    : null)
  const [domain, setDomain] = useState(proj?.domain || '')   // domainreset2
  const [mode, setMode] = useState(proj?.mode || 'public')   // 'public' | 'tunnel'
  const [track, setTrackState] = useState(proj?.track || 'shared') // 'frontend' | 'backend' | 'shared'
  const [port, setPort] = useState(proj?.port ? String(proj.port) : '3000')
  const [checking, setChecking] = useState(false)
  const [dnsResult, setDnsResult] = useState(dnsFromProj(proj))
  const [err, setErr] = useState('')
  const [stats, setStats] = useState(null)
  const [showDelete, setShowDelete] = useState(false)
  const [delPwd, setDelPwd] = useState('')
  const [delErr, setDelErr] = useState('')
  const [plan, setPlan] = useState(null)
  const [deploying, setDeploying] = useState(false)
  const [deployLog, setDeployLog] = useState('')
  // env vars + versions
  const [envRows, setEnvRows] = useState([])      // [{key,value}]
  const [envMsg, setEnvMsg] = useState('')
  const [envSaving, setEnvSaving] = useState(false)
  const [versions, setVersions] = useState([])
  const [rolling, setRolling] = useState('')      // version id being rolled back
  // CI/CD pipeline
  const [pipe, setPipe] = useState(null)          // deploy-status snapshot
  const [autoDeploy, setAutoDeploy] = useState(true)
  const [liveLog, setLiveLog] = useState('')
  const esRef = useRef(null)
  // tabbed layout + terminal overlay
  const [tab, setTab] = useState('overview')
  const [termOpen, setTermOpen] = useState(false)
  // settings
  const [newName, setNewName] = useState('')
  const [renaming, setRenaming] = useState(false)
  const [settingsMsg, setSettingsMsg] = useState('')
  // firewall
  const [sec, setSec] = useState(null)

  // professional confirm dialog + toast (replaces native alert/confirm)
  const [confirmReq, setConfirmReq] = useState(null)
  const ask = (opts) => new Promise((resolve) => setConfirmReq({ ...opts, resolve }))
  const resolveConfirm = (val) => { setConfirmReq((c) => { c && c.resolve(val); return null }) }
  const [toast, setToast] = useState(null)
  const notify = (text, kind = 'ok') => { setToast({ text, kind }); setTimeout(() => setToast(null), 3800) }

  const banIp = async (ip) => {
    if (!(await ask({ title: 'Block this IP?', body: `${ip} will be blocked at the firewall for 1 hour.`, confirmLabel: 'Block IP', danger: true }))) return
    try {
      const r = await api('/api/security/' + name + '/ban', { method: 'POST', body: JSON.stringify({ ip }) })
      notify(r.note || r.error || 'Done', r.ok === false ? 'err' : 'ok')
    } catch (e) { notify(e.message, 'err') }
  }

  const renameProj = async () => {
    const nn = newName.trim()
    if (!nn || nn === name) return
    setRenaming(true); setSettingsMsg('')
    try {
      await api('/api/rename/' + name, { method: 'POST', body: JSON.stringify({ newName: nn }) })
      refresh(); goHome() // project key changed — return to list (it refreshes)
    } catch (e) { setSettingsMsg(e.message) }
    setRenaming(false)
  }

  // Belt-and-suspenders: if the projects list loads/updates after mount,
  // re-sync this project's fields. Keyed by name so it only re-runs per project.
  useEffect(() => {
    setDomain(proj?.domain || '')
    setMode(proj?.mode || 'public')
    setTrackState(proj?.track || 'shared')
    setPort(proj?.port ? String(proj.port) : '3000')
    setDnsResult(dnsFromProj(proj))
  }, [name, proj?.domain]) // eslint-disable-line

  // load env vars + versions for this project
  const loadVersions = () => api('/api/versions/' + name).then((d) => setVersions(d.versions || [])).catch(() => {})
  useEffect(() => {
    setEnvMsg('')
    api('/api/env/' + name).then((d) => {
      const rows = Object.entries(d.vars || {}).map(([key, value]) => ({ key, value }))
      setEnvRows(rows)
    }).catch(() => setEnvRows([]))
    loadVersions()
  }, [name]) // eslint-disable-line

  // poll pipeline/deploy status (reflects CLI-triggered auto-deploys too)
  useEffect(() => {
    let alive = true
    const poll = () => api('/api/deploy-status/' + name).then((s) => {
      if (!alive) return
      setPipe(s)
      setAutoDeploy(!!s.autoDeploy)
      if (!s.running) loadVersions() // refresh versions after a run settles
    }).catch(() => {})
    poll()
    const id = setInterval(poll, 2500)
    return () => { alive = false; clearInterval(id) }
  }, [name]) // eslint-disable-line

  // stream live build log over SSE while a run is in progress
  useEffect(() => {
    if (!pipe || !pipe.running || esRef.current) return
    const url = '/api/deploy-stream/' + name + '?token=' + encodeURIComponent(getToken())
    const es = new EventSource(url)
    esRef.current = es
    setLiveLog('')
    es.onmessage = (e) => setLiveLog((l) => l + e.data + '\n')
    const close = () => { es.close(); esRef.current = null }
    es.addEventListener('done', close)
    es.onerror = close
    return close
  }, [pipe?.running, name]) // eslint-disable-line

  const toggleAuto = async () => {
    const on = !autoDeploy
    setAutoDeploy(on)
    try { await api('/api/autodeploy/' + name, { method: 'POST', body: JSON.stringify({ on }) }) } catch { /* ignore */ }
  }

  // poll per-site firewall / security stats
  useEffect(() => {
    let alive = true
    const load = () => api('/api/security/' + name).then((s) => alive && setSec(s)).catch(() => {})
    load()
    const id = setInterval(load, 6000)
    return () => { alive = false; clearInterval(id) }
  }, [name])

  const setEnvRow = (i, field, val) => setEnvRows((rows) => rows.map((r, j) => (j === i ? { ...r, [field]: val } : r)))
  const addEnvRow = () => setEnvRows((rows) => [...rows, { key: '', value: '' }])
  const removeEnvRow = (i) => setEnvRows((rows) => rows.filter((_, j) => j !== i))

  const saveEnv = async () => {
    setEnvSaving(true); setEnvMsg('')
    const vars = {}
    for (const r of envRows) { if (r.key.trim()) vars[r.key.trim()] = r.value }
    try {
      const res = await api('/api/env/' + name, { method: 'POST', body: JSON.stringify({ vars }) })
      setEnvMsg(res.note || 'saved')
    } catch (e) { setEnvMsg(e.message) }
    setEnvSaving(false)
  }

  const rollback = async (vid) => {
    if (!(await ask({ title: 'Roll back to this version?', body: 'Your current version is snapshotted first, then ' + vid + ' is restored and redeployed live.', confirmLabel: 'Roll back', danger: true }))) return
    setRolling(vid); setDeployLog(''); setErr('')
    try {
      const res = await api('/api/rollback/' + name, { method: 'POST', body: JSON.stringify({ version: vid }) })
      setDeployLog(res.log || res.error || '')
      loadVersions(); refresh()
    } catch (e) { setDeployLog(e.message) }
    setRolling('')
  }

  // poll per-project traffic (only meaningful once a domain + nginx is live)
  useEffect(() => {
    let alive = true
    const tick = () => api('/api/project-stats/' + name).then((s) => alive && setStats(s)).catch(() => {})
    tick()
    const id = setInterval(tick, 5000)
    return () => { alive = false; clearInterval(id) }
  }, [name])

  if (!proj) {
    return (
      <div className="empty">
        Project not found. <button className="btn mt" onClick={goHome}><ArrowLeft size={14} /> Back</button>
      </div>
    )
  }

  const saveDomain = async () => {
    setChecking(true)
    setErr('')
    try {
      const res = await api('/api/domain', {
        method: 'POST',
        body: JSON.stringify({ project: name, domain: domain.trim(), port: parseInt(port, 10), mode }),
      })
      setDnsResult(res)
      refresh()
    } catch (e) {
      setErr(e.message)
    }
    setChecking(false)
  }

  const switchMode = async (target) => {
    if (target === mode) return
    const body = target === 'tunnel'
      ? 'Tunnel mode routes this project through the Sixter gateway — no public IP needed, and SSL is issued + terminated at the gateway. When you save, any public nginx config and certificate for this domain on this server are removed, and traffic flows down the secure tunnel instead.'
      : 'Public Server mode serves this project directly from this server’s public IP via nginx, with its own Let’s Encrypt certificate. When you save, the Sixter gateway route for this domain is removed and the app is exposed through nginx instead.'
    const ok = await ask({
      title: `Switch to ${target === 'tunnel' ? 'Tunnel' : 'Public Server'} mode?`,
      body,
      confirmLabel: 'Switch & continue',
    })
    if (ok) setMode(target)
  }

  const saveTrack = async (t) => {
    setTrackState(t)
    try { await api('/api/track/' + encodeURIComponent(name), { method: 'POST', body: JSON.stringify({ track: t }) }); refresh() }
    catch (e) { setErr(e.message) }
  }

  const recheck = async () => {
    setChecking(true)
    setErr('')
    try {
      const res = await api('/api/domain/check?project=' + encodeURIComponent(name))
      setDnsResult(res)
      refresh()
    } catch (e) {
      setErr(e.message)
    }
    setChecking(false)
  }

  const detect = async () => {
    setErr('')
    try {
      const p = await api('/api/detect/' + name + '?port=' + parseInt(port || '3000', 10))
      setPlan(p)
    } catch (e) { setErr(e.message) }
  }

  const deploy = async () => {
    setDeploying(true); setErr('')
    try {
      const res = await api('/api/deploy/' + name, {
        method: 'POST',
        body: JSON.stringify({ port: parseInt(port || '3000', 10), domain: domain.trim() }),
      })
      if (res.plan) setPlan(res.plan)
      // the pipeline runs in the background; status poll + SSE take over
    } catch (e) { setErr(e.message) }
    setDeploying(false)
  }

  // pick the freshest log: live SSE while streaming, else the polled snapshot
  const showLog = (pipe && pipe.running && liveLog) ? liveLog : (pipe ? pipe.log : '')
  const phaseBadge = () => {
    if (!pipe) return null
    if (pipe.running) return <span className="badge amber"><Lock size={12} /> {pipe.phase || 'running'}…{pipe.holder ? ' by ' + pipe.holder : ''}</span>
    if (pipe.rolledBack) return <span className="badge" style={{ color: 'var(--amber)' }}>rolled back</span>
    if (pipe.ok) return <span className="badge green"><Check size={12} /> deployed</span>
    if (pipe.phase === 'failed') return <span className="badge" style={{ color: 'var(--red)' }}>failed</span>
    return null
  }

  const del = async () => {
    setDelErr('')
    try {
      await api('/api/projects/' + name, { method: 'DELETE', body: JSON.stringify({ confirm: delPwd }) })
      setShowDelete(false)
      refresh()
      goHome()
    } catch (e) {
      setDelErr(e.message || 'delete failed')
    }
  }

  const TABS = [
    ['overview', 'Overview'], ['pipeline', 'Pipeline'], ['env', 'Environment'],
    ['versions', 'Versions'], ['domain', 'Domain'], ['firewall', 'Firewall'],
    ['amm', 'AMM'], ['settings', 'Settings'],
  ]

  return (
    <>
      <div className="proj-header">
        <button className="icon-btn" onClick={goHome} title="Back"><ArrowLeft size={16} /></button>
        <div className="proj-head-main">
          <div className="proj-title">
            {proj.name}
            {(proj.dnsOk || proj.mode === 'tunnel') && proj.domain &&
              <span className="live-tag"><span className="live-ball" /> live on {proj.domain}</span>}
            {proj.mode === 'tunnel' && <span className="badge" style={{ background: 'rgba(139,124,255,.15)', color: '#a99bff' }}><Zap size={12} /> Tunnel</span>}
            {proj.https && <span className="badge blue"><Lock size={12} /> HTTPS</span>}
            <span className="badge" style={{
              background: track === 'backend' ? 'rgba(52,211,153,.15)' : track === 'frontend' ? 'rgba(96,165,250,.15)' : 'rgba(255,255,255,.06)',
              color: track === 'backend' ? '#34d399' : track === 'frontend' ? '#60a5fa' : 'var(--text-dim)',
            }}>{track}</span>
            {proj.pending && (
              <span className="badge" style={{ background: 'rgba(251,191,36,.15)', color: '#fbbf24' }}>
                <Clock size={12} /> pending push{proj.pendingBy ? ' by ' + proj.pendingBy : ''} — deploy to go live
              </span>
            )}
          </div>
          <div className="proj-path">{proj.path}</div>
        </div>
        {can('track') && (
          <select className="input" value={track} onChange={(e) => saveTrack(e.target.value)} title="Project track — who can access this project"
                  style={{ width: 'auto', padding: '6px 10px', marginRight: 8 }}>
            <option value="shared">Shared</option>
            <option value="frontend">Frontend</option>
            <option value="backend">Backend</option>
          </select>
        )}
        {can('domains') && (
          <div className="seg-toggle" title="Deployment mode" style={{ display: 'inline-flex', gap: 4, padding: 4, background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 10, marginRight: 8 }}>
            <button className={'btn' + (mode === 'public' ? ' primary' : '')} style={{ borderRadius: 7, padding: '6px 12px' }}
                    onClick={() => switchMode('public')}>Public</button>
            <button className={'btn' + (mode === 'tunnel' ? ' primary' : '')} style={{ borderRadius: 7, padding: '6px 12px' }}
                    onClick={() => switchMode('tunnel')}><Zap size={13} /> Tunnel</button>
          </div>
        )}
        {can('terminal') && (
          <button className="btn term-btn" onClick={() => setTermOpen(true)} title="Open terminal">
            <TerminalSquare size={15} />
            Terminal
          </button>
        )}
      </div>

      <div className="proj-tabs">
        {TABS.map(([id, label]) => (
          <button key={id} className={'proj-tab' + (tab === id ? ' active' : '')} onClick={() => setTab(id)}>
            {label}
          </button>
        ))}
      </div>

      {tab === 'overview' && (<div className="tab-panel">
      <div className="grid grid-3">
        <div className="card">
          <div className="card-title">FILES</div>
          <div className="metric">{proj.files}</div>
        </div>
        <div className="card">
          <div className="card-title">SIZE</div>
          <div className="metric">{proj.size}</div>
        </div>
        <div className="card">
          <div className="card-title">DOMAIN</div>
          <div className="metric" style={{ fontSize: 18, wordBreak: 'break-all' }}>
            {proj.domain || '—'}
          </div>
          <div className="metric-sub">{proj.domain ? (proj.dnsOk ? 'DNS verified' : 'waiting for DNS') : 'not configured'}</div>
        </div>
      </div>

      </div>)}

      {tab === 'pipeline' && (<div className="tab-panel">
      <div className="card mt">
        <div className="row-flex" style={{ justifyContent: 'space-between', alignItems: 'center' }}>
          <div className="card-title" style={{ marginBottom: 0 }}>
            CI/CD PIPELINE <span className="badge blue">auto</span> {phaseBadge()}
          </div>
          <label className="row-flex" style={{ gap: 8, fontSize: 13, color: 'var(--text-dim)', cursor: 'pointer' }}>
            <input type="checkbox" checked={autoDeploy} onChange={toggleAuto} />
            Auto-deploy on upload
          </label>
        </div>
        <p style={{ fontSize: 13, color: 'var(--text-dim)', lineHeight: 1.5, marginTop: 8 }}>
          Every upload is detected, built, restarted, and <b>health-checked</b> automatically.
          If a new version fails to come up, Sixter rolls back to the last working version.
        </p>
        <div className="row-flex mt">
          <button className="btn" onClick={detect} disabled={deploying || (pipe && pipe.running)}>Detect type</button>
          <button className="btn primary" onClick={deploy} disabled={deploying || (pipe && pipe.running)}>
            {(deploying || (pipe && pipe.running)) ? <span className="spin" /> : 'Deploy now'}
          </button>
        </div>
        {plan && (
          <div className="dns-status ok mt" style={{ alignItems: 'flex-start' }}>
            <div className="dns-tick"><Settings2 size={14} /></div>
            <div>
              <b>{plan.title}</b><br />
              <span style={{ color: 'var(--text-dim)' }}>{plan.notes}</span>
              {plan.buildCmd && <div className="code-block">{plan.buildCmd}</div>}
              {plan.runCmd && <div className="code-block">run: {plan.runCmd}</div>}
            </div>
          </div>
        )}
        {showLog && (
          <pre style={{
            marginTop: 12, padding: '12px 14px', background: 'var(--bg)',
            border: '1px solid var(--border)', borderRadius: 8, maxHeight: 300,
            overflow: 'auto', fontFamily: 'var(--mono)', fontSize: 11.5,
            color: 'var(--text-dim)', whiteSpace: 'pre-wrap',
          }}>{showLog}</pre>
        )}
        {pipe && pipe.history && pipe.history.length > 0 && (
          <div className="mt">
            <div className="metric-sub" style={{ marginBottom: 6 }}>Recent deploys</div>
            {pipe.history.slice(0, 6).map((h) => (
              <div className="row-flex" key={h.id} style={{
                justifyContent: 'space-between', fontSize: 12, padding: '6px 0',
                borderBottom: '1px solid var(--border)',
              }}>
                <span style={{ color: 'var(--text-dim)' }}>
                  {new Date(h.startedAt).toLocaleString()} · {h.trigger}
                </span>
                <span style={{ color: h.rolledBack ? 'var(--amber)' : (h.ok ? 'var(--green)' : 'var(--red)') }}>
                  {h.rolledBack
                    ? <><Ico as={RotateCcw} size={12} /> rolled back</>
                    : (h.ok ? <><Ico as={Check} size={12} /> ok</> : <><Ico as={X} size={12} /> {h.phase || 'failed'}</>)}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>
      </div>)}

      {tab === 'env' && (<div className="tab-panel">
      <Panel name="Release command"><ReleasePanel project={project} /></Panel>
      <div className="card mt">
        <div className="card-title">ENVIRONMENT VARIABLES</div>
        <p style={{ fontSize: 13, color: 'var(--text-dim)', lineHeight: 1.5 }}>
          Production config for your backend (database URLs, API keys, secrets).
          Stored securely on this server and injected into your app on deploy/restart.
          The same variables are given to your build and release commands.
        </p>
        {envRows.length === 0 && (
          <div style={{ fontSize: 13, color: 'var(--text-faint)', margin: '8px 0' }}>No variables yet.</div>
        )}
        {envRows.map((row, i) => (
          <div className="row-flex" key={i} style={{ marginTop: 8, alignItems: 'center' }}>
            <input className="input mono" placeholder="KEY" value={row.key} style={{ flex: '0 0 38%' }}
                   onChange={(e) => setEnvRow(i, 'key', e.target.value.toUpperCase())} />
            <input className="input mono" placeholder="value" value={row.value} style={{ flex: 1 }}
                   onChange={(e) => setEnvRow(i, 'value', e.target.value)} />
            <button className="btn" onClick={() => removeEnvRow(i)} title="remove" style={{ flex: '0 0 auto' }}><X size={14} /></button>
          </div>
        ))}
        <div className="row-flex mt">
          <button className="btn" onClick={addEnvRow}><Plus size={14} /> Add variable</button>
          <button className="btn primary" onClick={saveEnv} disabled={envSaving}>
            {envSaving ? <span className="spin" /> : 'Save variables'}
          </button>
        </div>
        {envMsg && <div className="dns-status ok mt"><div className="dns-tick"><Check size={14} /></div><div>{envMsg}</div></div>}
      </div>
      </div>)}

      {tab === 'versions' && (<div className="tab-panel">
      <div className="card mt">
        <div className="card-title">VERSIONS <span className="badge blue">rollback</span></div>
        <p style={{ fontSize: 13, color: 'var(--text-dim)', lineHeight: 1.5 }}>
          Every upload is snapshotted. If a new release breaks, roll back to a previous
          version — Sixter saves your current state first, then restores and redeploys it.
        </p>
        {versions.length === 0 ? (
          <div style={{ fontSize: 13, color: 'var(--text-faint)', marginTop: 8 }}>
            No previous versions yet — they appear here after you re-upload this project.
          </div>
        ) : (
          <div className="mt">
            {versions.map((v, i) => (
              <div className="row-flex" key={v.id} style={{
                justifyContent: 'space-between', alignItems: 'center',
                padding: '10px 12px', border: '1px solid var(--border)', borderRadius: 8, marginTop: 8,
              }}>
                <div>
                  <div style={{ fontFamily: 'var(--mono)', fontSize: 13 }}>
                    {new Date(v.time).toLocaleString()}{i === 0 ? '  (most recent)' : ''}
                  </div>
                  <div style={{ fontSize: 11.5, color: 'var(--text-faint)' }}>{v.sizeHuman} · {v.id}</div>
                </div>
                <button className="btn" onClick={() => rollback(v.id)} disabled={rolling !== ''}>
                  {rolling === v.id ? <span className="spin" /> : 'Roll back'}
                </button>
              </div>
            ))}
          </div>
        )}
        {deployLog && (
          <pre style={{
            marginTop: 12, padding: '12px 14px', background: 'var(--bg)',
            border: '1px solid var(--border)', borderRadius: 8, maxHeight: 240,
            overflow: 'auto', fontFamily: 'var(--mono)', fontSize: 11.5,
            color: 'var(--text-dim)', whiteSpace: 'pre-wrap',
          }}>{deployLog}</pre>
        )}
      </div>
      </div>)}

      {tab === 'domain' && (<div className="tab-panel">
      <div className="card mt">
        <div className="card-title">
          CUSTOM DOMAIN
          <span className="badge" style={{ marginLeft: 8, background: mode === 'tunnel' ? 'rgba(139,124,255,.15)' : 'var(--bg)', color: mode === 'tunnel' ? '#a99bff' : 'var(--text-dim)' }}>
            {mode === 'tunnel' ? <><Zap size={12} /> Tunnel mode</> : 'Public Server mode'}
          </span>
        </div>

        <p style={{ fontSize: 13, color: 'var(--text-dim)', lineHeight: 1.5, marginTop: 8 }}>
          {mode === 'public'
            ? 'Public Server: this machine has a public IP. Point your domain’s A record here and Sixter writes the nginx config + SSL automatically.'
            : 'Tunnel: no public IP needed. Sixter’s gateway accepts traffic for your domain, applies WAF + DDoS protection, terminates SSL, and forwards it down this server’s secure tunnel to your app.'}
          {' '}Use the <b>Public / Tunnel</b> switch at the top-right to change modes.
        </p>

        <label className="field-label">Domain name</label>
        <input className="input mono" placeholder="myapp.example.com" value={domain}
               onChange={(e) => setDomain(e.target.value)} />
        <label className="field-label">App port (where your app listens on this server)</label>
        <input className="input mono" type="number" placeholder="3000" value={port}
               onChange={(e) => setPort(e.target.value)} style={{ maxWidth: 180 }} />
        {mode === 'tunnel' && domain.trim() && (() => {
          const labels = domain.trim().split('.')
          const isApex = labels.length <= 2
          return (
            <div className="card" style={{ background: 'var(--bg)', marginTop: 14, borderColor: 'var(--border)' }}>
              <div className="card-title">DNS RECORD TO ADD</div>
              <p style={{ fontSize: 12.5, color: 'var(--text-dim)', lineHeight: 1.5 }}>
                Add this record at your DNS provider (Cloudflare, GoDaddy, Namecheap…) so traffic for <b>{domain.trim()}</b> reaches the Sixter gateway:
              </p>
              <div className="code-block">
                Type:  CNAME{'\n'}
                Name:  {isApex ? domain.trim() + '   (apex / root)' : labels[0] + '   → ' + domain.trim()}{'\n'}
                Value: sixter.in{'\n'}
                Proxy: DNS only (grey cloud)
              </div>
              {isApex && (
                <p style={{ fontSize: 12, color: 'var(--text-dim)', lineHeight: 1.5 }}>
                  This is an <b>apex domain</b> — plain DNS can’t put a CNAME at the root. Cloudflare flattens this automatically (works as shown). On providers without flattening, use an <b>A record</b> to the gateway IP instead.
                </p>
              )}
            </div>
          )
        })()}
        <div className="row-flex mt">
          <button className="btn primary" onClick={saveDomain} disabled={checking || !domain.trim()}>
            {checking ? <span className="spin" /> : (mode === 'tunnel' ? 'Save & connect tunnel' : 'Save & check DNS')}
          </button>
          {proj.domain && mode === 'public' && (
            <button className="btn" onClick={recheck} disabled={checking}>Re-check DNS</button>
          )}
        </div>
        {err && <div className="dns-status warn mt"><div className="dns-tick"><AlertTriangle size={14} /></div><div>{err}</div></div>}

        {/* ── Tunnel mode result ── */}
        {dnsResult && !err && mode === 'tunnel' && (
          <div className="dns-status ok">
            <div className="dns-tick"><Check size={14} /></div>
            <div>
              <b>Tunnel connected.</b><br />
              <span style={{ color: 'var(--text-dim)' }}>
                {dnsResult.domain || domain} is now served through the Sixter gateway → this server → your app on port {dnsResult.port || port}.<br />
                Point your domain at the gateway so traffic can reach it:
              </span>
              <div className="code-block">
                Type: CNAME{'\n'}Name: {(dnsResult.domain || domain)}{'\n'}Value: sixter.in
              </div>
              <span style={{ color: 'var(--text-dim)', fontSize: 12 }}>
                Apex domains (no subdomain) can’t use CNAME — use an A record to the gateway IP, or your DNS provider’s “CNAME flattening”.
                {dnsResult.note && <><br />{dnsResult.note}</>}
              </span>
            </div>
          </div>
        )}

        {/* ── Public mode result ── */}
        {dnsResult && !err && mode === 'public' && (
          dnsResult.dnsOk ? (
            <div className="dns-status ok">
              <div className="dns-tick"><Check size={14} /></div>
              <div>
                <b>DNS configured correctly.</b><br />
                <span style={{ color: 'var(--text-dim)' }}>
                  {dnsResult.domain || domain} → {dnsResult.domainIP} (this server: {dnsResult.serverIP})<br />
                  nginx config written to <span style={{ fontFamily: 'var(--mono)', fontSize: 12 }}>{dnsResult.nginxPath}</span>
                  {dnsResult.note && <><br />{dnsResult.note}</>}
                </span>
              </div>
            </div>
          ) : (
            <div className="dns-status warn">
              <div className="dns-tick"><Clock size={14} /></div>
              <div>
                <b>DNS not pointing here yet.</b><br />
                <span style={{ color: 'var(--text-dim)' }}>
                  Add an <b>A record</b> at your DNS provider (Cloudflare, GoDaddy, …):
                </span>
                <div className="code-block">
                  Type: A{'\n'}Name: {(dnsResult.domain || domain).split('.')[0]}{'\n'}Value: {dnsResult.serverIP || 'your server IP'}
                </div>
                <span style={{ color: 'var(--text-dim)', fontSize: 12 }}>
                  DNS changes can take a few minutes — hit “Re-check DNS” after.
                </span>
              </div>
            </div>
          )
        )}
      </div>
      </div>)}

      {tab === 'overview' && (<div className="tab-panel" style={{ marginTop: 18 }}>
      {stats?.hasCapture ? (
        <div className="card mt">
          <div className="card-title">
            WEB TRAFFIC — {stats.domain}
            <span className="badge blue">{(stats.total || 0).toLocaleString()} req / hr</span>
          </div>
          <AreaChart data={(stats.minutes || []).map((m) => ({ v: m.requests }))} label="requests/min" color="#8b7cff" height={140} />
          <div className="grid grid-2 mt">
            <div>
              <div className="card-title" style={{ marginTop: 6 }}>TOP PATHS</div>
              <BarList items={stats.topPaths || []} color="#8b7cff" />
            </div>
            <div>
              <div className="card-title" style={{ marginTop: 6 }}>LIVE PACKETS</div>
              <div className="tbl-wrap scroll-y" style={{ maxHeight: 230 }}>
                <table className="tbl">
                  <thead><tr><th>Time</th><th>Src</th><th>Method</th><th>Path</th><th>Status</th><th>Size</th></tr></thead>
                  <tbody>
                    {(stats.packets || []).slice(0, 60).map((p) => (
                      <tr key={p.id}>
                        <td>{p.time}</td><td>{p.src}</td><td>{p.method}</td>
                        <td title={p.path}>{p.path}</td>
                        <td className={'s' + Math.floor((p.status || 0) / 100)}>{p.status}</td>
                        <td>{fmtBytes(p.respBytes)}</td>
                      </tr>
                    ))}
                    {(stats.packets || []).length === 0 && (
                      <tr><td colSpan="6" style={{ textAlign: 'center', padding: 22, color: 'var(--text-faint)' }}>
                        Waiting for the first request to {stats.domain}…
                      </td></tr>
                    )}
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        </div>
      ) : (
        <div className="card mt">
          <div className="card-title">WEB TRAFFIC</div>
          <div className="empty">
            Per-project traffic graphs and packet capture turn on once this project
            has a <b>custom domain</b> with working DNS + nginx (configure it above).
          </div>
        </div>
      )}
      </div>)}

      {tab === 'amm' && <AMMPanel project={name} />}

      {tab === 'firewall' && (<div className="tab-panel">
      {proj.mode === 'tunnel' && (
        <div className="dns-status ok mt">
          <div className="dns-tick"><Zap size={14} /></div>
          <div>
            <b>Protected at the Sixter gateway.</b><br />
            <span style={{ color: 'var(--text-dim)' }}>
              Because {proj.domain || name} runs in Tunnel mode, every request is filtered by the gateway’s WAF (SQLi / XSS / path-traversal) and global DDoS guard <i>before</i> it enters the tunnel — even attacks never reach this server. Stats below reflect traffic that passed the gateway.
            </span>
          </div>
        </div>
      )}
      <div className="card mt">
        <div className="row-flex" style={{ justifyContent: 'space-between', alignItems: 'flex-start' }}>
          <div>
            <div className="card-title" style={{ marginBottom: 6 }}>FIREWALL — {proj.domain || name}</div>
            <p style={{ fontSize: 13, color: 'var(--text-dim)', lineHeight: 1.5, maxWidth: 520 }}>
              Every request to this site is scanned at the edge (WAF signatures + rate limiting),
              and the server is hardened at the kernel + nginx layers. Below is what we've seen.
            </p>
          </div>
          <span className={'badge ' + (sec && sec.score >= 85 ? 'green' : '')}
                style={sec && sec.score < 85 ? { color: 'var(--amber)' } : null}>
            {sec ? sec.status : '…'}
          </span>
        </div>

        {sec && (
          <div className="grid grid-4 mt">
            <div className="card"><div className="card-title">SAFETY SCORE</div>
              <div className="metric" style={{ color: sec.score >= 85 ? 'var(--green)' : sec.score >= 50 ? 'var(--amber)' : 'var(--red)' }}>{sec.score}</div>
              <div className="metric-sub">out of 100</div></div>
            <div className="card"><div className="card-title">REQUESTS SEEN</div>
              <div className="metric">{(sec.total || 0).toLocaleString()}</div></div>
            <div className="card"><div className="card-title">ATTACKS DETECTED</div>
              <div className="metric" style={{ color: sec.attacks ? 'var(--red)' : 'inherit' }}>{sec.attacks || 0}</div></div>
            <div className="card"><div className="card-title">RATE-LIMITED</div>
              <div className="metric">{sec.rateLimited || 0}</div></div>
          </div>
        )}
      </div>

      {sec && sec.offenders && sec.offenders.length > 0 && (
        <div className="card mt">
          <div className="card-title">TOP OFFENDERS</div>
          {sec.offenders.map((o) => (
            <div className="row-flex" key={o.ip} style={{ justifyContent: 'space-between', padding: '8px 0', borderBottom: '1px solid var(--border)' }}>
              <span className="mono">{o.ip}
                <span style={{ color: 'var(--text-faint)' }}> · {o.count} attack{o.count === 1 ? '' : 's'}</span>
                {o.proxy && <span className="badge" style={{ marginLeft: 8, color: 'var(--amber)' }}>via CDN — don't block</span>}
              </span>
              {o.proxy
                ? <button className="btn sm" disabled title="Cloudflare/CDN IP — blocking it would cut off real visitors">Protected</button>
                : <button className="btn sm danger" onClick={() => banIp(o.ip)}>Block 1h</button>}
            </div>
          ))}
        </div>
      )}

      <div className="card mt">
        <div className="card-title">RECENT SUSPICIOUS REQUESTS</div>
        {(!sec || !sec.events || sec.events.length === 0) ? (
          <div className="empty">No attacks detected. That's a good thing.</div>
        ) : (
          <div className="tbl-wrap scroll-y" style={{ maxHeight: 320 }}>
            <table className="tbl">
              <thead><tr><th>Time</th><th>IP</th><th>Type</th><th>Method</th><th>Path</th><th>Status</th></tr></thead>
              <tbody>
                {sec.events.map((e, i) => (
                  <tr key={i}>
                    <td>{e.time}</td><td className="mono">{e.ip}</td>
                    <td style={{ color: 'var(--red)' }}>{e.category}</td>
                    <td>{e.method}</td><td title={e.path} style={{ maxWidth: 260, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{e.path}</td>
                    <td>{e.status}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
      </div>)}

      {tab === 'settings' && (<div className="tab-panel">
      <div className="card mt">
        <div className="card-title">PROJECT NAME</div>
        <p style={{ fontSize: 13, color: 'var(--text-dim)', lineHeight: 1.5 }}>
          Rename this project. Its files, env vars, versions and deploy history move with it,
          and it redeploys automatically. Your domain &amp; public URL are unaffected.
        </p>
        <label className="field-label">New name</label>
        <div className="row-flex" style={{ alignItems: 'center' }}>
          <input className="input mono" placeholder={name} value={newName} style={{ maxWidth: 320 }}
                 onChange={(e) => setNewName(e.target.value.toLowerCase().replace(/[^a-z0-9._-]/g, ''))} />
          <button className="btn primary" onClick={renameProj}
                  disabled={renaming || !newName.trim() || newName.trim() === name}>
            {renaming ? <span className="spin" /> : 'Rename'}
          </button>
        </div>
        {settingsMsg && <div className="dns-status warn mt"><div className="dns-tick"><AlertTriangle size={14} /></div><div>{settingsMsg}</div></div>}
      </div>

      <div className="card mt">
        <div className="card-title">DETAILS</div>
        <div className="kv-row"><span>Files</span><b>{proj.files}</b></div>
        <div className="kv-row"><span>Size</span><b>{proj.size}</b></div>
        <div className="kv-row"><span>Path</span><b className="mono" style={{ fontWeight: 400 }}>{proj.path}</b></div>
        <div className="kv-row"><span>Domain</span><b>{proj.domain || '—'}</b></div>
        <div className="kv-row" style={{ border: 0 }}><span>Auto-deploy</span><b>{autoDeploy ? 'On' : 'Off'}</b></div>
      </div>

      <div className="card danger-zone mt">
        <div className="card-title" style={{ color: 'var(--red)' }}>DANGER ZONE</div>
        <div className="row-flex" style={{ justifyContent: 'space-between' }}>
          <span style={{ fontSize: 13, color: 'var(--text-dim)' }}>
            Permanently delete this project and its files from the server.
          </span>
          <button className="btn danger" onClick={() => { setDelPwd(''); setDelErr(''); setShowDelete(true) }}>Delete project</button>
        </div>
      </div>
      </div>)}

      {/* Terminal slide-over — accessible from any tab via the top-right button */}
      {termOpen && <TerminalDrawer name={name} onClose={() => setTermOpen(false)} />}

      {confirmReq && (
        <div className="modal-backdrop" onClick={() => resolveConfirm(false)}>
          <div className="modal" onClick={(e) => e.stopPropagation()}>
            <h3>{confirmReq.title}</h3>
            <p>{confirmReq.body}</p>
            <div className="row-flex mt" style={{ justifyContent: 'flex-end' }}>
              <button className="btn" onClick={() => resolveConfirm(false)}>Cancel</button>
              <button className={'btn ' + (confirmReq.danger ? 'danger' : 'primary')} autoFocus onClick={() => resolveConfirm(true)}>
                {confirmReq.confirmLabel || 'Confirm'}
              </button>
            </div>
          </div>
        </div>
      )}

      {toast && <div className={'toast ' + toast.kind}>{toast.text}</div>}

      {showDelete && (
        <div className="modal-backdrop" onClick={() => setShowDelete(false)}>
          <div className="modal" onClick={(e) => e.stopPropagation()}>
            <h3>Delete “{name}”?</h3>
            <p>This permanently removes the project and all its files from the server. This cannot be undone.</p>
            <label className="field-label">Type <b className="mono">{name}</b> to confirm</label>
            <input className="input mono" autoFocus value={delPwd} spellCheck={false} autoComplete="off"
                   onChange={(e) => setDelPwd(e.target.value)}
                   onKeyDown={(e) => e.key === 'Enter' && delPwd === name && del()} placeholder={name} />
            {delErr && <div className="dns-status warn mt"><div className="dns-tick"><AlertTriangle size={14} /></div><div>{delErr}</div></div>}
            <div className="row-flex mt" style={{ justifyContent: 'flex-end' }}>
              <button className="btn" onClick={() => setShowDelete(false)}>Cancel</button>
              <button className="btn danger" onClick={del} disabled={delPwd !== name}>Delete forever</button>
            </div>
          </div>
        </div>
      )}
    </>
  )
}

/* ════════════════ app shell ════════════════ */

const DB_ENGINES = { postgres: 'PostgreSQL', mysql: 'MySQL / MariaDB', redis: 'Redis', sqlite: 'SQLite' }

function DatabasesPage({ projects }) {
  const [data, setData] = useState(null)
  const [busy, setBusy] = useState('')
  const [engine, setEngine] = useState('postgres')
  const [name, setName] = useState('')
  const [created, setCreated] = useState(null)
  const [tables, setTables] = useState({})
  const [attachSel, setAttachSel] = useState({})
  const [attachInfo, setAttachInfo] = useState(null)
  const [toast, setToast] = useState(null)
  const [confirmReq, setConfirmReq] = useState(null)
  const notify = (t, k = 'ok') => { setToast({ t, k }); setTimeout(() => setToast(null), 3600) }
  const ask = (o) => new Promise((res) => setConfirmReq({ ...o, resolve: res }))
  const resolveConfirm = (v) => setConfirmReq((c) => { c && c.resolve(v); return null })

  const load = () => api('/api/databases').then(setData).catch(() => {})
  useEffect(() => { load() }, [])

  const doInstall = async (eng) => {
    setBusy('i' + eng)
    try { const r = await api('/api/databases/install', { method: 'POST', body: JSON.stringify({ engine: eng }) }); notify(r.ok ? DB_ENGINES[eng] + ' installed' : (r.error || 'install failed'), r.ok ? 'ok' : 'err'); await load() } catch (e) { notify(e.message, 'err') }
    setBusy('')
  }
  const doCreate = async () => {
    if (!name.trim()) return
    setBusy('c'); setCreated(null)
    try { const r = await api('/api/databases/create', { method: 'POST', body: JSON.stringify({ engine, name: name.trim() }) }); if (r.ok) { setCreated(r.database); setName(''); notify('Database created'); await load() } else notify(r.error || 'create failed', 'err') } catch (e) { notify(e.message, 'err') }
    setBusy('')
  }
  const doTables = async (db) => {
    setBusy('t' + db.id)
    try { const r = await api('/api/databases/' + db.id + '/tables'); setTables((p) => ({ ...p, [db.id]: r.ok ? r.tables : ['(error) ' + (r.error || '')] })) } catch (e) { notify(e.message, 'err') }
    setBusy('')
  }
  const doAttach = async (db) => {
    const p = attachSel[db.id]; if (!p) { notify('Pick a project first', 'err'); return }
    setBusy('a' + db.id)
    try {
      const r = await api('/api/databases/' + db.id + '/attach', { method: 'POST', body: JSON.stringify({ project: p }) })
      if (r.ok) { setAttachInfo({ db: db.name, project: p, framework: r.framework, snippet: r.snippet, vars: r.vars || [] }); notify(r.note || 'Connected'); await load() }
      else notify(r.error || 'failed', 'err')
    } catch (e) { notify(e.message, 'err') }
    setBusy('')
  }
  const doDelete = async (db) => {
    if (!(await ask({ title: 'Delete database?', body: `Permanently delete ${db.name} (${db.engine}) and all its data.`, confirmLabel: 'Delete', danger: true }))) return
    setBusy('d' + db.id)
    try { const r = await api('/api/databases/' + db.id, { method: 'DELETE' }); notify(r.ok ? 'Deleted' : 'failed', r.ok ? 'ok' : 'err'); await load() } catch (e) { notify(e.message, 'err') }
    setBusy('')
  }

  if (!data) return <div className="empty">Loading…</div>
  const installed = (data.engines || []).filter((e) => e.installed).map((e) => e.id)
  return (
    <>
      <div className="page-title">Databases</div>
      <div className="page-sub">Create databases on this server and attach them to your projects in one click.</div>

      <div className="grid grid-4">
        {(data.engines || []).map((e) => (
          <div className="card" key={e.id}>
            <div className="card-title">{DB_ENGINES[e.id] || e.id}</div>
            {e.installed
              ? <span className="badge green"><Check size={12} /> installed</span>
              : <button className="btn sm" disabled={busy === 'i' + e.id} onClick={() => doInstall(e.id)}>{busy === 'i' + e.id ? <span className="spin" /> : 'Install'}</button>}
          </div>
        ))}
      </div>

      <div className="card mt">
        <div className="card-title">CREATE DATABASE</div>
        <div className="row-flex mt" style={{ flexWrap: 'wrap' }}>
          <select className="input" style={{ maxWidth: 220 }} value={engine} onChange={(e) => setEngine(e.target.value)}>
            {Object.entries(DB_ENGINES).map(([id, label]) => (
              <option key={id} value={id} disabled={!installed.includes(id)}>{label}{installed.includes(id) ? '' : ' — install first'}</option>
            ))}
          </select>
          <input className="input mono" placeholder="db_name" value={name} style={{ maxWidth: 240 }}
                 onChange={(e) => setName(e.target.value.replace(/[^a-zA-Z0-9_]/g, ''))} />
          <button className="btn primary" disabled={busy === 'c' || !installed.includes(engine)} onClick={doCreate}>
            {busy === 'c' ? <span className="spin" /> : 'Create'}
          </button>
        </div>
        {created && (
          <div className="dns-status ok mt" style={{ alignItems: 'flex-start' }}>
            <div className="dns-tick"><Check size={14} /></div>
            <div><b>{created.name}</b> created. Connection string:
              <div className="code-block">{created.connString}</div>
            </div>
          </div>
        )}
      </div>

      <div className="card mt">
        <div className="card-title">YOUR DATABASES <span className="badge blue">{(data.databases || []).length}</span></div>
        {(data.databases || []).length === 0
          ? <div className="empty">No databases yet — create one above.</div>
          : (data.databases || []).map((db) => (
            <div key={db.id} style={{ padding: '12px 0', borderBottom: '1px solid var(--border)' }}>
              <div className="row-flex" style={{ justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 8 }}>
                <div>
                  <span className="badge blue">{db.engine}</span> <b className="mono">{db.name}</b>
                  {db.project && <span className="badge green" style={{ marginLeft: 6 }}>→ {db.project}</span>}
                </div>
                <div className="row-flex" style={{ gap: 6, flexWrap: 'wrap' }}>
                  <button className="btn sm" disabled={busy === 't' + db.id} onClick={() => doTables(db)}>View tables</button>
                  <select className="input" style={{ padding: '5px 8px', fontSize: 12, maxWidth: 150 }}
                          value={attachSel[db.id] || ''} onChange={(e) => setAttachSel((s) => ({ ...s, [db.id]: e.target.value }))}>
                    <option value="">attach to…</option>
                    {(projects || []).map((p) => <option key={p.name} value={p.name}>{p.name}</option>)}
                  </select>
                  <button className="btn sm" disabled={busy === 'a' + db.id} onClick={() => doAttach(db)}>Attach</button>
                  <button className="btn sm danger" disabled={busy === 'd' + db.id} onClick={() => doDelete(db)}>Delete</button>
                </div>
              </div>
              <div className="code-block" style={{ marginTop: 8 }}>{db.connString}</div>
              {tables[db.id] && (
                <div className="mt" style={{ fontSize: 12.5, color: 'var(--text-dim)' }}>
                  Tables: {tables[db.id].length ? tables[db.id].join(', ') : '(none yet)'}
                </div>
              )}
            </div>
          ))}
      </div>

      {attachInfo && (
        <div className="modal-backdrop" onClick={() => setAttachInfo(null)}>
          <div className="modal" onClick={(e) => e.stopPropagation()} style={{ width: 540 }}>
            <h3>Connected <span className="mono">{attachInfo.db}</span> → {attachInfo.project}</h3>
            <p>The connection was injected as environment variables — your app just needs to read them.</p>
            <div style={{ margin: '12px 0', fontSize: 12.5, color: 'var(--text-dim)' }}>
              Injected: {(attachInfo.vars || []).map((v) => <code key={v} className="mono" style={{ marginRight: 8 }}>{v}</code>)}
            </div>
            {attachInfo.framework && <div className="metric-sub" style={{ marginBottom: 6 }}>Detected: <b>{attachInfo.framework}</b> — use it like this:</div>}
            <pre className="code-block" style={{ whiteSpace: 'pre-wrap' }}>{attachInfo.snippet}</pre>
            <div className="row-flex mt" style={{ justifyContent: 'flex-end' }}>
              <button className="btn primary" onClick={() => setAttachInfo(null)}>Got it</button>
            </div>
          </div>
        </div>
      )}

      {confirmReq && (
        <div className="modal-backdrop" onClick={() => resolveConfirm(false)}>
          <div className="modal" onClick={(e) => e.stopPropagation()}>
            <h3>{confirmReq.title}</h3><p>{confirmReq.body}</p>
            <div className="row-flex mt" style={{ justifyContent: 'flex-end' }}>
              <button className="btn" onClick={() => resolveConfirm(false)}>Cancel</button>
              <button className={'btn ' + (confirmReq.danger ? 'danger' : 'primary')} onClick={() => resolveConfirm(true)}>{confirmReq.confirmLabel || 'Confirm'}</button>
            </div>
          </div>
        </div>
      )}
      {toast && <div className={'toast ' + toast.k}>{toast.t}</div>}
    </>
  )
}

function timeAgo(t) {
  const d = (Date.now() - new Date(t).getTime()) / 1000
  if (d < 60) return Math.max(0, Math.floor(d)) + 's ago'
  if (d < 3600) return Math.floor(d / 60) + 'm ago'
  if (d < 86400) return Math.floor(d / 3600) + 'h ago'
  return Math.floor(d / 86400) + 'd ago'
}

function ActivityPage() {
  const [events, setEvents] = useState(null)
  usePoll(async () => { try { const r = await api('/api/activity'); setEvents(r.events || []) } catch { /* ignore */ } }, 8000, [])
  return (
    <div className="card mt">
      <div className="card-title">TEAM ACTIVITY</div>
      <p style={{ fontSize: 13, color: 'var(--text-dim)', marginTop: 2, marginBottom: 14 }}>
        Every deploy, rollback and config change across your team — newest first. Doubles as an audit trail.
      </p>
      {events === null && <div style={{ color: 'var(--text-faint)', padding: 12 }}>Loading…</div>}
      {events && events.length === 0 && <div style={{ color: 'var(--text-faint)', padding: 12 }}>No activity yet.</div>}
      {events && events.map((e, i) => (
        <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '11px 2px', borderTop: i ? '1px solid var(--border)' : 'none' }}>
          <span style={{ width: 30, height: 30, borderRadius: '50%', background: 'linear-gradient(135deg,#8b7cff,#5a8bff)', color: '#fff', fontSize: 12, fontWeight: 700, display: 'grid', placeItems: 'center', flexShrink: 0 }}>
            {(e.actor || '?')[0].toUpperCase()}
          </span>
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ fontSize: 13.5 }}><b>{e.actor}</b> · {e.action}{e.project ? <> · <span style={{ color: 'var(--text-dim)' }}>{e.project}</span></> : null}</div>
            <div style={{ fontSize: 11.5, color: 'var(--text-faint)' }}>{timeAgo(e.time)}</div>
          </div>
          {e.status >= 400 && <span className="badge" style={{ background: 'rgba(248,113,113,.15)', color: '#f87171' }}>failed</span>}
        </div>
      ))}
    </div>
  )
}

function WorkspacesPage({ projects, refresh }) {
  const [comps, setComps] = useState({})
  const apex = (window.location.hostname.split('.').slice(1).join('.')) || 'sixter.in'

  useEffect(() => {
    api('/api/components').then((r) => setComps(r.byWorkspace || {})).catch(() => setComps({}))
  }, [])

  return (
    <>
      <div className="dx-page-head">
        <div>
          <h1 className="dx-page-title">Workspaces</h1>
          <p className="dx-page-sub">Team, roles and the join link live in your Sixter account. Component ownership on this server is shown below.</p>
        </div>
      </div>

      {/* one source of truth: manage workspaces on the gateway */}
      <div className="card" style={{ marginBottom: 18, background: 'linear-gradient(180deg, rgba(139,124,255,.08), var(--dx-card))', borderColor: 'rgba(139,124,255,.28)' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
          <span style={{ width: 38, height: 38, borderRadius: 11, display: 'grid', placeItems: 'center', background: 'rgba(139,124,255,.16)', color: 'var(--dx-primary)' }}><Boxes size={19} /></span>
          <div style={{ flex: '1 1 260px', minWidth: 0 }}>
            <b style={{ fontSize: 15 }}>Manage your team on Sixter</b>
            <div style={{ fontSize: 13, color: 'var(--dx-text2)', marginTop: 3 }}>
              Create a workspace, add teammates and copy the one permanent <span className="mono">sixter project init</span> link from your account console.
            </div>
          </div>
          <a className="btn primary" href={`https://${apex}/dashboard`} target="_blank" rel="noreferrer">Open Workspaces on {apex} ↗</a>
        </div>
        <div style={{ fontSize: 12, color: 'var(--dx-text3)', marginTop: 12, lineHeight: 1.6 }}>
          The join link is issued and verified by Sixter, so every member is identified by their own login. Tokens created here on the repo server won’t work with <span className="mono">sixter project init</span> — always use the link from your account console.
        </div>
      </div>

      {/* read-only: who owns which component on THIS server */}
      <div className="card">
        <div className="card-title">MEMBER COMPONENTS</div>
        <p style={{ fontSize: 12.5, color: 'var(--dx-text3)', marginTop: 2, marginBottom: 12 }}>
          Each member owns their own folder and can’t overwrite a teammate’s. Open a pending component from its project page to review and deploy it.
        </p>
        {Object.keys(comps).length === 0 && (
          <div className="empty">No components yet. When a teammate pushes with <span className="mono">sixter push</span>, their component appears here.</div>
        )}
        {Object.entries(comps).map(([wsName, list]) => (
          <div key={wsName || '_'} style={{ border: '1px solid var(--dx-border)', borderRadius: 12, padding: '10px 14px', marginBottom: 10 }}>
            <b style={{ fontSize: 13.5 }}>{wsName || 'Ungrouped'}</b>
            {list.map((c) => (
              <div key={c.project} style={{ display: 'flex', alignItems: 'center', gap: 9, padding: '7px 0', borderTop: '1px solid var(--dx-border)', flexWrap: 'wrap', fontSize: 13 }}>
                <b>{c.component || c.project}</b>
                <span className="badge">{c.role}</span>
                <span style={{ color: 'var(--dx-text3)', fontSize: 12 }}>owner: {c.owner}</span>
                <div style={{ flex: 1 }} />
                {c.pending
                  ? <span title={'pending push by ' + c.pending.author} style={{ display: 'inline-flex', alignItems: 'center', gap: 5, color: '#fbbf24', fontSize: 12 }}><Clock size={12} /> pending review</span>
                  : <span style={{ color: '#34d399', fontSize: 12 }}>● live</span>}
              </div>
            ))}
          </div>
        ))}
      </div>
    </>
  )
}
function DiffView({ files }) {
  if (!files || files.length === 0) return <div style={{ fontSize: 13, color: 'var(--text-faint)', padding: '10px 0' }}>No file changes.</div>
  const statusColor = { added: '#34d399', removed: '#fb7185', modified: '#fbbf24' }
  return (
    <div style={{ marginTop: 10 }}>
      {files.map((f) => (
        <div key={f.path} style={{ border: '1px solid var(--border)', borderRadius: 8, marginBottom: 10, overflow: 'hidden' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '8px 12px', background: 'var(--panel)', fontSize: 13 }}>
            <FileDiff size={13} style={{ color: statusColor[f.status] || 'var(--text-dim)' }} />
            <b style={{ fontFamily: 'var(--mono)', fontSize: 12.5, wordBreak: 'break-all' }}>{f.path}</b>
            <span className="badge" style={{ fontSize: 10, color: statusColor[f.status] }}>{f.status}</span>
            <div style={{ flex: 1 }} />
            {f.binary
              ? <span style={{ fontSize: 11.5, color: 'var(--text-faint)' }}>binary / large</span>
              : <span style={{ fontSize: 11.5, fontFamily: 'var(--mono)' }}><span style={{ color: '#34d399' }}>+{f.add}</span> <span style={{ color: '#fb7185' }}>−{f.del}</span></span>}
          </div>
          {!f.binary && f.diff && (
            <pre style={{ margin: 0, padding: '8px 12px', fontSize: 12, fontFamily: 'var(--mono)', lineHeight: 1.55, overflowX: 'auto', background: 'var(--bg)' }}>
              {f.diff.split('\n').map((ln, i) => {
                const c = ln[0]
                const bg = c === '+' ? 'rgba(52,211,153,0.12)' : c === '-' ? 'rgba(251,113,133,0.12)' : 'transparent'
                const col = c === '+' ? '#7ee2b8' : c === '-' ? '#fca5b4' : c === '⋯' ? 'var(--text-faint)' : 'var(--text-dim)'
                return <div key={i} style={{ background: bg, color: col, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>{ln || ' '}</div>
              })}
            </pre>
          )}
        </div>
      ))}
    </div>
  )
}

function PullRequestsPage({ me, openProject, refresh }) {
  const [prs, setPrs] = useState(null)
  const [open, setOpen] = useState(null)   // {project, branch}
  const [detail, setDetail] = useState(null) // {pr, files}
  const [busy, setBusy] = useState('')
  const [toast, setToast] = useState('')

  const canMerge = !!(me && (me.is_owner || (me.caps || []).includes('merge')))

  const load = () => api('/api/prs').then((r) => setPrs(r.prs || [])).catch(() => setPrs([]))
  useEffect(() => { load() }, [])

  const openPR = async (pr) => {
    if (open && open.project === pr.project && open.branch === pr.branch) { setOpen(null); setDetail(null); return }
    setOpen({ project: pr.project, branch: pr.branch }); setDetail(null)
    try {
      const d = await api('/api/pr/diff?project=' + encodeURIComponent(pr.project) + '&branch=' + encodeURIComponent(pr.branch))
      setDetail(d)
    } catch (e) { setDetail({ pr, files: [], error: e.message }) }
  }

  const flash = (m) => { setToast(m); setTimeout(() => setToast(''), 3200) }

  const merge = async (pr) => {
    setBusy(pr.project + pr.branch)
    try {
      await api('/api/pr/merge', { method: 'POST', body: JSON.stringify({ project: pr.project, branch: pr.branch }) })
      flash('Merged ' + pr.branch + ' → main. Deploy it from its project page to go live.')
      setOpen(null); setDetail(null); load(); refresh && refresh()
    } catch (e) { flash(e.message || 'merge failed') }
    setBusy('')
  }
  const close = async (pr) => {
    setBusy(pr.project + pr.branch)
    try {
      await api('/api/pr/close', { method: 'POST', body: JSON.stringify({ project: pr.project, branch: pr.branch }) })
      setOpen(null); setDetail(null); load()
    } catch (e) { flash(e.message || 'close failed') }
    setBusy('')
  }

  const timeAgo = (t) => {
    const s = Math.floor((Date.now() - new Date(t).getTime()) / 1000)
    if (s < 60) return s + 's ago'; if (s < 3600) return Math.floor(s / 60) + 'm ago'
    if (s < 86400) return Math.floor(s / 3600) + 'h ago'; return Math.floor(s / 86400) + 'd ago'
  }

  return (
    <div className="card mt">
      <div className="card-title">PULL REQUESTS</div>
      <p style={{ fontSize: 13, color: 'var(--text-dim)', marginTop: 2, marginBottom: 14 }}>
        Members push their work as a branch. Review the diff, then merge — the branch replaces main (a backup is snapshotted first) and is staged for deploy. {canMerge ? '' : 'Only a lead can merge.'}
      </p>

      {prs === null && <div style={{ fontSize: 13, color: 'var(--text-faint)' }}>Loading…</div>}
      {prs && prs.length === 0 && <div className="empty">No open pull requests. When a teammate pushes to a branch, it shows up here.</div>}

      {prs && prs.map((pr) => {
        const isOpen = open && open.project === pr.project && open.branch === pr.branch
        return (
          <div key={pr.project + '/' + pr.branch} style={{ border: '1px solid var(--border)', borderRadius: 10, padding: '12px 14px', marginBottom: 10 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
              <GitPullRequest size={16} style={{ color: '#8b7cff' }} />
              <b style={{ fontSize: 14 }}>{pr.branch}</b>
              <span className="badge" style={{ fontSize: 10.5 }}>{pr.project}</span>
              <span style={{ fontSize: 12, color: 'var(--text-faint)' }}>by {pr.author} · {pr.files} files · {timeAgo(pr.updated)}</span>
              <div style={{ flex: 1 }} />
              <button className="btn" style={{ padding: '5px 10px', fontSize: 12.5 }} onClick={() => openPR(pr)}>{isOpen ? 'Hide diff' : 'Review'}</button>
              {canMerge && (
                <>
                  <button className="btn primary" style={{ padding: '5px 10px', fontSize: 12.5 }} disabled={busy === pr.project + pr.branch} onClick={() => merge(pr)}>
                    <GitMerge size={13} /> Merge
                  </button>
                  <button className="btn" style={{ padding: '5px 10px', fontSize: 12.5, color: '#fb7185' }} disabled={busy === pr.project + pr.branch} onClick={() => close(pr)}>Close</button>
                </>
              )}
            </div>
            {pr.message && <div style={{ fontSize: 12.5, color: 'var(--text-dim)', marginTop: 6 }}>{pr.message}</div>}
            {isOpen && (
              <div style={{ marginTop: 8 }}>
                {!detail && <div style={{ fontSize: 12.5, color: 'var(--text-faint)' }}>Loading diff…</div>}
                {detail && detail.error && <div style={{ fontSize: 12.5, color: '#fb7185' }}>{detail.error}</div>}
                {detail && !detail.error && <DiffView files={detail.files} />}
              </div>
            )}
          </div>
        )
      })}

      {toast && <div className="toast ok">{toast}</div>}
    </div>
  )
}

const NAV = [
  { id: 'home', icon: Home, label: 'Account home' },
  { id: 'traffic', icon: Activity, label: 'Web traffic' },
  { id: 'packets', icon: ArrowRightLeft, label: 'Packet capture' },
  { id: 'databases', icon: Database, label: 'Databases' },
  { id: 'workspaces', icon: Boxes, label: 'Workspaces' },
  { id: 'prs', icon: GitPullRequest, label: 'Pull requests' },
  { id: 'activity', icon: History, label: 'Activity' },
]

export default function App() {
  const [authed, setAuthed] = useState(false)
  const [checked, setChecked] = useState(false)
  const [me, setMe] = useState(null) // { email, role, caps, is_owner }
  const [page, setPage] = useState('home')
  const [activeProject, setActiveProject] = useState(null)
  const [perf, setPerf] = useState(null)
  const [traffic, setTraffic] = useState(null)
  const [projects, setProjects] = useState([])
  const [serverName, setServerName] = useState('')
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem('sx_side_collapsed') === '1')
  const [paletteOpen, setPaletteOpen] = useCommandPalette()
  const [prCount, setPrCount] = useState(0)
  const [recent, setRecent] = useState(() => {
    try { return JSON.parse(localStorage.getItem('sx_recent') || '[]') } catch { return [] }
  })

  useEffect(() => { localStorage.setItem('sx_side_collapsed', collapsed ? '1' : '0') }, [collapsed])

  // most-recently-opened projects, surfaced in the command palette
  const pushRecent = (name) => {
    setRecent((cur) => {
      const next = [name, ...cur.filter((n) => n !== name)].slice(0, 5)
      localStorage.setItem('sx_recent', JSON.stringify(next))
      return next
    })
  }

  const loadMe = async () => { try { const w = await api('/api/whoami'); if (w.authed) setMe(w) } catch { /* ignore */ } }

  // Manual login only — never auto-authenticate. If a credential is already
  // stored (from a prior login), re-validate it; otherwise show the login gate.
  useEffect(() => {
    (async () => {
      if (getToken() || getSSO()) {
        try { await api('/api/performance'); setAuthed(true); await loadMe() }
        catch { clearToken(); clearSSO() }
      }
      setChecked(true)
    })()
  }, [])

  const refresh = useCallback(async () => {
    const [p, t, pr] = await Promise.all([
      api('/api/performance'),
      api('/api/traffic'),
      api('/api/projects'),
    ])
    setPerf(p)
    setTraffic(t)
    setProjects(pr.projects || [])
    if (p.server) setServerName(p.server)
  }, [])

  usePoll(async () => { if (authed) await refresh() }, 5000, [authed])

  // open-PR badge in the sidebar
  usePoll(async () => {
    if (!authed) return
    try { const r = await api('/api/prs'); setPrCount((r.prs || []).length) } catch { /* ignore */ }
  }, 20000, [authed])

  // presence heartbeat — tells the team who's online + which project
  const [online, setOnline] = useState([])
  usePoll(async () => {
    if (!authed) return
    try {
      const r = await api('/api/presence', { method: 'POST', body: JSON.stringify({ project: page === 'project' ? activeProject : '' }) })
      setOnline(r.online || [])
    } catch { /* ignore */ }
  }, 15000, [authed, page, activeProject])

  if (!checked) return null
  if (!authed) return <Gate onOk={() => { setAuthed(true); loadMe() }} />

  const openProject = (name) => { setActiveProject(name); setPage('project'); pushRecent(name) }
  const goHome = () => { setActiveProject(null); setPage('home') }
  const goNav = (id) => { setPage(id); setActiveProject(null) }

  const crumb = page === 'project'
    ? activeProject
    : (NAV_GROUPS.flatMap((g) => g.items).find((n) => n.id === page)?.label || 'Overview')

  const counts = { projects: projects.length, prs: prCount }

  return (
    <div className={'dx' + (collapsed ? ' collapsed' : '')}>
      <div className="dx-shell">
        <Sidebar
          page={page} go={goNav} counts={counts} perf={perf} serverName={serverName}
          collapsed={collapsed} onToggle={() => setCollapsed((c) => !c)} me={me}
        />

        <div className="dx-main">
          <TopBar
            crumb={crumb}
            onSearch={() => setPaletteOpen(true)}
            right={
              <>
                {online.length > 0 && (
                  <div style={{ display: 'flex', alignItems: 'center' }}>
                    {online.slice(0, 5).map((o, i) => (
                      <span key={i} title={`${o.actor}${o.project ? ' · ' + o.project : ''} (${o.role})`}
                        style={{ width: 24, height: 24, borderRadius: '50%', background: 'linear-gradient(135deg,#8b7cff,#5a8bff)', color: '#fff', fontSize: 11, fontWeight: 700, display: 'grid', placeItems: 'center', marginLeft: i ? -7 : 0, border: '2px solid var(--dx-bg)' }}>
                        {(o.actor || '?')[0].toUpperCase()}
                      </span>
                    ))}
                    <span style={{ fontSize: 11.5, color: 'var(--dx-text2)', marginLeft: 8 }}>{online.length} online</span>
                  </div>
                )}
                <span className="badge" style={{ color: '#4ade80', borderColor: 'rgba(34,197,94,.28)' }}>
                  <span style={{ width: 6, height: 6, borderRadius: '50%', background: '#22c55e', boxShadow: '0 0 6px #22c55e' }} /> Live
                </span>
              </>
            }
          />

          <main className="dx-content">
            {page === 'home' && (
              <Overview perf={perf} traffic={traffic} projects={projects} openProject={openProject} />
            )}
            {page === 'projects' && (
              <ProjectsPage projects={projects} openProject={openProject} loading={!perf} />
            )}
            {page === 'traffic' && <TrafficPage traffic={traffic} />}
            {page === 'packets' && <PacketsPage />}
            {page === 'databases' && <DatabasesPage projects={projects} />}
            {page === 'workspaces' && <WorkspacesPage projects={projects} refresh={refresh} />}
            {page === 'prs' && <PullRequestsPage me={me} openProject={openProject} refresh={refresh} />}
            {page === 'activity' && <ActivityPage />}
            {page === 'team' && <WorkspacesPage projects={projects} refresh={refresh} />}
            {page === 'profile' && <ProfileCard me={me} serverName={serverName} />}
            {page === 'project' && (
              <ProjectPage key={activeProject} name={activeProject} projects={projects} refresh={refresh} goHome={goHome} me={me} />
            )}
          </main>
        </div>
      </div>

      <CommandPalette
        open={paletteOpen} onClose={() => setPaletteOpen(false)}
        projects={projects} go={goNav} openProject={openProject} recent={recent}
      />
    </div>
  )
}

/* Profile — pulls the user's real details from their Sixter account (name,
   company, job title, location, etc.) using their gateway cookie, so the repo
   dashboard shows the same identity as the main console. */
function ProfileCard({ me, serverName }) {
  const apex = window.location.hostname.split('.').slice(1).join('.') || 'sixter.in'
  const [gw, setGw] = useState(null)
  const [tried, setTried] = useState(false)

  useEffect(() => {
    fetch(`https://${apex}/api/me/profile`, { credentials: 'include' })
      .then((r) => (r.ok ? r.json() : null))
      .then((d) => setGw(d))
      .catch(() => setGw(null))
      .finally(() => setTried(true))
  }, [apex])

  const p = gw?.profile || {}
  const isCompany = p.account_type === 'company'
  const display = (p.name || '').trim() || (isCompany && p.company) || (me?.is_owner ? 'Server owner' : me?.email?.split('@')[0]) || 'Member'
  const email = gw?.account?.email || me?.email || '—'

  const Row = ({ k, v }) => (
    <div className="kv-row"><span>{k}</span><b style={{ wordBreak: 'break-word' }}>{v || '—'}</b></div>
  )

  return (
    <>
      <div className="dx-page-head">
        <div>
          <h1 className="dx-page-title">Profile</h1>
          <p className="dx-page-sub">Your Sixter identity, as it appears across the platform.</p>
        </div>
      </div>

      <div className="card" style={{ maxWidth: 620, padding: 0, overflow: 'hidden' }}>
        <div style={{ height: 84, background: 'linear-gradient(120deg, rgba(139,124,255,.35), rgba(90,139,255,.16) 60%, rgba(34,197,94,.12))' }} />
        <div style={{ padding: '0 24px 22px' }}>
          <div style={{ display: 'flex', alignItems: 'flex-end', gap: 16, marginTop: -34, flexWrap: 'wrap' }}>
            <span style={{ width: 72, height: 72, borderRadius: 20, display: 'grid', placeItems: 'center', fontSize: 28, fontWeight: 700, color: '#fff', background: 'linear-gradient(140deg,#8b7cff,#5a8bff)', border: '3px solid var(--dx-card)', flexShrink: 0 }}>
              {display[0].toUpperCase()}
            </span>
            <div style={{ minWidth: 0, paddingBottom: 4 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 9, flexWrap: 'wrap' }}>
                <b style={{ fontSize: 19, wordBreak: 'break-word' }}>{display}</b>
                <span className="badge">{isCompany ? 'Company' : 'Individual'}</span>
                {me?.is_owner && <span className="badge" style={{ color: '#a99bff' }}>Server owner</span>}
              </div>
              <div style={{ color: 'var(--dx-text2)', fontSize: 13, marginTop: 4 }}>
                {p.job_title ? p.job_title + ' · ' : ''}{me?.role || 'lead'} on {serverName || 'this server'}
              </div>
            </div>
          </div>

          {p.bio && <p style={{ fontSize: 13.5, color: 'var(--dx-text2)', lineHeight: 1.6, marginTop: 16 }}>{p.bio}</p>}

          <div style={{ marginTop: 18 }}>
            <Row k="Email" v={email} />
            {isCompany && <Row k="Company" v={p.company} />}
            <Row k="Role / title" v={p.job_title} />
            <Row k="Location" v={p.location} />
            <Row k="Website" v={p.website ? <a href={p.website} target="_blank" rel="noreferrer" style={{ color: 'var(--dx-primary)' }}>{p.website.replace(/^https?:\/\//, '')}</a> : '—'} />
            <Row k="This server role" v={me?.role || 'lead'} />
            <Row k="Workspaces" v={Object.keys(me?.workspaces || {}).length || '—'} />
          </div>

          {tried && !gw && (
            <div style={{ fontSize: 12, color: 'var(--dx-text3)', marginTop: 14, lineHeight: 1.6 }}>
              Sign in at {apex} in this browser to show your full profile here.
            </div>
          )}

          <a className="btn primary" style={{ marginTop: 18, width: '100%' }} href={`https://${apex}/dashboard?tab=profile`} target="_blank" rel="noreferrer">
            Edit profile on {apex}
          </a>
        </div>
      </div>
    </>
  )
}
