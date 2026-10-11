import { useState } from 'react'
import { motion } from 'framer-motion'
import { Check, Download, BookOpen, Terminal, Server, Wifi, Monitor, ArrowUpRight, Cpu } from 'lucide-react'
import { api } from '../../shared.jsx'
import { PageHeader, Card, CardHead, CommandBox, Badge, fadeUp } from './widgets.jsx'

export default function GetStartedPage({ me, tunnelOn, repoOn, nav }) {
  const [code, setCode] = useState('')
  const [msg, setMsg] = useState(null)

  const approve = async (e) => {
    e.preventDefault(); setMsg(null)
    try {
      await api('/api/device/approve', { method: 'POST', body: JSON.stringify({ code }) })
      setMsg({ ok: true, text: 'Device approved — the CLI finishes automatically.' }); setCode('')
    } catch (e2) { setMsg({ ok: false, text: e2.message }) }
  }

  const steps = [
    {
      done: !!me.token_issued, icon: Terminal, title: 'Connect your CLI',
      desc: 'Install Sixter, then authenticate this account on your machine.',
      cmd: 'sixter login', cmdTitle: 'authenticate',
    },
    {
      done: tunnelOn, icon: Wifi, title: 'Start your first tunnel',
      desc: `Any local app instantly becomes ${me.tunnel_subdomain}.sixter.in — WebSockets included.`,
      cmd: 'sixter tunnel -port 3000', cmdTitle: 'tunnel',
    },
    {
      done: repoOn, icon: Server, title: 'Run the repo server on your VPS',
      desc: 'Then push projects to it from anywhere and manage them from this dashboard.',
      cmd: `sixter repo-server -token "<secret>" -server "${me.repo_subdomain}" -certbot-email ${me.email}`, cmdTitle: 'repo server',
    },
  ]
  const completed = steps.filter((s) => s.done).length
  const pct = Math.round((completed / steps.length) * 100)

  return (
    <>
      <PageHeader
        title="Get started"
        sub="Three commands from zero to a public, protected URL."
        actions={<Badge tone={pct === 100 ? 'green' : 'violet'}>{completed}/{steps.length} complete</Badge>}
      />

      {/* progress */}
      <motion.div {...fadeUp(0)} className="dx-card dx-card-pad" style={{ marginBottom: 24 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 10, fontSize: 13.5 }}>
          <b>Setup progress</b><span style={{ color: 'var(--dx-text2)' }}>{pct}%</span>
        </div>
        <div className="dx-bar" style={{ height: 8 }}>
          <motion.span initial={{ width: 0 }} animate={{ width: `${pct}%` }} transition={{ duration: 0.8, ease: 'easeOut' }} style={{ display: 'block', height: '100%', borderRadius: 99, background: 'linear-gradient(90deg,#8b7cff,#5a8bff)' }} />
        </div>
      </motion.div>

      <div className="dx-row c2-wide">
        {/* vertical timeline */}
        <motion.div {...fadeUp(1)}>
          <Card className="dx-card-pad">
            <CardHead icon={Terminal} title="Installation steps" />
            <div className="dx-timeline">
              {steps.map((s, i) => (
                <div className="dx-tl-item" key={i} style={{ paddingBottom: 26 }}>
                  <span className="dx-tl-ico" style={{ background: s.done ? 'rgba(34,197,94,0.14)' : 'var(--dx-card-2)', borderColor: s.done ? 'rgba(34,197,94,0.4)' : 'var(--dx-border)', color: s.done ? '#22c55e' : 'var(--dx-text2)' }}>
                    {s.done ? <Check size={16} /> : <s.icon size={16} />}
                  </span>
                  <div className="dx-tl-body" style={{ flex: 1 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 9 }}>
                      <b style={{ fontSize: 14.5 }}>{s.title}</b>
                      {s.done && <Badge tone="green" small>Done</Badge>}
                    </div>
                    <span style={{ display: 'block', margin: '4px 0 12px' }}>{s.desc}</span>
                    <CommandBox cmd={s.cmd} title={s.cmdTitle} />
                  </div>
                </div>
              ))}
            </div>
          </Card>
        </motion.div>

        {/* requirements + quick links + device */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <motion.div {...fadeUp(2)}>
            <Card className="dx-card-pad">
              <CardHead icon={Cpu} title="Requirements" />
              {[['Any OS', 'Windows, macOS or Linux'], ['A terminal', 'No daemon or config files'], ['A VPS (optional)', 'Only for project deployments']].map(([a, b]) => (
                <div key={a} style={{ display: 'flex', gap: 11, padding: '9px 0', alignItems: 'flex-start' }}>
                  <Check size={16} style={{ color: '#22c55e', marginTop: 2, flexShrink: 0 }} />
                  <div><div style={{ fontSize: 13.5, fontWeight: 500 }}>{a}</div><div style={{ fontSize: 12.5, color: 'var(--dx-text2)' }}>{b}</div></div>
                </div>
              ))}
            </Card>
          </motion.div>

          <motion.div {...fadeUp(3)}>
            <Card className="dx-card-pad">
              <CardHead icon={ArrowUpRight} title="Quick links" />
              <div style={{ display: 'flex', flexDirection: 'column', gap: 9 }}>
                <a href="/sixter-setup.exe" download><button className="dx-btn dx-btn-ghost" style={{ width: '100%', justifyContent: 'flex-start' }}><Download size={15} /> Download for Windows (.exe)</button></a>
                <a href="/sixter_2.0.0_amd64.deb" download><button className="dx-btn dx-btn-ghost" style={{ width: '100%', justifyContent: 'flex-start' }}><Download size={15} /> Download for Linux (.deb)</button></a>
                <button className="dx-btn dx-btn-ghost" style={{ width: '100%', justifyContent: 'flex-start' }} onClick={() => nav('/docs')}><BookOpen size={15} /> Read the documentation</button>
              </div>
            </Card>
          </motion.div>

          <motion.div {...fadeUp(4)}>
            <Card className="dx-card-pad">
              <CardHead icon={Monitor} title="Connect a device" />
              <p style={{ fontSize: 13, color: 'var(--dx-text2)', marginBottom: 14, lineHeight: 1.6 }}>
                Ran <span className="dx-mono" style={{ color: 'var(--dx-primary)' }}>sixter login</span> on an SSH-only server? Enter the code it printed:
              </p>
              <form onSubmit={approve} style={{ display: 'flex', gap: 10, flexWrap: 'wrap' }}>
                <input className="dx-input mono" style={{ flex: 1, minWidth: 150, fontSize: 16, letterSpacing: '0.08em' }} placeholder="XXXX-XXXX" maxLength={9} value={code} onChange={(e) => setCode(e.target.value)} />
                <button className="dx-btn dx-btn-primary" disabled={!code.trim()}>Approve</button>
              </form>
              {msg && (
                <div style={{ marginTop: 12, padding: '10px 13px', borderRadius: 10, fontSize: 13, background: msg.ok ? 'rgba(34,197,94,0.1)' : 'rgba(239,68,68,0.1)', border: `1px solid ${msg.ok ? 'rgba(34,197,94,0.3)' : 'rgba(239,68,68,0.3)'}`, color: msg.ok ? '#4ade80' : '#f87171' }}>
                  {msg.text}
                </div>
              )}
            </Card>
          </motion.div>
        </div>
      </div>
    </>
  )
}
