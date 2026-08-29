#!/usr/bin/env bash
# Provision native language runtimes into a target directory (default /opt/runtimes).
# Designed for NAT VPS / restricted containers: tarball-only installs, no PPAs,
# no systemd requirement, no network namespaces.
#
# Usage: sudo bash provision-runtimes.sh [/opt/runtimes] [node|python|java|all]
set -euo pipefail

DEST="${1:-/opt/runtimes}"
WHAT="${2:-all}"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64) GOARCH="amd64" ;;
  aarch64|arm64) GOARCH="arm64" ;;
  *) echo "unsupported arch: $ARCH" >&2; exit 1 ;;
esac

log() { printf '\033[1;36m[runtimes]\033[0m %s\n' "$*"; }

fetch() { # fetch <url> <dest>
  curl -fsSL "$1" -o "$2"
}

install_node() { # install_node <ver> <destdir>
  local ver="$1" dest="$2" url
  url="https://nodejs.org/dist/v${ver}/node-v${ver}-linux-${GOARCH}.tar.gz"
  log "node v${ver} -> ${dest}"
  mkdir -p "$(dirname "$dest")"
  fetch "$url" "/tmp/node-${ver}.tgz"
  mkdir -p "$dest"
  tar -xzf "/tmp/node-${ver}.tgz" -C "$dest" --strip-components=1
  rm -f "/tmp/node-${ver}.tgz"
}

install_python() { # install_python <ver> <destdir>
  local ver="$1" dest="$2" major minor
  IFS=. read -r major minor _ <<< "$ver"
  local url="https://github.com/indygreg/python-build-standalone/releases/latest/download/cpython-${ver}%2B20240415-x86_64-unknown-linux-gnu-install_only.tar.gz"
  # fallback to a known-good pinned release
  url="https://github.com/indygreg/python-build-standalone/releases/download/20240415/cpython-${ver}%2B20240415-${GOARCH/x86_64/x86_64}-unknown-linux-gnu-install_only.tar.gz"
  url="https://github.com/astral-sh/python-build-standalone/releases/download/20240415/cpython-${ver}%2B20240415-x86_64-unknown-linux-gnu-install_only.tar.gz"
  log "python v${ver} -> ${dest}"
  fetch "$url" "/tmp/python-${ver}.tgz"
  mkdir -p "$dest"
  tar -xzf "/tmp/python-${ver}.tgz" -C "$dest" --strip-components=1
  rm -f "/tmp/python-${ver}.tgz"
}

install_java() { # install_java <ver> <destdir>
  local ver="$1" dest="$2"
  local url="https://api.adoptium.net/v3/binary/latest/${ver}/ga/linux/${GOARCH}/jdk/hotspot/normal/eclipse"
  log "java v${ver} -> ${dest}"
  fetch "$url" "/tmp/java-${ver}.tgz"
  mkdir -p "$dest"
  tar -xzf "/tmp/java-${ver}.tgz" -C "$dest" --strip-components=1
  rm -f "/tmp/java-${ver}.tgz"
}

case "$WHAT" in
  all|node)
    install_node 20.18.1 "$DEST/node20"
    install_node 22.11.0 "$DEST/node22" ;;
esac
case "$WHAT" in
  all|python)
    install_python 3.11.9 "$DEST/python311"
    install_python 3.12.3 "$DEST/python312" ;;
esac
case "$WHAT" in
  all|java)
    install_java 17 "$DEST/java17"
    install_java 21 "$DEST/java21" ;;
esac

log "Runtimes installed under $DEST:"
ls -1 "$DEST" 2>/dev/null || true
