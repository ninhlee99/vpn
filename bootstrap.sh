#!/usr/bin/env bash
# One-liner install entry point: curl this and pipe to bash. It clones the
# source (install.sh itself needs the repo present on disk to build from —
# it's not a standalone installer) into a fixed local directory, then hands
# off to install.sh there. Re-running this later (or `vpn update`) reuses
# that same directory instead of re-cloning.
set -euo pipefail

REPO_URL="https://github.com/ninhlee99/vpn.git"
DEST="${VPN_SRC_DIR:-$HOME/.local/share/vpn-src}"

if ! command -v git >/dev/null 2>&1; then
  echo "git not found — install Xcode Command Line Tools first: xcode-select --install" >&2
  exit 1
fi

if [ -d "$DEST/.git" ]; then
  echo "Source already present at $DEST, pulling latest..."
  git -C "$DEST" pull --ff-only
else
  echo "Cloning into $DEST..."
  git clone "$REPO_URL" "$DEST"
fi

exec "$DEST/install.sh"
