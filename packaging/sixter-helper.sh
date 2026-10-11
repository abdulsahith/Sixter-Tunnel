#!/bin/bash
# Sixter privileged deploy helper. Installed as root at /usr/local/bin/sixter-helper
# and granted to the (non-root) sixter user via a single passwordless sudoers entry.
# It performs ONLY the specific privileged steps the repo-server needs, reading
# pre-staged files from /opt/sixter (sixter-owned). This avoids running the whole
# server as root.
set -e

SIXTER_DIR="${SIXTER_DIR:-/opt/sixter}"

# only allow safe names (letters, digits, dot, dash, underscore)
safe() { printf '%s' "$1" | grep -Eq '^[A-Za-z0-9._-]+$'; }

case "$1" in
  nginx)
    d="$2"; safe "$d" || { echo "bad domain"; exit 2; }
    src="$SIXTER_DIR/nginx/$d.conf"
    [ -f "$src" ] || { echo "staged config not found: $src"; exit 3; }
    install -m 644 "$src" "/etc/nginx/sites-available/$d.conf"
    ln -sf "/etc/nginx/sites-available/$d.conf" "/etc/nginx/sites-enabled/$d.conf"
    nginx -t
    systemctl reload nginx
    echo "nginx: $d enabled and reloaded"
    ;;

  rmnginx)
    d="$2"; safe "$d" || { echo "bad domain"; exit 2; }
    rm -f "/etc/nginx/sites-enabled/$d.conf" "/etc/nginx/sites-available/$d.conf"
    nginx -t && systemctl reload nginx
    echo "nginx: $d removed"
    ;;

  certbot)
    d="$2"; e="$3"
    safe "$d" || { echo "bad domain"; exit 2; }
    certbot --nginx -d "$d" --non-interactive --agree-tos --redirect -m "$e"
    echo "certbot: $d issued"
    ;;

  service)
    p="$2"; safe "$p" || { echo "bad project"; exit 2; }
    src="$SIXTER_DIR/services/sixter-$p.service"
    [ -f "$src" ] || { echo "staged unit not found: $src"; exit 3; }
    install -m 644 "$src" "/etc/systemd/system/sixter-$p.service"
    systemctl daemon-reload
    systemctl enable --now "sixter-$p"
    echo "service: sixter-$p started"
    ;;

  restart)
    p="$2"; safe "$p" || { echo "bad project"; exit 2; }
    systemctl restart "sixter-$p"
    echo "service: sixter-$p restarted"
    ;;

  rmservice)
    p="$2"; safe "$p" || { echo "bad project"; exit 2; }
    systemctl disable --now "sixter-$p" 2>/dev/null || true
    rm -f "/etc/systemd/system/sixter-$p.service"
    systemctl daemon-reload
    echo "service: sixter-$p removed"
    ;;

  banip)
    ip="$2"; printf '%s' "$ip" | grep -Eq '^[0-9a-fA-F:.]+$' || { echo "bad ip"; exit 2; }
    nft add element inet sixter flood_ban "{ $ip timeout 1h }"
    echo "banned $ip"
    ;;

  *)
    echo "usage: sixter-helper {nginx <domain>|rmnginx <domain>|certbot <domain> <email>|service <project>|restart <project>|rmservice <project>}"
    exit 1
    ;;
esac
