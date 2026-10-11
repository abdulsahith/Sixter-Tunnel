# AMM — Application Machine Monitor

Sixter's application runtime and fleet control plane.

AMM runs deployed applications in isolated containers, pulls portable OCI
images, spreads workloads across many machines, and publishes each one through
the Sixter gateway — with the WAF, TLS and rate limiting already in front of it.

---

## 1. Design position

> **AMM is not trying to beat containers on CPU.** A container is a process;
> in steady state its CPU overhead is ~0. The costs worth attacking are
> **startup latency, the I/O path, and memory density** — everything here is
> organised around those, not around raw compute.

What makes AMM different is not the isolation primitive (namespaces and cgroups
are 2008 technology). It's that Sixter owns **both ends of the connection** —
the gateway and the runtime — which no general-purpose orchestrator does.

| Concern | Kubernetes | Sixter + AMM |
|---|---|---|
| Ingress | ingress-nginx | gateway (already there) |
| TLS | cert-manager | gateway |
| WAF | third-party / none | built in |
| CI/CD | ArgoCD / Flux | push → PR → deploy |
| Networking to nodes | API server reachable, CNI | agent dials **out** |
| Orchestration | Kubernetes | AMM |

Six components and a team, versus one binary.

---

## 2. System architecture

```
                            ┌──────────────────────────────┐
   browser ────────────────▶│      SIXTER GATEWAY          │
   app.sixter.in            │  (cmd/tunnel-server)         │
                            │                              │
                            │  TLS · WAF · rate limit      │
                            │  node registry               │
                            │  scheduler (placement)       │
                            │  assignment store            │
                            │  subdomain router            │
                            └───────┬──────────────┬───────┘
                                    │              │
                    heartbeat (10s) │              │ reverse tunnels
                    outbound only   │              │ (traffic path)
                       ┌────────────┴──┐       ┌───┴────────────┐
                       ▼               ▼       ▼                ▼
              ┌─────────────┐  ┌─────────────┐  ┌─────────────┐
              │  NODE 1     │  │  NODE 2     │  │  NODE 3     │
              │ sixter-agent│  │ sixter-agent│  │ sixter-agent│
              │  ├ AMM      │  │  ├ AMM      │  │  ├ AMM      │
              │  ├ images   │  │  ├ images   │  │  ├ images   │
              │  └ routes   │  │  └ routes   │  │  └ routes   │
              │             │  │             │  │             │
              │ [container] │  │ [container] │  │ [container] │
              │ [container] │  │             │  │ [container] │
              └─────────────┘  └─────────────┘  └─────────────┘
                 AWS EC2          GCP VM          bare metal
```

**Every arrow points outward from the nodes.** The gateway never connects into
a customer's network: no inbound firewall rules, no VPC peering, works behind
NAT, identical on every cloud.

---

## 3. The runtime (single machine)

### 3.1 Tiers

One `Runtime` interface, three backends, chosen by policy — never by editing
call sites.

| Tier | Isolation | Use |
|---|---|---|
| `TierProcess` | none (unix users only) | fallback when the host can't do better |
| `TierContainer` | namespaces + cgroups + overlay | **default** — own/teammate code |
| `TierVM` | hardware boundary | future: untrusted code (integrate Firecracker, never hand-roll a VMM) |

If a host lacks root, cgroup v2 or overlayfs, AMM **logs why and degrades** —
it never silently pretends to isolate.

### 3.2 How a container is created

```
  parent (agent / repo-server)
    │ 1. pull OCI image (if specified)      → shared, read-only rootfs
    │ 2. overlay the project dir            → copy-on-write /app layer
    │ 3. create cgroup v2, write limits
    │ 4. re-exec /proc/self/exe as "amm-init"
    │    with CLONE_NEWNS|NEWPID|NEWUTS|NEWIPC [|NEWNET]
    │    and CgroupFD (kernel places child in cgroup at clone time)
    ▼
  amm-init  = PID 1 inside the new namespaces
    │ 5. make mount propagation private
    │ 6. bind image dirs (or host /bin,/usr,/lib) read-only into new root
    │ 7. bind the copy-on-write /app
    │ 8. pivot_root, unmount old root      → host filesystem unreachable
    │ 9. mount /proc /sys /dev /tmp
    │ 10. PR_SET_NO_NEW_PRIVS, drop 12 capabilities
    │ 11. chdir /app
    ▼
  exec the application (becomes PID 1)
```

Go cannot `fork()` safely (multi-threaded runtime), so the re-exec is the
standard pattern — the same one runc uses.

### 3.3 Resource limits (cgroup v2)

| Control | Behaviour |
|---|---|
| `cpu.weight` (default) | proportional share — only slows under contention |
| `cpu.max` (opt-in) | hard quota — **freezes the app until the next 100 ms window** |
| `memory.high` | throttle + reclaim at 90% of the limit |
| `memory.max` | hard ceiling (OOM) |
| `pids.max` | fork-bomb guard (default 512) |

Two decisions worth knowing:

- **`cpu.weight` is the default.** A hard quota produces the classic container
  p99 latency cliff. AMM surfaces `nr_throttled` in the UI so if you *do* set a
  cap, the damage is visible rather than mysterious.
- **`memory.high` before `memory.max`.** For a GC'd runtime that's the
  difference between "collected and carried on" and "OOM-killed for a spike it
  would have cleaned up".

Limits are written **idempotently** — setting a value back to 0 writes `max`,
clearing it. (Not doing this caused a real bug: throttling with no limit set.)

### 3.4 Statistics

Read from cgroup counters — **measured, not estimated**: `cpu.stat`,
`memory.current`, `pids.current`, plus throttling periods and stalled time.

---

## 4. Images (portability)

Without an image, a container's root is bind-mounted from **that host's**
`/bin`, `/usr`, `/lib`. It runs, but it is welded to that machine — nothing to
send anywhere, different behaviour on a different distro.

An image makes it portable, which is the hard prerequisite for multi-node.

```
  ParseImageRef("node:20-slim")
        │  registry-1.docker.io / library/node : 20-slim
        ▼
  anonymous token  (WWW-Authenticate → realm + scope)
        ▼
  manifest  (OCI + Docker media types; multi-arch index → this arch)
        ▼
  layers ──▶ <cache>/blobs/<digest>        content-addressed, shared
        ▼
  extract ─▶ <cache>/rootfs/<image>/       flattened, whiteouts applied
```

- Standard OCI protocol — every registry works (Docker Hub, GHCR, ECR, GCR).
  Inventing an image format would be "rebuilding Docker, badly".
- Layers **flattened** into one rootfs: slightly less dedup, dramatically
  simpler whiteout handling, extraction happens once per image.
- Concurrent pulls of the same image collapse into one download.
- Path traversal in tar entries is rejected — a malicious image must not write
  outside its rootfs.

---

## 5. Fleet (multiple machines)

### 5.1 Enrolment

```
  Console → Nodes → Add node        issues ONE permanent fleet token
        │
  machine:  curl -fsSL https://sixter.in/install.sh | sh -s -- --join <token>
        │     ├ downloads sixter-agent (+ client, cli)
        │     ├ writes /etc/sixter/agent.env (0600)
        │     ├ installs systemd unit
        │     └ starts the agent
        ▼
  agent → POST /api/node/enroll { token, name, os, arch, cpus, mem }
        ◀ { node_id, secret }        stored at /etc/sixter/node.json (0600)
```

The fleet token identifies the **account**; each machine exchanges it for its
own credentials. A token pasted into cloud-init can enrol a node but **cannot
impersonate one**. The secret is stored hashed.

**Zero-touch scaling** — the same one-liner in cloud-init `user-data` /
startup script means autoscaler-created machines enrol themselves.

### 5.2 Control loop

```
  agent ──POST /api/node/heartbeat──▶ gateway      every 10s
         { cpu, mem, load, instances, report[] }
         ◀───────────────────────────
         { assignments[] }   ← FULL desired state for this node

  agent reconciles:
     in desired, not running  → pull image, start container, publish route
     running, not in desired  → stop route, destroy container
     spec changed             → restart with the new spec
```

The heartbeat response **is** the control channel: agents dial out and may be
behind NAT, so the gateway can never push. Because it's a full-state diff
rather than an event stream, a missed message, an agent restart or a gateway
restart all **self-heal on the next beat**.

### 5.3 Placement

Least-loaded-first, weighting **memory over CPU** (you can share a core; you
cannot share a byte). Nodes that can't isolate are used only as a last resort,
never silently preferred for being idle.

```go
score = (100-memPct)*2 + (100-cpuPct) - placements*10
```

### 5.4 Routing — the part that needed no new proxy

A fleet node has no inbound port. So each assignment gets a **subdomain +
route token**, and the agent publishes the container by opening a reverse
tunnel with the existing `sixter-client` binary:

```
  browser → api-acme.sixter.in
       │
  gateway   TLS · WAF · rate limit
       │    tunnels[subdomain] lookup   ← the SAME routing tunnels already use
       ▼
  reverse tunnel (agent dialled out)
       │
  node → container :3000
```

Because the gateway holds every backend connection, **load balancing,
health-based removal and zero-downtime rolling deploys** fall out of the same
routing table.

---

## 6. Component map

| Path | Role |
|---|---|
| `internal/amm/runtime.go` | `Runtime` interface, `Spec`, `Instance`, `Stats`, tiers |
| `internal/amm/container_linux.go` | namespaces, cgroups v2, overlayfs, exec |
| `internal/amm/init_linux.go` | container PID 1: rootfs assembly, pivot_root, cap drop |
| `internal/amm/image.go` | OCI pull, layer cache, whiteout-aware extraction |
| `internal/amm/process.go` | unisolated fallback tier |
| `internal/amm/supervisor.go` | tier selection, graceful degradation |
| `internal/auth/nodes.go` | node registry types + store interface |
| `internal/auth/assignments.go` | desired state, scheduler, route tokens |
| `internal/auth/node_handlers.go` | enrol / heartbeat / fleet APIs |
| `cmd/sixter-agent/` | node agent: enrol, heartbeat, reconcile, publish |
| `cmd/tunnel-server/` | gateway: control plane, router, `/dl/` distribution |
| `cmd/repo-server/amm_integration.go` | per-project AMM toggle, single-node deploys |
| `landing/public/install.sh` | one-line node installer |
| `landing/src/.../NodesPage.jsx` | fleet + workload UI |

---

## 7. Two ways to run an app

**Single machine** (repo dashboard → project → **AMM** tab)
Toggle AMM on; that project's deploys become containers. Toggle off and it
returns to a normal service — a real rollback.

**Fleet** (console → **Nodes** → Fleet workloads)
Name + image + command + port → scheduler places it → agent runs and publishes
it within ~10s.

A fleet workload **requires an image** — the target node has nothing to pull
otherwise. The API rejects it explicitly rather than failing later.

---

## 8. Security posture

| Layer | Control |
|---|---|
| Namespaces | mount, pid, uts, ipc (net optional) |
| Root filesystem | `pivot_root`; host filesystem unreachable |
| Capabilities | 12 dangerous caps dropped, `no_new_privs` |
| Image layers | mounted read-only, shared between containers |
| cgroups | memory, CPU, pid ceilings |
| Node credentials | per-node, stored hashed, fleet token can't impersonate |
| Traffic | every request passes the gateway WAF before reaching a container |

**Honest limit:** namespaces + cgroups are a **shared-kernel** boundary. Good
for first-party and teammate code (the stated threat model). It is *not*
sufficient for hostile multi-tenancy — that needs gVisor or a microVM, which is
what `TierVM` is reserved for.

---

## 9. Status

**Working / built**
- Container runtime: namespaces, cgroups v2, overlay rootfs, capability drop
- Real per-project CPU/memory from cgroup counters
- OCI image pull, cache and extraction
- Per-project AMM toggle with live stats and throttling detection
- Node enrolment, heartbeat, fleet registry, one-line installer
- Placement scheduler and reconciliation loop
- Reverse-tunnel routing for fleet workloads

**Not built yet**
- `TierVM` (Firecracker integration)
- Private network namespace + veth (fleet apps use host networking)
- Multi-replica scaling and rolling deploys across nodes
- Stateful workloads / volume scheduling
- Shared-memory data plane (gateway ↔ container zero-copy)

**Verification caveat:** the container, image and fleet paths are written and
statically checked, but **not executed end-to-end** — they need root on real
Linux. Treat the first deployment as a shakeout. The likeliest first snags are
OCI pull against a real registry, and `sixter-client` being on `PATH` for the
agent to spawn (apps run but stay unreachable if it isn't).

---

## 10. Roadmap

1. **Shared-memory data plane** — replace the gateway↔container TCP hop with a
   shared ring buffer. ~10–20× lower latency on every request, and only Sixter
   can do it because only Sixter owns both ends. *The strongest remaining
   technical claim.*
2. **Preview environments per pull request** — ephemeral container + URL per
   PR, torn down on merge. All the pieces now exist.
3. **Snapshot / pre-warm** — sub-10 ms starts via checkpointed instances.
4. **`TierVM`** — Firecracker for untrusted code.
