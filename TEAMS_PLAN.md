# Sixter Teams — "Coworking Space" for the Repo Server

Turn a repo-server from a single-owner tool into a team workspace: members with
roles (lead / backend / frontend / viewer), project access by **track tag**,
sign-in via **SSO with the existing Sixter account**, plus the live "coworking"
touches (activity feed, presence, deploy locks).

Decisions locked in: **track-tag access** (not per-project assignment) and
**SSO** (members use their Sixter login, not a second identity system).

---

## Core model

A **team = the owner of a repo subdomain.** Whoever owns `sahith-repo` owns that
repo-server's team. Members are accounts added to that team with a role. The
gateway (which already holds all accounts in Postgres) is the identity + role
authority; the repo-server asks it "who is this, and what's their role here?"

```
account (exists)  ──< team_member >──  team (= owner of a repo sub)
project (repo-server)  ── track: frontend | backend | shared
role: lead | backend | frontend | viewer
```

### Roles → permissions

| Capability                         | Lead | Backend | Frontend | Viewer |
|------------------------------------|:----:|:-------:|:--------:|:------:|
| View projects / status / logs      |  ✔   |  track  |  track   |   ✔    |
| Deploy / rollback                  |  ✔   |  track  |  track   |        |
| Env vars                           |  ✔   |  track  |  track   |        |
| Databases                          |  ✔   |    ✔   |          |        |
| Terminal                           |  ✔   |  track  |          |        |
| Domains / Tunnel mode              |  ✔   |         |          |        |
| Firewall                           |  ✔   |         |          |        |
| Manage members / roles             |  ✔   |         |          |        |

"track" = only projects tagged with that member's track (plus `shared`).

---

## Phase 1 — Teams & roles foundation (gateway)

**Postgres (internal/auth):**
- `teams(id, owner_email, name)` — one per owner.
- `team_members(team_id, member_email, role, added_at)`.

**Store methods + interface (`storage.go`, `store.go`, `postgres.go`):**
`AddMember(owner, member, role)`, `RemoveMember`, `SetRole`, `ListMembers(owner)`,
`MemberRole(owner, member) (role, ok)`.

**Gateway APIs (authMux, owner-only):**
`GET/POST/DELETE /api/team/members`, `POST /api/team/role`.

**Landing UI:** a "Team" page — invite by email, set role, list, remove.

*Deliverable:* an owner can build a team. No enforcement yet.

---

## Phase 2 — SSO login + RBAC on the repo dashboard

**The key change:** repo dashboard moves from one shared `uploadToken` to
per-user identity.

1. **Cookie scope** — set the session cookie for `.sixter.in` (domain-wide) so it
   reaches `*.sixter.in` repo subdomains. (Check/adjust `SessionCookie`.)
2. **Introspection endpoint** on the gateway:
   `GET /api/session/introspect?repo=<sub>` → `{email, role, track-perms}` for the
   caller's session, resolving repoSub → owner → membership.
3. **Repo-server auth middleware** — on each request: read the `.sixter.in`
   session cookie, call the gateway introspect (cache ~30s), attach
   `{email, role}` to the request. Keep `uploadToken` for the **CLI/machine**
   push path (`sixter repo`) — humans use SSO, machines use the token.
4. **Role gate** — map each repo-server API to a required permission; reject
   with 403 if the role lacks it.
5. **Dashboard UI** — show the signed-in member + role; hide controls they can't use.

*Deliverable:* teammates log in with their own account and only see/do what their
role allows.

---

## Phase 3 — Project tracks + scoped visibility

- Add `track` (`frontend|backend|shared`, default `shared`) to each project's
  config in the repo-server; lead sets it in the UI.
- Filter the project list and gate actions by `(role, track)`.

*Deliverable:* frontend devs see frontend + shared projects; backend devs see
backend + shared; lead/viewer see everything.

---

## Phase 4 — Activity feed + audit log

- Append-only log on every mutating action (deploy, rollback, env change, DB op,
  domain change) with `actor, action, project, timestamp`.
- Dashboard "Activity" tab = a team timeline. Doubles as a security audit trail.

---

## Phase 5 — Real-time coworking polish

- **Presence** (WebSocket): who's online and which project they're viewing.
- **Deploy locks**: a per-project mutex with holder identity — "🔒 deploy in
  progress by Arjun" — so two people don't clobber a project.
- **Team-visible live deploy logs**: the SSE deploy stream you already have,
  surfaced to the whole team.
- **Notifications**: deploy success/fail in-app first; Slack/email later.

---

## Key technical notes / risks

- **Cross-subdomain cookie**: SSO hinges on the session cookie being valid for
  `.sixter.in`. Verify the cookie `Domain` is set wide enough.
- **Repo-server ↔ gateway dependency**: introspection adds a network hop; cache
  results briefly and fail safe (deny on gateway-unreachable, except the owner's
  cached token).
- **Dual auth**: keep token auth for CLI/CI uploads; add SSO for humans. Don't
  break the existing `sixter repo` push flow.
- **Backwards compatible**: a repo-server with no team behaves exactly as today
  (owner-only). Teams are additive.

---

## Suggested build order

1. Phase 1 (teams + roles in the gateway + Team UI) — safe, additive, no
   enforcement risk.
2. Phase 2 (SSO + RBAC) — the meaty one; gated carefully so the owner is never
   locked out.
3. Phase 3 (tracks).
4. Phases 4–5 (audit + real-time) — the polish that makes it feel like a
   coworking space.
