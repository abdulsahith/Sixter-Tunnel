#!/usr/bin/env bash
# Build the Sixter .deb on a Linux machine (your VPS or WSL).
# Requires: go, npm, dpkg-deb. Run from the project root:  ./packaging/build-deb.sh
set -e

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
VERSION="2.0.0"
TREE="packaging/sixter-deb"
BIN="$TREE/usr/local/bin"

echo "==> 1/4  Building frontends (embedded into the binaries)"
if command -v npm >/dev/null 2>&1; then
  ( cd landing   && npm install --no-audit --no-fund && npm run build )
  ( cd dashboard && npm install --no-audit --no-fund && npm run build )
  rm -rf cmd/tunnel-server/dist && cp -r landing/dist   cmd/tunnel-server/dist
  rm -rf cmd/repo-server/dist   && cp -r dashboard/dist cmd/repo-server/dist
else
  echo "    npm not found — using the dist folders already in cmd/*/dist"
fi

echo "==> 2/4  Compiling linux/amd64 binaries (static, no cgo)"
export GOOS=linux GOARCH=amd64 CGO_ENABLED=0
go mod tidy
mkdir -p "$BIN"
go build -buildvcs=false -ldflags="-s -w" -o "$BIN/sixter"            ./cmd/sixter
go build -buildvcs=false -ldflags="-s -w" -o "$BIN/sixter-client"     ./cmd/tunnel-client
go build -buildvcs=false -ldflags="-s -w" -o "$BIN/sixter-server"     ./cmd/tunnel-server
go build -buildvcs=false -ldflags="-s -w" -o "$BIN/sixter-repo"       ./cmd/repo-client
go build -buildvcs=false -ldflags="-s -w" -o "$BIN/sixter-reposerver" ./cmd/repo-server
rm -f "$BIN/.keep"

echo "==> 2b   Bundling the VPS hardening files"
SHARE="$TREE/usr/local/share/sixter/hardening"
mkdir -p "$SHARE"
cp packaging/hardening/sixter-sysctl.conf \
   packaging/hardening/sixter-nftables.nft \
   packaging/hardening/nginx-hardening.conf \
   packaging/hardening/apply-hardening.sh \
   packaging/hardening/README.md "$SHARE"/
chmod 0755 "$SHARE/apply-hardening.sh"

echo "==> 3/4  Setting permissions"
chmod 0755 "$TREE/DEBIAN/postinst" "$TREE/DEBIAN/prerm"
chmod 0755 "$BIN"/*

echo "==> 4/4  Building the package"
OUT="sixter_${VERSION}_amd64.deb"
dpkg-deb --build --root-owner-group "$TREE" "$OUT"
echo ""
echo "    Done -> $OUT"
echo "    Install with:  sudo dpkg -i $OUT"
