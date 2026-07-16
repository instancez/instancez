#!/bin/sh
# instancez installer for macOS and Linux.
#
#   curl -fsSL https://get.instancez.ai | sh
#
# Downloads the inz binary that matches your OS and CPU, checks it against the
# release checksums, and drops it in ~/.local/bin. Override the target with
# INSTANCEZ_INSTALL_DIR, or pin a release with INSTANCEZ_VERSION=1.2.3.
set -eu

REPO="instancez/instancez"
BIN="inz"
INSTALL_DIR="${INSTANCEZ_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${INSTANCEZ_VERSION:-}"

info() { printf '%s\n' "$*" >&2; }
err() { printf 'error: %s\n' "$*" >&2; exit 1; }

# curl is preferred; fall back to wget so the script runs on minimal images.
download() { # url dest
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$2" "$1"
  else
    err "need curl or wget to download"
  fi
}

os=$(uname -s)
case "$os" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) err "unsupported OS '$os'. On Windows, run: irm https://get.instancez.ai/windows | iex" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) err "unsupported architecture '$arch'. instancez ships amd64 and arm64." ;;
esac

asset="${BIN}_${os}_${arch}"

# An empty VERSION means latest. A pinned version uses the same stable asset
# name under that tag's release, so the only thing that changes is the path.
if [ -n "$VERSION" ]; then
  VERSION="${VERSION#v}"
  base="https://github.com/${REPO}/releases/download/v${VERSION}"
else
  base="https://github.com/${REPO}/releases/latest/download"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

info "Downloading ${asset}..."
download "${base}/${asset}" "${tmp}/${BIN}" ||
  err "could not download ${base}/${asset} (is the release published for your platform?)"

# Verify the download against the release checksums. We skip only when no
# sha256 tool is present, and say so rather than failing silently.
if download "${base}/checksums.txt" "${tmp}/checksums.txt" 2>/dev/null; then
  expected=$(awk -v f="$asset" '$2 == f { print $1 }' "${tmp}/checksums.txt")
  if [ -z "$expected" ]; then
    info "warning: ${asset} not listed in checksums.txt, skipping verification"
  elif command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "${tmp}/${BIN}" | awk '{ print $1 }')
  elif command -v shasum >/dev/null 2>&1; then
    actual=$(shasum -a 256 "${tmp}/${BIN}" | awk '{ print $1 }')
  else
    info "warning: no sha256 tool found, skipping checksum verification"
  fi
  if [ -n "${actual:-}" ] && [ "$actual" != "$expected" ]; then
    err "checksum mismatch for ${asset} (expected ${expected}, got ${actual})"
  fi
else
  info "warning: could not fetch checksums.txt, skipping verification"
fi

chmod +x "${tmp}/${BIN}"
mkdir -p "$INSTALL_DIR"
mv "${tmp}/${BIN}" "${INSTALL_DIR}/${BIN}"
info "Installed ${BIN} to ${INSTALL_DIR}/${BIN}"

# Tell the user how to reach the binary if its directory isn't already on PATH.
case ":${PATH}:" in
  *":${INSTALL_DIR}:"*) ;;
  *)
    info ""
    info "${INSTALL_DIR} is not on your PATH. Add this to your shell profile:"
    info "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    ;;
esac

info ""
"${INSTALL_DIR}/${BIN}" version 2>/dev/null || info "Run '${BIN} version' to confirm the install."
