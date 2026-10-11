import { useState, useEffect } from 'react'
import { api } from '../shared.jsx'

/*
Read-only profile of a teammate, opened from the Workspaces page.
Only visible to a workspace owner viewing someone in one of their workspaces —
the gateway enforces that; this page just renders what it's allowed to see.
*/

const Row = ({ k, v }) => (
  <div style={{ display: 'flex', gap: 14, padding: '11px 0', borderTop: '1px solid var(--border)' }}>
    <div style={{ width: 150, flexShrink: 0, fontSize: 13, color: 'var(--dim)' }}>{k}</div>
    <div style={{ fontSize: 13.5, wordBreak: 'break-word' }}>{v || '—'}</div>
  </div>
)

export default function MemberProfile({ nav }) {
  const email = new URLSearchParams(window.location.search).get('email') || ''
  const [data, setData] = useState(null)
  const [err, setErr] = useState('')

  useEffect(() => {
    if (!email) { setErr('No member specified.'); return }
    api('/api/member/profile?email=' + encodeURIComponent(email))
      .then(setData)
      .catch((e) => setErr(e.message || 'could not load this profile'))
  }, [email])

  if (err) {
    return (
      <div className="auth-wrap">
        <div className="auth-card">
          <h1>Profile unavailable</h1>
          <p className="sub">{err}</p>
          <button className="btn mt" style={{ width: '100%' }} onClick={() => nav('/dashboard')}>Back to console</button>
        </div>
      </div>
    )
  }
  if (!data) {
    return <div className="docs"><div className="docs-body"><p className="lead">Loading…</p></div></div>
  }

  const p = data.profile || {}
  const isCompany = p.account_type === 'company'
  const display = (p.name || '').trim() || (isCompany && p.company) || data.email.split('@')[0]
  const joined = data.created_at
    ? new Date(data.created_at).toLocaleDateString(undefined, { month: 'long', day: 'numeric', year: 'numeric' })
    : '—'

  return (
    <div className="docs">
      <div className="docs-body" style={{ maxWidth: 720 }}>
        <button className="linkbtn" onClick={() => nav('/dashboard')} style={{ marginBottom: 18 }}>← Back to console</button>

        <div style={{ display: 'flex', alignItems: 'center', gap: 18, flexWrap: 'wrap', marginBottom: 8 }}>
          <span style={{
            width: 76, height: 76, borderRadius: 20, display: 'grid', placeItems: 'center',
            fontSize: 30, fontWeight: 700, color: '#fff',
            background: 'linear-gradient(140deg,#8b7cff,#5a8bff)',
          }}>{display[0].toUpperCase()}</span>
          <div style={{ minWidth: 0 }}>
            <h1 style={{ marginBottom: 4, wordBreak: 'break-word' }}>{display}</h1>
            <div style={{ color: 'var(--dim)', fontSize: 14 }}>
              {p.job_title ? p.job_title + ' · ' : ''}{isCompany ? 'Company account' : 'Individual'}
            </div>
          </div>
        </div>

        {p.bio && <p className="lead" style={{ marginTop: 14 }}>{p.bio}</p>}

        <div style={{ marginTop: 22 }}>
          <Row k="Email" v={<a href={'mailto:' + data.email} style={{ color: 'var(--accent-2)' }}>{data.email}</a>} />
          {isCompany && <Row k="Company" v={p.company} />}
          <Row k="Role / title" v={p.job_title} />
          <Row k="Location" v={p.location} />
          <Row
            k="Website"
            v={p.website ? <a href={p.website} target="_blank" rel="noreferrer" style={{ color: 'var(--accent-2)' }}>{p.website.replace(/^https?:\/\//, '')}</a> : ''}
          />
          <Row k="Member since" v={joined} />
          <Row
            k="Tunnel"
            v={data.tunnel_subdomain
              ? <a href={`https://${data.tunnel_subdomain}.sixter.in`} target="_blank" rel="noreferrer" style={{ color: 'var(--accent-2)' }}>{data.tunnel_subdomain}.sixter.in</a>
              : 'not allocated yet'}
          />
          {data.is_self && <Row k="Two-factor" v={data.totp_enabled ? 'Enabled' : 'Not enabled'} />}
        </div>

        <p style={{ fontSize: 12.5, color: 'var(--dim)', marginTop: 20 }}>
          These details come from what {display} filled in on their own profile page.
        </p>
      </div>
    </div>
  )
}
