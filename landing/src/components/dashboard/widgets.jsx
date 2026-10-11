import { useState } from 'react'
import { motion } from 'framer-motion'
import { Copy, Check } from 'lucide-react'

/* ════════ shared dashboard primitives ════════ */

export const fadeUp = (i = 0) => ({
  initial: { opacity: 0, y: 16 },
  animate: { opacity: 1, y: 0 },
  transition: { duration: 0.4, delay: i * 0.05, ease: [0.22, 1, 0.36, 1] },
})

export function PageHeader({ title, sub, actions }) {
  return (
    <motion.div className="dx-page-head" {...fadeUp(0)}>
      <div>
        <h1 className="dx-page-title">{title}</h1>
        {sub && <p className="dx-page-sub">{sub}</p>}
      </div>
      {actions && <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>{actions}</div>}
    </motion.div>
  )
}

export function Badge({ tone = 'gray', live, children, small }) {
  return (
    <span className={`dx-badge ${small ? 'dx-badge-sm' : ''} ${tone}`}>
      {live && <span className="dx-live-dot" />}
      {children}
    </span>
  )
}

/* tiny inline SVG sparkline — cheap, one per stat card */
export function Sparkline({ data, color = '#8b7cff', height = 38 }) {
  const w = 240
  const max = Math.max(...data), min = Math.min(...data)
  const range = max - min || 1
  const step = w / (data.length - 1)
  const pts = data.map((v, i) => [i * step, height - ((v - min) / range) * (height - 6) - 3])
  const line = pts.map((p, i) => `${i ? 'L' : 'M'}${p[0].toFixed(1)} ${p[1].toFixed(1)}`).join(' ')
  const area = `${line} L${w} ${height} L0 ${height} Z`
  const id = 'sp' + color.replace('#', '')
  return (
    <svg className="dx-stat-spark" viewBox={`0 0 ${w} ${height}`} preserveAspectRatio="none" aria-hidden="true">
      <defs>
        <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity="0.28" />
          <stop offset="100%" stopColor={color} stopOpacity="0" />
        </linearGradient>
      </defs>
      <path d={area} fill={`url(#${id})`} />
      <path d={line} fill="none" stroke={color} strokeWidth="2" vectorEffect="non-scaling-stroke" />
    </svg>
  )
}

export function StatCard({ icon: Icon, label, value, tone = 'violet', spark, badge, i = 0 }) {
  const colors = {
    violet: '#8b7cff', blue: '#5a8bff', green: '#22c55e', amber: '#f59e0b', red: '#ef4444',
  }
  const c = colors[tone] || colors.violet
  return (
    <motion.div className="dx-stat" {...fadeUp(i)}>
      <div className="dx-stat-top">
        <span className="dx-stat-ico" style={{ background: `${c}22`, border: `1px solid ${c}33`, color: c }}>
          <Icon size={18} />
        </span>
        {badge}
      </div>
      <div className="dx-stat-label">{label}</div>
      <div className="dx-stat-value">{value}</div>
      {spark && <Sparkline data={spark} color={c} />}
    </motion.div>
  )
}

export function Card({ children, className = '', style, ...rest }) {
  return <div className={`dx-card ${className}`} style={style} {...rest}>{children}</div>
}

export function CardHead({ icon: Icon, title, action }) {
  return (
    <div className="dx-card-h">
      {Icon && <span className="dx-card-ico"><Icon size={19} /></span>}
      <h3>{title}</h3>
      {action && <div style={{ marginLeft: 'auto' }}>{action}</div>}
    </div>
  )
}

export function Metric({ k, v, dot }) {
  return (
    <div className="dx-metric">
      <div className="k">{k}</div>
      <div className="v">{dot && <span style={{ width: 8, height: 8, borderRadius: '50%', background: dot }} />}{v}</div>
    </div>
  )
}

/* warp / github style command box with copy animation + light syntax highlight */
export function CommandBox({ cmd, title = 'bash', highlight = true }) {
  const [copied, setCopied] = useState(false)
  const copy = () => { navigator.clipboard.writeText(cmd); setCopied(true); setTimeout(() => setCopied(false), 1400) }

  const render = () => {
    if (!highlight) return cmd
    const tokens = cmd.split(/(\s+)/)
    return tokens.map((t, i) => {
      if (i === 0) return <span key={i} className="tk-cmd">{t}</span>
      if (t.startsWith('-')) return <span key={i} className="tk-flag">{t}</span>
      if (t.startsWith('"') || t.startsWith("'")) return <span key={i} className="tk-str">{t}</span>
      return <span key={i}>{t}</span>
    })
  }

  return (
    <div className="dx-cmd">
      <div className="dx-cmd-bar">
        <span className="d" style={{ background: '#ff5f57' }} />
        <span className="d" style={{ background: '#febc2e' }} />
        <span className="d" style={{ background: '#28c840' }} />
        <span>{title}</span>
      </div>
      <div className="dx-cmd-body">
        <code>{render()}</code>
        <button className="dx-cmd-copy" onClick={copy} aria-label="Copy command">
          <motion.span key={copied ? 'y' : 'n'} initial={{ scale: 0.6, opacity: 0 }} animate={{ scale: 1, opacity: 1 }} transition={{ duration: 0.18 }} style={{ color: copied ? '#22c55e' : 'inherit', display: 'grid', placeItems: 'center' }}>
            {copied ? <Check size={16} /> : <Copy size={16} />}
          </motion.span>
        </button>
      </div>
    </div>
  )
}

/* animated donut for security score / allowed-vs-blocked */
export function Donut({ segments, size = 150, thickness = 14, center }) {
  const r = (size - thickness) / 2
  const C = 2 * Math.PI * r
  const total = segments.reduce((s, x) => s + x.value, 0) || 1
  let offset = 0
  return (
    <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
      <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="rgba(255,255,255,0.06)" strokeWidth={thickness} />
      {segments.map((s, i) => {
        const len = (s.value / total) * C
        const el = (
          <motion.circle
            key={i} cx={size / 2} cy={size / 2} r={r} fill="none" stroke={s.color} strokeWidth={thickness}
            strokeLinecap="round" transform={`rotate(-90 ${size / 2} ${size / 2})`}
            strokeDasharray={`${len} ${C}`} strokeDashoffset={-offset}
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            transition={{ duration: 0.9, ease: 'easeOut' }}
          />
        )
        offset += len
        return el
      })}
      {center && (
        <foreignObject x="0" y="0" width={size} height={size}>
          <div style={{ width: size, height: size, display: 'grid', placeItems: 'center', textAlign: 'center' }}>{center}</div>
        </foreignObject>
      )}
    </svg>
  )
}

export function Switch({ on, onClick, disabled }) {
  return (
    <button className={`dx-switch ${on ? 'on' : ''}`} onClick={onClick} disabled={disabled} aria-pressed={on} role="switch">
      <span />
    </button>
  )
}

/* horizontal labelled bar (threat categories etc.) */
export function BarRow({ label, value, max, color = '#8b7cff' }) {
  return (
    <div style={{ display: 'grid', gridTemplateColumns: '140px 1fr 52px', alignItems: 'center', gap: 12, padding: '7px 0' }}>
      <span style={{ fontSize: 13, color: 'var(--dx-text2)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{label}</span>
      <div className="dx-bar" style={{ height: 8 }}>
        <motion.span initial={{ width: 0 }} animate={{ width: `${(value / (max || 1)) * 100}%` }} transition={{ duration: 0.7, ease: 'easeOut' }} style={{ display: 'block', height: '100%', borderRadius: 99, background: color }} />
      </div>
      <span style={{ fontSize: 13, fontWeight: 600, textAlign: 'right' }}>{value.toLocaleString()}</span>
    </div>
  )
}
