import { useState } from 'react'
import { Menu, X } from 'lucide-react'
import { Button } from './ui.jsx'

/* Premium sticky nav for the landing page. */
export default function Nav({ nav, me, onLogout }) {
  const [open, setOpen] = useState(false)
  const goSection = (id) => {
    setOpen(false)
    if (window.location.pathname !== '/') nav('/')
    setTimeout(() => document.getElementById(id)?.scrollIntoView({ behavior: 'smooth' }), 60)
  }
  return (
    <nav className="sx-nav">
      <div className="sx-wrap sx-nav-inner">
        <button className="flex items-center" onClick={() => nav('/')} style={{ background: 'none', border: 0, cursor: 'pointer' }}>
          <img src="/sixter-logo.png" alt="Sixter" style={{ height: 28 }} />
        </button>

        <div className="hidden md:flex" style={{ gap: 26, marginLeft: 8 }}>
          <button className="sx-nav-link" onClick={() => goSection('features')}>Features</button>
          <button className="sx-nav-link" onClick={() => goSection('how')}>How it works</button>
          <button className="sx-nav-link" onClick={() => goSection('cli')}>Download</button>
          <button className="sx-nav-link" onClick={() => nav('/docs')}>Docs</button>
        </div>

        <div className="hidden md:flex" style={{ marginLeft: 'auto', gap: 12, alignItems: 'center' }}>
          {me ? (
            <>
              <button className="sx-nav-link" onClick={() => nav('/dashboard')}>Console</button>
              <Button variant="ghost" onClick={onLogout}>Log out</Button>
            </>
          ) : (
            <>
              <button className="sx-nav-link" onClick={() => nav('/login')}>Log in</button>
              <Button onClick={() => nav('/signup')}>Get started</Button>
            </>
          )}
        </div>

        {/* mobile toggle */}
        <button
          className="md:hidden"
          onClick={() => setOpen(!open)}
          style={{ marginLeft: 'auto', background: 'none', border: 0, color: '#fff', cursor: 'pointer' }}
          aria-label="Menu"
        >
          {open ? <X size={24} /> : <Menu size={24} />}
        </button>
      </div>

      {/* mobile menu */}
      {open && (
        <div className="md:hidden" style={{ borderTop: '1px solid rgba(255,255,255,0.06)', padding: '18px 24px', display: 'flex', flexDirection: 'column', gap: 16, background: 'rgba(5,8,22,0.96)' }}>
          <button className="sx-nav-link" style={{ textAlign: 'left' }} onClick={() => goSection('features')}>Features</button>
          <button className="sx-nav-link" style={{ textAlign: 'left' }} onClick={() => goSection('how')}>How it works</button>
          <button className="sx-nav-link" style={{ textAlign: 'left' }} onClick={() => goSection('cli')}>Download</button>
          <button className="sx-nav-link" style={{ textAlign: 'left' }} onClick={() => { setOpen(false); nav('/docs') }}>Docs</button>
          <div style={{ height: 1, background: 'rgba(255,255,255,0.06)' }} />
          {me ? (
            <>
              <Button variant="ghost" onClick={() => { setOpen(false); nav('/dashboard') }}>Console</Button>
              <Button onClick={onLogout}>Log out</Button>
            </>
          ) : (
            <>
              <Button variant="ghost" onClick={() => { setOpen(false); nav('/login') }}>Log in</Button>
              <Button onClick={() => { setOpen(false); nav('/signup') }}>Get started</Button>
            </>
          )}
        </div>
      )}
    </nav>
  )
}
