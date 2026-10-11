# Sixter — Windows installer

Produces `sixter-setup.exe`, the Windows equivalent of the Linux `.deb`. It
installs the Sixter CLI and its helpers and puts them on the system `PATH`:

| Binary             | Purpose                                  |
|--------------------|------------------------------------------|
| `sixter.exe`       | main CLI — `sixter login`, `tunnel`, `repo` |
| `sixter-client.exe`| tunnel client (exposes localhost)        |
| `sixter-repo.exe`  | repo client (uploads/deploys projects)   |

The Linux server binaries (`sixter-server`, `sixter-reposerver`) are **not**
included — those run on your VPS via the `.deb`.

## Build it

Requirements:
- **Go** in `PATH`
- **Inno Setup 6** — https://jrsoftware.org/isdl.php (gives you `ISCC.exe`)

```powershell
packaging\windows\build-windows-installer.ps1                 # version 2.0.0
packaging\windows\build-windows-installer.ps1 -Version 2.1.0  # custom version
```

Output: `installer\sixter-setup.exe`.

The script (1) cross-builds the Windows client binaries into `bin\windows\`,
then (2) compiles `sixter-setup.iss` with Inno Setup.

## What the installer does

- Installs to `C:\Program Files\Sixter`
- Optionally adds that folder to the system `PATH` (so `sixter` works in any
  terminal — checked by default)
- Adds a Start-menu "Sixter Terminal" + Docs + Uninstall
- Offers to run `sixter login` at the end

After install, open a **new** terminal and run `sixter login`.

> Tip: to avoid the SmartScreen "unknown publisher" warning for public
> distribution, sign `sixter-setup.exe` with a code-signing certificate
> (`signtool sign /fd sha256 /a installer\sixter-setup.exe`).
