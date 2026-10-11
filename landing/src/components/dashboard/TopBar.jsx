import { useState, useEffect, useRef } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import { Search, Bell, BookOpen, TerminalSquare, Menu, ChevronDown, User, Settings, LogOut, ExternalLink } from 'lucide-react'

function Pop({ children, onClose }) {
  const ref = useRef(null)
  useEffect(() => {
    const h = (e) => { if (ref.current && !ref.current.contains(e.target)) onClose() }
    document.addEventListener('mousedown', h)
    return () => document.removeEventListener('mousedown', h)
  }, [onClose])
  return (
    <motion.div ref={ref} className="dx-pop" initial={{ opacity: 0, y: -6, scale: 0.97 }} animate={{ opacity: 1, y: 0, scale: 1 }} exit={{ opacity: 0, y: -6, scale: 0.97 }} transition={{ duration: 0.15 }}>
      {children}
    </motion.div>
  )
}

export default function TopBar({ me, nav, onLogout, onMenu, repoUrl, status, security, setTab }) {
  const [menu, setMenu] = useState(null) // 'notif' | 'profile' | null

  // build real notifications from the data the dashboard already polls
  const t = security?.tunnel, r = security?.repo
  const blocked = (t?.blocked || 0) + (r?.blocked || 0)
  const banned = (t?.banned_ips || 0) + (r?.banned_ips || 0)
  const notifs = []
  if (blocked > 0) notifs.push(['Firewall blocked ' + blocked.toLocaleString() + ' request' + (blocked === 1 ? '' : 's'), 'edge'])
  if (banned > 0) notifs.push([banned + ' IP' + (banned === 1 ? '' : 's') + ' currently banned', 'edge'])
  if (status?.tunnel?.connected) notifs.push(['Tunnel is live — ' + me.tunnel_subdomain + '.sixter.in', 'now'])
  if (status?.repo?.connected) notifs.push(['Repo server connected', 'now'])
  if (me?.totp_enabled) notifs.push(['Two-factor authentication is on', 'secured'])
  const hasAlert = blocked > 0 || banned > 0

  return (
    <div className="dx-top">
      <button className="dx-icon-btn dx-hamb" onClick={onMenu} aria-label="Open menu"><Menu size={18} /></button>

      <div className="dx-search">
        <Search size={16} />
        <input placeholder="Search projects, domains, logs…" aria-label="Search" />
        <kbd>⌘K</kbd>
      </div>

      <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 10 }}>
        <button className="dx-top-btn dx-hide-sm" onClick={() => nav('/docs')}><BookOpen size={15} /> Docs</button>
        {repoUrl
          ? <a href={repoUrl} target="_blank" rel="noreferrer"><button className="dx-top-btn dx-hide-sm"><TerminalSquare size={15} /> Console <ExternalLink size={13} /></button></a>
          : <button className="dx-top-btn dx-hide-sm" onClick={() => nav('/')}><TerminalSquare size={15} /> Console</button>}

        <div style={{ position: 'relative' }}>
          <button className="dx-icon-btn" aria-label="Notifications" onClick={() => setMenu(menu === 'notif' ? null : 'notif')}>
            <Bell size={17} />{hasAlert && <span className="dx-dot-badge" />}
          </button>
          <AnimatePresence>
            {menu === 'notif' && (
              <Pop onClose={() => setMenu(null)}>
                <div className="dx-pop-head" style={{ fontWeight: 600, fontSize: 13.5 }}>Notifications</div>
                <div className="dx-pop-sep" />
                {notifs.length === 0 ? (
                  <div className="dx-pop-item" style={{ color: 'var(--dx-text2)', fontSize: 13 }}>You're all caught up.</div>
                ) : notifs.map(([msg, w]) => (
                  <div key={msg} className="dx-pop-item" style={{ flexDirection: 'column', alignItems: 'flex-start', gap: 2 }}>
                    <span style={{ color: 'var(--dx-text)', fontSize: 13 }}>{msg}</span>
                    <span style={{ fontSize: 11.5, color: 'var(--dx-text3)' }}>{w}</span>
                  </div>
                ))}
              </Pop>
            )}
          </AnimatePresence>
        </div>

        <div style={{ position: 'relative' }}>
          <button className="dx-top-btn" onClick={() => setMenu(menu === 'profile' ? null : 'profile')} style={{ paddingLeft: 6 }}>
            <span style={{ width: 26, height: 26, borderRadius: 7, display: 'grid', placeItems: 'center', fontWeight: 700, fontSize: 12, color: '#fff', background: 'linear-gradient(135deg,#8b7cff,#5a8bff)' }}>{me.email[0].toUpperCase()}</span>
            <ChevronDown size={14} />
          </button>
          <AnimatePresence>
            {menu === 'profile' && (
              <Pop onClose={() => setMenu(null)}>
                <div className="dx-pop-head">
                  <div style={{ fontWeight: 600, fontSize: 13.5 }}>{me.email.split('@')[0]}</div>
                  <div style={{ fontSize: 12, color: 'var(--dx-text2)' }}>{me.email}</div>
                </div>
                <div className="dx-pop-sep" />
                <button className="dx-pop-item" onClick={() => { setMenu(null); setTab && setTab('overview') }}><User size={15} /> Account</button>
                <button className="dx-pop-item" onClick={() => { setMenu(null); setTab && setTab('setup') }}><Settings size={15} /> Settings</button>
                <div className="dx-pop-sep" />
                <button className="dx-pop-item" onClick={onLogout} style={{ color: '#f87171' }}><LogOut size={15} /> Log out</button>
              </Pop>
            )}
          </AnimatePresence>
        </div>
      </div>
    </div>
  )
}
