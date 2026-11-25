#!/usr/bin/env bash
# Run regen-knowledge-proto.sh inside Docker when host lacks go/protoc.
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
docker run --rm \
  -v "${REPO_ROOT}:/src" \
  -w /src \
  golang:1.26-bookworm \
  bash -lc 'export PATH=/usr/local/go/bin:$PATH && export GOTOOLCHAIN=auto && apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq protobuf-compiler && bash scripts/regen-knowledge-proto.sh'
