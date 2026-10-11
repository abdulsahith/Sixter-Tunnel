import { useState } from 'react'
import { motion } from 'framer-motion'
import { Copy, Check, Download, Monitor, Terminal, ShieldCheck } from 'lucide-react'
import { Reveal, SectionHeading } from '../common/ui.jsx'

// Real release artifacts served from the platform's static root.
const WIN_EXE = '/sixter-setup.exe'
const LINUX_DEB = '/sixter_2.0.0_amd64.deb'
const LINUX_CMD = 'curl -fsSL https://sixter.in/sixter_2.0.0_amd64.deb -o sixter.deb && sudo dpkg -i sixter.deb'

const SESSION = [
  { p: '❯', c: 'sixter login' },
  { o: '  ✓ Logged in as you@example.com' },
  { o: '  ✓ tunnel : https://you.sixter.in' },
  { p: '❯', c: 'sixter tunnel -port 3000' },
  { hi: 'Tunnel live → https://you.sixter.in' },
  { o: 'Forwarding all traffic → http://127.0.0.1:3000' },
]

function DownloadCard({ icon: Icon, platform, meta, label, href, i }) {
  return (
    <Reveal delay={i * 0.08}>
      <motion.a
        href={href}
        download
        className="sx-glass sx-glass-hover"
        whileHover={{ y: -4 }}
        style={{ display: 'flex', alignItems: 'center', gap: 18, padding: 22, textDecoration: 'none', color: 'inherit' }}
      >
        <div style={{
          width: 52, height: 52, borderRadius: 14, display: 'grid', placeItems: 'center', flexShrink: 0,
          background: 'linear-gradient(135deg, rgba(139,124,255,0.22), rgba(168,85,247,0.12))',
          border: '1px solid rgba(139,124,255,0.3)', color: '#c4b9ff',
        }}>
          <Icon size={26} />
        </div>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontSize: 17, fontWeight: 700, letterSpacing: '-0.02em' }}>{platform}</div>
          <div style={{ fontSize: 13, color: 'var(--sx-dim)', marginTop: 3 }}>{meta}</div>
        </div>
        <span className="sx-btn sx-btn-primary" style={{ flexShrink: 0, pointerEvents: 'none' }}>
          <Download size={16} /> {label}
        </span>
      </motion.a>
    </Reveal>
  )
}

export default function CLIInstall() {
  const [copied, setCopied] = useState(false)
  const copy = () => { navigator.clipboard.writeText(LINUX_CMD); setCopied(true); setTimeout(() => setCopied(false), 1400) }

  return (
    <section className="sx-section" id="cli">
      <div className="sx-wrap">
        <SectionHeading
          eyebrow="Download"
          title="Get Sixter on your machine"
          sub="Grab the installer for your platform and you're a single command away from a public, protected URL."
        />

        <div className="sx-two-col" style={{ marginTop: 52, alignItems: 'stretch' }}>
          {/* download cards + linux one-liner */}
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            <DownloadCard
              i={0} icon={Monitor} platform="Windows"
              meta="sixter-setup.exe · Windows 10/11 · 64-bit"
              label="Download .exe" href={WIN_EXE}
            />
            <DownloadCard
              i={1} icon={Terminal} platform="Linux"
              meta="sixter_2.0.0_amd64.deb · Debian / Ubuntu · amd64"
              label="Download .deb" href={LINUX_DEB}
            />

            {/* optional one-line install for Linux */}
            <Reveal delay={0.16}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 12, background: 'rgba(0,0,0,0.4)', border: '1px solid rgba(255,255,255,0.08)', borderRadius: 14, padding: '14px 16px' }}>
                <Terminal size={16} style={{ color: '#a1a0ff', flexShrink: 0 }} />
                <code style={{ fontFamily: 'JetBrains Mono, monospace', fontSize: 12.5, color: '#e9e7ff', flex: 1, overflowX: 'auto', whiteSpace: 'nowrap' }}>{LINUX_CMD}</code>
                <button onClick={copy} aria-label="Copy install command" style={{ background: 'none', border: 0, cursor: 'pointer', color: copied ? '#34d399' : 'var(--sx-dim)', flexShrink: 0, display: 'grid', placeItems: 'center' }}>
                  {copied ? <Check size={17} /> : <Copy size={17} />}
                </button>
              </div>
            </Reveal>

            <div style={{ display: 'flex', gap: 16, color: 'var(--sx-dim)', fontSize: 13, flexWrap: 'wrap' }}>
              <span style={{ display: 'flex', alignItems: 'center', gap: 7 }}><ShieldCheck size={15} /> Free &amp; open source</span>
              <span style={{ display: 'flex', alignItems: 'center', gap: 7 }}><Terminal size={15} /> No daemon, no config</span>
            </div>
          </div>

          {/* animated terminal showcase */}
          <Reveal delay={0.12}>
            <div className="sx-window" style={{ height: '100%' }}>
              <div className="sx-window-bar">
                <span className="sx-dot" style={{ background: '#ff5f57' }} />
                <span className="sx-dot" style={{ background: '#febc2e' }} />
                <span className="sx-dot" style={{ background: '#28c840' }} />
                <span style={{ marginLeft: 10, fontSize: 12.5, color: 'var(--sx-faint)', fontFamily: 'JetBrains Mono, monospace' }}>you@laptop ~ %</span>
              </div>
              <div style={{ padding: 22, fontFamily: 'JetBrains Mono, monospace', fontSize: 13, lineHeight: 1.9 }}>
                {SESSION.map((l, i) => (
                  <motion.div
                    key={i}
                    initial={{ opacity: 0, x: -6 }}
                    whileInView={{ opacity: 1, x: 0 }}
                    viewport={{ once: true }}
                    transition={{ delay: i * 0.35, duration: 0.3 }}
                  >
                    {l.c && <><span style={{ color: '#8b7cff' }}>{l.p} </span><span style={{ color: '#e9e7ff' }}>{l.c}</span></>}
                    {l.o && <span style={{ color: 'var(--sx-dim)' }}>{l.o}</span>}
                    {l.hi && <span style={{ color: '#34d399' }}>{l.hi}</span>}
                  </motion.div>
                ))}
              </div>
            </div>
          </Reveal>
        </div>
      </div>
    </section>
  )
}
