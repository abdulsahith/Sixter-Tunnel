#!/bin/sh
# Sixter node installer.
#
#   curl -fsSL https://sixter.in/install.sh | sh -s -- --join <token>
#
# Installs the Sixter agent (AMM runtime) and enrols this machine with your
# account. The agent only makes OUTBOUND connections, so no inbound firewall
# rules, no VPC peering, and it works behind NAT — the same command on AWS,
# GCP, Azure or a server in your rack.
#
# Safe to re-run: it upgrades in place and keeps the existing enrolment.

set -eu

SIXTER_URL="${SIXTER_URL:-https://sixter.in}"
BIN_DIR="/usr/local/bin"
CONF_DIR="/etc/sixter"
JOIN_TOKEN=""
NODE_NAME="$(hostname 2>/dev/null || echo sixter-node)"

say()  { printf '\033[0;36m==>\033[0m %s\n' "$*"; }
ok()   { printf '\033[0;32m  ✓\033[0m %s\n' "$*"; }
die()  { printf '\033[0;31mError:\033[0m %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --join)  JOIN_TOKEN="${2:-}"; shift 2 ;;
    --name)  NODE_NAME="${2:-}"; shift 2 ;;
    --url)   SIXTER_URL="${2:-}"; shift 2 ;;
    --help|-h)
      cat <<EOF
Sixter node installer

  --join <token>   enrolment token from Console → Nodes → Add node
  --name <name>    friendly node name (default: this machine's hostname)
  --url  <url>     gateway URL (default: https://sixter.in)

Example:
  curl -fsSL https://sixter.in/install.sh | sh -s -- --join sx_node_abc123
EOF
      exit 0 ;;
    *) die "unknown option: $1 (try --help)" ;;
  esac
done

# ── preflight ────────────────────────────────────────────────────────────────
[ "$(id -u)" -eq 0 ] || die "run as root (use sudo)"
[ "$(uname -s)" = "Linux" ] || die "the Sixter agent requires Linux"

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "unsupported architecture: $ARCH" ;;
esac

command -v curl >/dev/null 2>&1 || die "curl is required"

# The runtime needs cgroup v2 and overlayfs. Warn rather than abort: the agent
# still runs, it just falls back to unisolated processes — and it says so.
ISOLATION="container"
if [ ! -f /sys/fs/cgroup/cgroup.controllers ]; then
  printf '\033[0;33m  !\033[0m cgroup v2 not found — containers unavailable, will run unisolated\n'
  ISOLATION="process"
fi
# overlayfs usually EXISTS as a module but isn't loaded on fresh cloud images
# (Debian/Ubuntu on EC2). Load it now and persist across reboots, so a brand
# new instance gets containers instead of silently degrading to processes.
if ! grep -q overlay /proc/filesystems 2>/dev/null; then
  modprobe overlay 2>/dev/null || true
  if grep -q overlay /proc/filesystems 2>/dev/null; then
    mkdir -p /etc/modules-load.d
    echo overlay > /etc/modules-load.d/sixter-overlay.conf
  fi
fi
if ! grep -q overlay /proc/filesystems 2>/dev/null; then
  printf '\033[0;33m  !\033[0m overlayfs not available — containers unavailable\n'
  ISOLATION="process"
fi

# WireGuard powers the private overlay: it is what lets an app on this machine
# reach a database on another. Optional — without it the node still runs
# workloads, it just can't reach private services elsewhere.
#
# apt-get install without an update first fails on fresh cloud images, whose
# package lists are stale from the day the AMI was built. That single missing
# step is why this "optional" dependency silently never installs.
if ! command -v wg >/dev/null 2>&1; then
  say "Installing WireGuard (private overlay)"
  if command -v apt-get >/dev/null 2>&1; then
    DEBIAN_FRONTEND=noninteractive apt-get update -qq >/dev/null 2>&1 || true
    DEBIAN_FRONTEND=noninteractive apt-get install -y wireguard-tools iproute2 >/dev/null 2>&1 || true
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y wireguard-tools iproute >/dev/null 2>&1 || true
  elif command -v yum >/dev/null 2>&1; then
    yum install -y wireguard-tools iproute >/dev/null 2>&1 || true
  elif command -v apk >/dev/null 2>&1; then
    apk add --no-cache wireguard-tools iproute2 >/dev/null 2>&1 || true
  fi
  if command -v wg >/dev/null 2>&1; then
    ok "wireguard-tools installed"
  else
    printf '\033[0;33m  !\033[0m could not install wireguard-tools — this node will not reach\n'
    printf '    private services (databases) on other machines. Install it manually\n'
    printf '    with: apt-get update \&\& apt-get install -y wireguard-tools\n'
  fi
fi

# The pod network needs one sysctl. Set it here so it survives reboots rather
# than only being applied when the first container starts.
if [ -d /etc/sysctl.d ]; then
  echo 'net.ipv4.ip_forward=1' > /etc/sysctl.d/99-sixter.conf
  sysctl -p /etc/sysctl.d/99-sixter.conf >/dev/null 2>&1 || true
fi

say "Installing Sixter agent (linux/$ARCH)"

# ── download ─────────────────────────────────────────────────────────────────
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

for bin in sixter-agent sixter-reposerver sixter-client sixter; do
  url="$SIXTER_URL/dl/linux-$ARCH/$bin"
  if curl -fsSL "$url" -o "$TMP/$bin" 2>/dev/null; then
    install -m 0755 "$TMP/$bin" "$BIN_DIR/$bin"
    ok "installed $bin"
  fi
done
[ -x "$BIN_DIR/sixter-agent" ] || die "could not download the agent from $SIXTER_URL"

# ── enrol ────────────────────────────────────────────────────────────────────
mkdir -p "$CONF_DIR"
chmod 0700 "$CONF_DIR"

if [ -n "$JOIN_TOKEN" ]; then
  cat > "$CONF_DIR/agent.env" <<EOF
SIXTER_GATEWAY=$SIXTER_URL
SIXTER_JOIN_TOKEN=$JOIN_TOKEN
SIXTER_NODE_NAME=$NODE_NAME
EOF
  chmod 0600 "$CONF_DIR/agent.env"
  ok "enrolment saved for node '$NODE_NAME'"
elif [ ! -f "$CONF_DIR/agent.env" ]; then
  die "no --join token given and no existing enrolment at $CONF_DIR/agent.env"
else
  ok "keeping existing enrolment"
fi

# ── service ──────────────────────────────────────────────────────────────────
cat > /etc/systemd/system/sixter-agent.service <<EOF
[Unit]
Description=Sixter node agent (AMM runtime)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=$CONF_DIR/agent.env
ExecStart=$BIN_DIR/sixter-agent
Restart=always
RestartSec=5
# The runtime creates namespaces and cgroups, which needs real root.
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable sixter-agent >/dev/null 2>&1 || true

# RESTART, not "enable --now".
#
# `--now` starts a stopped unit but leaves a running one alone, so re-running
# this installer would replace the binary on disk while the OLD process kept
# running. The upgrade silently does nothing, and every symptom you were
# chasing persists against code that has already been fixed.
systemctl restart sixter-agent >/dev/null 2>&1 || die "failed to start sixter-agent"

sleep 1
if systemctl is-active --quiet sixter-agent; then
  ok "agent running (isolation: $ISOLATION)"
else
  die "agent failed to start — check: journalctl -u sixter-agent -n 50"
fi

cat <<EOF

  Node '$NODE_NAME' is enrolled.

  It should appear in your console within a few seconds:
      $SIXTER_URL/dashboard  →  Nodes

  Useful commands:
      systemctl status sixter-agent
      journalctl -u sixter-agent -f

EOF
