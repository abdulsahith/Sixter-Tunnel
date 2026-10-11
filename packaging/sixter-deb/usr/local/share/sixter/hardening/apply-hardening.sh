#!/bin/bash
# Apply the Sixter VPS hardening bundle (kernel + nftables + nginx).
# Run as root from this directory:  sudo ./apply-hardening.sh
set -e
cd "$(dirname "$0")"

echo "[1/3] kernel sysctl…"
install -m 644 sixter-sysctl.conf /etc/sysctl.d/99-sixter.conf
sysctl --system >/dev/null
echo "    ✓ applied"

echo "[2/3] nftables edge rules…"
install -m 644 sixter-nftables.nft /etc/sixter-nftables.nft
if command -v nft >/dev/null 2>&1; then
    nft -f /etc/sixter-nftables.nft
    # persist across reboots: load our file from the main nftables config
    if [ -f /etc/nftables.conf ] && ! grep -q '/etc/sixter-nftables.nft' /etc/nftables.conf; then
        echo 'include "/etc/sixter-nftables.nft"' >> /etc/nftables.conf
    fi
    systemctl enable nftables >/dev/null 2>&1 || true
    echo "    ✓ loaded + persisted"
else
    echo "    ! nft not installed — run: apt install nftables, then re-run this script"
fi

echo "[3/3] nginx hardening…"
if command -v nginx >/dev/null 2>&1; then
    install -m 644 nginx-hardening.conf /etc/nginx/conf.d/sixter-hardening.conf
    if nginx -t >/dev/null 2>&1; then
        systemctl reload nginx
        echo "    ✓ applied + reloaded"
    else
        echo "    ! nginx -t failed — removing snippet so nginx stays up"
        rm -f /etc/nginx/conf.d/sixter-hardening.conf
        nginx -t || true
    fi
else
    echo "    ! nginx not installed — skipped"
fi

echo
echo "Done. App-layer DDoS protection (rate limit, auto-ban, browser challenge)"
echo "is built into the Sixter tunnel server. For VOLUMETRIC (L3/L4) floods you"
echo "still need upstream scrubbing — your VPS provider's DDoS protection or"
echo "Cloudflare in front of the apex domain."
