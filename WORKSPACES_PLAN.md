# Sixter Workspaces — group projects into one app, team per workspace

Decisions locked in:
- A **workspace = one product/app** made of **multiple projects** (e.g. *E-commerce*
  = a frontend project + a backend project).
- **Team is per workspace**: each workspace has its own **lead + backend team +
  frontend team**. A backend dev on *E-commerce* is not automatically on your
  other apps.

This sits on top of everything already built (CI/CD, SSO identity, roles, tracks,
activity, presence). The only new core idea is the **Workspace container** and
moving team membership from global → per-workspace.

---

## Model

```
account (owner)
   └──< Workspace  (E-commerce)
          ├── projects: storefront (track: frontend), api (track: backend), …
          └── members:  lead + backend devs + frontend devs   (per-workspace roles)
```

- **Workspace** lives on the repo-server (it groups the repo-server's projects):
  `{ id, name, created }`.
- **Project → workspace**: each project belongs to one workspace (or "Unassigned").
  Project keeps its existing **track** (frontend / backend / shared) *within* the
  workspace.
- **Membership is per workspace**: `workspace_members { workspace, email, role }`.
  Role ∈ lead | backend | frontend | viewer.

### Who authenticates vs who authorizes (important)

- **Gateway = identity.** SSO already proves "this request is `dev2@…`" (Phase 2).
  No change needed there.
- **Repo-server = authorization.** It owns workspaces + per-workspace membership,
  so it answers "what is `dev2@…`'s role in the workspace that owns this project?"
  This cleanly decouples the two: gateway says *who you are*, repo-server says
  *what you can do here*.

Result for any request on project P:
1. owner/token → full access (lead of everything).
2. else SSO identity = email → find P's workspace W → look up the member's role in W.
3. apply role caps (Phase 2) **and** track scope (Phase 3) within W.
4. no membership in W → no access.

---

## Phase W1 — Workspace container (repo-server + dashboard)

- Store: `workspaces.json` (`{id,name}`) + project→workspace assignment (extend the
  track store, or a sibling map). Default existing projects → "Unassigned".
- API (lead-only): create / rename / delete workspace; assign a project to a
  workspace.
- Dashboard: the project list becomes **grouped by workspace** — a collapsible
  "E-commerce" section showing its frontend + backend projects with their track
  badges. An "Unassigned" group holds the rest.

*Deliverable:* you can create *E-commerce*, drop the frontend + backend projects
into it, and see them grouped as one app.

---

## Phase W2 — Per-workspace teams + authorization

- Move membership to the workspace: `workspace_members { workspace, email, role }`
  on the repo-server (lead-managed from the dashboard).
- The repo-server resolves a member's role from **(email, workspace-of-the-project)**
  instead of the global team. The gateway SSO still supplies identity.
- RBAC gate (Phase 2/3) now keys on the **workspace role** + project track.
- Dashboard: a **"Members" panel inside each workspace** — the lead adds a teammate
  by email and picks frontend/backend/viewer **for that workspace**.

*Deliverable:* *E-commerce* has its own lead + backend + frontend team; a backend
member sees only *E-commerce*'s backend (+ shared) projects, nothing from your
other workspaces.

> Note: the landing **Team** page (Phase 1, global team) becomes optional — it can
> stay as "people who can access the account at all", or we retire it in favour of
> per-workspace membership. Recommended: per-workspace is the source of truth.

---

## Phase W3 — Workspace home (the "app" view)

- A single page per workspace: status of every project in it (frontend + backend
  health, domains, last deploy), the team, and **workspace-scoped activity** (the
  Phase 4 feed filtered to this app).
- "Deploy all" / per-project deploy from one place; CI/CD per project is unchanged
  (each upload still auto-builds + deploys itself).

*Deliverable:* one screen that *is* the product — see the whole E-commerce app,
who's working on it, and ship it.

---

## What this reuses (so it's not from scratch)

- **CI/CD** — unchanged; each project auto-deploys on upload as today.
- **SSO identity** (Phase 2) — unchanged; supplies the member's email.
- **Roles + caps** (Phase 2) — reused, now resolved per workspace.
- **Tracks** (Phase 3) — reused, now scoped within a workspace.
- **Activity + presence** (Phase 4/5) — reused, filtered per workspace.

The genuinely new code is: the Workspace container (W1) and per-workspace
membership/authorization (W2). W3 is assembly of existing data into one view.

---

## Build order

1. **W1** — workspace container + grouped dashboard (safe, additive; projects
   default to Unassigned so nothing breaks).
2. **W2** — per-workspace teams + authorization (the meaty one; owner always
   retains full access).
3. **W3** — the workspace home view (polish that makes it feel like a product hub).
