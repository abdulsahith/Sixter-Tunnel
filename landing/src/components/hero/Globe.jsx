import { useEffect, useRef } from 'react'
import { motion } from 'framer-motion'

/* ════════════════════════════════════════════════════════════════
   Realistic 3D network globe — pure <canvas>, no WebGL / no CDN.
   • Fibonacci-distributed dots on a sphere
   • Real continents via a lat/lon land mask → bright neon dots
   • Oceans → faint grid dots, giving the "dotted earth" look
   • 23.4° axial tilt + slow continuous Y-rotation
   • Depth-shaded (front hemisphere brighter), back hemisphere faded
   • Atmosphere glow, orbit rings + comets, glowing connection arcs
   Renders identically everywhere and is cheap (one rAF, ~2600 dots).
   ════════════════════════════════════════════════════════════════ */

// Coarse land boxes [latMax, latMin, lonMin, lonMax] — enough to read as Earth.
const LAND = [
  [83, 60, -55, -18],   // Greenland
  [72, 49, -141, -52],  // Canada
  [71, 53, -168, -141], // Alaska
  [49, 25, -125, -67],  // USA
  [30, 14, -118, -86],  // Mexico
  [16, 7, -92, -77],    // Central America
  [12, 0, -80, -50],    // N. South America
  [0, -23, -74, -34],   // Brazil / central SA
  [-23, -55, -73, -53], // Southern cone
  [71, 36, -10, 30],    // Europe
  [68, 50, 30, 60],     // W. Russia
  [75, 50, 60, 180],    // Siberia / N. Asia
  [37, 18, -12, 30],    // N. Africa / Sahara
  [18, -8, 8, 42],      // Central Africa
  [-8, -35, 12, 40],    // Southern Africa
  [40, 12, 35, 60],     // Middle East
  [38, 8, 60, 90],      // India / S. Asia
  [50, 20, 90, 122],    // China
  [45, 30, 128, 146],   // Japan / Korea
  [20, -10, 95, 141],   // SE Asia / Indonesia
  [-10, -39, 113, 154], // Australia
]

function isLand(lat, lon) {
  if (lat < -62) return true // Antarctica
  for (const [a, b, c, d] of LAND) if (lat <= a && lat >= b && lon >= c && lon <= d) return true
  return false
}

export default function Globe() {
  const canvasRef = useRef(null)

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const ctx = canvas.getContext('2d')
    const reduce = window.matchMedia('(prefers-reduced-motion: reduce)').matches

    // Build the point cloud once (Fibonacci sphere).
    const N = 2600
    const pts = []
    const golden = Math.PI * (3 - Math.sqrt(5))
    for (let i = 0; i < N; i++) {
      const y = 1 - (i / (N - 1)) * 2
      const r = Math.sqrt(1 - y * y)
      const theta = golden * i
      const x = Math.cos(theta) * r
      const z = Math.sin(theta) * r
      const lat = Math.asin(y) * (180 / Math.PI)
      const lon = Math.atan2(z, x) * (180 / Math.PI)
      pts.push({ x, y, z, land: isLand(lat, lon) })
    }

    const TILT = (23.4 * Math.PI) / 180
    const cosT = Math.cos(TILT)
    const sinT = Math.sin(TILT)
    let raf, t0
    let dpr = 1, cx = 0, cy = 0, R = 0

    const resize = () => {
      const size = canvas.clientWidth
      dpr = Math.min(window.devicePixelRatio || 1, 2)
      canvas.width = size * dpr
      canvas.height = size * dpr
      cx = (size * dpr) / 2
      cy = (size * dpr) / 2
      R = (size * dpr) / 2 * 0.74
    }
    resize()
    const ro = new ResizeObserver(resize)
    ro.observe(canvas)

    const draw = (ts) => {
      if (t0 === undefined) t0 = ts
      const ang = reduce ? 0.6 : ((ts - t0) / 1000) * 0.18 // slow spin
      const cosA = Math.cos(ang)
      const sinA = Math.sin(ang)
      ctx.clearRect(0, 0, canvas.width, canvas.height)

      for (let i = 0; i < pts.length; i++) {
        const p = pts[i]
        // rotate around Y
        let rx = p.x * cosA - p.z * sinA
        let rz = p.x * sinA + p.z * cosA
        let ry = p.y
        // axial tilt around X
        const ty = ry * cosT - rz * sinT
        const tz = ry * sinT + rz * cosT
        const sx = cx + rx * R
        const sy = cy - ty * R
        const depth = (tz + 1) / 2 // 0 (back) → 1 (front)

        if (p.land) {
          const a = 0.18 + depth * 0.82
          const size = (0.7 + depth * 1.5) * dpr
          // neon brand→accent shift by latitude-ish using index
          ctx.fillStyle = depth > 0.5
            ? `rgba(167, 139, 250, ${a})`
            : `rgba(120, 110, 200, ${a * 0.7})`
          ctx.beginPath()
          ctx.arc(sx, sy, size, 0, 6.283)
          ctx.fill()
        } else if (depth > 0.34) {
          // faint ocean grid, front hemisphere only
          const a = (depth - 0.34) * 0.18
          ctx.fillStyle = `rgba(110, 130, 200, ${a})`
          ctx.beginPath()
          ctx.arc(sx, sy, 0.6 * dpr, 0, 6.283)
          ctx.fill()
        }
      }
      raf = requestAnimationFrame(draw)
    }
    raf = requestAnimationFrame(draw)

    return () => { cancelAnimationFrame(raf); ro.disconnect() }
  }, [])

  return (
    <motion.div
      className="sx-globe"
      initial={{ opacity: 0, scale: 0.92 }}
      animate={{ opacity: 1, scale: 1 }}
      transition={{ duration: 1, ease: [0.22, 1, 0.36, 1] }}
    >
      <div className="sx-globe-atm" />
      <motion.div
        animate={{ y: [0, -12, 0] }}
        transition={{ duration: 7, repeat: Infinity, ease: 'easeInOut' }}
        style={{ position: 'absolute', inset: 0 }}
      >
        <canvas ref={canvasRef} className="sx-globe-canvas" aria-hidden="true" />

        {/* orbit rings + traveling comets + connection arcs */}
        <svg className="sx-globe-orbit" viewBox="0 0 460 460" fill="none" aria-hidden="true">
          <defs>
            <linearGradient id="sxArc" x1="0" y1="0" x2="1" y2="1">
              <stop offset="0%" stopColor="#c4b9ff" />
              <stop offset="100%" stopColor="#57a0f5" />
            </linearGradient>
            <filter id="sxGlow" x="-80%" y="-80%" width="260%" height="260%">
              <feGaussianBlur stdDeviation="3.4" result="b" />
              <feMerge><feMergeNode in="b" /><feMergeNode in="SourceGraphic" /></feMerge>
            </filter>
          </defs>

          <g style={{ transformOrigin: '230px 230px', animation: 'sx-spin 26s linear infinite' }}>
            <ellipse cx="230" cy="230" rx="222" ry="80" stroke="rgba(170,164,242,0.30)" strokeWidth="1" strokeDasharray="2 9" transform="rotate(-18 230 230)" />
            <g transform="rotate(-18 230 230)">
              <circle r="4.5" fill="#aaa4f2" filter="url(#sxGlow)">
                <animateMotion dur="9s" repeatCount="indefinite" path="M8,230 a222,80 0 1,1 444,0 a222,80 0 1,1 -444,0" />
              </circle>
            </g>
          </g>
          <g style={{ transformOrigin: '230px 230px', animation: 'sx-spin-rev 34s linear infinite' }}>
            <ellipse cx="230" cy="230" rx="226" ry="58" stroke="rgba(87,160,245,0.26)" strokeWidth="1" strokeDasharray="2 10" transform="rotate(16 230 230)" />
            <g transform="rotate(16 230 230)">
              <circle r="4" fill="#57a0f5" filter="url(#sxGlow)">
                <animateMotion dur="12s" begin="1.5s" repeatCount="indefinite" path="M4,230 a226,58 0 1,1 452,0 a226,58 0 1,1 -452,0" />
              </circle>
            </g>
          </g>

          {/* glowing connection arcs across the sphere */}
          <g fill="none" stroke="url(#sxArc)" strokeWidth="1.4" filter="url(#sxGlow)" strokeLinecap="round" opacity="0.9">
            <path d="M96 196 Q 230 78 366 214" strokeDasharray="3 240">
              <animate attributeName="stroke-dashoffset" from="243" to="0" dur="3s" repeatCount="indefinite" />
            </path>
            <path d="M104 280 Q 230 392 360 256" strokeDasharray="3 230">
              <animate attributeName="stroke-dashoffset" from="233" to="0" dur="3.6s" begin="0.8s" repeatCount="indefinite" />
            </path>
            <path d="M140 150 Q 300 210 326 326" strokeDasharray="3 220">
              <animate attributeName="stroke-dashoffset" from="223" to="0" dur="4.2s" begin="1.4s" repeatCount="indefinite" />
            </path>
          </g>
        </svg>
      </motion.div>
    </motion.div>
  )
}
