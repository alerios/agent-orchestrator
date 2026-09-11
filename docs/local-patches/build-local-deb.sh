#!/bin/bash
# Builds a local .deb from an agent-orchestrator checkout's current HEAD,
# stamping frontend/package.json's version from git tags for the duration of
# the build only. Mirrors upstream's own .github/workflows/build-artifacts.yml
# "Stamp version" step, which does the identical transient edit but with a
# conductor-supplied version instead of one derived from git tags — package.json
# on main is never meant to carry the real version; see LOCAL_AO_PATCH.md.
#
# Usage: ./build-local-ao-deb.sh [agent-orchestrator-checkout] [suffix]
#   checkout defaults to the current repo root
#   suffix   defaults to "local"
set -euo pipefail

AO_REPO="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
SUFFIX="${2:-local}"
PKG_JSON="$AO_REPO/frontend/package.json"

if [ ! -f "$PKG_JSON" ]; then
  echo "error: $PKG_JSON not found — is $AO_REPO an agent-orchestrator checkout?" >&2
  exit 1
fi

if [ -n "$(git -C "$AO_REPO" status --porcelain -- frontend/package.json)" ]; then
  echo "error: frontend/package.json already has uncommitted changes in $AO_REPO — resolve before building" >&2
  exit 1
fi

# git describe --long always appends -N-gHASH, even exactly on a tag (N=0),
# so this format is stable whether or not HEAD is a tagged commit.
DESCRIBE="$(git -C "$AO_REPO" describe --tags --long --match 'v[0-9]*' HEAD)"
# git describe --long always ends in -<N>-g<hash>; strip that from the *end*
# first, since the nearest tag itself can carry its own suffix (upstream cuts
# tags like v0.12.13-nightly.202609091711), which would otherwise land between
# the version and the distance and break a fixed-prefix parse.
DISTANCE="$(sed -E 's/.*-([0-9]+)-g[0-9a-f]+$/\1/' <<<"$DESCRIBE")"
TAG_PART="$(sed -E 's/-[0-9]+-g[0-9a-f]+$//' <<<"$DESCRIBE")"
BASE="$(sed -E 's/^v([0-9]+\.[0-9]+\.[0-9]+).*/\1/' <<<"$TAG_PART")"
VERSION="${BASE}-${SUFFIX}.${DISTANCE}"

echo "Checkout:        $AO_REPO"
echo "Branch:          $(git -C "$AO_REPO" branch --show-current)"
echo "Nearest tag:     $DESCRIBE"
echo "Stamped version: $VERSION"
echo

cleanup() {
  git -C "$AO_REPO" checkout -- frontend/package.json
  echo "Reverted the version stamp in $PKG_JSON"
}
trap cleanup EXIT

node -e "
	const fs = require('fs');
	const path = '$PKG_JSON';
	const p = JSON.parse(fs.readFileSync(path, 'utf8'));
	p.version = '$VERSION';
	fs.writeFileSync(path, JSON.stringify(p, null, '\t') + '\n');
"

command -v bison >/dev/null || { echo "error: bison not installed (needed to build tmux from source) — run: sudo apt-get install -y bison" >&2; exit 1; }

export PATH="$HOME/go-sdk/bin:$PATH"
( cd "$AO_REPO/frontend" && npm run make -- --targets=@electron-forge/maker-deb )

DEB="$(find "$AO_REPO/frontend/out/make/deb" -name '*.deb' -newer "$PKG_JSON" 2>/dev/null | head -1)"
[ -z "$DEB" ] && DEB="$(find "$AO_REPO/frontend/out/make/deb" -name "*${VERSION}*.deb" | head -1)"

echo
echo "Built: $DEB"
echo "Install with:  sudo dpkg -i \"$DEB\""
