import { useState, useEffect, useRef } from 'react'

const DOCS = [
  {
    id: 'install', title: 'Installation',
    body: (
      <>
        <p><b>Windows:</b> download <a href="/sixter-setup.exe">sixter-setup.exe</a> and run it. The installer adds <b>sixter</b> to your PATH.</p>
        <p><b>Linux:</b></p>
        <div className="codeblock">{`curl -fsSL https://sixter.in/sixter_2.0.0_amd64.deb -o sixter.deb
sudo dpkg -i sixter.deb`}</div>
        <p>Verify with <b>sixter help</b>. The CLI ships with its companion binaries (tunnel client, repo tools) in the same folder.</p>
      </>
    ),
  },
  {
    id: 'login', title: 'Logging in',
    body: (
      <>
        <div className="codeblock">{`sixter login`}</div>
        <p>The CLI prints a URL and a short code. Open the URL on <b>any device</b> — your phone works — log in, and approve. Credentials are saved to <b>~/.sixter/config.yaml</b>.</p>
        <p>On an SSH-only server, use <b>sixter login -no-browser</b> and approve the code from your dashboard&apos;s &ldquo;Connect a device&rdquo; card.</p>
        <p>Every account gets two permanent subdomains: <b>&lt;name&gt;.sixter.in</b> for tunnels and <b>&lt;name&gt;-repo.sixter.in</b> for the repo dashboard. Your API token is shown once and stored hashed.</p>
        <div className="codeblock">{`sixter whoami   # show current account
sixter logout   # remove local credentials`}</div>
      </>
    ),
  },
  {
    id: 'tunnel', title: 'Tunnels',
    body: (
      <>
        <div className="codeblock">{`sixter tunnel -port 3000`}</div>
        <p>Forwards <b>https://&lt;name&gt;.sixter.in</b> to <b>http://127.0.0.1:3000</b>. HTTP and WebSocket traffic both work. Subdomain, server and token come from your config — flags override:</p>
        <div className="codeblock">{`sixter tunnel -port 8080 -subdomain yourname`}</div>
        <p>Only your two registered subdomains are accepted — everything else is rejected with 401.</p>
      </>
    ),
  },
  {
    id: 'repo', title: 'Deploying projects',
    body: (
      <>
        <p>Set up the repo server <b>on your VPS</b> with one command — it prompts for your subdomain, email and token, then creates and starts a managed <code>sixter</code> service:</p>
        <div className="codeblock">{`sudo sixter repo-server -setup`}</div>
        <p>Push the current folder from any machine (you'll be prompted for the token securely — no secrets in your shell history):</p>
        <div className="codeblock">{`cd myproject
sixter repo -server "yourname-repo" -project myapp
Token: ********`}</div>
        <p>Every upload is auto-deployed: detected, built, health-checked, and rolled back if it fails. node_modules, .git and build artifacts are excluded automatically, and each upload is versioned.</p>
        <p>Open <b>https://yourname-repo.sixter.in</b> for the dashboard: live traffic, packet capture, a web terminal per project, and domain management.</p>
      </>
    ),
  },
  {
    id: 'domains', title: 'Custom domains & HTTPS',
    body: (
      <>
        <p>In the repo dashboard, open a project → <b>Custom domain</b>. Enter your domain and the port your app listens on. Sixter writes the nginx config, verifies your DNS A record against the server&apos;s public IP, shows a <b style={{ color: 'var(--green)' }}>green tick</b> when it matches, and requests a Let&apos;s Encrypt certificate automatically (with -certbot-email).</p>
        <div className="codeblock">{`# server prerequisites (Ubuntu/Debian)
sudo apt install nginx certbot python3-certbot-nginx`}</div>
      </>
    ),
  },
  {
    id: 'ssh', title: 'SSH access',
    body: (
      <>
        <p>Every deployed project gets its own SSH login, jailed to that project&apos;s folder. The repo server runs an SSH service on <b>port 2222</b> (your server&apos;s normal SSH on port 22 is untouched).</p>
        <div className="codeblock">{`ssh <project>@<your-server-ip> -p 2222
# username = the project name (e.g. myapp)
# password = your repo-server token`}</div>
        <p>You land directly inside the project folder with a full interactive shell. If you try to <b>cd</b> out of it, Sixter bounces you straight back. Configure with <b>-ssh-port</b> (0 disables), and open the port: <b>sudo ufw allow 2222</b>. Linux servers only.</p>
      </>
    ),
  },
  {
    id: 'firewall', title: 'Firewall (WAF)',
    body: (
      <>
        <p>Because your traffic flows through sixter.in, we inspect every request at our edge <b>before</b> it reaches your machine. The firewall runs automatically on both your subdomains — no setup needed.</p>
        <p>Out of the box it blocks <b>SQL injection</b>, <b>cross-site scripting</b>, <b>path traversal</b>, <b>command injection</b> and common <b>scanner probes</b>, plus a per-IP rate limit. IPs that keep attacking are temporarily auto-banned.</p>
        <p>Open the <b>Security</b> tab in your dashboard to see live counts of allowed vs blocked requests and a feed of recent blocked requests. In the <b>Firewall</b> tab you can toggle protection, set the rate limit, and add your own block/allow rules by IP/CIDR, path, method, user-agent or country — applied instantly.</p>
      </>
    ),
  },
  {
    id: 'security', title: 'Security model',
    body: (
      <>
        <p><b>Passwords</b> are hashed with PBKDF2-SHA256 (210,000 iterations). <b>API tokens</b> are 256-bit random values shown once; only SHA-256 hashes are stored. <b>Repo server tokens</b> are chosen by you and compared in constant time.</p>
        <p>Tunnel connections must present a valid token for the exact subdomain. Browser sessions are HttpOnly cookies with SameSite=Lax. The web terminal and SSH service are confined to the project directory.</p>
        <p>Self-hosting? The tunnel server supports PostgreSQL for accounts (<b>-db</b>), per-subdomain rate limiting (<b>-rate-limit</b>), and custom domain mapping (<b>-domains</b>).</p>
      </>
    ),
  },
  {
    id: 'coworkspace', title: 'Coworkspaces & pull requests',
    body: (
      <>
        <p>A <b>workspace</b> groups one app&apos;s work and gives each teammate their own <b>component</b> — a named, independently deployable folder (a frontend, an API, a jobs worker). Members can&apos;t overwrite each other: everyone owns their own component.</p>
        <p><b>Roles:</b> a <b>lead</b> pushes and deploys anything; <b>backend</b>/<b>frontend</b> developers push their own component; <b>deployment</b> promotes to production; <b>tester</b> sees everything and previews staging (no push/deploy); <b>viewer</b> is read-only.</p>

        <p><b>1. Join a workspace.</b> Every workspace has <b>one permanent join link</b> — the lead shares the same command with the whole team (Console → Workspaces). You must be logged in (<b>sixter login</b>): the link identifies the workspace, and your own account identifies you, so you get the role the lead gave you. <code>-component</code> names your folder — it defaults to the current directory&apos;s name:</p>
        <div className="codeblock">{`cd api
sixter project init <workspace-link> -component api`}</div>
        <p>The lead adds teammates by email in the Workspaces page first; someone who isn&apos;t on the team is rejected even with the link. If the link ever leaks, the lead clicks <b>Rotate</b> — already-enrolled members keep working.</p>

        <p><b>2. Pull the latest</b> main into your folder — like starting from the lead&apos;s template:</p>
        <div className="codeblock">{`sixter pull                 # pull the component's main
sixter pull -branch <name>  # pull a specific branch
sixter pull -force          # overwrite local files that differ`}</div>

        <p><b>3. Push your work.</b> A lead or the component owner writes straight to main; anyone else opens a <b>pull request</b> on a branch (never touching main):</p>
        <div className="codeblock">{`sixter push -m "add login page"          # owner/lead → main; others → a PR
sixter push -branch login-page -m "wip"  # push to a named branch (a PR)`}</div>

        <p><b>4. Review &amp; merge.</b> The lead opens <b>Pull requests</b> in the repo dashboard, reviews the file diff, and clicks <b>Merge</b> — the branch replaces main (a backup snapshot is taken first) and is staged for deploy.</p>

        <p><b>5. Deploy.</b> A lead or deployment member promotes the merged main:</p>
        <div className="codeblock">{`sixter project deploy   # lead / deployment role only`}</div>
        <p>Every step — PR opened, merged, deployed — is recorded in the workspace activity log.</p>

        <p><b>Everyday commands</b> — run these inside a component folder:</p>
        <div className="codeblock">{`sixter status              # what this folder is, your role, what's pending
sixter branch              # branches / open pull requests on this component
sixter members             # who's in the workspace and their role
sixter log -n 20           # recent team activity
sixter pr list             # open pull requests
sixter pr view <branch>    # read the diff in your terminal
sixter pr merge <branch>   # merge it (lead only)`}</div>
      </>
    ),
  },
  {
    id: 'amm', title: 'AMM runtime (containers)',
    body: (
      <>
        <p><b>AMM</b> (Application Machine Monitor) is Sixter&apos;s runtime. By default a deployed project runs as an ordinary process on your server. Turn AMM on and it runs in an <b>isolated container</b> instead — its own process tree, its own filesystem view, and enforced CPU and memory limits.</p>

        <p><b>Turning it on.</b> Open a project in your repo dashboard → the <b>AMM</b> tab → <b>Activate AMM</b>. The next deploy runs in a container. Toggle it off and the project goes back to a normal service — it&apos;s a real rollback, not a one-way door.</p>

        <p><b>What you get:</b></p>
        <ul className="check-list">
          <li><span>Every deploy starts a <b>fresh container</b>, so nothing carries over from the last release</span></li>
          <li><span>The app writes to a private layer — your deployed source is never modified</span></li>
          <li><span><b>Real</b> CPU and memory numbers, measured from the kernel rather than estimated</span></li>
          <li><span>A runaway app can&apos;t take the whole server down</span></li>
        </ul>

        <p><b>Resource limits.</b> Memory is in MB; CPU is a percentage where <b>100 = one core</b> (the AMM tab shows how many cores the machine has).</p>
        <p>Leave <b>CPU at 0</b> unless you specifically need a hard cap. 0 means fair share: the app uses whatever is free and is only slowed when the server is genuinely busy. A hard cap works differently — once the quota is spent the app is <b>frozen until the next 100 ms window</b>, even on an idle machine, which shows up as sudden latency spikes. AMM warns you on the AMM tab when this is happening.</p>
        <p><b>Memory is worth setting.</b> There is no &ldquo;fair share&rdquo; for memory — a leaking app consumes until something gets killed. A limit contains the damage to that one app.</p>

        <p><b>Base image (optional, but it matters).</b> Without an image, the container borrows the host&apos;s <code>/bin</code>, <code>/usr</code> and <code>/lib</code>. That runs fine, but the container is <b>pinned to that machine</b> and behaves differently on a different OS. Set an image and the filesystem comes from a fixed, downloadable artifact, so the same container runs identically anywhere:</p>
        <div className="codeblock">{`node:20-slim        # Node apps
python:3.12-slim    # Python apps
golang:1.24         # Go apps
alpine:3.20         # tiny, static binaries`}</div>
        <p>The first pull takes a moment; afterwards it&apos;s cached and shared by every container using it. Any public OCI registry works — Docker Hub, GHCR, ECR, GCR.</p>

        <p><b>Requirements.</b> Linux with cgroup v2 and overlayfs, and the repo server running as root. If a host can&apos;t provide containers, AMM tells you why and keeps running your projects as normal processes rather than silently pretending to isolate them.</p>
      </>
    ),
  },
  {
    id: 'nodes', title: 'Multiple machines (nodes)',
    body: (
      <>
        <p>One server is the starting point, not the limit. Install the <b>Sixter agent</b> on any machine — an EC2 instance, a GCP VM, a box in your rack — and it joins your account as a node that can run workloads.</p>

        <p><b>1. Get a join token</b> from your console → <b>Nodes</b> → <b>Add node</b>. One token enrols your whole fleet; rotating it stops new machines joining without affecting existing ones.</p>

        <p><b>2. Run one command</b> on each machine:</p>
        <div className="codeblock">{`curl -fsSL https://sixter.in/install.sh | sh -s -- --join <your-token>`}</div>
        <p>That installs the agent, registers it as a systemd service and enrols the machine. Identical on every cloud and on bare metal.</p>

        <p><b>3. Zero-touch for new machines.</b> You shouldn&apos;t run that by hand on fifty servers. Put it in your instance start-up script and machines enrol themselves as they boot — including ones an autoscaler creates at 3am:</p>
        <div className="codeblock">{`#cloud-config
runcmd:
  - curl -fsSL https://sixter.in/install.sh | sh -s -- --join <your-token>`}</div>
        <p>On AWS that&apos;s <b>User data</b>, on GCP it&apos;s <b>Startup script</b>, on Azure <b>Custom data</b>. Or bake the agent into your machine image and every instance from it joins on boot.</p>

        <p><b>No network changes required.</b> The agent only makes <b>outbound</b> connections to Sixter. There are no inbound ports to open, no security group edits, no VPC peering, and it works behind NAT. Sixter never connects into your network — your machines reach out.</p>

        <p><b>How traffic reaches your app.</b> The Sixter gateway already terminates every request, so once an app runs on several nodes the gateway balances across them, drops unhealthy ones automatically, and drains a node during a deploy for zero-downtime releases. You don&apos;t configure a load balancer — it&apos;s the same gateway that already serves your tunnel.</p>

        <p><b>Scaling adds capacity to your existing address.</b> It does not give you a second one. Your project keeps the domain it already has; the gateway puts every copy behind it and sends each request to whichever one is least busy. Your primary server stays in the pool, so scaling shares its load rather than moving traffic off it. There is no new URL to remember and nothing to change in your DNS.</p>

        <p><b>The domain must be in tunnel mode.</b> A public-mode domain is terminated by nginx on your primary server, and nodes dial outward with no inbound address — so requests can never reach them. Switch the project&apos;s domain to <b>tunnel mode</b> on the Domains tab before scaling; the Scale panel warns you if it isn&apos;t.</p>

        <p><b>Portability requires an image.</b> A container can only be scheduled onto another machine if its filesystem can be downloaded there. Set a base image on the AMM tab for any project you want to run across nodes — the tab shows whether a project is <b>portable</b> or <b>pinned to this server</b>.</p>

        <div className="codeblock">{`# useful on any node
systemctl status sixter-agent
journalctl -u sixter-agent -f`}</div>
      </>
    ),
  },
  {
    id: 'cli', title: 'CLI reference',
    body: (
      <div className="codeblock">{`sixter login [-api https://sixter.in] [-no-browser]
sixter whoami
sixter logout
sixter tunnel -port <port> [-subdomain <name>] [-server <ws-url>]
sixter repo -server <name> [-project <name>]      # prompts for token (or $SIXTER_TOKEN)
sixter repo-server -setup                          # one-command managed-service setup
sixter repo-server -server <name> [-certbot-email <email>] [-port 8080]
                   [-ssh-port 2222] [-public-ip <ip>] [-dir <dir>] [-no-tunnel]

# Coworkspace (team members)
sixter project init <workspace-link> [-component <name>]   # join (one link per team)
sixter pull [-branch <name>] [-force]             # download main (or a branch)
sixter push [-branch <name>] [-m "message"]       # push; opens a PR if you're not the owner/lead
sixter status                                      # folder, role, pending work
sixter branch                                      # branches / open PRs
sixter members                                     # workspace team + roles
sixter log [-n 20]                                 # recent team activity
sixter pr list | view <branch> | merge <branch> | close <branch>
sixter project deploy                              # promote main (lead / deployment only)

# SSH into a deployed project (jailed, Linux servers)
ssh <project>@<server-ip> -p 2222    # password = repo-server token`}</div>
    ),
  },
]

export function Docs() {
  const [active, setActive] = useState(DOCS[0].id)
  const refs = useRef({})

  useEffect(() => {
    const obs = new IntersectionObserver(
      (entries) => { entries.forEach((e) => { if (e.isIntersecting) setActive(e.target.id) }) },
      { rootMargin: '-20% 0px -70% 0px' }
    )
    Object.values(refs.current).forEach((el) => el && obs.observe(el))
    return () => obs.disconnect()
  }, [])

  return (
    <div className="docs">
      <aside className="docs-toc">
        {DOCS.map((d) => (
          <button key={d.id} className={`toc-item ${active === d.id ? 'on' : ''}`}
                  onClick={() => refs.current[d.id]?.scrollIntoView({ behavior: 'smooth' })}>
            {d.title}
          </button>
        ))}
      </aside>
      <div className="docs-body">
        <h1>Documentation</h1>
        <p className="lead">Everything you need to run Sixter — from first tunnel to production deploys.</p>
        {DOCS.map((d) => (
          <section key={d.id} id={d.id} className="doc-sec" ref={(el) => (refs.current[d.id] = el)}>
            <h2>{d.title}</h2>
            {d.body}
          </section>
        ))}
      </div>
    </div>
  )
}
