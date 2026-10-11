# Sixter VPS hardening bundle

Defense-in-depth for the layers a single VPS *can* handle. Run on the server:

```bash
cd packaging/hardening
sudo ./apply-hardening.sh
```

| File | Layer | What it does |
|------|-------|--------------|
| `sixter-sysctl.conf` | kernel (L3/L4) | SYN cookies, bigger backlogs, anti-spoof, larger conntrack |
| `sixter-nftables.nft` | network (L4) | per-IP connection cap + new-connection rate limit on :80/:443, auto-ban floods |
| `nginx-hardening.conf` | proxy (L7) | global `limit_conn`/`limit_req`, slowloris timeouts, request hygiene |

The **application layer (L7)** is handled in the Sixter tunnel server itself —
no setup needed:
- edge-wide request-rate detection → **"under attack" mode**
- per-IP, cross-tunnel **auto-ban** (escalating)
- a **"checking your browser" challenge** for untrusted clients during attacks
- live status in **admin → Overview** (`req/s`, banned IPs, DDoS guard state)

## The one thing software can't do

Volumetric floods (UDP/amplification, huge SYN floods) saturate your network
pipe *before* the server sees them. No VPS can self-defend against those. Use:

1. Your **VPS provider's built-in DDoS protection** (Hostinger KVM includes
   network-level scrubbing — check the panel), and/or
2. **Cloudflare (free)** in front of the apex domain `sixter.in`.

Everything in this bundle assumes that upstream layer handles the volumetric
part; it hardens against everything below it.
