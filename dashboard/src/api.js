// API helpers — token auth + fetch wrappers

export function getToken() {
  return localStorage.getItem('sixter_token') || ''
}

export function setToken(t) {
  localStorage.setItem('sixter_token', t)
}

export function clearToken() {
  localStorage.removeItem('sixter_token')
}

// SSO (team member) token — minted by the gateway, verified server-side.
export function getSSO() {
  return localStorage.getItem('sixter_sso') || ''
}
export function setSSO(t) {
  localStorage.setItem('sixter_sso', t)
}
export function clearSSO() {
  localStorage.removeItem('sixter_sso')
}

// trySSO attempts a same-site grant from the gateway using the user's
// sixter.in session cookie. Returns {email, role} for a team member, else null.
export async function trySSO() {
  try {
    const parts = window.location.hostname.split('.')
    const repoSub = parts[0]
    const apex = parts.slice(1).join('.') || window.location.hostname
    const r = await fetch(`https://${apex}/api/sso/grant?repo=${encodeURIComponent(repoSub)}`, {
      credentials: 'include',
    })
    if (!r.ok) return null
    const j = await r.json()
    if (j && j.member && j.token) {
      setSSO(j.token)
      return { email: j.email, role: j.role }
    }
    return null
  } catch {
    return null
  }
}

export function hasSession() {
  return !!(getToken() || getSSO())
}

export async function api(path, opts = {}) {
  const headers = {
    ...(opts.headers || {}),
    ...(opts.body ? { 'Content-Type': 'application/json' } : {}),
  }
  const t = getToken()
  if (t) headers['X-Token'] = t
  const s = getSSO()
  if (s) headers['X-Sixter-SSO'] = s
  const res = await fetch(path, { ...opts, headers })
  if (res.status === 401) {
    const err = new Error('unauthorized')
    err.unauthorized = true
    throw err
  }
  if (!res.ok) {
    const text = await res.text()
    throw new Error(text || `HTTP ${res.status}`)
  }
  return res.json()
}

export function wsURL(path) {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  let url = proto + '//' + window.location.host + path + (path.includes('?') ? '&' : '?')
  const t = getToken()
  if (t) url += 'token=' + encodeURIComponent(t)
  else url += 'sso=' + encodeURIComponent(getSSO())
  return url
}

export function fmtBytes(b) {
  if (b == null) return '—'
  if (b < 1024) return b + ' B'
  const units = ['KB', 'MB', 'GB', 'TB']
  let i = -1
  do {
    b /= 1024
    i++
  } while (b >= 1024 && i < units.length - 1)
  return b.toFixed(1) + ' ' + units[i]
}

export function fmtDuration(ms) {
  if (ms == null) return '—'
  if (ms < 1) return '<1 ms'
  if (ms < 1000) return Math.round(ms) + ' ms'
  return (ms / 1000).toFixed(2) + ' s'
}
