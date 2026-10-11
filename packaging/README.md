# Building the Sixter .deb

The `.deb` ships 5 statically-linked binaries to `/usr/local/bin`. The dashboards
and shell code are embedded **inside** the binaries — no extra files needed at runtime.

## Folder structure (already created)

```
packaging/sixter-deb/
├── DEBIAN/
│   ├── control        package metadata
│   ├── postinst       runs after install (chmod + prints usage)
│   └── prerm          runs before removal
└── usr/local/bin/     ← the 5 compiled binaries go here
```

---

## Option A — build everything on Linux (recommended)

On your VPS (or WSL), with `go`, `npm` and `dpkg-deb` installed:

```bash
cd "Sixter Tunnel"
chmod +x packaging/build-deb.sh
./packaging/build-deb.sh
sudo dpkg -i sixter_2.0.0_amd64.deb
```

That builds the frontends, compiles the binaries, and produces the `.deb` in one go.

If `go` isn't on the VPS:
```bash
sudo apt update && sudo apt install -y golang-go npm dpkg-dev
```

---

## Option B — cross-compile on Windows, package on Linux

1. On Windows, build the Linux binaries (this also embeds the frontends):
   ```powershell
   cd "D:\Sixter Tunnel"
   .\build.ps1 -Dashboard -Landing -Linux
   ```
2. Copy the 5 files from `bin\linux\` into `packaging\sixter-deb\usr\local\bin\`.
3. Copy the whole `packaging\sixter-deb` folder to the VPS, then:
   ```bash
   chmod 0755 sixter-deb/DEBIAN/postinst sixter-deb/DEBIAN/prerm
   chmod 0755 sixter-deb/usr/local/bin/*
   dpkg-deb --build --root-owner-group sixter-deb sixter_2.0.0_amd64.deb
   sudo dpkg -i sixter_2.0.0_amd64.deb
   ```

---

## After install

Binaries are now on `PATH` (`/usr/local/bin`). The old `~/.sixter/` copies are
no longer used — you can delete them to avoid confusion:

```bash
which sixter            # should print /usr/local/bin/sixter
sixter login            # device-flow login (approve from your phone)
sixter repo-server -token "YOUR_SECRET" -server "yourname-repo" -certbot-email you@example.com
```

> Note: the repo-server's tunnel needs a valid token, so the **upgraded**
> `sixter-server` must be running on sixter.in. If sixter.in still runs v1,
> the new client/repo-server can't authenticate.

Optional prerequisites:
- Custom domains + HTTPS: `sudo apt install nginx certbot python3-certbot-nginx`
- SSH into projects: `sudo ufw allow 2222`
