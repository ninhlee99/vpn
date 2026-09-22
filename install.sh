#!/usr/bin/env bash
# Installs `vpn` into /usr/local/bin, setuid-root, so `vpn connect`/
# `disconnect`/`repair` don't need `sudo` on every invocation. Tries a
# prebuilt release binary first (no Go needed on this machine at all);
# falls back to building from source (installing Go first, if needed) if
# no release is available for this architecture.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

case "$(uname -m)" in
  arm64) ARCH=arm64 ;;
  x86_64) ARCH=amd64 ;;
  *) ARCH="" ;;
esac

BIN=/tmp/vpn-build
DOWNLOADED=0
if [ -n "$ARCH" ]; then
  URL="https://github.com/ninhlee99/vpn/releases/latest/download/vpn-darwin-$ARCH"
  echo "Trying prebuilt release for $ARCH..."
  if curl -fsSL -o "$BIN" "$URL" 2>/dev/null && chmod +x "$BIN" && "$BIN" version >/dev/null 2>&1; then
    VERSION="$("$BIN" version | awk '{print $2}')"
    echo "Downloaded prebuilt vpn $VERSION — no Go toolchain needed."
    DOWNLOADED=1
  else
    rm -f "$BIN"
    echo "No usable prebuilt release for $ARCH — building from source instead."
  fi
fi

if [ "$DOWNLOADED" = 0 ]; then
  # `command -v go` only checks that *something* named `go` is on PATH —
  # on a machine using asdf/mise, that's just a shim that fails at run
  # time if no Go version is actually configured, which command -v can't
  # detect. Actually invoke `go version` to know it's really usable.
  if ! go version >/dev/null 2>&1; then
    if command -v go >/dev/null 2>&1; then
      echo "Found 'go' on PATH but it doesn't run — this usually means a Go" >&2
      echo "version manager (asdf/mise) has no version selected. Output:" >&2
      echo >&2
      go version >&2 || true
      echo >&2
      echo "Fix your Go version manager (e.g. 'asdf set golang <version>' or" >&2
      echo "'mise use go@<version>' in this directory), then re-run install.sh." >&2
      exit 1
    fi
    echo "Go not found, installing..."
    if ! command -v brew >/dev/null 2>&1; then
      echo "Homebrew not found, installing it first..."
      /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
    fi
    brew install go
  fi

  echo "Go: $(go version)"

  SRC_DIR="$(pwd)"
  VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo 1.0.0)"

  echo "Building vpn ($VERSION)..."
  # -trimpath: don't embed this machine's build path in the binary.
  # -s -w: strip debug symbols/DWARF — smaller binary, nothing a release
  # build needs to ship with.
  go build -trimpath -ldflags "-s -w -X main.version=$VERSION -X main.sourceDir=$SRC_DIR" -o "$BIN" ./cmd/vpn
fi

OWNER_UID="$(id -u)"
sudo mv "$BIN" /usr/local/bin/vpn
sudo chown root:wheel /usr/local/bin/vpn
sudo chmod 4755 /usr/local/bin/vpn

# The owner-uid file, not a build-time constant, is what CheckOwner reads
# (see internal/privilege) — this is what lets the exact same prebuilt
# binary be shared across users/machines while still restricting who can
# run *this installed copy* to whoever just ran install.sh.
echo "$OWNER_UID" | sudo tee /etc/vpn-owner-uid >/dev/null
sudo chown root:wheel /etc/vpn-owner-uid
sudo chmod 600 /etc/vpn-owner-uid

cat <<EOF

Installed /usr/local/bin/vpn setuid-root, locked to the current user
(uid $OWNER_UID) — only this user can run \`vpn\` at all; every other user
on this machine is refused immediately, even for commands that don't
need root.

For this user, \`vpn connect\`/\`disconnect\`/\`repair\` no longer need sudo.
The binary drops to the real user right at startup and only briefly
regains root for the specific steps that need it (opening utun, changing
routes/DNS) before dropping back — see internal/privilege in the source
if you want to check the mechanism.
EOF

echo
echo "Installed: $(vpn version)"
echo "Next: vpn init"
