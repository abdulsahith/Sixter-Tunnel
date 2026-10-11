import { useRef } from 'react'
import { motion, useInView } from 'framer-motion'

/* Shared primitives for the premium landing. */

export function Button({ variant = 'primary', size = 'md', className = '', children, ...rest }) {
  const v = variant === 'ghost' ? 'sx-btn-ghost' : 'sx-btn-primary'
  const s = size === 'lg' ? 'sx-btn-lg' : ''
  return (
    <button className={`sx-btn ${v} ${s} ${className}`} {...rest}>
      {children}
    </button>
  )
}

export function GlassCard({ className = '', hover = true, children, ...rest }) {
  return (
    <div className={`sx-glass ${hover ? 'sx-glass-hover' : ''} ${className}`} {...rest}>
      {children}
    </div>
  )
}

/* Scroll-reveal wrapper — fades + lifts content into view once. */
export function Reveal({ children, delay = 0, y = 28, className = '' }) {
  const ref = useRef(null)
  const inView = useInView(ref, { once: true, margin: '-80px' })
  return (
    <motion.div
      ref={ref}
      className={className}
      initial={{ opacity: 0, y }}
      animate={inView ? { opacity: 1, y: 0 } : {}}
      transition={{ duration: 0.6, delay, ease: [0.22, 1, 0.36, 1] }}
    >
      {children}
    </motion.div>
  )
}

/* Centered section heading: eyebrow + title + subtitle. */
export function SectionHeading({ eyebrow, title, sub, center = true }) {
  return (
    <Reveal className={center ? 'text-center mx-auto' : ''} >
      <div style={center ? { maxWidth: 720, margin: '0 auto', textAlign: 'center' } : {}}>
        {eyebrow && <span className="sx-eyebrow">{eyebrow}</span>}
        <h2 className="sx-h2">{title}</h2>
        {sub && <p className="sx-sub" style={center ? { margin: '0 auto' } : {}}>{sub}</p>}
      </div>
    </Reveal>
  )
}
