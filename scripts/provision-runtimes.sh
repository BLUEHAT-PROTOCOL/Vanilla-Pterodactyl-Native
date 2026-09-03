#!/usr/bin/env bash
# Provision native language runtimes into a target directory (default /opt/runtimes).
# Designed for NAT VPS / restricted containers: tarball-only installs, no PPAs,
# no systemd requirement, no network namespaces.
#
# Architecture naming per vendor (do NOT reuse one variable across vendors):
#   x86_64: GOARCH=amd64  NODEARCH=x64      JAVAARCH=x64      PYARCH=x86_64
#   arm64:  GOARCH=arm64  NODEARCH=arm64    JAVAARCH=aarch64  PYARCH=aarch64
#
# After every install the real binary is version-checked: a directory that
# exists is NOT proof of a healthy runtime.
#
# Usage: sudo bash provision-runtimes.sh [/opt/runtimes] [node|python|java|all]
# Override versions via env: NODE20_VERSION NODE22_VERSION NODE24_VERSION
#                            PY311_VERSION PY312_VERSION JAVA17_VERSION JAVA21_VERSION
set -euo pipefail

DEST="${1:-/opt/runtimes}"
WHAT="${2:-all}"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)
    GOARCH="amd64"; NODEARCH="x64"; JAVAARCH="x64"; PYARCH="x86_64" ;;
  aarch64|arm64)
    GOARCH="arm64"; NODEARCH="arm64"; JAVAARCH="aarch64"; PYARCH="aarch64" ;;
  *) echo "unsupported arch: $ARCH" >&2; exit 1 ;;
esac

# Defaults are known-good releases verified against the official indexes
# (nodejs.org/dist, astral-sh/python-build-standalone, Adoptium API).
NODE20_VERSION="${NODE20_VERSION:-20.20.2}"
NODE22_VERSION="${NODE22_VERSION:-22.23.2}"
NODE24_VERSION="${NODE24_VERSION:-24.20.0}"
PY311_VERSION="${PY311_VERSION:-3.11.9}"
PY312_VERSION="${PY312_VERSION:-3.12.3}"
JAVA17_VERSION="${JAVA17_VERSION:-17}"
JAVA21_VERSION="${JAVA21_VERSION:-21}"
# python-build-standalone release tag
PBS_TAG="${PBS_TAG:-20240415}"

log() { printf '[runtimes] %s\n' "$*"; }
die() { printf '[error] %s\n' "$*" >&2; exit 1; }

fetch() { # fetch <url> <dest>
  curl -fsSL "$1" -o "$2" || die "download failed: $1"
}

install_node() { # install_node <ver> <destdir>
  local ver="$1" dest="$2" url
  url="https://nodejs.org/dist/v${ver}/node-v${ver}-linux-${NODEARCH}.tar.gz"
  log "node v${ver} -> ${dest}"
  mkdir -p "$(dirname "$dest")"
  fetch "$url" "/tmp/node-${ver}.tgz"
  mkdir -p "$dest"
  tar -xzf "/tmp/node-${ver}.tgz" -C "$dest" --strip-components=1
  rm -f "/tmp/node-${ver}.tgz"
  log "verify: $("$dest/bin/node" --version)"
}

install_python() { # install_python <ver> <destdir>
  local ver="$1" dest="$2" url
  # python-build-standalone uses target-triple naming: x86_64/aarch64-unknown-linux-gnu
  url="https://github.com/astral-sh/python-build-standalone/releases/download/${PBS_TAG}/cpython-${ver}%2B${PBS_TAG}-${PYARCH}-unknown-linux-gnu-install_only.tar.gz"
  log "python v${ver} -> ${dest}"
  fetch "$url" "/tmp/python-${ver}.tgz"
  mkdir -p "$dest"
  tar -xzf "/tmp/python-${ver}.tgz" -C "$dest" --strip-components=1
  rm -f "/tmp/python-${ver}.tgz"
  log "verify: $("$dest/bin/python3" --version)"
}

install_java() { # install_java <ver> <destdir>
  local ver="$1" dest="$2" url
  # Adoptium API uses x64/aarch64 naming and follows redirects to the real artifact
  url="https://api.adoptium.net/v3/binary/latest/${ver}/ga/linux/${JAVAARCH}/jdk/hotspot/normal/eclipse"
  log "java v${ver} -> ${dest}"
  fetch "$url" "/tmp/java-${ver}.tgz"
  mkdir -p "$dest"
  tar -xzf "/tmp/java-${ver}.tgz" -C "$dest" --strip-components=1
  rm -f "/tmp/java-${ver}.tgz"
  log "verify: $("$dest/bin/java" -version 2>&1 | head -1)"
}

case "$WHAT" in
  all|node)
    install_node "$NODE20_VERSION" "$DEST/node20"
    install_node "$NODE22_VERSION" "$DEST/node22"
    install_node "$NODE24_VERSION" "$DEST/node24" ;;
esac
case "$WHAT" in
  all|python)
    install_python "$PY311_VERSION" "$DEST/python311"
    install_python "$PY312_VERSION" "$DEST/python312" ;;
esac
case "$WHAT" in
  all|java)
    install_java "$JAVA17_VERSION" "$DEST/java17"
    install_java "$JAVA21_VERSION" "$DEST/java21" ;;
esac

log "done."
