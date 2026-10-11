import Nav from './common/Nav.jsx'
import Hero from './hero/Hero.jsx'
import TunnelFlow from './features/TunnelFlow.jsx'
import Features from './features/Features.jsx'
import HowItWorks from './features/HowItWorks.jsx'
import FirewallDemo from './features/FirewallDemo.jsx'
import DashboardPreview from './features/DashboardPreview.jsx'
import CLIInstall from './features/CLIInstall.jsx'
import Testimonials from './testimonials/Testimonials.jsx'
import Footer from './common/Footer.jsx'

/* The redesigned premium marketing site. Everything lives inside `.sx`
   so the premium theme/tokens never leak into the auth/console pages. */
export default function PremiumLanding({ nav, me, onLogout }) {
  return (
    <div className="sx">
      <Nav nav={nav} me={me} onLogout={onLogout} />
      <Hero nav={nav} />
      <TunnelFlow />
      <Features />
      <HowItWorks />
      <FirewallDemo />
      <DashboardPreview />
      <CLIInstall />
      <Testimonials />
      <Footer nav={nav} />
    </div>
  )
}
