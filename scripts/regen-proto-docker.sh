#!/usr/bin/env bash
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
docker run --rm \
  -v "${REPO_ROOT}:/src" \
  -w /src \
  golang:1.26-bookworm \
  bash -lc 'export PATH=/usr/local/go/bin:$PATH && export GOTOOLCHAIN=auto && apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq protobuf-compiler && go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11 && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1 && make generate'
