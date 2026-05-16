#!/usr/bin/env bash
# Cross-compile releaser archives for every (GOOS, GOARCH) target.
#
# Produces dist/releaser_<os>_<arch>.tar.gz files, each containing:
#   - the releaser binary (releaser.exe on Windows)
#   - LICENSE
#   - README.md
#
# Output layout matches the download URL the composite action in
# action.yml expects:
#   releases/download/<tag>/releaser_<os>_<arch>.tar.gz
#
# Targets are read from $RELEASER_GO_TARGETS (space-separated
# "<goos>/<goarch>" pairs) when set — that variable is exported by the
# basic "go" adapter at build time. A hardcoded fallback list keeps the
# script working when it is invoked by an older releaser binary that
# predates the BuildEnv hook, or by `mise run package` without env
# overrides.
#
# RELEASER_VERSION is required; the build embeds it into the binary
# via -ldflags so `releaser --version` reports the release tag even
# when source is mid-bump.

set -euo pipefail

: "${RELEASER_VERSION:?RELEASER_VERSION must be set}"

targets="${RELEASER_GO_TARGETS:-linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64}"

rm -rf dist
mkdir -p dist

for target in $targets; do
  goos="${target%/*}"
  goarch="${target#*/}"

  binary="releaser"
  if [[ "$goos" == "windows" ]]; then
    binary="releaser.exe"
  fi

  staging="$(mktemp -d)"
  trap 'rm -rf "$staging"' EXIT

  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build \
    -trimpath \
    -ldflags "-s -w -X github.com/bombfork/releaser/internal/cli.Version=${RELEASER_VERSION}" \
    -o "${staging}/${binary}" \
    ./cmd

  cp LICENSE README.md "${staging}/"

  tar -czf "dist/releaser_${goos}_${goarch}.tar.gz" -C "${staging}" "${binary}" LICENSE README.md

  rm -rf "$staging"
  trap - EXIT
done
