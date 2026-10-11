# Google + GitHub sign-in — deployment

Credentials are read from the environment. They are **not** in the repo and
**not** compiled into the binary. A provider with no credentials is simply
hidden on the login page, so the gateway still runs without them.

## 1. Provider setup (already done)

| Provider | Callback URL |
|---|---|
| Google | `https://sixter.in/api/auth/google/callback` |
| GitHub | `https://sixter.in/api/auth/github/callback` |

Google also needs `https://sixter.in` under *Authorized JavaScript origins*.

## 2. Put the secrets on the server

```bash
sudo install -m 600 /dev/null /etc/sixter/oauth.env
sudo nano /etc/sixter/oauth.env
```

```ini
SIXTER_BASE_URL=https://sixter.in
SIXTER_GOOGLE_CLIENT_ID=...apps.googleusercontent.com
SIXTER_GOOGLE_CLIENT_SECRET=...
SIXTER_GITHUB_CLIENT_ID=...
SIXTER_GITHUB_CLIENT_SECRET=...
```

`chmod 600` and root-owned matters — this file is the whole login system.

## 3. Load it into the service

```bash
sudo systemctl edit sixter-tunnel
```

```ini
[Service]
EnvironmentFile=/etc/sixter/oauth.env
```

```bash
sudo systemctl daemon-reload
sudo systemctl restart sixter-tunnel
curl -s https://sixter.in/api/auth/providers   # → {"github":true,"google":true}
```

If that returns `false`, the env file isn't reaching the process — check
`systemctl show sixter-tunnel -p Environment` and the drop-in path.

## What the flow does

1. `/api/auth/<provider>/start` sets a single-use state cookie (CSRF) and
   redirects to the provider.
2. `/api/auth/<provider>/callback` verifies state, exchanges the code, and
   reads the **verified** email (GitHub's `/user/emails`, Google's
   `email_verified`). Unverified emails are rejected.
3. Account resolution: linked provider id → same email → create new.
   Signing in with Google on an existing password account **links** it rather
   than creating a duplicate.
4. New OAuth accounts have no subdomain, so they land on `/welcome` to claim
   one. That claim is one-shot (`ClaimSubdomain` refuses if a sub exists).
5. Accounts with TOTP already enabled still have to pass it — the callback
   redirects to `/login?mfa=<challenge>`.

## Behaviour change worth knowing

Forced TOTP enrollment now applies to **password accounts only**. Accounts
that sign in through Google/GitHub rely on that provider's MFA instead, so
they aren't pushed into the authenticator screen.

To make 2FA mandatory for everyone again, in `landing/src/App.jsx`:

```diff
-  if (me && me.totp_enabled === false && !(me.providers || []).length) {
+  if (me && me.totp_enabled === false) {
```

## Postgres note

`OpenPostgres` runs the migrations on boot: `oauth_identities` is created, and
`users.tunnel_sub` / `users.repo_sub` become nullable (OAuth signups have no
subdomain until onboarding). No manual SQL needed. The JSON store needs
nothing — new fields default in.
