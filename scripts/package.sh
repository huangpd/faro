#!/bin/sh
# Fetches the pinned sing-box core, then builds faro.app and the disk image
# into build/darwin-<arch>/.
set -eu
cd "$(dirname "$0")/.."
./scripts/fetch-singbox.sh
./scripts/fetch-rulesets.sh
go test ./... -count=1
go tool mygo build "$@"
