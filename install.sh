#!/usr/bin/env bash
# Installs Go (if missing), then builds + installs `vpn` into
# /usr/local/bin, setuid-root, so `vpn connect`/`disconnect`/`repair`
# don't need `sudo` on every invocation.
set -euo pipefail

# `command -v go` only checks that *something* named `go` is on PATH — on
# a machine using asdf/mise, that's just a shim that fails at run time if
# no Go version is actually configured, which command -v can't detect.
# Actually invoke `go version` to know it's really usable.
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

cd "$(dirname "${BASH_SOURCE[0]}")"

SRC_DIR="$(pwd)"
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo 1.0.0)"
OWNER_UID="$(id -u)"

echo "Building vpn ($VERSION)..."
# -trimpath: don't embed this machine's build path in the binary.
# -s -w: strip debug symbols/DWARF — smaller binary, nothing a release
# build needs to ship with.
go build -trimpath -ldflags "-s -w -X main.version=$VERSION -X main.sourceDir=$SRC_DIR -X main.allowedUID=$OWNER_UID" -o /tmp/vpn-build ./cmd/vpn
sudo mv /tmp/vpn-build /usr/local/bin/vpn
sudo chown root:wheel /usr/local/bin/vpn
sudo chmod 4755 /usr/local/bin/vpn

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
