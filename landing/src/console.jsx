import { useState, useEffect } from 'react'
import { AnimatePresence, motion } from 'framer-motion'
import { api } from './shared.jsx'
import Sidebar from './components/dashboard/Sidebar.jsx'
import TopBar from './components/dashboard/TopBar.jsx'
import OverviewPage from './components/dashboard/OverviewPage.jsx'
import SecurityPage from './components/dashboard/SecurityPage.jsx'
import FirewallPage from './components/dashboard/FirewallPage.jsx'
import TeamPage from './components/dashboard/TeamPage.jsx'
import WorkspacesPage from './components/dashboard/WorkspacesPage.jsx'
import GetStartedPage from './components/dashboard/GetStartedPage.jsx'
import ProfilePage from './components/dashboard/ProfilePage.jsx'
import NodesPage from './components/dashboard/NodesPage.jsx'

/* ════════════════════════════════════════════════════════════════
   User dashboard — premium shell. Data flow is unchanged from before:
   polls /api/me/status + /api/me/security every 5s and feeds the pages.
   All firewall/device API calls live inside their respective pages.
   ════════════════════════════════════════════════════════════════ */
export function Dashboard({ me, nav, onLogout }) {
  const [tab, setTab] = useState('overview')
  const [status, setStatus] = useState(null)
  const [security, setSecurity] = useState(null)
  const [drawer, setDrawer] = useState(false)

  useEffect(() => {
    let alive = true
    const tick = () => {
      api('/api/me/status').then((s) => alive && setStatus(s)).catch(() => {})
      api('/api/me/security').then((s) => alive && setSecurity(s)).catch(() => {})
    }
    tick()
    const id = setInterval(tick, 5000)
    return () => { alive = false; clearInterval(id) }
  }, [])

  const tunnelOn = status?.tunnel?.connected
  const repoOn = status?.repo?.connected
  const repoUrl = repoOn ? `https://${status.repo.subdomain}.sixter.in` : null

  return (
    <div className="dx">
      <div className="dx-shell">
        <Sidebar
          me={me} tab={tab} setTab={setTab} nav={nav}
          tunnelOn={tunnelOn} repoOn={repoOn}
          status={status} security={security}
          open={drawer} onClose={() => setDrawer(false)}
        />
        <div className="dx-main">
          <TopBar me={me} nav={nav} onLogout={onLogout} onMenu={() => setDrawer(true)} repoUrl={repoUrl}
                  status={status} security={security} setTab={setTab} />
          <main className="dx-content">
            <AnimatePresence mode="wait">
              <motion.div key={tab} initial={{ opacity: 0, y: 6 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0 }} transition={{ duration: 0.2 }}>
                {tab === 'overview' && <OverviewPage me={me} status={status} security={security} setTab={setTab} />}
                {tab === 'security' && <SecurityPage security={security} me={me} />}
                {tab === 'firewall' && <FirewallPage me={me} />}
                {tab === 'workspaces' && <WorkspacesPage me={me} />}
                {tab === 'team' && <TeamPage me={me} />}
                {tab === 'nodes' && <NodesPage />}
                {tab === 'profile' && <ProfilePage me={me} setTab={setTab} />}
                {tab === 'setup' && <GetStartedPage me={me} tunnelOn={tunnelOn} repoOn={repoOn} nav={nav} />}
              </motion.div>
            </AnimatePresence>
          </main>
        </div>
      </div>
    </div>
  )
}
