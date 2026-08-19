#!/usr/bin/env bash
set -euo pipefail

# Cross-compiles the build tool and produces the archives a release carries.
#
# GoReleaser is not used here. Its monorepo support, which is what teaches it to
# read a tag like cmd/celerity-go/v0.1.0, is a Pro feature; without it the tag
# fails GoReleaser's semver parsing, and the workarounds amount to overriding
# every template that would have used the version. Cross-compiling pure Go is a
# loop, so the loop is what this is.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

MODULE_DIR="$ROOT/cmd/celerity-go"
DIST="$MODULE_DIR/dist"
BINARY="celerity-go"

# Go derives a submodule's version from a tag carrying its directory prefix, so
# the version is the last segment of cmd/celerity-go/v0.1.0.
TAG="${TAG:-$(git -C "$ROOT" describe --tags --match 'cmd/celerity-go/v*' --abbrev=0 2>/dev/null || echo "")}"
if [ -z "$TAG" ]; then
  echo "no release tag: set TAG, or tag the module as cmd/celerity-go/vX.Y.Z" >&2
  exit 1
fi
VERSION="${TAG##*/}"

PLATFORMS=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
  "windows/amd64"
  "windows/arm64"
)

rm -rf "$DIST"
mkdir -p "$DIST"
cd "$MODULE_DIR"

for platform in "${PLATFORMS[@]}"; do
  os="${platform%/*}"
  arch="${platform#*/}"

  name="$BINARY"
  if [ "$os" = "windows" ]; then
    name="$BINARY.exe"
  fi

  echo "building $os/$arch"
  staging="$(mktemp -d)"

  # CGO off so every artefact is static and runs on whatever the Celerity CLI
  # downloaded it onto. -trimpath keeps the build machine's paths out of it,
  # which is both smaller and one less thing to leak.
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build \
    -trimpath \
    -ldflags "-s -w -X main.version=$VERSION" \
    -o "$staging/$name" .

  archive="$DIST/${BINARY}_${VERSION}_${os}_${arch}.tar.gz"
  tar -czf "$archive" -C "$staging" "$name"
  rm -rf "$staging"
done

cd "$DIST"
# sha256sum is coreutils and absent on a stock macOS, where shasum is the one
# that is always there.
if command -v sha256sum > /dev/null; then
  sha256sum ./*.tar.gz > "${BINARY}_${VERSION}_checksums.txt"
else
  shasum -a 256 ./*.tar.gz > "${BINARY}_${VERSION}_checksums.txt"
fi

echo ""
echo "archives in $DIST:"
ls -1 "$DIST"
