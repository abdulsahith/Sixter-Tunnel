import { Reveal } from '../common/ui.jsx'

/* Logo / wordmark marquee — "trusted by developers". Uses neutral wordmarks
   (text) so we don't ship third-party logo assets. */
const NAMES = ['Acme Cloud', 'Northwind', 'Vortex', 'Beacon', 'Hyperloop', 'Quanta', 'Stackline', 'Nimbus', 'Forgepay', 'Lumen']

export default function TrustedBy() {
  const row = [...NAMES, ...NAMES]
  return (
    <section className="sx-wrap" style={{ paddingTop: 24, paddingBottom: 24 }}>
      <Reveal>
        <p style={{ textAlign: 'center', color: 'var(--sx-faint)', fontSize: 13, fontWeight: 600, letterSpacing: '0.14em', textTransform: 'uppercase', marginBottom: 26 }}>
          Trusted by developers shipping at every scale
        </p>
      </Reveal>
      <div className="sx-marquee-mask" style={{ overflow: 'hidden' }}>
        <div className="sx-marquee">
          {row.map((n, i) => (
            <span key={i} style={{ fontSize: 21, fontWeight: 700, color: 'rgba(255,255,255,0.34)', whiteSpace: 'nowrap', letterSpacing: '-0.02em' }}>
              {n}
            </span>
          ))}
        </div>
      </div>
    </section>
  )
}
