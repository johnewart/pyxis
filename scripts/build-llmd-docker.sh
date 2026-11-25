#!/usr/bin/env bash
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
docker run --rm \
  -v "${REPO_ROOT}:/src" \
  -w /src \
  golang:1.26-bookworm \
  bash -lc 'export PATH=/usr/local/go/bin:$PATH && export GOTOOLCHAIN=auto && go build -C cmd/llmd -o /dev/null ./...'
