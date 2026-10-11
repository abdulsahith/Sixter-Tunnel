import { useState, useEffect, useRef, useCallback } from 'react'

/* ═══════════ icons (inline SVG, lucide-style) ═══════════ */

export const I = {
  zap: <path d="M13 2 3 14h7l-1 8 10-12h-7l1-8z" />,
  globe: <><circle cx="12" cy="12" r="10" /><path d="M2 12h20M12 2a15 15 0 0 1 0 20M12 2a15 15 0 0 0 0 20" /></>,
  shield: <path d="M12 22s8-3.5 8-10V5l-8-3-8 3v7c0 6.5 8 10 8 10z" />,
  activity: <path d="M22 12h-4l-3 9L9 3l-3 9H2" />,
  terminal: <><path d="m4 17 6-6-6-6" /><path d="M12 19h8" /></>,
  server: <><rect x="2" y="2" width="20" height="8" rx="2" /><rect x="2" y="14" width="20" height="8" rx="2" /><path d="M6 6h.01M6 18h.01" /></>,
  upload: <><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" /><path d="m17 8-5-5-5 5M12 3v12" /></>,
  lock: <><rect x="3" y="11" width="18" height="11" rx="2" /><path d="M7 11V7a5 5 0 0 1 10 0v4" /></>,
  check: <path d="M20 6 9 17l-5-5" />,
  arrow: <path d="M5 12h14m-7-7 7 7-7 7" />,
  external: <><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" /><path d="M15 3h6v6M10 14 21 3" /></>,
  key: <><circle cx="7.5" cy="15.5" r="5.5" /><path d="m21 2-9.6 9.6M15.5 7.5l3 3L22 7l-3-3" /></>,
  monitor: <><rect x="2" y="3" width="20" height="14" rx="2" /><path d="M8 21h8M12 17v4" /></>,
  windows: <path d="M3 5.5 10 4.4v7.1H3V5.5zm0 13 7 1.1v-7H3v5.9zM11 4.2 21 3v8.5H11V4.2zm0 15.6 10 1.2v-8.5H11v7.3z" />,
  linux: <><path d="M12 2a4 4 0 0 1 4 4v3c0 1 .6 2.2 1.5 3.5 1 1.5 2.5 3.4 2.5 5.5a3 3 0 0 1-3 3H7a3 3 0 0 1-3-3c0-2.1 1.4-4 2.5-5.5C7.4 11.2 8 10 8 9V6a4 4 0 0 1 4-4z" /><path d="M10 8h.01M14 8h.01" /></>,
  cpu: <><rect x="4" y="4" width="16" height="16" rx="2" /><rect x="9" y="9" width="6" height="6" /><path d="M9 2v2M15 2v2M9 20v2M15 20v2M2 9h2M2 15h2M20 9h2M20 15h2" /></>,
  wifi: <><path d="M5 13a10 10 0 0 1 14 0M8.5 16.5a5 5 0 0 1 7 0M2 9.5a15 15 0 0 1 20 0" /><circle cx="12" cy="20" r="1" /></>,
  box: <><path d="M21 8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z" /><path d="m3.3 7 8.7 5 8.7-5M12 22V12" /></>,
  shieldCheck: <><path d="M12 22s8-3.5 8-10V5l-8-3-8 3v7c0 6.5 8 10 8 10z" /><path d="m9 12 2 2 4-4" /></>,
  alert: <><path d="M10.3 3.3 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.3a2 2 0 0 0-3.4 0z" /><path d="M12 9v4M12 17h.01" /></>,
  ban: <><circle cx="12" cy="12" r="10" /><path d="m4.9 4.9 14.2 14.2" /></>,
  plus: <path d="M12 5v14M5 12h14" />,
  trash: <><path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" /></>,
  list: <><path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01" /></>,
  home: <><path d="m3 9 9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" /><path d="M9 22V12h6v10" /></>,
}

export function Ic({ n, size = 20 }) {
  return (
    <svg className="ic" style={{ width: size, height: size }} viewBox="0 0 24 24" fill="none"
         stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      {I[n]}
    </svg>
  )
}

/* ═══════════ router ═══════════ */

export function useRoute() {
  const [path, setPath] = useState(window.location.pathname)
  useEffect(() => {
    const onPop = () => setPath(window.location.pathname)
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])
  const nav = useCallback((to) => {
    window.history.pushState({}, '', to)
    setPath(to.split('?')[0].split('#')[0])
    if (!to.includes('#')) window.scrollTo(0, 0)
  }, [])
  return [path, nav]
}

/* ═══════════ api + helpers ═══════════ */

export async function api(path, opts = {}) {
  const res = await fetch(path, {
    ...opts,
    headers: { ...(opts.body ? { 'Content-Type': 'application/json' } : {}) },
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    const e = new Error(data.error || `HTTP ${res.status}`)
    e.status = res.status
    throw e
  }
  return data
}

export function fmtUptime(secs) {
  if (!secs) return '0s'
  if (secs < 60) return secs + 's'
  if (secs < 3600) return Math.floor(secs / 60) + 'm'
  if (secs < 86400) return Math.floor(secs / 3600) + 'h ' + Math.floor((secs % 3600) / 60) + 'm'
  return Math.floor(secs / 86400) + 'd ' + Math.floor((secs % 86400) / 3600) + 'h'
}

export function timeAgo(iso) {
  const s = Math.floor((Date.now() - new Date(iso).getTime()) / 1000)
  if (s < 60) return s + 's ago'
  if (s < 3600) return Math.floor(s / 60) + 'm ago'
  if (s < 86400) return Math.floor(s / 3600) + 'h ago'
  return Math.floor(s / 86400) + 'd ago'
}

export function Copy({ text }) {
  const [ok, setOk] = useState(false)
  return (
    <button className="copy-btn" type="button"
      onClick={() => navigator.clipboard.writeText(text).then(() => { setOk(true); setTimeout(() => setOk(false), 1200) })}>
      {ok ? 'copied' : 'copy'}
    </button>
  )
}

export function CmdRow({ cmd }) {
  return (
    <div className="cmd-row">
      <code>{cmd}</code>
      <Copy text={cmd} />
    </div>
  )
}

export function Reveal({ children, delay = 0 }) {
  const ref = useRef(null)
  const [vis, setVis] = useState(false)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const obs = new IntersectionObserver(([e]) => {
      if (e.isIntersecting) { setVis(true); obs.disconnect() }
    }, { threshold: 0.15 })
    obs.observe(el)
    return () => obs.disconnect()
  }, [])
  return (
    <div ref={ref} className={`reveal ${vis ? 'visible' : ''}`} style={{ transitionDelay: `${delay}ms` }}>
      {children}
    </div>
  )
}
