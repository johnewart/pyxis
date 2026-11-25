#!/usr/bin/env bash
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
docker run --rm \
  -v "${REPO_ROOT}:/src" \
  -w /src \
  golang:1.25-bookworm bash -c '
    set -euo pipefail
    apt-get update -qq
    DEBIAN_FRONTEND=noninteractive apt-get install -qq -y libsqlite3-dev gcc protobuf-compiler >/dev/null
    go build -C cmd/llmd -trimpath -o /dev/null ./...
    echo "llmd build ok"
  '
