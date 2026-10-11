import { useState, useEffect, useMemo } from 'react'
import { motion } from 'framer-motion'
import {
  User, Building2, Check, Mail, Globe, MapPin, Briefcase, IdCard,
  ShieldCheck, ShieldAlert, Calendar, Wifi, Server, ExternalLink,
} from 'lucide-react'
import { api } from '../../shared.jsx'
import { PageHeader, Card, CardHead, Badge, fadeUp } from './widgets.jsx'

const EMPTY = {
  name: '', account_type: 'individual', company: '', job_title: '',
  website: '', location: '', bio: '',
}

const TYPES = [
  { v: 'individual', label: 'Individual', icon: User, desc: 'A personal account — freelancer, student or solo developer.' },
  { v: 'company', label: 'Company', icon: Building2, desc: 'A business or team account with company details on record.' },
]

function Field({ label, hint, icon: Icon, children }) {
  return (
    <label style={{ display: 'block' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12.5, fontWeight: 600, color: 'var(--dx-text2)', marginBottom: 7 }}>
        {Icon && <Icon size={13} />} {label}
      </div>
      {children}
      {hint && <div style={{ fontSize: 11.5, color: 'var(--dx-text3)', marginTop: 6 }}>{hint}</div>}
    </label>
  )
}

function Row({ icon: Icon, k, v, action }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '12px 2px', borderTop: '1px solid var(--dx-border)' }}>
      <span style={{ width: 32, height: 32, borderRadius: 9, display: 'grid', placeItems: 'center', background: 'var(--dx-card-2)', border: '1px solid var(--dx-border)', color: 'var(--dx-text3)', flexShrink: 0 }}>
        <Icon size={15} />
      </span>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 12, color: 'var(--dx-text3)' }}>{k}</div>
        <div style={{ fontSize: 13.5, fontWeight: 600, wordBreak: 'break-all' }}>{v}</div>
      </div>
      {action}
    </div>
  )
}

export default function ProfilePage({ me, setTab }) {
  const [form, setForm] = useState(null)
  const [account, setAccount] = useState(null)
  const [busy, setBusy] = useState(false)
  const [saved, setSaved] = useState(false)
  const [err, setErr] = useState('')
  const [dirty, setDirty] = useState(false)

  useEffect(() => {
    api('/api/me/profile')
      .then((r) => {
        setForm({ ...EMPTY, ...(r.profile || {}) })
        setAccount(r.account || null)
      })
      .catch(() => setForm({ ...EMPTY }))
  }, [])

  const set = (k) => (e) => { setForm((f) => ({ ...f, [k]: e.target.value })); setDirty(true); setErr('') }
  const setType = (v) => { setForm((f) => ({ ...f, account_type: v })); setDirty(true); setErr('') }

  const isCompany = form?.account_type === 'company'
  const displayName = form?.name?.trim() || (isCompany && form?.company?.trim()) || me.email.split('@')[0]
  const initial = (displayName || me.email)[0].toUpperCase()

  // completion is honest: percentage of the fields that apply to this account type
  const completion = useMemo(() => {
    if (!form) return 0
    const keys = isCompany
      ? ['name', 'company', 'job_title', 'website', 'location', 'bio']
      : ['name', 'job_title', 'website', 'location', 'bio']
    const done = keys.filter((k) => String(form[k] || '').trim()).length
    return Math.round((done / keys.length) * 100)
  }, [form, isCompany])

  const save = async () => {
    setErr(''); setBusy(true)
    try {
      const r = await api('/api/me/profile', { method: 'POST', body: JSON.stringify(form) })
      if (r.profile) setForm({ ...EMPTY, ...r.profile })
      setDirty(false)
      setSaved(true); setTimeout(() => setSaved(false), 1800)
    } catch (e) { setErr(e.message || 'could not save profile') }
    setBusy(false)
  }

  if (!form) {
    return (
      <>
        <PageHeader title="Profile" sub="Your account details." />
        <Card className="dx-card-pad"><div className="dx-empty">Loading your profile…</div></Card>
      </>
    )
  }

  const memberSince = account?.created_at
    ? new Date(account.created_at).toLocaleDateString(undefined, { month: 'long', day: 'numeric', year: 'numeric' })
    : '—'

  return (
    <>
      <PageHeader
        title="Profile"
        sub="How you and your organisation appear across Sixter — used on invites, workspaces and support."
        actions={saved ? <Badge tone="green"><Check size={13} /> Saved</Badge> : dirty ? <Badge tone="amber">Unsaved changes</Badge> : null}
      />

      {/* identity banner */}
      <motion.div {...fadeUp(0)}>
        <Card style={{ padding: 0, overflow: 'hidden', marginBottom: 18 }}>
          <div style={{ height: 96, background: 'linear-gradient(120deg, rgba(139,124,255,0.35), rgba(90,139,255,0.18) 55%, rgba(34,197,94,0.14))' }} />
          <div style={{ display: 'flex', alignItems: 'flex-end', gap: 18, padding: '0 24px 22px', marginTop: -38, flexWrap: 'wrap' }}>
            <span style={{
              width: 84, height: 84, borderRadius: 22, display: 'grid', placeItems: 'center',
              fontSize: 32, fontWeight: 700, color: '#fff', flexShrink: 0,
              background: 'linear-gradient(140deg, #8b7cff, #5a8bff)',
              border: '3px solid var(--dx-card)', boxShadow: '0 10px 30px rgba(0,0,0,0.35)',
            }}>{initial}</span>
            <div style={{ flex: '1 1 260px', minWidth: 0, paddingBottom: 4 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
                <h2 style={{ fontSize: 21, fontWeight: 700, margin: 0, wordBreak: 'break-word' }}>{displayName}</h2>
                <Badge tone={isCompany ? 'blue' : 'violet'} small>{isCompany ? 'Company' : 'Individual'}</Badge>
                {account?.is_admin && <Badge tone="amber" small>Admin</Badge>}
              </div>
              <div style={{ fontSize: 13, color: 'var(--dx-text2)', marginTop: 5, display: 'flex', gap: 14, flexWrap: 'wrap' }}>
                <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5 }}><Mail size={13} /> {me.email}</span>
                {form.job_title && <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5 }}><Briefcase size={13} /> {form.job_title}</span>}
                {form.location && <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5 }}><MapPin size={13} /> {form.location}</span>}
                {form.website && (
                  <a href={form.website} target="_blank" rel="noreferrer" style={{ display: 'inline-flex', alignItems: 'center', gap: 5, color: 'var(--dx-primary)' }}>
                    <Globe size={13} /> {form.website.replace(/^https?:\/\//, '')} <ExternalLink size={11} />
                  </a>
                )}
              </div>
            </div>
            <div style={{ width: 168, paddingBottom: 6 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12, color: 'var(--dx-text3)', marginBottom: 6 }}>
                <span>Profile complete</span><b style={{ color: 'var(--dx-text2)' }}>{completion}%</b>
              </div>
              <div className="dx-bar"><span style={{ width: `${completion}%` }} /></div>
            </div>
          </div>
        </Card>
      </motion.div>

      <div className="dx-row c2-wide">
        {/* editable details */}
        <motion.div {...fadeUp(1)}>
          <Card className="dx-card-pad">
            <CardHead icon={IdCard} title="Your details" />

            {/* account type */}
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 20 }}>
              {TYPES.map((t) => {
                const on = form.account_type === t.v
                return (
                  <button
                    key={t.v}
                    onClick={() => setType(t.v)}
                    style={{
                      textAlign: 'left', padding: 14, borderRadius: 13, cursor: 'pointer',
                      background: on ? 'rgba(139,124,255,0.10)' : 'var(--dx-card-2)',
                      border: `1px solid ${on ? 'rgba(139,124,255,0.5)' : 'var(--dx-border)'}`,
                      color: 'var(--dx-text)', transition: 'all 0.16s', fontFamily: 'inherit',
                    }}
                  >
                    <div style={{ display: 'flex', alignItems: 'center', gap: 9, marginBottom: 6 }}>
                      <t.icon size={17} style={{ color: on ? 'var(--dx-primary)' : 'var(--dx-text3)' }} />
                      <b style={{ fontSize: 14 }}>{t.label}</b>
                      {on && <Check size={15} style={{ marginLeft: 'auto', color: 'var(--dx-primary)' }} />}
                    </div>
                    <div style={{ fontSize: 12, color: 'var(--dx-text2)', lineHeight: 1.5 }}>{t.desc}</div>
                  </button>
                )
              })}
            </div>

            <div style={{ display: 'grid', gap: 16 }}>
              <Field label="Full name" icon={User}>
                <input className="dx-input" style={{ width: '100%' }} value={form.name} onChange={set('name')} placeholder="Ada Lovelace" maxLength={120} />
              </Field>

              {isCompany && (
                <Field label="Company name" icon={Building2} hint="Required for company accounts — shown on workspace invites.">
                  <input className="dx-input" style={{ width: '100%' }} value={form.company} onChange={set('company')} placeholder="Acme Technologies Pvt Ltd" maxLength={120} />
                </Field>
              )}

              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
                <Field label={isCompany ? 'Your role' : 'Job title'} icon={Briefcase}>
                  <input className="dx-input" style={{ width: '100%' }} value={form.job_title} onChange={set('job_title')} placeholder={isCompany ? 'CTO' : 'Full-stack developer'} maxLength={120} />
                </Field>
                <Field label="Location" icon={MapPin}>
                  <input className="dx-input" style={{ width: '100%' }} value={form.location} onChange={set('location')} placeholder="Hyderabad, India" maxLength={120} />
                </Field>
              </div>

              <Field label={isCompany ? 'Company website' : 'Website'} icon={Globe} hint="https:// is added automatically if you leave it out.">
                <input className="dx-input" style={{ width: '100%' }} value={form.website} onChange={set('website')} placeholder="acme.com" maxLength={200} />
              </Field>

              <Field label={isCompany ? 'About the company' : 'About you'} hint={`${(form.bio || '').length}/1000 characters`}>
                <textarea
                  className="dx-input" rows={4} maxLength={1000} value={form.bio} onChange={set('bio')}
                  placeholder={isCompany ? 'What your company builds, who it serves, where it operates.' : 'What you build and what you use Sixter for.'}
                  style={{ width: '100%', resize: 'vertical', lineHeight: 1.6, fontFamily: 'inherit' }}
                />
              </Field>
            </div>

            {err && <div style={{ marginTop: 14, fontSize: 13, color: '#fb7185' }}>{err}</div>}

            <div style={{ display: 'flex', gap: 10, alignItems: 'center', marginTop: 20 }}>
              <button className="dx-btn dx-btn-primary" onClick={save} disabled={busy || !dirty}>
                {busy ? 'Saving…' : 'Save changes'}
              </button>
              <span style={{ fontSize: 12.5, color: 'var(--dx-text3)' }}>
                {form.updated_at && !dirty ? `Last updated ${new Date(form.updated_at).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })}` : 'Only you and your workspace members can see these details.'}
              </span>
            </div>
          </Card>
        </motion.div>

        {/* read-only account facts */}
        <motion.div {...fadeUp(2)}>
          <Card className="dx-card-pad" style={{ height: '100%' }}>
            <CardHead icon={ShieldCheck} title="Account" />
            <Row icon={Mail} k="Email" v={me.email} />
            <Row icon={Calendar} k="Member since" v={memberSince} />
            <Row
              icon={Wifi} k="Tunnel subdomain"
              v={<a href={`https://${me.tunnel_subdomain}.sixter.in`} target="_blank" rel="noreferrer" style={{ color: 'var(--dx-primary)' }}>{me.tunnel_subdomain}.sixter.in</a>}
            />
            <Row
              icon={Server} k="Repo subdomain"
              v={<a href={`https://${me.repo_subdomain}.sixter.in`} target="_blank" rel="noreferrer" style={{ color: 'var(--dx-primary)' }}>{me.repo_subdomain}.sixter.in</a>}
            />
            <Row
              icon={account?.totp_enabled ? ShieldCheck : ShieldAlert}
              k="Two-factor authentication"
              v={account?.totp_enabled ? 'Enabled' : 'Not enabled'}
              action={<Badge tone={account?.totp_enabled ? 'green' : 'amber'} small>{account?.totp_enabled ? 'SECURE' : 'OFF'}</Badge>}
            />
            <Row
              icon={IdCard} k="CLI token"
              v={account?.token_issued ? 'Issued' : 'Not issued yet'}
              action={setTab ? <button className="dx-btn dx-btn-ghost dx-btn-sm" onClick={() => setTab('setup')}>Manage</button> : null}
            />

            <div style={{ fontSize: 12.5, color: 'var(--dx-text3)', marginTop: 16, lineHeight: 1.6 }}>
              Subdomains are allocated when your account is created and stay reserved for you.
              Email and subdomains can't be changed here — contact support if you need them moved.
            </div>
          </Card>
        </motion.div>
      </div>
    </>
  )
}
