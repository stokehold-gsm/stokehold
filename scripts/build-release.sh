#!/usr/bin/env bash
# Builds the release files into dist/. Signing is a separate, manual step on the
# owner's machine (see docs/release.md); the private key is never in CI.
#   scripts/build-release.sh v0.1.0
set -euo pipefail
VERSION="${1:?usage: build-release.sh vX.Y.Z}"
cd "$(dirname "$0")/.."
rm -rf dist && mkdir dist
for arch in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "dist/stokehold-linux-$arch" ./cmd/stokehold
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags "-s -w" -o "dist/stokehold-helper-linux-$arch" ./cmd/stokehold-helper
done
cp install/install.sh install/uninstall.sh dist/
( cd dist && sha256sum stokehold-linux-* stokehold-helper-linux-* install.sh uninstall.sh > SHA256SUMS )
echo "built $VERSION:"; cat dist/SHA256SUMS
